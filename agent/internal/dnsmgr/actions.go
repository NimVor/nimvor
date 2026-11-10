package dnsmgr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/fw"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/selinux"
)

var mu sync.Mutex

// query is replaced in tests.
var query = querySOA

// verifyWait is how long verify waits between asks (shortened in tests).
var verifyWait = 250 * time.Millisecond

func configured(env *host.Env, p paths) bool {
	cur, _ := env.Root.ReadFile(p.conf)
	_, wired := p.d.wire(env, p, string(cur))
	return wired && env.Root.Exists(p.list)
}

func installed(env *host.Env) bool { return active(env).installed(env) }

var secondariesRe = regexp.MustCompile(`(?m)^; secondaries (.*)$`)

// zoneInfo is what the zone list needs from a zone file.
type zoneInfo struct {
	Name        string   `json:"zone"`
	Serial      uint32   `json:"serial"`
	Secondaries []string `json:"secondaries"`
}

func listZones(env *host.Env, p paths) []zoneInfo {
	files, _ := env.Root.Glob(p.dir + "/db.*")
	out := []zoneInfo{}
	for _, f := range files {
		name := strings.TrimPrefix(filepath.Base(f), "db.")
		b, err := env.Root.ReadFile(f)
		if err != nil || strings.HasSuffix(name, ".partial") {
			continue
		}
		zi := zoneInfo{Name: name, Serial: FileSerial(string(b)), Secondaries: []string{}}
		if m := secondariesRe.FindStringSubmatch(string(b)); m != nil && strings.TrimSpace(m[1]) != "" {
			zi.Secondaries = strings.Split(strings.TrimSpace(m[1]), ",")
		}
		out = append(out, zi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RHEL's named.conf listens on loopback only (a caching resolver for this
// machine); hosting zones needs every address.
var listenLocalRe = regexp.MustCompile(`(?m)^(\s*listen-on(?:-v6)?\s+port\s+53\s*)\{\s*(?:127\.0\.0\.1|::1);\s*\};`)

func localOnly(conf string) bool { return listenLocalRe.MatchString(conf) }

func openListen(conf string) string { return listenLocalRe.ReplaceAllString(conf, "${1}{ any; };") }

func checkconf(ctx context.Context, env *host.Env) error {
	out, err := env.Runner.Run(ctx, "named-checkconf")
	if err != nil {
		return fmt.Errorf("named-checkconf: %s", host.Why(out, err))
	}
	return nil
}

func rndc(ctx context.Context, env *host.Env, args ...string) error {
	out, err := env.Runner.Run(ctx, "rndc", args...)
	if err != nil {
		return fmt.Errorf("rndc %s: %s", strings.Join(args, " "), host.Why(out, err))
	}
	return nil
}

// running reports whether named answers rndc.
func running(ctx context.Context, env *host.Env) bool {
	return rndc(ctx, env, "status") == nil
}

// verify waits until the nameserver serves the zone with the given serial (a
// signed zone may be served with a higher one: signing raises it).
func verify(ctx context.Context, env *host.Env, name, zone string, serial uint32, atLeast bool) error {
	if env.Cfg.SimulateCommands {
		return nil
	}
	var last error
	for i := 0; i < 20; i++ {
		got, err := query(ctx, "127.0.0.1:53", zone)
		if err == nil && (got == serial || (atLeast && got > serial)) {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("%s serves serial %d, expected %d", name, got, serial)
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(verifyWait):
		}
	}
	return last
}

// zoneDirMode: BIND signs inside the zone folder and needs to write there.
func zoneDirMode(d driver, signed map[string]bool) os.FileMode {
	if len(signed) > 0 && d.dnssec() != nil && d.id() == "bind" {
		return 0o770
	}
	return 0o750
}

func readConf(env *host.Env, p paths) (cur string, exists bool, err error) {
	b, err := env.Root.ReadFile(p.conf)
	if err != nil {
		if p.d.id() == "bind" {
			return "", false, err // BIND's file is the package's: it must exist
		}
		return "", false, nil
	}
	return string(b), true, nil
}

func confMode(env *host.Env, p paths) os.FileMode {
	mode := os.FileMode(0o644)
	if real, err := env.Root.Path(p.conf); err == nil {
		if fi, err := os.Stat(real); err == nil {
			mode = fi.Mode().Perm()
		}
	}
	return mode
}

// configureMessage says what NimVor changes and asks for confirmation.
func configureMessage(env *host.Env, p paths, cur string) string {
	d := p.d
	switch {
	case d.id() == "bind":
		msg := "NimVor adds one include line to " + p.conf + " (a file of the BIND package)"
		if localOnly(cur) {
			msg += " and makes BIND listen on all addresses instead of only this machine"
		}
		return msg + ". Confirm to continue; a backup is kept."
	case d.wholeFile():
		return "NimVor takes over " + p.conf + " (the configuration of " + d.label() + "); the present file is kept as " + p.conf + ".pre-nimvor. Confirm to continue."
	default:
		return "NimVor writes its settings for " + d.label() + " to " + p.conf + ". Confirm to continue; a backup is kept."
	}
}

// configureDriver sets a nameserver up for NimVor's zones: the zone folder, the
// zone list and the daemon's configuration, checked before it is kept and
// loaded; everything is restored when the daemon does not take it.
func configureDriver(ctx context.Context, env *host.Env, d driver, confirm bool) (any, error) {
	p := pathsFor(env, d)
	if !d.installed(env) {
		return nil, actions.Conflict(d.label() + " is not installed. Install it first (Server → DNS → Nameserver selection).")
	}
	cur, exists, err := readConf(env, p)
	if err != nil {
		return nil, actions.Failed("Could not read "+p.conf+".", err.Error())
	}
	content, already := d.wire(env, p, cur)
	if !already && !confirm {
		return nil, actions.Conflict(configureMessage(env, p, cur))
	}
	signed := signedZones(env, p)
	owner := zoneOwner(env, d)
	set := env.Changes("dns")
	set.Mkdir(p.dir, zoneDirMode(d, signed), owner)
	set.Write(p.list, []byte(d.renderList(env, p, listZones(env, p), signed)), 0o640, owner)
	if !already {
		if d.wholeFile() && exists && !env.Root.Exists(p.conf+".pre-nimvor") {
			set.Write(p.conf+".pre-nimvor", []byte(cur), 0o600, agentfs.RootOwner)
		}
		o := agentfs.Keep
		if !exists {
			o = agentfs.RootOwner
		}
		set.Write(p.conf, []byte(content), confMode(env, p), o)
	}
	applied, err := set.Commit(func() error {
		if err := d.prepare(ctx, env, p); err != nil {
			return err
		}
		return d.checkConf(ctx, env, p)
	})
	if err != nil {
		return nil, host.FailWithValidation(d.label()+" was not set up", err)
	}
	// SELinux: the daemon reads only its policy labels.
	for _, pth := range []string{p.conf, p.dir} {
		if err := selinux.Restore(ctx, env, pth); err != nil {
			env.Log.Warn("selinux", "err", err)
		}
	}
	wasRunning := d.running(ctx, env)
	var startErr error
	if wasRunning {
		startErr = d.activate(ctx, env, "", false, true)
	} else {
		env.Systemctl(ctx, "enable", d.unit(env))
		if startErr = env.Systemctl(ctx, "start", d.unit(env)); startErr == nil && !env.Cfg.SimulateCommands {
			startErr = fmt.Errorf("%s did not start", d.label())
			for i := 0; i < 20; i++ {
				if d.running(ctx, env) {
					startErr = nil
					break
				}
				time.Sleep(250 * time.Millisecond)
			}
		}
	}
	if startErr != nil {
		zones, _ := env.Root.Glob(p.dir + "/*")
		files := env.Describe(append([]string{p.conf, p.dir, p.list}, zones...)...)
		applied.Rollback()
		if wasRunning {
			d.activate(ctx, env, "", false, true)
		}
		return nil, actions.Failed(d.label()+" did not load the configuration; the previous files were restored.", startErr.Error()+"\n"+env.JournalTail(ctx, d.unit(env), 30)+"\n"+files)
	}
	notes := []string{}
	notes = append(notes, fw.Open(ctx, env, "DNS", "53/tcp", "53/udp"))
	return map[string]any{"configured": true, "changed": applied.Changed, "notes": notes}, nil
}

// Configured reports whether the active nameserver loads NimVor's zone list.
func Configured(env *host.Env) bool { return configured(env, dnsPaths(env)) }

// Units are the service units to watch: the nameserver in use.
func Units(env *host.Env) []string { return []string{active(env).unit(env)} }
