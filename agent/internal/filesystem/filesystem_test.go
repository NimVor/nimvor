package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPathRejectsTraversal(t *testing.T) {
	r, _ := NewRoot("/srv/root")
	for _, p := range []string{"relative/path", "/etc/../../root", "/a/..", "/x\x00y"} {
		if _, err := r.Path(p); !errors.Is(err, ErrOutsideRoot) {
			t.Errorf("Path(%q) error = %v, want ErrOutsideRoot", p, err)
		}
	}
	got, err := r.Path("/etc/nginx/nginx.conf")
	if err != nil || got != "/srv/root/etc/nginx/nginx.conf" {
		t.Fatalf("Path = %q, %v", got, err)
	}
}

func TestResolveKeepsAbsoluteLinksInsideRoot(t *testing.T) {
	base := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(base, "etc/a"), 0o755))
	must(t, os.WriteFile(filepath.Join(base, "etc/a/file"), []byte("inside"), 0o644))
	must(t, os.Symlink("/etc/a/file", filepath.Join(base, "etc/abs-link")))
	must(t, os.Symlink("../../../../../../etc/passwd", filepath.Join(base, "etc/escape")))
	r, _ := NewRoot(base)

	got, err := r.Resolve("/etc/abs-link")
	if err != nil || got != "/etc/a/file" {
		t.Fatalf("Resolve = %q, %v", got, err)
	}
	data, err := r.ReadFile("/etc/abs-link")
	if err != nil || string(data) != "inside" {
		t.Fatalf("ReadFile via link = %q, %v", data, err)
	}
	// ".." past the root is clamped at the root, like a chroot.
	got, err = r.Resolve("/etc/escape")
	if err == nil && got != "/etc/passwd" {
		t.Fatalf("escape resolved to %q", got)
	}
	if _, err := r.ReadFile("/etc/escape"); err == nil {
		t.Fatal("reading a link that escapes the root must fail (target does not exist in root)")
	}
}

func TestReadFileLimit(t *testing.T) {
	base := t.TempDir()
	must(t, os.WriteFile(filepath.Join(base, "big"), make([]byte, 100), 0o644))
	r, _ := NewRoot(base)
	r.MaxRead = 10
	if _, err := r.ReadFile("/big"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestReadFileRejectsDirectories(t *testing.T) {
	r, _ := NewRoot(t.TempDir())
	if _, err := r.ReadFile("/"); err == nil {
		t.Fatal("expected error reading a directory")
	}
}

func TestDirSizeBounded(t *testing.T) {
	base := t.TempDir()
	for i := 0; i < 5; i++ {
		must(t, os.WriteFile(filepath.Join(base, string(rune('a'+i))), make([]byte, 10), 0o644))
	}
	r, _ := NewRoot(base)
	size, complete, err := r.DirSize("/", 100)
	if err != nil || size != 50 || !complete {
		t.Fatalf("DirSize = %d, %v, %v", size, complete, err)
	}
	_, complete, _ = r.DirSize("/", 2)
	if complete {
		t.Fatal("expected incomplete walk")
	}
}

func TestWithinPrefixes(t *testing.T) {
	if !WithinPrefixes("/etc/nginx/sites-enabled/x", []string{"/etc/nginx"}) {
		t.Error("expected within")
	}
	if WithinPrefixes("/etc/nginxevil/x", []string{"/etc/nginx"}) {
		t.Error("prefix match must respect path boundaries")
	}
	if WithinPrefixes("/etc/nginx/../shadow", []string{"/etc/nginx"}) {
		t.Error("cleaned path must be checked")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
