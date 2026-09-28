// Package netmgr reports the server's IP addresses and makes routed IPv6
// ranges usable by websites.
//
// WhaCP does not edit the operating system's network configuration
// (netplan, NetworkManager, ifcfg): an address must already be configured
// on an interface before a website can listen on it. The one exception is a
// routed IPv6 range: its addresses become local through a "local" route on
// the loopback interface (AnyIP), kept by WhaCP's own systemd unit. Ranges
// that overlap an interface's on-link prefix or contain the default
// gateway are refused, because such a route would cut the server off from
// its neighbours.
package netmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Unit keeps the IPv6 ranges' local routes across reboots.
const Unit = "whacp-ipv6-ranges.service"

// Address is one address on an interface.
type Address struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Family    int    `json:"family"` // 4 or 6
	PrefixLen int    `json:"prefix_len"`
	Scope     string `json:"scope"`
}

// State is what the agent keeps.
type State struct {
	Ranges []string `json:"ipv6_ranges"`
}

func statePath(e *host.Env) string { return filepath.Join(e.Cfg.StateDir, "ipv6-ranges.json") }
func unitPath(e *host.Env) string  { return filepath.Join(e.M.UnitDir, Unit) }

// LoadState reads the managed ranges.
func LoadState(e *host.Env) State {
	var st State
	if b, err := e.Root.ReadFile(statePath(e)); err == nil {
		json.Unmarshal(b, &st)
	}
	if st.Ranges == nil {
		st.Ranges = []string{}
	}
	return st
}

type ipAddr struct {
	IfName   string `json:"ifname"`
	AddrInfo []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		PrefixLen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
	} `json:"addr_info"`
}

// ParseAddresses reads `ip -j addr show`.
func ParseAddresses(out []byte) ([]Address, error) {
	var ifs []ipAddr
	if err := json.Unmarshal(out, &ifs); err != nil {
		return nil, err
	}
	res := []Address{}
	for _, i := range ifs {
		for _, a := range i.AddrInfo {
			fam := 4
			if a.Family == "inet6" {
				fam = 6
			}
			res = append(res, Address{Interface: i.IfName, Address: a.Local, Family: fam, PrefixLen: a.PrefixLen, Scope: a.Scope})
		}
	}
	return res, nil
}

// Addresses lists the server's addresses.
func Addresses(ctx context.Context, e *host.Env) ([]Address, error) {
	out, err := e.Runner.Run(ctx, "ip", "-j", "addr", "show")
	if err != nil {
		return nil, fmt.Errorf("ip addr: %s", host.Output(out))
	}
	return ParseAddresses(out)
}

func gateways(ctx context.Context, e *host.Env) []netip.Addr {
	var gws []netip.Addr
	for _, fam := range []string{"-4", "-6"} {
		out, err := e.Runner.Run(ctx, "ip", "-j", fam, "route", "show", "default")
		if err != nil {
			continue
		}
		var routes []struct {
			Gateway string `json:"gateway"`
		}
		if json.Unmarshal(out, &routes) == nil {
			for _, r := range routes {
				if a, err := netip.ParseAddr(r.Gateway); err == nil {
					gws = append(gws, a)
				}
			}
		}
	}
	return gws
}

// CheckRange validates a routed IPv6 range against the interfaces' prefixes
// and gateways.
func CheckRange(r string, addrs []Address, gws []netip.Addr) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(r))
	if err != nil || !p.Addr().Is6() || p.Addr().Is4In6() {
		return netip.Prefix{}, fmt.Errorf("%s is not an IPv6 range such as 2001:db8:1::/64", r)
	}
	if p.Masked() != p {
		return netip.Prefix{}, fmt.Errorf("%s: write the range's first address, %s", r, p.Masked())
	}
	if p.Bits() < 48 || p.Bits() > 128 {
		return netip.Prefix{}, fmt.Errorf("%s: the prefix length must be /48 to /128", r)
	}
	if !netip.MustParsePrefix("2000::/3").Contains(p.Addr()) && !netip.MustParsePrefix("fc00::/7").Contains(p.Addr()) {
		return netip.Prefix{}, fmt.Errorf("%s is not a global or unique local range", r)
	}
	for _, a := range addrs {
		if a.Family != 6 || a.Scope == "link" || a.Scope == "host" {
			continue
		}
		ip, err := netip.ParseAddr(a.Address)
		if err != nil {
			continue
		}
		onLink := netip.PrefixFrom(ip, a.PrefixLen).Masked()
		if a.PrefixLen < 128 && (onLink.Overlaps(p)) {
			return netip.Prefix{}, fmt.Errorf("%s overlaps %s on %s (on-link): a local route for it would cut the server off from that network. Ask your provider to route a separate range to this server", r, onLink, a.Interface)
		}
	}
	for _, g := range gws {
		if p.Contains(g) {
			return netip.Prefix{}, fmt.Errorf("%s contains the default gateway %s", r, g)
		}
	}
	return p, nil
}

