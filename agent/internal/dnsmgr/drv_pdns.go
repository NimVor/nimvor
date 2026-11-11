package dnsmgr

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
)

// pdnsDriver runs the zones with the PowerDNS Authoritative Server, using
// its BIND backend: it reads the same zone files, listed in a BIND-style
// zone list. DNSSEC keys and per-zone metadata live in a small SQLite
// database NimVor creates next to PowerDNS' own files.
type pdnsDriver struct{}

func (pdnsDriver) id() string    { return "powerdns" }
func (pdnsDriver) label() string { return "PowerDNS" }
func (pdnsDriver) packages() []string {
	// Debian and Ubuntu ship the BIND backend apart from the server; without it pdns_server does not know the
	// bind-* settings. (The same package as the server on AlmaLinux and Rocky.)
	return []string{"pdns-server", "pdns-backend-bind"}
}

// complete reports whether the packages the BIND backend needs are all installed.
func (d pdnsDriver) complete(ctx context.Context, env *host.Env) bool {
	return len(pkgs.Installed(ctx, env, d.packages())) == len(d.packages())
}
func (pdnsDriver) installed(env *host.Env) bool  { return env.Root.Exists("/usr/sbin/pdns_server") }
func (pdnsDriver) unit(env *host.Env) string     { return env.M.PDNSUnit }
func (pdnsDriver) account(env *host.Env) string  { return "pdns" }
func (pdnsDriver) confFile(env *host.Env) string { return env.M.PDNSConf }
func (pdnsDriver) mainConf(env *host.Env) string { return env.M.PDNSConf }
func (pdnsDriver) listName() string              { return "zones-pdns.conf" }
func (pdnsDriver) wholeFile() bool               { return false }
func (pdnsDriver) dnssec() dnssecOps             { return pdnsSigning{} }

func (pdnsDriver) advantages() []string {
	return []string{"Answers quickly, even with many zones.", "Starts almost at once.", "Uses little memory.", "Signs zones itself (DNSSEC); keys are kept in a small database."}
}
func (pdnsDriver) disadvantages() []string {
	return []string{"Is not a caching resolver: this server needs another resolver in resolv.conf.", "Signing many zones takes noticeable CPU."}
}
func (pdnsDriver) notes() []string {
	return []string{"Runs with its BIND backend: it reads the same zone files as the others.", "On Debian and Ubuntu NimVor also installs pdns-backend-bind."}
}

func dnssecDB(env *host.Env) string { return env.M.PDNSStateDir + "/nimvor-dnssec.db" }

var launchBindRe = regexp.MustCompile(`(?m)^\s*launch\s*\+?=.*\bbind\b`)

func (d pdnsDriver) wire(env *host.Env, p paths, cur string) (string, bool) {
	settings := fmt.Sprintf("bind-config=%s\nbind-dnssec-db=%s\nbind-check-interval=0\n", p.list, dnssecDB(env))
	if addrs := listenAddresses(env); addrs != nil {
		// systemd-resolved holds 127.0.0.53:53: bind the machine's own addresses instead of all of them.
		settings += "local-address=" + strings.Join(addrs, ", ") + "\n"
	} else if !hasIPv6(env) {
		settings += "local-address=0.0.0.0\n"
	}
	wired := func(c string) bool { return strings.Contains(c, "bind-config="+p.list) }
	if env.M.PDNSDropIn {
		// Debian's own drop-in already launches the BIND backend (a second
		// launch of it is an error); this one sorts after it and wins.
		content := "# Managed by NimVor (DNS). Do not edit: changes are overwritten.\n" + settings
		return content, wired(cur)
	}
	if !launchBindRe.MatchString(strings.ReplaceAll(cur, blockBegin, "")) || hasBlock(cur) {
		settings = "launch+=bind\n" + settings
	}
	next, _ := withBlock(cur, settings)
	return next, hasBlock(cur) && wired(cur)
}

func (pdnsDriver) renderList(env *host.Env, p paths, zones []zoneInfo, signed map[string]bool) string {
	var b strings.Builder
	b.WriteString("// Managed by NimVor (DNS). Do not edit: changes are overwritten.\n")
	for _, z := range zones {
		fmt.Fprintf(&b, "zone \"%s\" {\n\ttype master;\n\tfile \"%s\";\n};\n", z.Name, p.zoneFile(z.Name))
	}
	return b.String()
}

// prepare creates the DNSSEC database PowerDNS' BIND backend keeps its keys
// and zone metadata in. pdnsutil refuses an existing file and runs as root, so
// it writes a scratch file that is then placed with the daemon's ownership.
func (d pdnsDriver) prepare(ctx context.Context, env *host.Env, p paths) error {
	db := dnssecDB(env)
	if env.Root.Exists(db) {
		return nil
	}
	if env.Cfg.SimulateCommands {
		return nil
	}
	owner := agentfs.RootOwner
	if u, err := env.LookupUser("pdns"); err == nil {
		owner = agentfs.Owner{UID: u.UID, GID: u.GID}
	}
	// pdnsutil cannot open a database in a folder that is not there ("Could not connect to the SQLite
	// database"); some packages do not create PowerDNS' state folder until the daemon first runs.
	if _, err := env.Writer.MkdirAll(env.M.PDNSStateDir, 0o755, owner); err != nil {
		return fmt.Errorf("creating %s: %w", env.M.PDNSStateDir, err)
	}
	scratch := db + ".new"
	real, err := env.Root.Path(scratch)
	if err != nil {
		return err
	}
	env.Writer.Remove(scratch) // left by an earlier attempt
	if out, err := env.Runner.Run(ctx, "pdnsutil", "create-bind-db", real); err != nil {
		return fmt.Errorf("pdnsutil create-bind-db: %s", host.Why(out, err))
	}
	data, err := env.Root.ReadFile(scratch)
	if err != nil {
		return err
	}
	_, err = env.Writer.WriteFile(db, data, 0o640, owner)
	env.Writer.Remove(scratch)
	return err
}

