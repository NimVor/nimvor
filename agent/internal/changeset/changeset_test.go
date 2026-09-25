package changeset

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
)

func setup(t *testing.T) (agentfs.Writer, string) {
	base := t.TempDir()
	os.MkdirAll(filepath.Join(base, "etc/nginx/sites-available"), 0o755)
	os.MkdirAll(filepath.Join(base, "etc/nginx/sites-enabled"), 0o755)
	os.MkdirAll(filepath.Join(base, "var/lib/whacp-agent"), 0o755)
	root, _ := filesystem.NewRoot(base)
	return agentfs.Writer{Root: root, Policy: agentfs.Policy{Patterns: []string{
		"/etc/nginx/sites-available/whacp-*", "/etc/nginx/sites-enabled/whacp-*", "/var/lib/whacp-agent/**",
	}}, Owners: agentfs.SelfOwners()}, base
}

func read(t *testing.T, base, p string) string {
	b, err := os.ReadFile(filepath.Join(base, p))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func TestCommitRollsBackOnValidationFailure(t *testing.T) {
	w, base := setup(t)
	os.WriteFile(filepath.Join(base, "etc/nginx/sites-available/whacp-a.conf"), []byte("working"), 0o644)

	s := New(w, "/var/lib/whacp-agent/config-backups", "a")
	s.Write("/etc/nginx/sites-available/whacp-a.conf", []byte("broken"), 0o644, agentfs.Keep)
	s.Write("/etc/nginx/sites-available/whacp-b.conf", []byte("new"), 0o644, agentfs.Keep)
	s.Link("../sites-available/whacp-b.conf", "/etc/nginx/sites-enabled/whacp-b.conf")

	_, err := s.Commit(func() error {
		if read(t, base, "etc/nginx/sites-available/whacp-a.conf") != "broken" {
			t.Error("validation must see the new files")
		}
		return &ValidationError{Step: "nginx -t", Output: "emerg", Err: errors.New("exit 1")}
	})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("err = %v", err)
	}
	if got := read(t, base, "etc/nginx/sites-available/whacp-a.conf"); got != "working" {
		t.Errorf("previous config not restored: %q", got)
	}
	if got := read(t, base, "etc/nginx/sites-available/whacp-b.conf"); got != "<missing>" {
		t.Errorf("new file not removed: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(base, "etc/nginx/sites-enabled/whacp-b.conf")); err == nil {
		t.Error("symlink not removed")
	}
}

func TestCommitSuccessKeepsBackupAndIsIdempotent(t *testing.T) {
	w, base := setup(t)
	os.WriteFile(filepath.Join(base, "etc/nginx/sites-available/whacp-a.conf"), []byte("v1"), 0o644)
	mk := func() *Set {
		s := New(w, "/var/lib/whacp-agent/config-backups", "a")
		s.Write("/etc/nginx/sites-available/whacp-a.conf", []byte("v2"), 0o644, agentfs.Keep)
		return s
	}
	calls := 0
	a, err := mk().Commit(func() error { calls++; return nil })
	if err != nil || len(a.Changed) != 1 || a.Backup == "" {
		t.Fatalf("commit: %+v %v", a, err)
	}
	orig := read(t, base, filepath.Join(a.Backup, "000.orig"))
	if orig != "v1" {
		t.Errorf("backup content = %q", orig)
	}
	a, err = mk().Commit(func() error { calls++; return nil })
	if err != nil || len(a.Changed) != 0 || calls != 1 {
		t.Fatalf("second commit should be a no-op without validation: %+v calls=%d", a, calls)
	}
}

func TestApplyRollsBackOnMidwayError(t *testing.T) {
	w, base := setup(t)
	s := New(w, "", "x")
	s.Write("/etc/nginx/sites-available/whacp-ok.conf", []byte("x"), 0o644, agentfs.Keep)
	s.Write("/etc/passwd", []byte("pwned"), 0o644, agentfs.Keep)
	if _, err := s.Apply(); err == nil {
		t.Fatal("expected policy error")
	}
	if read(t, base, "etc/nginx/sites-available/whacp-ok.conf") != "<missing>" {
		t.Error("earlier op not rolled back")
	}
}