// Local reports whether ip is an address of this server: on an interface,
// or inside a managed IPv6 range.
func Local(ctx context.Context, e *host.Env, ip string) (bool, error) {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false, nil
	}
	addrs, err := Addresses(ctx, e)
	if err != nil {
		return false, err
	}
	for _, x := range addrs {
		if b, err := netip.ParseAddr(x.Address); err == nil && b == a {
			return true, nil
		}
	}
	if a.Is6() {
		for _, r := range LoadState(e).Ranges {
			if p, err := netip.ParsePrefix(r); err == nil && p.Contains(a) {
				return true, nil
			}
		}
	}
	return false, nil
}

func renderUnit(ranges []string) string {
	var b strings.Builder
	b.WriteString("# Managed by WhaCP (IP addresses → IPv6 ranges): makes routed IPv6 ranges local.\n[Unit]\nDescription=WhaCP routed IPv6 ranges\nAfter=network-online.target\nWants=network-online.target\n\n[Service]\nType=oneshot\nRemainAfterExit=yes\n")
	for _, r := range ranges {
		fmt.Fprintf(&b, "ExecStart=/usr/sbin/ip -6 route replace local %s dev lo\n", r)
	}
	for _, r := range ranges {
		fmt.Fprintf(&b, "ExecStop=-/usr/sbin/ip -6 route del local %s dev lo\n", r)
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// Register adds net.addresses and net.ipv6_ranges.
func Register(r *actions.Registry, e *host.Env) {
	e.Writer.Policy.Patterns = append(e.Writer.Policy.Patterns, unitPath(e))

	r.Register(actions.Action{Name: "net.addresses", Description: "The server's IP addresses (per interface), default gateways and the routed IPv6 ranges WhaCP makes local.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		addrs, err := Addresses(ctx, e)
		if err != nil {
			return nil, actions.Failed("The IP addresses could not be read.", err.Error())
		}
		gws := []string{}
		for _, g := range gateways(ctx, e) {
			gws = append(gws, g.String())
		}
		return map[string]any{"addresses": addrs, "gateways": gws, "ipv6_ranges": LoadState(e).Ranges, "ipv6": e.IPv6()}, nil
	}})

	r.Register(actions.Action{Name: "net.ipv6_ranges", Mutating: true, Description: "Set the routed IPv6 ranges (exactly these) whose addresses websites may use: each becomes a local route on lo, kept at boot by " + Unit + ". Ranges overlapping an interface's on-link prefix or containing a gateway are refused.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Ranges []string `json:"ranges"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if len(in.Ranges) > 64 {
			return nil, actions.InvalidParams("at most 64 ranges")
		}
		addrs, err := Addresses(ctx, e)
		if err != nil {
			return nil, actions.Failed("The IP addresses could not be read.", err.Error())
		}
		gws := gateways(ctx, e)
		var want []netip.Prefix
		for _, s := range in.Ranges {
			p, err := CheckRange(s, addrs, gws)
			if err != nil {
				return nil, actions.InvalidParams(err.Error())
			}
			for _, q := range want {
				if q.Overlaps(p) {
					return nil, actions.InvalidParams(fmt.Sprintf("%s overlaps %s", p, q))
				}
			}
			want = append(want, p)
		}
		list := make([]string, len(want))
		for i, p := range want {
			list[i] = p.String()
		}
		sort.Strings(list)
		old := LoadState(e).Ranges
		keep := map[string]bool{}
		for _, r := range list {
			keep[r] = true
		}
		// Routes now; the unit keeps them at boot.
		var added []string
		undo := func() {
			for _, r := range added {
				e.Runner.Run(ctx, "ip", "-6", "route", "del", "local", r, "dev", "lo")
			}
		}
		for _, r := range list {
			if out, err := e.Runner.Run(ctx, "ip", "-6", "route", "replace", "local", r, "dev", "lo"); err != nil {
				undo()
				return nil, actions.Failed("The route for "+r+" could not be added.", host.Output(out))
			}
			added = append(added, r)
		}
		for _, r := range old {
			if !keep[r] {
				e.Runner.Run(ctx, "ip", "-6", "route", "del", "local", r, "dev", "lo")
			}
		}
		b, _ := json.MarshalIndent(State{Ranges: list}, "", "  ")
		set := e.Changes("ipv6-ranges")
		set.Write(statePath(e), b, 0o600, agentfs.RootOwner)
		if len(list) > 0 {
			set.Write(unitPath(e), []byte(renderUnit(list)), 0o644, agentfs.RootOwner)
		} else {
			set.Remove(unitPath(e))
		}
		if _, err := set.Apply(); err != nil {
			return nil, host.FailWithValidation("The IPv6 ranges were not saved", err)
		}
		e.Runner.Run(ctx, "systemctl", "daemon-reload")
		verb := "enable"
		if len(list) == 0 {
			verb = "disable"
		}
		e.Runner.Run(ctx, "systemctl", verb, "--", Unit)
		return map[string]any{"ipv6_ranges": list}, nil
	}})
}
