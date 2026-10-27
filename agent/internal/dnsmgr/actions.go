package dnsmgr

import (
	"context"
	"encoding/json"
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
	"github.com/NimVor/nimvor/agent/internal/validate"
)

var mu sync.Mutex

// query is replaced in tests.
var query = querySOA

type paths struct{ dir, list, conf string }

func dnsPaths(env *host.Env) paths {
	return paths{dir: env.M.DNSZoneDir, list: env.M.DNSZoneDir + "/zones.conf", conf: env.M.DNSConfFile}
}

func (p paths) zoneFile(zone string) string { return p.dir + "/db." + zone }

func (p paths) includeLine() string {
	return fmt.Sprintf("include \"%s\"; // WhaCP DNS zones (added by WhaCP)", p.list)
}

func configured(env *host.Env, p paths) bool {
	b, err := env.Root.ReadFile(p.conf)
	return err == nil && strings.Contains(string(b), fmt.Sprintf("include \"%s\";", p.list))
}

func installed(env *host.Env) bool {
	return env.Root.Exists("/usr/sbin/named")
}

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

// renderList produces zones.conf for the given zones.
func renderList(p paths, zones []zoneInfo) string {
	var b strings.Builder
	b.WriteString("// Managed by WhaCP (DNS). Do not edit: changes are overwritten.\n")
	for _, z := range zones {
		transfer := "none;"
		notify := "no"
		if len(z.Secondaries) > 0 {
			transfer = strings.Join(z.Secondaries, "; ") + ";"
			notify = "yes"
		}
		// allow-query: RHEL's named.conf answers only localhost by default.
		fmt.Fprintf(&b, "zone \"%s\" {\n\ttype master;\n\tfile \"%s\";\n\tallow-query { any; };\n\tallow-transfer { %s };\n\tnotify %s;\n", z.Name, p.zoneFile(z.Name), transfer, notify)
		if len(z.Secondaries) > 0 {
			fmt.Fprintf(&b, "\talso-notify { %s };\n", transfer)
		}
		b.WriteString("};\n")
	}
	return b.String()
}

// RHEL's named.conf listens on loopback only (a caching resolver for this
// machine); hosting zones needs every address.
var listenLocalRe = regexp.MustCompile(`(?m)^(\s*listen-on(?:-v6)?\s+port\s+53\s*)\{\s*(?:127\.0\.0\.1|::1);\s*\};`)

func localOnly(conf string) bool { return listenLocalRe.MatchString(conf) }

func openListen(conf string) string { return listenLocalRe.ReplaceAllString(conf, "${1}{ any; };") }

func dnsOwner(env *host.Env) agentfs.Owner {
	if u, err := env.LookupUser(env.M.DNSUser); err == nil {
		return agentfs.Owner{UID: 0, GID: u.GID}
	}
	return agentfs.RootOwner
}

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

