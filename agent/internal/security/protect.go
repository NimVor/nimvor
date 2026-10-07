package security

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Firewall protections in the manner of CSF/LFD (SYNFLOOD, CONNLIMIT,
// PORTFLOOD, PACKET_FILTER, ICMP_IN_RATE), kept next to the firewall
// (UFW or firewalld), which stays in charge: the table inet whacp_flood
// only drops or rejects, so it never opens what the firewall closes. Its
// chain runs before the firewall's; an exempt address skips this table
// only, the firewall still applies to it. Changes are guarded like
// firewall rules: undone unless confirmed.

const (
	floodTable    = "whacp_flood"
	FloodUnit     = "whacp-flood.service"
	floodUnitPath = "/etc/systemd/system/" + FloodUnit
	// FloodConfigFile is the applied configuration (restored with the
	// rules when a change is undone).
	FloodConfigFile = "/etc/whacp/flood.json"
)

// PortLimit limits one port per address: Max connections at once
// (connection limits) or Max new connections per minute (port flood).
type PortLimit struct {
	Port  int    `json:"port"`
	Proto string `json:"proto"` // tcp or udp (rate limits); tcp for connection limits
	Max   int    `json:"max"`
}

// Protection is the wanted configuration.
type Protection struct {
	Enabled bool `json:"enabled"`
	// Invalid drops packets of no known connection and impossible TCP flag
	// combinations (port scans: NULL, Xmas, SYN+FIN, SYN+RST).
	Invalid bool `json:"invalid"`
	// SynRate: new TCP connections per second one address may open (0: off).
	SynRate int `json:"syn_rate"`
	// ConnLimits: connections at once per address and port.
	ConnLimits []PortLimit `json:"conn_limits"`
	// PortFlood: new connections per minute per address and port.
	PortFlood []PortLimit `json:"port_flood"`
	// ICMPRate: pings per second one address may send (0: off).
	ICMPRate int `json:"icmp_rate"`
	// Exempt addresses and networks skip these protections.
	Exempt []string `json:"exempt"`
}

// Limits of a configuration.
const (
	maxPortLimits = 50
	maxExempt     = 500
)

// Normalize validates the configuration and sorts it.
func (p *Protection) Normalize() error {
	if p.SynRate < 0 || p.SynRate > 100000 {
		return fmt.Errorf("SYN flood: 1 to 100000 new connections per second (0: off)")
	}
	if p.ICMPRate < 0 || p.ICMPRate > 10000 {
		return fmt.Errorf("ping: 1 to 10000 per second (0: off)")
	}
	if len(p.ConnLimits) > maxPortLimits || len(p.PortFlood) > maxPortLimits {
		return fmt.Errorf("at most %d port limits of each kind", maxPortLimits)
	}
	check := func(what string, l []PortLimit, maxMax int, udp bool) ([]PortLimit, error) {
		seen := map[string]bool{}
		out := []PortLimit{}
		for _, x := range l {
			if x.Proto == "" {
				x.Proto = "tcp"
			}
			if x.Proto != "tcp" && (!udp || x.Proto != "udp") {
				return nil, fmt.Errorf("%s: protocol %q", what, x.Proto)
			}
			if x.Port < 1 || x.Port > 65535 {
				return nil, fmt.Errorf("%s: port %d", what, x.Port)
			}
			if x.Max < 1 || x.Max > maxMax {
				return nil, fmt.Errorf("%s: port %d: 1 to %d", what, x.Port, maxMax)
			}
			k := x.Proto + strconv.Itoa(x.Port)
			if seen[k] {
				return nil, fmt.Errorf("%s: port %d/%s twice", what, x.Port, x.Proto)
			}
			seen[k] = true
			out = append(out, x)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Port != out[j].Port {
				return out[i].Port < out[j].Port
			}
			return out[i].Proto < out[j].Proto
		})
		return out, nil
	}
	var err error
	if p.ConnLimits, err = check("connection limit", p.ConnLimits, 100000, false); err != nil {
		return err
	}
	if p.PortFlood, err = check("port flood", p.PortFlood, 1000000, true); err != nil {
		return err
	}
	if len(p.Exempt) > maxExempt {
		return fmt.Errorf("at most %d exempt addresses", maxExempt)
	}
	ex := []string{}
	seen := map[string]bool{}
	for _, e := range p.Exempt {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		pf, err := netip.ParsePrefix(e)
		if err != nil {
			a, aerr := netip.ParseAddr(e)
			if aerr != nil {
				return fmt.Errorf("exempt: %q is not an IP address or network", e)
			}
			pf = netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
		}
		pf = pf.Masked()
		s := pf.String()
		if pf.Bits() == pf.Addr().BitLen() {
			s = pf.Addr().String()
		}
		if !seen[s] {
			seen[s] = true
			ex = append(ex, s)
		}
	}
	sort.Strings(ex)
	p.Exempt = ex
	return nil
}

