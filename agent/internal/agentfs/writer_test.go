package agentfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/filesystem"
)

func newWriter(t *testing.T) (Writer, string) {
	t.Helper()
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "etc/nginx/sites-available"), 0o755)
	os.MkdirAll(filepath.Join(base, "home/nimvor"), 0o755)
	root, _ := filesystem.NewRoot(base)
	return Writer{Root: root, Policy: Policy{Patterns: []string{"/etc/nginx/sites-available/nimvor-*.conf", "/home/nimvor/**"}}}, base
}

func TestPolicy(t *testing.T) {
	p := Policy{Patterns: []string{"/etc/nginx/sites-available/nimvor-*.conf", "/home/nimvor/**"}}
	for _, ok := range []string{"/etc/nginx/sites-available/nimvor-a.com.conf", "/home/nimvor", "/home/nimvor/a.com/public_html/x"} {
		if !p.Allows(ok) {
			t.Errorf("%s refused", ok)
		}
	}
	for _, bad := range []string{"/etc/nginx/sites-available/default", "/etc/nginx/sites-available/nimvor-a/../../passwd", "/home/nimvorx/a", "/etc/passwd", "relative/nimvor-x.conf", "/etc/nginx/sites-available/sub/nimvor-a.conf"} {
		if p.Allows(bad) {
			t.Errorf("%s allowed", bad)
		}
	}
}

func TestWriteFileAtomicAndIdempotent(t *testing.T) {
	w, base := newWriter(t)
	p := "/etc/nginx/sites-available/nimvor-a.com.conf"
	changed, err := w.WriteFile(p, []byte("one"), 0o644, Keep)
	if err != nil || !changed {
		t.Fatalf("first write: %v %v", changed, err)
	}
	changed, err = w.WriteFile(p, []byte("one"), 0o644, Keep)
	if err != nil || changed {
		t.Fatalf("identical write should be a no-op: %v %v", changed, err)
	}
	if _, err := w.WriteFile("/etc/nginx/sites-available/default", []byte("x"), 0o644, Keep); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("unmanaged file written: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(base, "etc/nginx/sites-available"))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestSymlinkInsideUserSubtreeIsRefused(t *testing.T) {
	w, base := newWriter(t)
	os.MkdirAll(filepath.Join(base, "home/nimvor/a.com"), 0o755)
	os.Symlink("/etc", filepath.Join(base, "home/nimvor/a.com/evil"))
	if _, err := w.WriteFile("/home/nimvor/a.com/evil/passwd", []byte("x"), 0o644, Keep); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("write through symlink: %v", err)
	}
	os.Symlink(filepath.Join(base, "etc/hostname"), filepath.Join(base, "home/nimvor/a.com/link"))
	if _, err := w.WriteFile("/home/nimvor/a.com/link", []byte("x"), 0o644, Keep); !errors.Is(err, ErrNotPermitted) {
		t.Fatalf("write onto symlink: %v", err)
	}
}

func TestSnapshotRestore(t *testing.T) {
	w, _ := newWriter(t)
	p := "/etc/nginx/sites-available/nimvor-b.conf"
	w.WriteFile(p, []byte("old"), 0o640, Keep)
	st, _ := w.Snapshot(p)
	w.WriteFile(p, []byte("new"), 0o644, Keep)
	if err := w.Restore(p, st); err != nil {
		t.Fatal(err)
	}
	st2, _ := w.Snapshot(p)
	if string(st2.Data) != "old" || st2.Mode != 0o640 {
		t.Fatalf("restore: %q %v", st2.Data, st2.Mode)
	}
	if err := w.Restore(p, State{}); err != nil {
		t.Fatal(err)
	}
	if st3, _ := w.Snapshot(p); st3.Exists {
		t.Fatal("restore to non-existent should delete")
	}
}

// Replacing a file with Keep leaves its owner and group as they were (an
// atomic write creates a new file, which would otherwise be root's).
func TestKeepPreservesOwnerOnReplace(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to own files as another account")
	}
	w, base := newWriter(t)
	p := filepath.Join(base, "home/nimvor/named.conf")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("old"), 0o640)
	os.Chown(p, 0, 4242)
	if _, err := w.WriteFile("/home/nimvor/named.conf", []byte("new"), 0o640, Keep); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if st := fi.Sys().(*syscall.Stat_t); st.Uid != 0 || st.Gid != 4242 {
		t.Fatalf("owner %d:%d, want 0:4242", st.Uid, st.Gid)
	}
}

// A directory that exists keeps its mode: /etc/nimvor is 0750 root:nimvor so the panel can read its config, and an
// action that only needs a place for a root-only file must not lock the panel out (it once did, with MkdirAll).
func TestEnsureDirLeavesAnExistingDirectoryAlone(t *testing.T) {
	w, base := newWriter(t)
	w.Policy.Patterns = append(w.Policy.Patterns, "/etc/nimvor", "/etc/nimvor/**")
	w.Owners = SelfOwners()
	dir := filepath.Join(base, "etc/nimvor")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o750)
	created, err := w.EnsureDir("/etc/nimvor", 0o700, RootOwner)
	if err != nil || created {
		t.Fatalf("EnsureDir existing: created=%v err=%v", created, err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o750 {
		t.Fatalf("mode changed to %v", info.Mode().Perm())
	}
	// A missing one is created with the given mode.
	created, err = w.EnsureDir("/etc/nimvor/sub", 0o700, RootOwner)
	if err != nil || !created {
		t.Fatalf("EnsureDir missing: created=%v err=%v", created, err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "sub")); info.Mode().Perm() != 0o700 {
		t.Fatalf("new directory mode %v", info.Mode().Perm())
	}
	// A file in the way is an error, not silently replaced.
	os.WriteFile(filepath.Join(base, "etc/nimvor/file"), []byte("x"), 0o600)
	if _, err := w.EnsureDir("/etc/nimvor/file", 0o700, RootOwner); err == nil {
		t.Fatal("a file was treated as a directory")
	}
}
