//go:build realhost

package agentfs

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/filesystem"
)

// A full disk never leaves a half-written configuration: the write fails
// with ENOSPC, the previous file is untouched and no temporary file stays.
func TestRealHostFullDiskKeepsTheOldFile(t *testing.T) {
	base := t.TempDir()
	if out, err := exec.Command("mount", "-t", "tmpfs", "-o", "size=64k", "tmpfs", base).CombinedOutput(); err != nil {
		t.Skipf("cannot mount a tmpfs: %v %s", err, out)
	}
	defer exec.Command("umount", base).Run()
	os.MkdirAll(filepath.Join(base, "etc/nginx/sites-available"), 0o755)
	root, _ := filesystem.NewRoot(base)
	w := Writer{Root: root, Policy: Policy{Patterns: []string{"/etc/nginx/sites-available/nimvor-*.conf"}}}
	p := "/etc/nginx/sites-available/nimvor-a.com.conf"
	old := []byte("server { listen 80; }\n")
	if _, err := w.WriteFile(p, old, 0o644, Keep); err != nil {
		t.Fatal(err)
	}
	_, err := w.WriteFile(p, bytes.Repeat([]byte("x"), 256<<10), 0o644, Keep)
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("write on a full disk: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(base, p)); !bytes.Equal(got, old) {
		t.Fatalf("old file changed: %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(base, "etc/nginx/sites-available/.nimvor-tmp-*")); len(left) > 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}