func floodSet(name, typ, flags, extra string, elems []string) string {
	s := "\tset " + name + " {\n\t\ttype " + typ + "\n\t\tflags " + flags + "\n" + extra
	if len(elems) > 0 {
		s += "\t\telements = { " + strings.Join(elems, ", ") + " }\n"
	}
	return s + "\t}\n"
}

// FloodRules renders the table (a configuration that is off removes it).
// Each rule's comment names the protection, for the counters.
func FloodRules(p Protection) string {
	var b strings.Builder
	b.WriteString("# Managed by WhaCP: firewall protections (Security → Firewall → Protection).\n")
	b.WriteString("table inet " + floodTable + "\ndelete table inet " + floodTable + "\n")
	if !p.Enabled {
		return b.String()
	}
	var ex4, ex6 []string
	for _, e := range p.Exempt {
		if strings.Contains(e, ":") {
			ex6 = append(ex6, e)
		} else {
			ex4 = append(ex4, e)
		}
	}
	dyn := "\t\tsize 65535\n"
	dynT := "\t\ttimeout 1m\n\t\tsize 65535\n"
	b.WriteString("table inet " + floodTable + " {\n")
	b.WriteString(floodSet("exempt4", "ipv4_addr", "interval", "", ex4))
	b.WriteString(floodSet("exempt6", "ipv6_addr", "interval", "", ex6))
	var r strings.Builder
	if p.Invalid {
		r.WriteString("\t\tct state invalid counter drop comment \"invalid\"\n")
		for _, f := range []string{"tcp flags & (fin|syn) == fin|syn", "tcp flags & (syn|rst) == syn|rst", "tcp flags & (fin|syn|rst|psh|ack|urg) == 0x0", "tcp flags & (fin|psh|urg) == fin|psh|urg"} {
			fmt.Fprintf(&r, "\t\t%s counter drop comment \"invalid\"\n", f)
		}
	}
	rule := func(fam, set, match, limit, action, comment string) {
		saddr := "ip saddr"
		if fam == "6" {
			saddr = "ip6 saddr"
		}
		fmt.Fprintf(&r, "\t\t%s add @%s%s { %s %s } counter %s comment %q\n", match, set, fam, saddr, limit, action, comment)
	}
	if p.SynRate > 0 {
		for _, fam := range []string{"4", "6"} {
			b.WriteString(floodSet("syn"+fam, "ipv"+fam+"_addr", "dynamic,timeout", dynT, nil))
			rule(fam, "syn", "tcp flags & (syn|ack) == syn ct state new", fmt.Sprintf("limit rate over %d/second burst %d packets", p.SynRate, p.SynRate*2), "drop", "syn")
		}
	}
	for _, l := range p.ConnLimits {
		for _, fam := range []string{"4", "6"} {
			name := fmt.Sprintf("conn%d_", l.Port)
			b.WriteString(floodSet(name+fam, "ipv"+fam+"_addr", "dynamic", dyn, nil))
			rule(fam, name, fmt.Sprintf("tcp dport %d ct state new", l.Port), fmt.Sprintf("ct count over %d", l.Max), "reject with tcp reset", fmt.Sprintf("conn:%d", l.Port))
		}
	}
	for _, l := range p.PortFlood {
		for _, fam := range []string{"4", "6"} {
			name := fmt.Sprintf("rate%s%d_", l.Proto, l.Port)
			b.WriteString(floodSet(name+fam, "ipv"+fam+"_addr", "dynamic,timeout", dynT, nil))
			burst := l.Max
			rule(fam, name, fmt.Sprintf("%s dport %d ct state new", l.Proto, l.Port), fmt.Sprintf("limit rate over %d/minute burst %d packets", l.Max, burst), "drop", fmt.Sprintf("rate:%s/%d", l.Proto, l.Port))
		}
	}
	if p.ICMPRate > 0 {
		b.WriteString(floodSet("icmp4", "ipv4_addr", "dynamic,timeout", dynT, nil))
		b.WriteString(floodSet("icmp6", "ipv6_addr", "dynamic,timeout", dynT, nil))
		limit := fmt.Sprintf("limit rate over %d/second burst %d packets", p.ICMPRate, p.ICMPRate*2)
		rule("4", "icmp", "icmp type echo-request", limit, "drop", "icmp")
		rule("6", "icmp", "icmpv6 type echo-request", limit, "drop", "icmp")
	}
	b.WriteString("\tchain input {\n\t\ttype filter hook input priority -4; policy accept;\n")
	b.WriteString("\t\tiifname \"lo\" accept\n\t\tip saddr @exempt4 accept\n\t\tip6 saddr @exempt6 accept\n")
	b.WriteString(r.String())
	b.WriteString("\t}\n}\n")
	return b.String()
}

