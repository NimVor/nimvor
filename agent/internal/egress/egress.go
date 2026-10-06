// Package egress limits the outgoing connections of website accounts with
// an nftables table WhaCP owns (inet whacp_egress), loaded at boot by its
// own service:
//
//   - Cloud metadata (on unless the administrator turns it off): website
//     code cannot reach the instance metadata services (169.254.169.254
//     and the other providers' addresses), which hand out the server's
//     cloud credentials and configuration. Root, the system's services
//     (cloud-init, guest agents, time sync) are not affected.
//   - Private networks (optional): no connections to private, link-local
//     and shared address ranges (the provider's internal network, other
//     tenants' VMs, routers).
//   - Ports (optional): connections only to the listed TCP and UDP ports
//     (e.g. 53, 80, 443): no direct SMTP, SSH, database or IRC
//     connections out of a website.
//
// Only the accounts whose home is a website folder are matched (by UID);
// connections over the loopback interface (the server's own addresses) are
// never limited. The UID list is refreshed when the agent starts and when
// websites create or delete accounts.
package egress

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/limits"
)

// Unit loads the table at boot.
const (
	Unit     = "whacp-egress.service"
	unitPath = "/etc/systemd/system/" + Unit
	table    = "whacp_egress"
)

// Metadata4 and Metadata6 are the instance metadata services: AWS, GCP,
// Azure, DigitalOcean, Hetzner, Oracle, OpenStack (169.254.169.254), AWS
// ECS task metadata (169.254.170.2), Alibaba Cloud (100.100.100.200), AWS
// over IPv6 (fd00:ec2::254).
var (
	Metadata4 = []string{"169.254.169.254", "169.254.170.2", "100.100.100.200"}
	Metadata6 = []string{"fd00:ec2::254"}
)

// Private4 and Private6 are the private, link-local and shared ranges.
var (
	Private4 = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10"}
	Private6 = []string{"fc00::/7", "fe80::/10"}
)

// Settings is what the administrator chose.
type Settings struct {
	// MetadataAllowed turns the metadata block off (it is on by default).
	MetadataAllowed bool `json:"metadata_allowed"`
	BlockPrivate    bool `json:"block_private"`
	// TCPPorts and UDPPorts, when not empty, are the only remote ports
	// website accounts may connect to.
	TCPPorts []int `json:"tcp_ports"`
	UDPPorts []int `json:"udp_ports"`
}

func (s Settings) blocksAnything() bool {
	return !s.MetadataAllowed || s.BlockPrivate || len(s.TCPPorts) > 0 || len(s.UDPPorts) > 0
}