func (pdnsDriver) checkConf(ctx context.Context, env *host.Env, p paths) error {
	out, err := env.Runner.Run(ctx, "pdns_server", "--config=check")
	if err != nil {
		return fmt.Errorf("pdns_server --config=check: %s", host.Why(out, err))
	}
	return nil
}

func (pdnsDriver) checkZone(ctx context.Context, env *host.Env, p paths, zone, file string) error {
	out, err := env.Runner.Run(ctx, "pdnsutil", "check-zone", zone)
	if err != nil {
		return fmt.Errorf("pdnsutil check-zone: %s", host.Why(out, err))
	}
	// check-zone reports problems on its output and still exits 0 for some.
	if s := string(out); strings.Contains(s, "[Error]") || regexp.MustCompile(`, [1-9]\d* errors`).MatchString(s) {
		return fmt.Errorf("pdnsutil check-zone: %s", host.Why(out, nil))
	}
	return nil
}

func (d pdnsDriver) running(ctx context.Context, env *host.Env) bool {
	return env.UnitActive(ctx, d.unit(env))
}

func pdnsControl(ctx context.Context, env *host.Env, args ...string) error {
	out, err := env.Runner.Run(ctx, "pdns_control", args...)
	if err != nil {
		return fmt.Errorf("pdns_control %s: %s", strings.Join(args, " "), host.Why(out, err))
	}
	return nil
}

func (pdnsDriver) activate(ctx context.Context, env *host.Env, zone string, found, listChanged bool) error {
	if listChanged || !found {
		if err := pdnsControl(ctx, env, "reload"); err != nil {
			return err
		}
	}
	if found && zone != "" {
		return pdnsControl(ctx, env, "bind-reload-now", zone)
	}
	return nil
}

// afterApply keeps a zone's secondaries as PowerDNS metadata: who may
// transfer it and who is told when it changes.
func (pdnsDriver) afterApply(ctx context.Context, env *host.Env, p paths, zone string, secondaries []string) error {
	if env.Cfg.SimulateCommands {
		return nil
	}
	for _, kind := range []string{"ALLOW-AXFR-FROM", "ALSO-NOTIFY"} {
		args := []string{"set-meta", zone, kind}
		if len(secondaries) == 0 {
			args = []string{"delete-meta", zone, kind}
		} else {
			args = append(args, secondaries...)
		}
		if out, err := env.Runner.Run(ctx, "pdnsutil", args...); err != nil && len(secondaries) > 0 {
			return fmt.Errorf("pdnsutil %s: %s", args[0], host.Why(out, err))
		}
	}
	return nil
}

// pdnsSigning: `pdnsutil secure-zone` creates a combined signing key and
// PowerDNS signs the zone on the fly; the zone file stays unsigned.
type pdnsSigning struct{}

func (pdnsSigning) supported(ctx context.Context, env *host.Env) (bool, string) {
	if !env.Runner.Available("pdnsutil") && !env.Cfg.SimulateCommands {
		return false, "PowerDNS' pdnsutil is not installed."
	}
	return true, ""
}

func (pdnsSigning) signedDirs(p paths) []string { return nil }

func (pdnsSigning) enable(ctx context.Context, env *host.Env, p paths, zone string) error {
	if out, err := env.Runner.Run(ctx, "pdnsutil", "secure-zone", zone); err != nil {
		return fmt.Errorf("pdnsutil secure-zone: %s", host.Why(out, err))
	}
	if out, err := env.Runner.Run(ctx, "pdnsutil", "rectify-zone", zone); err != nil {
		return fmt.Errorf("pdnsutil rectify-zone: %s", host.Why(out, err))
	}
	return pdnsControl(ctx, env, "bind-reload-now", zone)
}

func (pdnsSigning) disable(ctx context.Context, env *host.Env, p paths, zone string) error {
	if out, err := env.Runner.Run(ctx, "pdnsutil", "disable-dnssec", zone); err != nil {
		return fmt.Errorf("pdnsutil disable-dnssec: %s", host.Why(out, err))
	}
	return pdnsControl(ctx, env, "bind-reload-now", zone)
}

func (pdnsSigning) ds(ctx context.Context, env *host.Env, p paths, zone string) ([]DS, error) {
	out, err := env.Runner.Run(ctx, "pdnsutil", "export-zone-ds", zone)
	if err != nil {
		return nil, fmt.Errorf("pdnsutil export-zone-ds: %s", host.Why(out, err))
	}
	return parseDS(string(out)), nil
}