func floodUnit() string {
	nft := "/usr/sbin/nft"
	return "# Managed by WhaCP: firewall protections.\n[Unit]\nDescription=WhaCP firewall protections (nftables)\nAfter=network-pre.target\nBefore=network.target\n\n" +
		"[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=" + nft + " -f " + execx.NftFloodFile + "\nExecReload=" + nft + " -f " + execx.NftFloodFile +
		"\nExecStop=-" + nft + " delete table inet " + floodTable + "\n\n[Install]\nWantedBy=multi-user.target\n"
}

// LoadProtection reads the applied configuration (off when there is none).
func LoadProtection(env *host.Env) Protection {
	p := Protection{ConnLimits: []PortLimit{}, PortFlood: []PortLimit{}, Exempt: []string{}}
	if b, err := env.Root.ReadFile(FloodConfigFile); err == nil {
		json.Unmarshal(b, &p)
	}
	return p
}

var floodCounterRe = regexp.MustCompile(`counter packets (\d+) bytes \d+ .*comment "([^"]+)"`)

// FloodCounters sums the packets each protection dropped since it was
// loaded, by comment ("syn", "invalid", "icmp", "conn:22", "rate:tcp/80").
func FloodCounters(out string) map[string]int64 {
	c := map[string]int64{}
	for _, l := range strings.Split(out, "\n") {
		if m := floodCounterRe.FindStringSubmatch(l); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			c[m[2]] += n
		}
	}
	return c
}

// ProtectionStatus is the state for the panel.
func ProtectionStatus(ctx context.Context, env *host.Env) map[string]any {
	available := env.Runner.Available("nft")
	active := false
	counters := map[string]int64{}
	if available {
		if out, err := env.Runner.Run(ctx, "nft", "list", "table", "inet", floodTable); err == nil {
			active = true
			counters = FloodCounters(string(out))
		}
	}
	return map[string]any{"available": available, "active": active, "config": LoadProtection(env), "dropped": counters}
}

