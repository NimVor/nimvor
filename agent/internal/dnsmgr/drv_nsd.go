package dnsmgr

import (
	"context"
	"fmt"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// nsdDriver runs the zones with NSD (NLnet Labs): an authoritative-only
// server. Zones are listed in a file NimVor includes from nsd.conf.
type nsdDriver struct{}

func (nsdDriver) id() string                                      { return "nsd" }
func (nsdDriver) label() string                                   { return "NSD" }
func (nsdDriver) packages() []string                              { return []string{"nsd"} }
func (nsdDriver) installed(env *host.Env) bool                    { return env.Root.Exists("/usr/sbin/nsd") }
func (nsdDriver) unit(env *host.Env) string                       { return env.M.NSDUnit }
func (nsdDriver) account(env *host.Env) string                    { return "nsd" }
func (nsdDriver) confFile(env *host.Env) string                   { return env.M.NSDConf }
func (nsdDriver) mainConf(env *host.Env) string                   { return env.M.NSDMain }
func (nsdDriver) listName() string                                { return "zones-nsd.conf" }
func (nsdDriver) wholeFile() bool                                 { return false }
func (nsdDriver) dnssec() dnssecOps                               { return nil }
func (nsdDriver) prepare(context.Context, *host.Env, paths) error { return nil }
func (nsdDriver) afterApply(context.Context, *host.Env, paths, string, []string) error {
	return nil
}

func (nsdDriver) advantages() []string {
	return []string{"Very high performance.", "Small and simple: it only answers for the zones it hosts.", "Low memory requirements.", "Designed for the security of an authoritative server."}
}
func (nsdDriver) disadvantages() []string {
	return []string{"Does not sign zones: DNSSEC needs the zones to be signed elsewhere.", "Does not provide a recursive (caching) nameserver.", "A new or removed zone restarts the service (a changed zone does not)."}
}
func (nsdDriver) notes() []string { return []string{"No DNSSEC support in NimVor with NSD."} }

func (d nsdDriver) wire(env *host.Env, p paths, cur string) (string, bool) {
	include := fmt.Sprintf("include: \"%s\"", p.list)
	if env.M.NSDDropIn {
		return "# Managed by NimVor (DNS). Do not edit: changes are overwritten.\n" + include + "\n", strings.Contains(cur, include)
	}
	return withBlock(cur, include)
}

func (nsdDriver) renderList(env *host.Env, p paths, zones []zoneInfo, signed map[string]bool) string {
	var b strings.Builder
	b.WriteString("# Managed by NimVor (DNS). Do not edit: changes are overwritten.\n")
	for _, z := range zones {
		fmt.Fprintf(&b, "zone:\n\tname: \"%s\"\n\tzonefile: \"%s\"\n", z.Name, p.zoneFile(z.Name))
		for _, s := range z.Secondaries {
			fmt.Fprintf(&b, "\tprovide-xfr: %s NOKEY\n\tnotify: %s NOKEY\n", s, s)
		}
	}
	return b.String()
}

func (nsdDriver) checkConf(ctx context.Context, env *host.Env, p paths) error {
	out, err := env.Runner.Run(ctx, "nsd-checkconf", env.M.NSDMain)
	if err != nil {
		return fmt.Errorf("nsd-checkconf: %s", host.Why(out, err))
	}
	return nil
}

func (nsdDriver) checkZone(ctx context.Context, env *host.Env, p paths, zone, file string) error {
	out, err := env.Runner.Run(ctx, "nsd-checkzone", zone, file)
	if err != nil {
		return fmt.Errorf("nsd-checkzone: %s", host.Why(out, err))
	}
	return nil
}

func (d nsdDriver) running(ctx context.Context, env *host.Env) bool {
	return env.UnitActive(ctx, d.unit(env))
}

// A changed zone is read again on a reload; a new or removed zone (or new
// secondaries) needs NSD to read its configuration again, which a restart
// does.
func (d nsdDriver) activate(ctx context.Context, env *host.Env, zone string, found, listChanged bool) error {
	if found && !listChanged && zone != "" {
		return env.Systemctl(ctx, "reload", d.unit(env))
	}
	return env.Systemctl(ctx, "restart", d.unit(env))
}
