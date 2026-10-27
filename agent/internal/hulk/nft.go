package hulk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/security"
)

// Block is an address blocked until a Unix time: at the firewall level
// (All: every port) or for the login services only.
type Block struct {
	IP    string `json:"ip"`
	Until int64  `json:"until"`
	All   bool   `json:"all"`
}

// Range is a range of addresses (a blacklisted country's).
type Range struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// State is what the panel wants enforced.
type State struct {
	Enabled   bool     `json:"enabled"`
	Blocks    []Block  `json:"blocks"`
	Countries []Range  `json:"countries"`
	Whitelist []string `json:"whitelist"`
	// CountriesAll blocks blacklisted countries on every port (CSF's
	// CC_DENY), not only the login services.
	CountriesAll bool `json:"countries_all,omitempty"`
}

// Limits of one state.
const (
	MaxBlocks    = 50000
	MaxRanges    = 200000
	MaxWhitelist = 1000
)

// LoginPorts are the ports of the login services (the SSH port is added).
var LoginPorts = []int{21, 110, 143, 465, 587, 993, 995, 4190}

func (s State) validate(now int64) error {
	if len(s.Blocks) > MaxBlocks || len(s.Countries) > MaxRanges || len(s.Whitelist) > MaxWhitelist {
		return fmt.Errorf("too many entries")
	}
	for _, b := range s.Blocks {
		if _, err := netip.ParseAddr(b.IP); err != nil {
			return fmt.Errorf("%q is not an IP address", b.IP)
		}
		if b.Until <= now || b.Until > now+366*86400 {
			return fmt.Errorf("the block of %s must end within a year", b.IP)
		}
	}
	for _, r := range s.Countries {
		f, err1 := netip.ParseAddr(r.From)
		t, err2 := netip.ParseAddr(r.To)
		if err1 != nil || err2 != nil || f.Is4() != t.Is4() || t.Less(f) {
			return fmt.Errorf("%s-%s is not an address range", r.From, r.To)
		}
	}
	for _, w := range s.Whitelist {
		if _, err := netip.ParsePrefix(w); err != nil {
			if _, err := netip.ParseAddr(w); err != nil {
				return fmt.Errorf("%q is not an IP address or network", w)
			}
		}
	}
	return nil
}

func set(name, typ, flags string, elems []string) string {
	s := "\tset " + name + " {\n\t\ttype " + typ + "\n\t\tflags " + flags + "\n"
	if len(elems) > 0 {
		s += "\t\telements = { " + strings.Join(elems, ", ") + " }\n"
	}
	return s + "\t}\n"
}

// rules renders the table. Its chain runs before the firewall's (priority
// -5): an accept here does not bypass the firewall, a drop is final.
func rules(s State, sshPorts []int, now int64) string {
	var all4, all6, login4, login6, cc4, cc6, wl4, wl6 []string
	for _, b := range s.Blocks {
		a := netip.MustParseAddr(b.IP).Unmap()
		e := fmt.Sprintf("%s timeout %ds", a, b.Until-now)
		switch {
		case a.Is4() && b.All:
			all4 = append(all4, e)
		case b.All:
			all6 = append(all6, e)
		case a.Is4():
			login4 = append(login4, e)
		default:
			login6 = append(login6, e)
		}
	}
	for _, r := range s.Countries {
		f, t := netip.MustParseAddr(r.From).Unmap(), netip.MustParseAddr(r.To).Unmap()
		e := f.String() + "-" + t.String()
		if f == t {
			e = f.String()
		}
		if f.Is4() {
			cc4 = append(cc4, e)
		} else {
			cc6 = append(cc6, e)
		}
	}
	for _, w := range s.Whitelist {
		if strings.Contains(w, ":") {
			wl6 = append(wl6, w)
		} else {
			wl4 = append(wl4, w)
		}
	}
	ports := map[int]bool{}
	for _, p := range append(append([]int{}, LoginPorts...), sshPorts...) {
		ports[p] = true
	}
	var pl []string
	for p := range ports {
		pl = append(pl, strconv.Itoa(p))
	}
	sort.Slice(pl, func(i, j int) bool { a, _ := strconv.Atoi(pl[i]); b, _ := strconv.Atoi(pl[j]); return a < b })
	dport := "tcp dport { " + strings.Join(pl, ", ") + " }"
	var b strings.Builder
	b.WriteString("# Managed by WhaCP: brute force protection (Security → cPHulk Brute Force Protection).\n")
	b.WriteString("table inet whacp_hulk\ndelete table inet whacp_hulk\ntable inet whacp_hulk {\n")
	b.WriteString(set("allow4", "ipv4_addr", "interval", wl4))
	b.WriteString(set("allow6", "ipv6_addr", "interval", wl6))
	b.WriteString(set("block4", "ipv4_addr", "timeout", all4))
	b.WriteString(set("block6", "ipv6_addr", "timeout", all6))
	b.WriteString(set("login4", "ipv4_addr", "timeout", login4))
	b.WriteString(set("login6", "ipv6_addr", "timeout", login6))
	b.WriteString(set("country4", "ipv4_addr", "interval", cc4))
	b.WriteString(set("country6", "ipv6_addr", "interval", cc6))
	b.WriteString("\tchain input {\n\t\ttype filter hook input priority -5; policy accept;\n")
	b.WriteString("\t\tiifname \"lo\" accept\n\t\tip saddr @allow4 accept\n\t\tip6 saddr @allow6 accept\n")
	b.WriteString("\t\tip saddr @block4 counter drop\n\t\tip6 saddr @block6 counter drop\n")
	fmt.Fprintf(&b, "\t\t%s ip saddr @login4 counter reject with tcp reset\n\t\t%s ip6 saddr @login6 counter reject with tcp reset\n", dport, dport)
	if s.CountriesAll {
		b.WriteString("\t\tip saddr @country4 counter drop\n\t\tip6 saddr @country6 counter drop\n")
	} else {
		fmt.Fprintf(&b, "\t\t%s ip saddr @country4 counter drop\n\t\t%s ip6 saddr @country6 counter drop\n", dport, dport)
	}
	b.WriteString("\t}\n}\n")
	return b.String()
}

