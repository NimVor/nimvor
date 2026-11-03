package pgmgr

import (
	"os"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// Saving the provisioning credentials must not change /etc/nimvor, which is 0750 root:nimvor so the panel's own
// account can read its configuration there. A 0700 folder made every panel page fail with a 500.
func TestSavingTheAdminFileKeepsTheConfigFolderReadableForThePanel(t *testing.T) {
	h := testhost.New(t)
	dir, err := h.Env.Root.Path("/etc/nimvor")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o750)
	if err := saveAdminFile(h.Env, Target{Host: "127.0.0.1", Port: 5432, User: AdminRole, Password: "secret"}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o750 {
		t.Fatalf("/etc/nimvor is now %v; the panel can no longer read its configuration", info.Mode().Perm())
	}
	f, _ := h.Env.Root.Path(AdminFile)
	if info, err := os.Stat(f); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file: %v %v", info, err)
	}
}
