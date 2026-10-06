//go:build realhost

package mailmgr

import (
	"fmt"
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
func selinuxEnabled() bool {
	out, err := exec.Command("getenforce").Output()
	return err == nil && strings.TrimSpace(string(out)) != "Disabled"
}

func selinuxSockets(t *testing.T) {
	if !selinuxEnabled() {
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

// Postfix is stopped and started through systemd where it runs (as the
// agent does): a Postfix started by "postfix start" outside its unit makes
// a later "systemctl start postfix" fail ("already running") and leaves the
// unit's state wrong for the tests after.
func underSystemd() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

func postfixStop() {
	if underSystemd() {
		exec.Command("systemctl", "stop", "postfix").Run()
	}
	exec.Command("postfix", "stop").Run()
}

func postfixStart() ([]byte, error) {
	if underSystemd() {
		return exec.Command("systemctl", "restart", "postfix").CombinedOutput()
	}
	return exec.Command("postfix", "start").CombinedOutput()
}

// mailDiagnosis: why Postfix did not consult a policy service (its log,
// the restrictions, the socket and SELinux denials).
func mailDiagnosis(socket string) string {
	var b strings.Builder
	for _, c := range [][]string{
		{"sh", "-c", "postconf mynetworks smtpd_recipient_restrictions smtpd_relay_restrictions authorized_submit_users inet_interfaces"},
		{"sh", "-c", "ls -lZ /var/spool/postfix/private/" + socket + " 2>&1 || ls -l /var/spool/postfix/private/" + socket},
		{"journalctl", "--no-pager", "-n", "25", "--since", "-3min", "-t", "postfix/smtpd", "-t", "postfix/postdrop", "-t", "postfix/pickup", "-t", "postfix/cleanup", "-t", "postfix/master"},
		{"journalctl", "--no-pager", "-n", "15", "--since", "-3min", "-u", socket + ".service", "-u", socket + ".socket"},
		{"sh", "-c", "command -v ausearch >/dev/null && ausearch -m AVC -ts recent 2>&1 | tail -20"},
	} {
		out, _ := exec.Command(c[0], c[1:]...).CombinedOutput()
		fmt.Fprintf(&b, "$ %s\n%s\n", strings.Join(c, " "), strings.TrimSpace(string(out)))
	}
	return b.String()
}
