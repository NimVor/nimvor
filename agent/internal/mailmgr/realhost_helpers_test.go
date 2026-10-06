//go:build realhost

package mailmgr

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// verifyErrors: the lines of systemd-analyze verify about WhaCP's own
// units. It also reports other units of the host (e.g. snapd's on Ubuntu,
// xfs_scrub's on newer systemd), which are not this test's concern.
// Offline verify cannot resolve the socket's postfix user.
func verifyErrors(out []byte) []string {
	var bad []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.Contains(l, "whacp-") && !strings.Contains(l, "postfix") && !strings.Contains(l, "Failed to prepare filename") && !strings.Contains(l, "kernel does not support IPv6") {
			bad = append(bad, l)
		}
	}
	return bad
}

// selinuxSockets installs the installer's SELinux module that lets systemd
// create the policy sockets in Postfix's private folder (as the installer
// does on SELinux systems).
func selinuxSockets(t *testing.T) {
	out, err := exec.Command("getenforce").Output()
	if err != nil || strings.TrimSpace(string(out)) == "Disabled" {
		return
	}
	module := "../../../installer/selinux/whacp_postfix_sockets.cil"
	if _, err := os.Stat(module); err != nil {
		t.Fatalf("SELinux module: %v", err)
	}
	if out, err := exec.Command("semodule", "-i", module).CombinedOutput(); err != nil {
		t.Fatalf("semodule -i: %s", out)
	}
}
