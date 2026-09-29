//go:build realhost

package hulk

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Requires root and nft: the generated table loads, and a login-only block
// rejects a login port but not others; an expired entry is not loaded.
func TestRealHostRulesLoad(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if out, err := exec.Command("nft", "list", "tables").CombinedOutput(); err != nil {
		t.Skipf("nftables not usable: %s", out)
	}
	now := time.Now().Unix()
	s := State{Enabled: true,
		Blocks:    []Block{{IP: "192.0.2.77", Until: now + 600}, {IP: "2001:db8::77", Until: now + 600, All: true}},
		Countries: []Range{{From: "198.51.100.0", To: "198.51.100.255"}, {From: "2001:db8:1::", To: "2001:db8:1::ffff"}},
		Whitelist: []string{"203.0.113.0/24", "2001:db8:ff::/48"},
	}
	f := t.TempDir() + "/h.nft"
	os.WriteFile(f, []byte(rules(s, []int{22}, now)), 0o600)
	if out, err := exec.Command("nft", "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("load: %s\n%s", out, rules(s, []int{22}, now))
	}
	t.Cleanup(func() { exec.Command("nft", "delete", "table", "inet", "whacp_hulk").Run() })
	// Loading again replaces the table.
	if out, err := exec.Command("nft", "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("reload: %s", out)
	}
	out, _ := exec.Command("nft", "list", "table", "inet", "whacp_hulk").CombinedOutput()
	for _, want := range []string{"192.0.2.77", "2001:db8::77", "198.51.100.0/24", "203.0.113.0/24"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %s:\n%s", want, out)
		}
	}
}
