//go:build realhost

package security

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// A key added for an administrator in the panel lets it sign in over SSH
// (owner, modes and, on RHEL, SELinux labels right); removing it ends that.
func TestRealHostAdminSSHKey(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(30*time.Second), jm, log)
	ctx := context.Background()

	group := "sudo"
	if b, _ := os.ReadFile("/etc/group"); !strings.Contains(string(b), "\nsudo:") {
		group = "wheel"
	}
	const name = "whacpadm"
	if out, err := exec.Command("useradd", "-m", "-s", "/bin/bash", "-G", group, name).CombinedOutput(); err != nil {
		t.Fatalf("useradd: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("userdel", "-r", name).Run() })

	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "rt@whacp", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	k, err := AddKey(ctx, env, name, string(pub))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range Admins(env) {
		for _, have := range a.Keys {
			found = found || (a.User == name && have.Fingerprint == k.Fingerprint)
		}
	}
	if !found {
		t.Fatalf("the key is not listed: %+v", Admins(env))
	}
	login := func() ([]byte, error) {
		// Standard output only: ssh warns about the host key on stderr.
		return exec.Command("ssh", "-i", key, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "ConnectTimeout=10", name+"@127.0.0.1", "id -un").Output()
	}
	var out []byte
	for i := 0; i < 5; i++ {
		if out, err = login(); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil || strings.TrimSpace(string(out)) != name {
		ls, _ := exec.Command("ls", "-laZ", "/home/"+name+"/.ssh").CombinedOutput()
		t.Fatalf("ssh with the added key: %v %s\n%s", err, out, ls)
	}
	if err := RemoveKey(ctx, env, name, k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if out, err := login(); err == nil {
		t.Fatalf("still signed in after the key was removed: %s", out)
	}
}
