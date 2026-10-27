package egress

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func setup(t *testing.T) *testhost.Host {
	t.Helper()
	h := testhost.New(t)
	f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
	// Two website accounts, an administrator and a service account.
	f.WriteString("shop:x:1600:1600:WhaCP website shop.test:/home/whacp/shop.test:/usr/sbin/nologin\n" +
		"blog:x:1601:1601:WhaCP website blog.test:/home/whacp/blog.test:/usr/sbin/nologin\n" +
		"rocky:x:1000:1000::/home/rocky:/bin/bash\nchrony:x:990:990::/var/lib/chrony:/sbin/nologin\n")
	f.Close()
	AllowPaths(h.Env)
	return h
}

// By default only the cloud metadata services are blocked, for website
// accounts only: the administrator's account and services (chrony syncs
// from metadata.google.internal on GCP) are not matched.
func TestDefaultBlocksMetadataForWebsiteAccounts(t *testing.T) {
	h := setup(t)
	if err := Refresh(context.Background(), h.Env); err != nil {
		t.Fatal(err)
	}
	rules := h.Read(execx.NftEgressFile)
	for _, want := range []string{
		"table inet whacp_egress\ndelete table inet whacp_egress\n",
		`oifname "lo" accept`,
		`meta skuid { 1600, 1601 } ip daddr { 169.254.169.254, 169.254.170.2, 100.100.100.200 } counter reject comment "cloud metadata"`,
		`meta skuid { 1600, 1601 } ip6 daddr { fd00:ec2::254 } counter reject`,
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("rules lack %q:\n%s", want, rules)
		}
	}
	for _, not := range []string{"1000", "990", "10.0.0.0/8", "dport"} {
		if strings.Contains(rules, not) {
			t.Errorf("rules contain %q:\n%s", not, rules)
		}
	}
	calls := strings.Join(h.Runner.Calls, "\n")
	for _, want := range []string{"nft -c -f " + execx.NftEgressFile, "systemctl enable -- " + Unit, "systemctl restart -- " + Unit} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing %q in %s", want, calls)
		}
	}
	// The same accounts: nothing reloaded.
	n := len(h.Runner.Calls)
	Refresh(context.Background(), h.Env)
	for _, c := range h.Runner.Calls[n:] {
		if strings.Contains(c, "restart") || strings.HasPrefix(c, "nft -c") {
			t.Errorf("reloaded unchanged rules: %s", c)
		}
	}
	// A new website account: in the rules.
	f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("news:x:1602:1602:WhaCP website news.test:/home/whacp/news.test:/usr/sbin/nologin\n")
	f.Close()
	Refresh(context.Background(), h.Env)
	if !strings.Contains(h.Read(execx.NftEgressFile), "meta skuid { 1600, 1601, 1602 }") {
		t.Errorf("new account missing:\n%s", h.Read(execx.NftEgressFile))
	}
}