// SetProtection applies a configuration under the guard: a change that
// cuts off access is undone unless confirmed.
func (s *Service) SetProtection(ctx context.Context, p Protection) (*Pending, error) {
	env := s.env
	if !env.Runner.Available("nft") {
		return nil, fmt.Errorf("nftables (nft) is not installed; install the nftables package first")
	}
	if err := p.Normalize(); err != nil {
		return nil, err
	}
	if err := s.guard.Busy(); err != nil {
		return nil, err
	}
	prev := LoadProtection(env)
	pj, _ := json.Marshal(prev)
	nj, _ := json.Marshal(p)
	if string(pj) == string(nj) && env.Root.Exists(execx.NftFloodFile) {
		return nil, nil
	}
	firstTime := !env.Root.Exists(floodUnitPath)
	files := []FileRestore{snapshot(env, execx.NftFloodFile), snapshot(env, FloodConfigFile), snapshot(env, floodUnitPath)}
	cfg, _ := json.MarshalIndent(p, "", "  ")
	set := env.Changes("flood")
	set.Mkdir(filepath.Dir(execx.NftFloodFile), 0o755, agentfs.RootOwner)
	set.Write(execx.NftFloodFile, []byte(FloodRules(p)), 0o600, agentfs.RootOwner)
	set.Write(FloodConfigFile, cfg, 0o600, agentfs.RootOwner)
	set.Write(floodUnitPath, []byte(floodUnit()), 0o644, agentfs.RootOwner)
	applied, err := set.Commit(func() error {
		if out, err := env.Runner.Run(ctx, "nft", "-c", "-f", execx.NftFloodFile); err != nil {
			return fmt.Errorf("nft rejected the rules: %s", host.Output(out))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = env.DaemonReload(ctx)
	if err == nil {
		err = env.Systemctl(ctx, "enable", FloodUnit)
	}
	if err == nil {
		err = env.Systemctl(ctx, "restart", FloodUnit)
	}
	if err == nil && p.Enabled {
		if out, lerr := env.Runner.Run(ctx, "nft", "list", "table", "inet", floodTable); lerr != nil {
			err = fmt.Errorf("the rules are not loaded: %s", host.Output(out))
		}
	}
	if err != nil {
		applied.Rollback()
		env.DaemonReload(ctx)
		if firstTime {
			env.Systemctl(ctx, "stop", FloodUnit)
			env.Systemctl(ctx, "disable", FloodUnit)
		} else {
			env.Systemctl(ctx, "restart", FloodUnit)
		}
		return nil, err
	}
	summary := "firewall protections: off"
	if p.Enabled {
		var on []string
		if p.Invalid {
			on = append(on, "invalid packets")
		}
		if p.SynRate > 0 {
			on = append(on, fmt.Sprintf("SYN flood %d/s", p.SynRate))
		}
		if len(p.ConnLimits) > 0 {
			on = append(on, fmt.Sprintf("%d connection limit(s)", len(p.ConnLimits)))
		}
		if len(p.PortFlood) > 0 {
			on = append(on, fmt.Sprintf("%d port flood limit(s)", len(p.PortFlood)))
		}
		if p.ICMPRate > 0 {
			on = append(on, fmt.Sprintf("ping %d/s", p.ICMPRate))
		}
		if len(on) == 0 {
			on = []string{"on, nothing selected"}
		}
		summary = "firewall protections: " + strings.Join(on, ", ")
	}
	pending := Pending{Kind: "firewall", Summary: summary, Files: files}
	if firstTime {
		// The unit stops (its ExecStop removes the table) before its files go.
		pending.Units = [][2]string{{"stop", FloodUnit}, {"disable", FloodUnit}}
	} else {
		pending.ReloadUnits = []string{FloodUnit}
	}
	return s.guard.Start(pending)
}

// AllowProtectionPaths lets the agent write the protections' files.
func AllowProtectionPaths(env *host.Env) {
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, execx.NftFloodFile, FloodConfigFile, floodUnitPath, filepath.Dir(execx.NftFloodFile))
}
