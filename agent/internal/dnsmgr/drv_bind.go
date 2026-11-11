package dnsmgr

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// bindDriver runs the zones with BIND (named), NimVor's original and default
// nameserver: one include line in the package's configuration file.
type bindDriver struct{}

func (bindDriver) id() string                                      { return "bind" }
func (bindDriver) label() string                                   { return "BIND" }
func (bindDriver) packages() []string                              { return []string{"bind9"} }
func (bindDriver) installed(env *host.Env) bool                    { return env.Root.Exists("/usr/sbin/named") }
func (bindDriver) unit(env *host.Env) string                       { return env.M.DNSUnit }
func (bindDriver) account(env *host.Env) string                    { return env.M.DNSUser }
func (bindDriver) confFile(env *host.Env) string                   { return env.M.DNSConfFile }
func (bindDriver) mainConf(env *host.Env) string                   { return env.M.DNSConfFile }
func (bindDriver) listName() string                                { return "zones.conf" }
func (bindDriver) wholeFile() bool                                 { return false }
func (bindDriver) dnssec() dnssecOps                               { return bindSigning{} }
func (bindDriver) prepare(context.Context, *host.Env, paths) error { return nil }
func (bindDriver) afterApply(context.Context, *host.Env, paths, string, []string) error {
	return nil
}

func (bindDriver) advantages() []string {
	return []string{"Its configuration can be edited by hand.", "Highly configurable.", "Can also act as a caching resolver.", "Signs zones itself (DNSSEC, BIND 9.16 or newer)."}
}
func (bindDriver) disadvantages() []string { return []string{"Uses more memory than the others."} }
func (bindDriver) notes() []string {
	return []string{"The longest-established choice, and the one NimVor started with."}
}

func (d bindDriver) wire(env *host.Env, p paths, cur string) (string, bool) {
	line := fmt.Sprintf("include \"%s\"; // NimVor DNS zones (added by NimVor)", p.list)
	if strings.Contains(cur, fmt.Sprintf("include \"%s\";", p.list)) {
		return cur, true
	}
	return strings.TrimRight(openListen(cur), "\n") + "\n\n" + line + "\n", false
}

func (bindDriver) renderList(env *host.Env, p paths, zones []zoneInfo, signed map[string]bool) string {
	var b strings.Builder
	b.WriteString("// Managed by NimVor (DNS). Do not edit: changes are overwritten.\n")
	if len(signed) > 0 {
		b.WriteString("dnssec-policy \"nimvor\" {\n\tkeys { csk lifetime unlimited algorithm ecdsap256sha256; };\n};\n")
	}
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
		if signed[z.Name] {
			fmt.Fprintf(&b, "\tdnssec-policy \"nimvor\";\n\tinline-signing yes;\n\tkey-directory \"%s\";\n", p.keysDir())
		}
		b.WriteString("};\n")
	}
	return b.String()
}

func (bindDriver) checkConf(ctx context.Context, env *host.Env, p paths) error {
	return checkconf(ctx, env)
}

func (bindDriver) checkZone(ctx context.Context, env *host.Env, p paths, zone, file string) error {
	out, err := env.Runner.Run(ctx, "named-checkzone", "-i", "local", zone, file)
	if err != nil {
		return fmt.Errorf("named-checkzone: %s", host.Why(out, err))
	}
	return nil
}

func (bindDriver) running(ctx context.Context, env *host.Env) bool { return running(ctx, env) }

func (bindDriver) activate(ctx context.Context, env *host.Env, zone string, found, listChanged bool) error {
	if err := rndc(ctx, env, "reconfig"); err != nil {
		return err
	}
	if found && zone != "" {
		return rndc(ctx, env, "reload", zone)
	}
	return nil
}

var bindVersionRe = regexp.MustCompile(`BIND\s+(\d+)\.(\d+)`)

// bindSigning signs with BIND's own dnssec-policy and inline signing (9.16+):
// the zone file stays unsigned, the signed copy is BIND's.
type bindSigning struct{}

func (bindSigning) supported(ctx context.Context, env *host.Env) (bool, string) {
	out, err := env.Runner.Run(ctx, "named", "-v")
	if err != nil {
		return env.Cfg.SimulateCommands, "BIND's version could not be read."
	}
	m := bindVersionRe.FindStringSubmatch(string(out))
	if m == nil {
		return false, "BIND's version could not be read."
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < 9 || (major == 9 && minor < 16) {
		return false, "BIND " + m[1] + "." + m[2] + " cannot sign zones by policy; BIND 9.16 or newer can."
	}
	return true, ""
}

// BIND writes the signed copy and the keys: its folder must be writable.
func (bindSigning) signedDirs(p paths) []string { return []string{p.dir, p.keysDir()} }

func (bindSigning) enable(ctx context.Context, env *host.Env, p paths, zone string) error {
	return nil // the zone list carries the policy; BIND creates the keys
}

func (bindSigning) disable(ctx context.Context, env *host.Env, p paths, zone string) error {
	return nil // the keys stay for a later start
}

func (bindSigning) ds(ctx context.Context, env *host.Env, p paths, zone string) ([]DS, error) {
	keys, _ := env.Root.Glob(p.keysDir() + "/K" + zone + ".+*.key")
	var out strings.Builder
	for _, k := range keys {
		b, err := env.Root.ReadFile(k)
		if err != nil || !strings.Contains(string(b), " DNSKEY 257 ") {
			continue // only the key-signing (or combined) key makes a DS
		}
		real, err := env.Root.Path(k)
		if err != nil {
			continue
		}
		for _, alg := range []string{"SHA-256", "SHA-384"} {
			res, err := env.Runner.Run(ctx, "dnssec-dsfromkey", "-a", alg, real)
			if err == nil {
				out.Write(res)
			}
		}
	}
	return parseDS(out.String()), nil
}

// before lets named write next to the zone files on systems where AppArmor
// confines it (Debian, Ubuntu): one line in the profile's local override,
// added once, then the profile is reloaded. Best effort: where it fails BIND
// reports why when it cannot sign and NimVor restores the previous state.
func (bindSigning) before(ctx context.Context, env *host.Env, p paths) error {
	const local = "/etc/apparmor.d/local/usr.sbin.named"
	b, err := env.Root.ReadFile(local)
	if err != nil {
		return nil // no AppArmor profile for named
	}
	line := p.dir + "/** rwk,"
	if strings.Contains(string(b), line) {
		return nil
	}
	content := strings.TrimRight(string(b), "\n") + "\n# NimVor DNSSEC: named signs zones in NimVor's zone folder.\n" + line + "\n"
	if _, err := env.Writer.WriteFile(local, []byte(content), 0o644, agentfs.RootOwner); err != nil {
		return err
	}
	if env.Root.Exists("/etc/apparmor.d/usr.sbin.named") {
		if out, err := env.Runner.Run(ctx, "apparmor_parser", "-r", "/etc/apparmor.d/usr.sbin.named"); err != nil {
			return fmt.Errorf("apparmor_parser: %s", host.Why(out, err))
		}
	}
	return nil
}
