package security

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
)

func fullProtection() Protection {
	return Protection{Enabled: true, Invalid: true, SynRate: 50,
		ConnLimits: []PortLimit{{Port: 22, Max: 10}, {Port: 80, Max: 100}},
		PortFlood:  []PortLimit{{Port: 80, Proto: "tcp", Max: 300}, {Port: 53, Proto: "udp", Max: 600}},
		ICMPRate:   5, Exempt: []string{"203.0.113.5", "2001:db8::/32", "198.51.100.7/24"}}
}

func TestProtectionNormalize(t *testing.T) {
	p := fullProtection()
	if err := p.Normalize(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.Exempt, " ") != "198.51.100.0/24 2001:db8::/32 203.0.113.5" {
		t.Errorf("exempt: %v", p.Exempt)
	}
	for _, bad := range []Protection{
		{SynRate: -1},
		{ConnLimits: []PortLimit{{Port: 0, Max: 5}}},
		{ConnLimits: []PortLimit{{Port: 22, Max: 0}}},
		{ConnLimits: []PortLimit{{Port: 53, Proto: "udp", Max: 5}}}, // connection counts: TCP only
		{PortFlood: []PortLimit{{Port: 80, Max: 5}, {Port: 80, Max: 6}}},
		{PortFlood: []PortLimit{{Port: 80, Proto: "icmp", Max: 5}}},
		{Exempt: []string{"not-an-ip"}},
		{Exempt: []string{"1.2.3.4; flush ruleset"}},
	} {
		if err := bad.Normalize(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestFloodRulesText(t *testing.T) {
	p := fullProtection()
	p.Normalize()
	r := FloodRules(p)
	for _, want := range []string{
		"type filter hook input priority -4; policy accept;",
		"ip saddr @exempt4 accept",
		"elements = { 198.51.100.0/24, 203.0.113.5 }",
		"ct state invalid counter drop comment \"invalid\"",
		"tcp dport 22 ct state new add @conn22_4 { ip saddr ct count over 10 } counter reject with tcp reset comment \"conn:22\"",
		"tcp dport 22 ct state new add @conn22_6 { ip6 saddr ct count over 10 }",
		"udp dport 53 ct state new add @rateudp53_4 { ip saddr limit rate over 600/minute burst 600 packets } counter drop comment \"rate:udp/53\"",
		"icmpv6 type echo-request add @icmp6",
		"limit rate over 50/second burst 100 packets",
	} {
		if !strings.Contains(r, want) {
			t.Errorf("missing %q in\n%s", want, r)
		}
	}
	// Exemptions come before every drop.
	if strings.Index(r, "@exempt4 accept") > strings.Index(r, "counter drop") {
		t.Error("exempt addresses are checked after a drop")
	}
	off := FloodRules(Protection{})
	if strings.Contains(off, "{") || !strings.Contains(off, "delete table inet nimvor_flood") {
		t.Errorf("off: %s", off)
	}
}

// The rules load in a real kernel (needs root and nftables).
func TestFloodRulesLoadInNft(t *testing.T) {
	nft, err := exec.LookPath("nft")
	if err != nil || os.Geteuid() != 0 {
		t.Skip("needs nft and root")
	}
	p := fullProtection()
	p.Normalize()
	f := filepath.Join(t.TempDir(), "flood.nft")
	os.WriteFile(f, []byte(FloodRules(p)), 0o600)
	if out, err := exec.Command(nft, "-c", "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("nft -c refused the rules: %s\n%s", out, FloodRules(p))
	}
	if out, err := exec.Command(nft, "-f", f).CombinedOutput(); err != nil {
		t.Skipf("cannot load here: %s", out)
	}
	defer exec.Command(nft, "delete", "table", "inet", "nimvor_flood").Run()
	out, _ := exec.Command(nft, "list", "table", "inet", "nimvor_flood").CombinedOutput()
	c := FloodCounters(string(out))
	for _, k := range []string{"invalid", "syn", "conn:22", "rate:tcp/80", "rate:udp/53", "icmp"} {
		if _, ok := c[k]; !ok {
			t.Errorf("no counter for %s in\n%s", k, out)
		}
	}
	// Off: the same file format removes the table.
	os.WriteFile(f, []byte(FloodRules(Protection{})), 0o600)
	if out, err := exec.Command(nft, "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("off: %s", out)
	}
	if err := exec.Command(nft, "list", "table", "inet", "nimvor_flood").Run(); err == nil {
		t.Error("table still loaded after turning off")
	}
}

func TestSetProtectionGuardedAndRolledBack(t *testing.T) {
	old := ConfirmWindow
	ConfirmWindow = 100 * time.Millisecond
	defer func() { ConfirmWindow = old }()
	h, s := setup(t, "")
	os.MkdirAll(filepath.Join(h.Base, "etc/nimvor"), 0o755)
	ctx := context.Background()
	p, err := s.SetProtection(ctx, fullProtection())
	if err != nil {
		t.Fatal(err)
	}
	if p == nil || !strings.Contains(p.Summary, "SYN flood 50/s") {
		t.Fatalf("pending: %+v", p)
	}
	for _, want := range []string{"nft -c -f " + execx.NftFloodFile, "systemctl enable -- " + FloodUnit, "systemctl restart -- " + FloodUnit, "nft list table inet nimvor_flood"} {
		if !h.Runner.Called(want) {
			t.Errorf("missing %q: %v", want, h.Runner.Calls)
		}
	}
	if !strings.Contains(h.Read(execx.NftFloodFile), "conn22_4") || LoadProtection(h.Env).SynRate != 50 {
		t.Fatal("rules or configuration not written")
	}
	// Another change waits for this one.
	if _, err := s.SetProtection(ctx, Protection{Enabled: true, Invalid: true}); err == nil {
		t.Fatal("second change accepted while one is pending")
	}
	waitRollback(t, s)
	if !h.Runner.Called("systemctl stop -- " + FloodUnit) {
		t.Errorf("first change not undone by stopping the unit: %v", h.Runner.Calls)
	}
	if h.Exists(execx.NftFloodFile) || h.Exists(FloodConfigFile) || h.Exists(floodUnitPath) {
		t.Error("files of an undone first change left behind")
	}
}

func TestSetProtectionRefusedRulesKeepNothing(t *testing.T) {
	h, s := setup(t, "")
	os.MkdirAll(filepath.Join(h.Base, "etc/nimvor"), 0o755)
	h.Fail["nft -c"] = "Error: syntax error"
	if _, err := s.SetProtection(context.Background(), fullProtection()); err == nil {
		t.Fatal("rules nft refused were applied")
	}
	if h.Exists(execx.NftFloodFile) || h.Exists(floodUnitPath) || s.guard.Current() != nil {
		t.Error("something kept after nft refused the rules")
	}
}

func TestFloodCounters(t *testing.T) {
	out := `		tcp dport 22 ct state new add @conn22_4 { ip saddr ct count over 10 } counter packets 7 bytes 420 reject with tcp reset comment "conn:22"
		tcp dport 22 ct state new add @conn22_6 { ip6 saddr ct count over 10 } counter packets 3 bytes 240 reject with tcp reset comment "conn:22"
		ct state invalid counter packets 12 bytes 600 drop comment "invalid"`
	c := FloodCounters(out)
	if c["conn:22"] != 10 || c["invalid"] != 12 {
		t.Fatalf("%v", c)
	}
}