func stateHash(s State) string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func loaded(ctx context.Context, env *host.Env) bool {
	_, err := env.Runner.Run(ctx, "nft", "list", "table", "inet", "whacp_hulk")
	return err == nil
}

// sshPorts are the SSH server's ports (22 when unknown).
func sshPorts(ctx context.Context, env *host.Env) []int {
	var out []int
	if eff, err := security.SSHEffective(ctx, env); err == nil {
		for _, f := range strings.Fields(eff["port"]) {
			if p, err := strconv.Atoi(f); err == nil && p > 0 && p < 65536 {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		out = []int{22}
	}
	return out
}

// Sync applies the wanted state (unchanged state and a loaded table: nothing
// to do). Blocks are removed by nftables itself when they expire; the panel
// syncs every minute, which also restores the table after a restart.
func Sync(ctx context.Context, env *host.Env, s State, now time.Time) (bool, error) {
	if !env.Runner.Available("nft") {
		return false, fmt.Errorf("nftables (nft) is not installed; install the nftables package first")
	}
	// Expired blocks are left out.
	blocks := s.Blocks[:0:0]
	for _, b := range s.Blocks {
		if b.Until > now.Unix() {
			blocks = append(blocks, b)
		}
	}
	s.Blocks = blocks
	if err := s.validate(now.Unix()); err != nil {
		return false, err
	}
	hashFile := filepath.Join(env.Cfg.StateDir, "hulk.sha")
	h := stateHash(s)
	isLoaded := loaded(ctx, env)
	if old, err := env.Root.ReadFile(hashFile); err == nil && string(old) == h && isLoaded == s.Enabled {
		return false, nil
	}
	if !s.Enabled {
		if isLoaded {
			if out, err := env.Runner.Run(ctx, "nft", "delete", "table", "inet", "whacp_hulk"); err != nil {
				return false, fmt.Errorf("the table could not be removed: %s", host.Output(out))
			}
		}
		set := env.Changes("hulk")
		if env.Root.Exists(execx.NftHulkFile) {
			set.Remove(execx.NftHulkFile)
		}
		set.Write(hashFile, []byte(h), 0o600, agentfs.RootOwner)
		_, err := set.Commit(nil)
		return true, err
	}
	set := env.Changes("hulk")
	set.Mkdir(filepath.Dir(execx.NftHulkFile), 0o755, agentfs.RootOwner)
	set.Write(execx.NftHulkFile, []byte(rules(s, sshPorts(ctx, env), now.Unix())), 0o600, agentfs.RootOwner)
	applied, err := set.Commit(func() error {
		if out, err := env.Runner.Run(ctx, "nft", "-c", "-f", execx.NftHulkFile); err != nil {
			return fmt.Errorf("nft rejected the rules: %s", host.Output(out))
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	if out, err := env.Runner.Run(ctx, "nft", "-f", execx.NftHulkFile); err != nil {
		applied.Rollback()
		return false, fmt.Errorf("the rules could not be loaded: %s", host.Output(out))
	}
	hs := env.Changes("hulk-state")
	hs.Write(hashFile, []byte(h), 0o600, agentfs.RootOwner)
	_, err = hs.Commit(nil)
	return true, err
}
