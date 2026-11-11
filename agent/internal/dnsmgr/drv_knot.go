package dnsmgr

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// knotDriver runs the zones with Knot DNS (CZ.NIC). Knot's configuration has
// no safe way to be extended in part (its listen addresses and templates are
// in the one file), so NimVor owns knot.conf while Knot is chosen and keeps
// the original next to it as knot.conf.pre-nimvor.
type knotDriver struct{}

func (knotDriver) id() string                                      { return "knot" }
func (knotDriver) label() string                                   { return "Knot DNS" }
func (knotDriver) packages() []string                              { return []string{"knot", "knot-dnssecutils"} }
func (knotDriver) installed(env *host.Env) bool                    { return env.Root.Exists("/usr/sbin/knotd") }
func (knotDriver) unit(env *host.Env) string                       { return env.M.KnotUnit }
func (knotDriver) account(env *host.Env) string                    { return "knot" }
func (knotDriver) confFile(env *host.Env) string                   { return env.M.KnotConf }
func (knotDriver) mainConf(env *host.Env) string                   { return env.M.KnotConf }
func (knotDriver) listName() string                                { return "zones-knot.conf" }
func (knotDriver) wholeFile() bool                                 { return true }
func (knotDriver) dnssec() dnssecOps                               { return knotSigning{} }
func (knotDriver) prepare(context.Context, *host.Env, paths) error { return nil }
func (knotDriver) afterApply(context.Context, *host.Env, paths, string, []string) error {
	return nil
}

func (knotDriver) advantages() []string {
	return []string{"Fast, and handles many zones well.", "Signs zones itself (DNSSEC) and rolls keys over by itself.", "Loads new zones and settings without a restart.", "Uses little memory."}
}
func (knotDriver) disadvantages() []string {
	return []string{"NimVor replaces its configuration file (the original is kept next to it).", "Is not a caching resolver."}
}
func (knotDriver) notes() []string {
	return []string{"Signing is part of the server; no extra tool is needed beyond its key manager."}
}

func (d knotDriver) wire(env *host.Env, p paths, cur string) (string, bool) {
	listen := `[ "0.0.0.0@53", "::@53" ]`
	if !hasIPv6(env) {
		listen = `[ "0.0.0.0@53" ]`
	}
	if addrs := listenAddresses(env); addrs != nil {
		// systemd-resolved holds 127.0.0.53:53: bind the machine's own addresses instead of all of them.
		items := make([]string, len(addrs))
		for i, a := range addrs {
			items[i] = `"` + a + `@53"`
		}
		listen = "[ " + strings.Join(items, ", ") + " ]"
	}
	conf := fmt.Sprintf(`# Managed by NimVor (DNS). Do not edit: changes are overwritten.
# The configuration this server had before is kept as %s.pre-nimvor.
server:
    rundir: "/run/knot"
    user: knot:knot
    listen: %s

log:
  - target: syslog
    any: info

database:
    storage: "/var/lib/knot"
    kasp-db: "/var/lib/knot/keys"

policy:
  - id: nimvor
    algorithm: ecdsap256sha256

template:
  - id: nimvor
    storage: "%s"
    file: "db.%%s"
    zonefile-sync: -1
    zonefile-load: whole
    journal-content: none
    semantic-checks: on
  - id: nimvor-signed
    storage: "%s"
    file: "db.%%s"
    zonefile-sync: -1
    zonefile-load: whole
    journal-content: none
    semantic-checks: on
    dnssec-signing: on
    dnssec-policy: nimvor

include: "%s"
`, p.conf, listen, p.dir, p.dir, p.list)
	return conf, strings.Contains(cur, "Managed by NimVor") && strings.Contains(cur, fmt.Sprintf("include: \"%s\"", p.list))
}

