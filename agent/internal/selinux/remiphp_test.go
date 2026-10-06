package selinux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

// A Remi PHP-FPM without its SELinux labels is relabelled (once) and
// restarted rather than reloaded; a correctly labelled one is left alone.
func TestRemiPHPRelabel(t *testing.T) {
	h := testhost.New(t)
	h.Env.M.PHPCompact = true
	h.Env.M.PHPFPMUnit = "php%s-php-fpm.service"
	bin := filepath.Join(h.Base, "opt/remi/php82/root/usr/sbin/php-fpm")
	os.MkdirAll(filepath.Dir(bin), 0o755)
	os.WriteFile(bin, []byte("x"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "etc/opt/remi/php82"), 0o755)
	label := "system_u:object_r:usr_t:s0"
	old := fileLabel
	fileLabel = func(string) string { return label }
	t.Cleanup(func() { fileLabel = old })
	h.Respond = func(id string, a []string) (string, error, bool) {
		switch id {
		case "getenforce":
			return "Enforcing\n", nil, true
		case "systemctl":
			if len(a) > 0 && a[0] == "is-active" {
				return "active\n", nil, true
			}
		}
		return "", nil, false
	}
	if !remiPHP(context.Background(), h.Env, "8.2") {
		t.Fatal("not relabelled")
	}
	for _, want := range []string{"semanage fcontext -a -e / /opt/remi/php82/root", "semanage fcontext -a -e /etc /etc/opt/remi/php82", "restorecon -R -- /opt/remi/php82/root"} {
		if !h.Runner.Called(want) {
			t.Errorf("missing %q in\n%s", want, strings.Join(h.Runner.Calls, "\n"))
		}
	}
	label = "system_u:object_r:httpd_exec_t:s0"
	if remiPHP(context.Background(), h.Env, "8.2") {
		t.Error("relabelled a correctly labelled PHP-FPM")
	}
	h.Env.M.PHPCompact = false
	if remiPHP(context.Background(), h.Env, "8.2") {
		t.Error("ran on a Debian-style layout")
	}
}