func TestSettings(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	if err := Set(ctx, h.Env, Settings{BlockPrivate: true, TCPPorts: []int{443, 80, 53, 80}, UDPPorts: []int{53}}); err != nil {
		t.Fatal(err)
	}
	rules := h.Read(execx.NftEgressFile)
	for _, want := range []string{
		"ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16, 100.64.0.0/10 } counter reject",
		"ip6 daddr { fc00::/7, fe80::/10 } counter reject",
		"meta skuid { 1600, 1601 } tcp dport != { 53, 80, 443 } counter reject with tcp reset",
		"meta skuid { 1600, 1601 } udp dport != { 53 } counter reject",
		"cloud metadata",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("rules lack %q:\n%s", want, rules)
		}
	}
	st := Status(ctx, h.Env)
	if st["metadata_blocked"] != true || st["block_private"] != true || st["accounts"] != 2 {
		t.Errorf("status: %v", st)
	}
	for _, bad := range []Settings{{TCPPorts: []int{0}}, {UDPPorts: []int{70000}}} {
		if err := Set(ctx, h.Env, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// nft refusing the rules: the earlier rules and settings stay.
	h.Fail["nft -c"] = "Error: syntax error"
	if err := Set(ctx, h.Env, Settings{}); err == nil {
		t.Fatal("rules nft refused were accepted")
	}
	if h.Read(execx.NftEgressFile) != rules || !Load(h.Env).BlockPrivate {
		t.Errorf("not kept:\n%s\n%+v", h.Read(execx.NftEgressFile), Load(h.Env))
	}
	delete(h.Fail, "nft -c")
	// Nothing to limit: the table, rules and service are removed.
	if err := Set(ctx, h.Env, Settings{MetadataAllowed: true}); err != nil {
		t.Fatal(err)
	}
	if h.Exists(execx.NftEgressFile) || h.Exists(unitPath) || !h.Runner.Called("nft delete table inet whacp_egress") {
		t.Errorf("not removed: %v", h.Runner.Calls)
	}
}

// Without nftables the agent's start and website changes go on.
func TestRefreshWithoutNft(t *testing.T) {
	h := setup(t)
	h.Runner.Unavailable = map[string]bool{"nft": true}
	if err := Refresh(context.Background(), h.Env); err != nil || h.Exists(execx.NftEgressFile) {
		t.Fatalf("%v", err)
	}
	if Status(context.Background(), h.Env)["available"] != false {
		t.Error("available without nft")
	}
}

// Loopback isolation (item 80): website accounts are refused the shared
// cache ports on loopback, except an allowlisted account and port.
func TestLoopbackIsolation(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	if err := Set(ctx, h.Env, Settings{MetadataAllowed: true, LoopbackIsolation: true, LoopbackAllow: map[string][]int{"shop": {6379}}}); err != nil {
		t.Fatal(err)
	}
	rules := h.Read(execx.NftEgressFile)
	for _, want := range []string{
		// shop (1600) may still reach Redis on loopback.
		"meta skuid 1600 ip daddr 127.0.0.0/8 tcp dport { 6379 } accept",
		"meta skuid 1600 ip6 daddr ::1 tcp dport { 6379 } accept",
		// both accounts are refused the full guarded set (shop's 6379 is
		// accepted first above, so only its other ports are rejected).
		"meta skuid { 1600, 1601 } ip daddr 127.0.0.0/8 tcp dport { 6379, 6380, 11211 } counter reject with tcp reset",
		"meta skuid { 1600, 1601 } ip6 daddr ::1 tcp dport { 6379, 6380, 11211 } counter reject with tcp reset",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("rules lack %q:\n%s", want, rules)
		}
	}
	// The blanket loopback accept comes after the isolation rules.
	iso := strings.Index(rules, "shared cache")
	lo := strings.Index(rules, `oifname "lo" accept`)
	if iso < 0 || lo < 0 || iso > lo {
		t.Errorf("the shared-cache reject must precede the loopback accept (iso=%d lo=%d)", iso, lo)
	}
	st := Status(ctx, h.Env)
	if st["loopback_isolation"] != true {
		t.Errorf("status loopback_isolation: %v", st["loopback_isolation"])
	}
	// An account allowed every guarded port gets no reject rule of its own.
	if err := Set(ctx, h.Env, Settings{MetadataAllowed: true, LoopbackIsolation: true, LoopbackPorts: []int{6379}, LoopbackAllow: map[string][]int{"shop": {6379}}}); err != nil {
		t.Fatal(err)
	}
	rules = h.Read(execx.NftEgressFile)
	if strings.Contains(rules, "meta skuid { 1600, 1601 }") || !strings.Contains(rules, "meta skuid { 1601 }") {
		t.Errorf("a fully-allowed account should be dropped from the reject set:\n%s", rules)
	}
	// An invalid account name in the exceptions is refused.
	if err := Set(ctx, h.Env, Settings{LoopbackIsolation: true, LoopbackAllow: map[string][]int{"bad name!": {6379}}}); err == nil {
		t.Error("an invalid account name was accepted")
	}
}
