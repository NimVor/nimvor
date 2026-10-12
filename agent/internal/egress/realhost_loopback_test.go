//go:build realhost

package egress

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// Loopback isolation enforced by the real nftables (security item 80): a
// website account is refused a guarded loopback service port, an allowlisted
// account reaches it, and an ordinary (non-website) account is unaffected.
// Requires root and nft.
func TestRealHostLoopbackIsolation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Skip("needs nft")
	}
	uid := func(name string) string {
		exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", name).Run()
		t.Cleanup(func() { exec.Command("userdel", name).Run() })
		u, err := exec.Command("id", "-u", name).Output()
		if err != nil {
			t.Fatalf("id %s: %v", name, err)
		}
		return string([]byte(u[:len(u)-1]))
	}
	allowed := uid("whacp-lo-a")
	denied := uid("whacp-lo-b")

	// A listener on a guarded port.
	const port = 16379
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	// Load the isolation rules directly (allowing only whacp-lo-a).
	rules := Rules(
		Settings{MetadataAllowed: true, LoopbackIsolation: true, LoopbackPorts: []int{port}},
		[]string{allowed, denied},
		map[string][]int{allowed: {port}},
	)
	f := t.TempDir() + "/loopback.nft"
	os.WriteFile(f, []byte(rules), 0o600)
	if out, err := exec.Command("nft", "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("nft -f: %v\n%s\n%s", err, out, rules)
	}
	t.Cleanup(func() { exec.Command("nft", "delete", "table", "inet", table).Run() })

	connect := func(asUser string) bool {
		// bash /dev/tcp returns non-zero when the connection is refused.
		cmd := exec.Command("runuser", "-u", asUser, "--", "timeout", "3", "bash", "-c", "exec 3<>/dev/tcp/127.0.0.1/"+strconv.Itoa(port))
		return cmd.Run() == nil
	}

	if connect("whacp-lo-b") {
		t.Error("the denied account reached the guarded loopback port")
	}
	if !connect("whacp-lo-a") {
		t.Error("the allowlisted account could not reach the guarded loopback port")
	}
	if !connect("root") {
		t.Error("root (not a website account) should not be limited")
	}
}