// verify waits until BIND serves the zone with the given serial.
func verify(ctx context.Context, env *host.Env, zone string, serial uint32) error {
	if env.Cfg.SimulateCommands {
		return nil
	}
	var last error
	for i := 0; i < 20; i++ {
		got, err := query(ctx, "127.0.0.1:53", zone)
		if err == nil && got == serial {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("BIND serves serial %d, expected %d", got, serial)
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return last
}

// Register adds the DNS actions.
func Register(r *actions.Registry, env *host.Env) {
	p := dnsPaths(env)

	r.Register(actions.Action{Name: "dns.status", Description: "Whether BIND is installed, set up for WhaCP and running, and the zones WhaCP serves (with serials).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{
			"installed": installed(env), "configured": configured(env, p), "running": installed(env) && running(ctx, env),
			"config_file": p.conf, "zones": listZones(env, p),
		}, nil
	}})

	r.Register(actions.Action{Name: "dns.configure", Mutating: true, Description: "Set up BIND for WhaCP's zones: adds one include line to BIND's configuration (requires confirm_edit_package_config), validates with named-checkconf, starts or reconfigures named and opens port 53 when the firewall is active.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Confirm bool `json:"confirm_edit_package_config"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if !installed(env) {
			return nil, actions.Conflict("BIND is not installed. Install BIND under Server → Packages first.")
		}
		already := configured(env, p)
		if !already && !in.Confirm {
			msg := "WhaCP adds one include line to " + p.conf + " (a file of the BIND package)"
			if b, err := env.Root.ReadFile(p.conf); err == nil && localOnly(string(b)) {
				msg += " and makes BIND listen on all addresses instead of only this machine"
			}
			return nil, actions.Conflict(msg + ". Confirm to continue; a backup is kept.")
		}
		owner := dnsOwner(env)
		set := env.Changes("dns")
		set.Mkdir(p.dir, 0o750, owner)
		set.Write(p.list, []byte(renderList(p, listZones(env, p))), 0o640, owner)
		if !already {
			b, err := env.Root.ReadFile(p.conf)
			if err != nil {
				return nil, actions.Failed("Could not read "+p.conf+".", err.Error())
			}
			mode := os.FileMode(0o644)
			if real, err := env.Root.Path(p.conf); err == nil {
				if fi, err := os.Stat(real); err == nil {
					mode = fi.Mode().Perm()
				}
			}
			content := strings.TrimRight(openListen(string(b)), "\n") + "\n\n" + p.includeLine() + "\n"
			set.Write(p.conf, []byte(content), mode, agentfs.Keep)
		}
		applied, err := set.Commit(func() error { return checkconf(ctx, env) })
		if err != nil {
			return nil, host.FailWithValidation("BIND was not set up", err)
		}
		// SELinux: named reads only its policy labels (named_conf_t,
		// named_zone_t).
		for _, pth := range []string{p.conf, p.dir} {
			if err := selinux.Restore(ctx, env, pth); err != nil {
				env.Log.Warn("selinux", "err", err)
			}
		}
		wasRunning := running(ctx, env)
		var startErr error
		if wasRunning {
			startErr = rndc(ctx, env, "reconfig")
		} else {
			env.Systemctl(ctx, "enable", env.M.DNSUnit)
			if startErr = env.Systemctl(ctx, "start", env.M.DNSUnit); startErr == nil && !env.Cfg.SimulateCommands {
				startErr = fmt.Errorf("named did not answer rndc")
				for i := 0; i < 20; i++ {
					if running(ctx, env) {
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
				rndc(ctx, env, "reconfig")
			}
			return nil, actions.Failed("BIND did not load the configuration; the previous files were restored.", startErr.Error()+"\n"+env.JournalTail(ctx, env.M.DNSUnit, 30)+"\n"+files)
		}
		notes := []string{}
		notes = append(notes, fw.Open(ctx, env, "DNS", "53/tcp", "53/udp"))
		return map[string]any{"configured": true, "changed": applied.Changed, "notes": notes}, nil
	}})

	r.Register(actions.Action{Name: "dns.zone_parse", Description: "Read a zone file (BIND format) for import: the supported records, relative to the zone, and the lines skipped with reasons. Nothing is changed.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Zone string `json:"zone"`
			Text string `json:"text"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if err := validate.Domain("zone", strings.TrimSuffix(in.Zone, ".")); err != nil {
			return nil, err
		}
		if len(in.Text) > 1<<20 {
			return nil, actions.InvalidParams("the zone file is larger than 1 MB")
		}
		return ParseZoneFile(in.Text, in.Zone), nil
	}})

	r.Register(actions.Action{Name: "dns.zone_render", Description: "Validate a zone and return its zone file (BIND format) without writing it, e.g. for export.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Zone
			Serial uint32 `json:"serial"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if err := in.Zone.Validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		return map[string]any{"text": in.Zone.Render(in.Serial)}, nil
	}})

	r.Register(actions.Action{Name: "dns.zone_apply", Mutating: true, Description: "Write a complete zone (SOA, records, optional secondaries) with a new serial, validate it with named-checkzone and named-checkconf, load it and verify BIND serves the new serial; rolls back on failure.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var z Zone
		if err := host.Decode(raw, &z); err != nil {
			return nil, err
		}
		if err := z.Validate(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		mu.Lock()
		defer mu.Unlock()
		if !configured(env, p) {
			return nil, actions.Conflict("Set up the DNS server first (Server → DNS).")
		}
		file := p.zoneFile(z.Name)
		var old uint32
		if b, err := env.Root.ReadFile(file); err == nil {
			old = FileSerial(string(b))
		}
		serial := NextSerial(old, env.Now())
		content := z.Render(serial)
		content = strings.Replace(content, "\n$ORIGIN", "\n; secondaries "+strings.Join(z.Secondaries, ",")+"\n$ORIGIN", 1)
		zones := listZones(env, p)
		found := false
		for i := range zones {
			if zones[i].Name == z.Name {
				zones[i].Secondaries = z.Secondaries
				found = true
			}
		}
		if !found {
			zones = append(zones, zoneInfo{Name: z.Name, Secondaries: z.Secondaries})
			sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })
		}
		owner := dnsOwner(env)
		set := env.Changes("dns-zone")
		set.Write(file, []byte(content), 0o640, owner)
		set.Write(p.list, []byte(renderList(p, zones)), 0o640, owner)
		applied, err := set.Commit(func() error {
			out, err := env.Runner.Run(ctx, "named-checkzone", "-i", "local", z.Name, file)
			if err != nil {
				return fmt.Errorf("named-checkzone: %s", host.Why(out, err))
			}
			return checkconf(ctx, env)
		})
		if err != nil {
			return nil, host.FailWithValidation("The zone was not saved", err)
		}
		if !running(ctx, env) {
			return map[string]any{"zone": z.Name, "serial": serial, "loaded": false, "warning": "BIND is not running; the zone is loaded when it starts."}, nil
		}
		rndc(ctx, env, "reconfig")
		if found {
			rndc(ctx, env, "reload", z.Name)
		}
		if err := verify(ctx, env, z.Name, serial); err != nil {
			applied.Rollback()
			rndc(ctx, env, "reconfig")
			if found {
				rndc(ctx, env, "reload", z.Name)
			}
			return nil, actions.Failed("BIND did not serve the new zone; the previous version was restored.", err.Error())
		}
		return map[string]any{"zone": z.Name, "serial": serial, "loaded": true}, nil
	}})

	r.Register(actions.Action{Name: "dns.zone_delete", Mutating: true, Description: "Stop serving a zone and remove its file (confirm = zone name).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Zone    string `json:"zone"`
			Confirm string `json:"confirm"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		z := Zone{Name: in.Zone}
		z.Name = strings.ToLower(strings.TrimSuffix(z.Name, "."))
		if in.Confirm != z.Name {
			return nil, actions.InvalidParams("confirm must repeat the zone name")
		}
		mu.Lock()
		defer mu.Unlock()
		file := p.zoneFile(z.Name)
		if !env.Root.Exists(file) {
			return nil, actions.NotFound("WhaCP does not serve the zone " + z.Name)
		}
		var keep []zoneInfo
		for _, zi := range listZones(env, p) {
			if zi.Name != z.Name {
				keep = append(keep, zi)
			}
		}
		set := env.Changes("dns-zone")
		set.Write(p.list, []byte(renderList(p, keep)), 0o640, dnsOwner(env))
		set.Remove(file)
		applied, err := set.Commit(func() error { return checkconf(ctx, env) })
		if err != nil {
			return nil, host.FailWithValidation("The zone was not removed", err)
		}
		if running(ctx, env) {
			if err := rndc(ctx, env, "reconfig"); err != nil {
				applied.Rollback()
				rndc(ctx, env, "reconfig")
				return nil, actions.Failed("BIND did not reload; the zone was restored.", err.Error())
			}
		}
		return map[string]any{"zone": z.Name, "deleted": true}, nil
	}})
}

// Configured reports whether named loads WhaCP's zone list.
func Configured(env *host.Env) bool { return configured(env, dnsPaths(env)) }