func (knotDriver) renderList(env *host.Env, p paths, zones []zoneInfo, signed map[string]bool) string {
	var b strings.Builder
	b.WriteString("# Managed by NimVor (DNS). Do not edit: changes are overwritten.\n")
	// One remote and one transfer rule per secondary address.
	seen := map[string]bool{}
	var ips []string
	for _, z := range zones {
		for _, s := range z.Secondaries {
			if !seen[s] {
				seen[s] = true
				ips = append(ips, s)
			}
		}
	}
	sort.Strings(ips)
	id := func(ip string) string { return "nimvor-" + strings.NewReplacer(".", "-", ":", "-").Replace(ip) }
	if len(ips) > 0 {
		b.WriteString("remote:\n")
		for _, ip := range ips {
			fmt.Fprintf(&b, "  - id: %s\n    address: %s\n", id(ip), ip)
		}
		b.WriteString("acl:\n")
		for _, ip := range ips {
			fmt.Fprintf(&b, "  - id: %s\n    address: %s\n    action: transfer\n", id(ip), ip)
		}
	}
	b.WriteString("zone:\n")
	for _, z := range zones {
		tpl := "nimvor"
		if signed[z.Name] {
			tpl = "nimvor-signed"
		}
		fmt.Fprintf(&b, "  - domain: \"%s\"\n    template: %s\n", z.Name, tpl)
		if len(z.Secondaries) > 0 {
			ids := make([]string, len(z.Secondaries))
			for i, s := range z.Secondaries {
				ids[i] = id(s)
			}
			fmt.Fprintf(&b, "    notify: [ %s ]\n    acl: [ %s ]\n", strings.Join(ids, ", "), strings.Join(ids, ", "))
		}
	}
	return b.String()
}

func (d knotDriver) checkConf(ctx context.Context, env *host.Env, p paths) error {
	out, err := env.Runner.Run(ctx, "knotc", "-c", d.confFile(env), "conf-check")
	if err != nil {
		return fmt.Errorf("knotc conf-check: %s", host.Why(out, err))
	}
	return nil
}

func (knotDriver) checkZone(ctx context.Context, env *host.Env, p paths, zone, file string) error {
	out, err := env.Runner.Run(ctx, "kzonecheck", "-o", zone, file)
	if err != nil {
		return fmt.Errorf("kzonecheck: %s", host.Why(out, err))
	}
	return nil
}

func (d knotDriver) running(ctx context.Context, env *host.Env) bool {
	return env.UnitActive(ctx, d.unit(env))
}

func (knotDriver) activate(ctx context.Context, env *host.Env, zone string, found, listChanged bool) error {
	if listChanged || !found {
		if out, err := env.Runner.Run(ctx, "knotc", "reload"); err != nil {
			return fmt.Errorf("knotc reload: %s", host.Why(out, err))
		}
	}
	if found && zone != "" {
		// Forced and blocking: Knot skips a reload when the file's time is
		// unchanged (two writes within one second) unless forced.
		if out, err := env.Runner.Run(ctx, "knotc", "-b", "-f", "zone-reload", zone); err != nil {
			return fmt.Errorf("knotc zone-reload: %s", host.Why(out, err))
		}
	}
	return nil
}

// knotSigning: Knot signs a zone by policy; the zone file stays unsigned (it
// is read again whole each time) and the keys live in Knot's own database.
type knotSigning struct{}

func (knotSigning) supported(ctx context.Context, env *host.Env) (bool, string) {
	if !env.Runner.Available("keymgr") && !env.Cfg.SimulateCommands {
		return false, "Knot's key manager (keymgr) is not installed."
	}
	return true, ""
}

func (knotSigning) signedDirs(p paths) []string { return nil }

func (knotSigning) enable(ctx context.Context, env *host.Env, p paths, zone string) error {
	return nil
}

func (knotSigning) disable(ctx context.Context, env *host.Env, p paths, zone string) error {
	return nil
}

func (knotSigning) ds(ctx context.Context, env *host.Env, p paths, zone string) ([]DS, error) {
	out, err := env.Runner.Run(ctx, "keymgr", "-c", p.conf, zone, "ds")
	if err != nil {
		return nil, fmt.Errorf("keymgr: %s", host.Why(out, err))
	}
	return parseDS(string(out)), nil
}