// Normalize checks the ports and sorts them.
func (s *Settings) Normalize() error {
	for _, ps := range []*[]int{&s.TCPPorts, &s.UDPPorts} {
		seen := map[int]bool{}
		var out []int
		for _, p := range *ps {
			if p < 1 || p > 65535 {
				return fmt.Errorf("port %d is not between 1 and 65535", p)
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
		if len(out) > 64 {
			return fmt.Errorf("at most 64 ports")
		}
		sort.Ints(out)
		*ps = out
	}
	return nil
}

var mu sync.Mutex

func statePath(env *host.Env) string { return filepath.Join(env.Cfg.StateDir, "egress.json") }

// Load reads the settings (defaults: metadata blocked, nothing else).
func Load(env *host.Env) Settings {
	var s Settings
	if b, err := env.Root.ReadFile(statePath(env)); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

// uids are the website accounts' UIDs, in order.
func uids(env *host.Env) []string {
	var ids []int
	for _, name := range limits.WebsiteAccounts(env) {
		if u, err := env.LookupUser(name); err == nil {
			ids = append(ids, u.UID)
		}
	}
	sort.Ints(ids)
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = strconv.Itoa(id)
	}
	return out
}

func join(l []string) string { return strings.Join(l, ", ") }

func ints(l []int) string {
	s := make([]string, len(l))
	for i, p := range l {
		s[i] = strconv.Itoa(p)
	}
	return join(s)
}

// Rules renders the table for the website accounts' UIDs.
func Rules(s Settings, ids []string) string {
	var b strings.Builder
	b.WriteString("# Managed by WhaCP: outgoing connections of website accounts.\n")
	fmt.Fprintf(&b, "table inet %s\ndelete table inet %s\ntable inet %s {\n\tchain output {\n\t\ttype filter hook output priority 0; policy accept;\n\t\toifname \"lo\" accept\n", table, table, table)
	if len(ids) > 0 {
		who := "meta skuid { " + join(ids) + " } "
		if !s.MetadataAllowed {
			fmt.Fprintf(&b, "\t\t%sip daddr { %s } counter reject comment \"cloud metadata\"\n", who, join(Metadata4))
			fmt.Fprintf(&b, "\t\t%sip6 daddr { %s } counter reject comment \"cloud metadata\"\n", who, join(Metadata6))
		}
		if s.BlockPrivate {
			fmt.Fprintf(&b, "\t\t%sip daddr { %s } counter reject comment \"private networks\"\n", who, join(Private4))
			fmt.Fprintf(&b, "\t\t%sip6 daddr { %s } counter reject comment \"private networks\"\n", who, join(Private6))
		}
		if len(s.TCPPorts) > 0 {
			fmt.Fprintf(&b, "\t\t%stcp dport != { %s } counter reject with tcp reset comment \"ports\"\n", who, ints(s.TCPPorts))
		}
		if len(s.UDPPorts) > 0 {
			fmt.Fprintf(&b, "\t\t%sudp dport != { %s } counter reject comment \"ports\"\n", who, ints(s.UDPPorts))
		}
	}
	b.WriteString("\t}\n}\n")
	return b.String()
}

func unit() string {
	return "# Managed by WhaCP: outgoing connections of website accounts.\n[Unit]\nDescription=WhaCP outgoing connection limits of website accounts (nftables)\nAfter=network-pre.target\nBefore=network.target\n\n" +
		"[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/usr/sbin/nft -f " + execx.NftEgressFile + "\nExecStop=/usr/sbin/nft delete table inet " + table + "\n\n[Install]\nWantedBy=multi-user.target\n"
}

// Status is the state for the panel.
func Status(ctx context.Context, env *host.Env) map[string]any {
	s := Load(env)
	available := env.Runner.Available("nft")
	active := false
	if available && env.Root.Exists(execx.NftEgressFile) {
		_, err := env.Runner.Run(ctx, "nft", "list", "table", "inet", table)
		active = err == nil
	}
	tcp, udp := s.TCPPorts, s.UDPPorts
	if tcp == nil {
		tcp = []int{}
	}
	if udp == nil {
		udp = []int{}
	}
	return map[string]any{"available": available, "active": active, "metadata_blocked": !s.MetadataAllowed, "block_private": s.BlockPrivate,
		"tcp_ports": tcp, "udp_ports": udp, "accounts": len(uids(env)), "metadata": append(append([]string{}, Metadata4...), Metadata6...)}
}

// Set saves the settings and applies them.
func Set(ctx context.Context, env *host.Env, s Settings) error {
	if err := s.Normalize(); err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()
	prev := Load(env)
	b, _ := json.MarshalIndent(s, "", "  ")
	set := env.Changes("egress")
	set.Write(statePath(env), b, 0o600, agentfs.RootOwner)
	if _, err := set.Apply(); err != nil {
		return err
	}
	if err := apply(ctx, env, s, true); err != nil {
		b, _ := json.MarshalIndent(prev, "", "  ")
		set := env.Changes("egress")
		set.Write(statePath(env), b, 0o600, agentfs.RootOwner)
		set.Apply()
		return err
	}
	return nil
}

// Refresh applies the saved settings for the current website accounts
// (agent start, accounts created or deleted); nothing happens when the
// rules loaded are already these. Without nft it does nothing.
func Refresh(ctx context.Context, env *host.Env) error {
	if !env.Runner.Available("nft") {
		return nil
	}
	mu.Lock()
	defer mu.Unlock()
	return apply(ctx, env, Load(env), false)
}

// apply writes the rules and service and loads them (checked with nft -c
// first; the previous files are restored when loading fails), or removes
// them when nothing is to be limited.
func apply(ctx context.Context, env *host.Env, s Settings, force bool) error {
	if !env.Runner.Available("nft") {
		return fmt.Errorf("nftables (nft) is not installed; install the nftables package first")
	}
	if !s.blocksAnything() {
		if env.Root.Exists(unitPath) {
			env.Systemctl(ctx, "disable", Unit)
			env.Systemctl(ctx, "stop", Unit)
		}
		env.Runner.Run(ctx, "nft", "delete", "table", "inet", table)
		set := env.Changes("egress")
		for _, p := range []string{execx.NftEgressFile, unitPath} {
			if env.Root.Exists(p) {
				set.Remove(p)
			}
		}
		if _, err := set.Commit(nil); err != nil {
			return err
		}
		return env.DaemonReload(ctx)
	}
	rules := Rules(s, uids(env))
	cur, _ := env.Root.ReadFile(execx.NftEgressFile)
	curUnit, _ := env.Root.ReadFile(unitPath)
	if !force && string(cur) == rules && string(curUnit) == unit() {
		if _, err := env.Runner.Run(ctx, "nft", "list", "table", "inet", table); err == nil {
			return nil // loaded and current
		}
	}
	set := env.Changes("egress")
	set.Mkdir(filepath.Dir(execx.NftEgressFile), 0o755, agentfs.RootOwner)
	set.Write(execx.NftEgressFile, []byte(rules), 0o644, agentfs.RootOwner)
	set.Write(unitPath, []byte(unit()), 0o644, agentfs.RootOwner)
	applied, err := set.Commit(func() error {
		if out, err := env.Runner.Run(ctx, "nft", "-c", "-f", execx.NftEgressFile); err != nil {
			return fmt.Errorf("nft rejected the rules: %s", host.Output(out))
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = env.DaemonReload(ctx)
	if err == nil {
		err = env.Systemctl(ctx, "enable", Unit)
	}
	if err == nil {
		err = env.Systemctl(ctx, "restart", Unit)
	}
	if err == nil {
		if out, lerr := env.Runner.Run(ctx, "nft", "list", "table", "inet", table); lerr != nil {
			err = fmt.Errorf("the rules are not loaded: %s", host.Output(out))
		}
	}
	if err != nil {
		applied.Rollback()
		env.DaemonReload(ctx)
		if env.Root.Exists(execx.NftEgressFile) {
			env.Systemctl(ctx, "restart", Unit) // the previous rules
		} else {
			env.Runner.Run(ctx, "nft", "delete", "table", "inet", table)
		}
		return err
	}
	return nil
}

// AllowPaths lets the agent write this package's files.
func AllowPaths(env *host.Env) {
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, execx.NftEgressFile, filepath.Dir(execx.NftEgressFile), unitPath)
}
