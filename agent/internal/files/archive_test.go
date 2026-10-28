package files

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openSite(t *testing.T) (*Site, string) {
	t.Helper()
	h, _, root := setup(t)
	s, err := Open(h.Env, "site.test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root
}

func TestCompressAndExtractRoundTrip(t *testing.T) {
	for _, ext := range []string{".zip", ".tar.gz"} {
		s, root := openSite(t)
		os.WriteFile(filepath.Join(root, "public_html/css/app.css"), []byte("body{}"), 0o640)
		var log bytes.Buffer
		n, err := s.Compress(context.Background(), []string{"public_html"}, "backup"+ext, &log)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if n < 3 {
			t.Errorf("%s: %d entries", ext, n)
		}
		os.MkdirAll(filepath.Join(root, "restore"), 0o755)
		res, err := s.Extract(context.Background(), "backup"+ext, "restore", false, &log)
		if err != nil {
			t.Fatalf("%s: %v\n%s", ext, err, log.String())
		}
		b, err := os.ReadFile(filepath.Join(root, "restore/public_html/css/app.css"))
		if err != nil || string(b) != "body{}" {
			t.Fatalf("%s: extracted %q %v (%+v)", ext, b, err, res)
		}
		if info, _ := os.Stat(filepath.Join(root, "restore/public_html/css/app.css")); info.Mode().Perm() != 0o640 {
			t.Errorf("%s: mode %v", ext, info.Mode())
		}
		if _, err := os.Lstat(filepath.Join(root, "restore/public_html/etc-link")); err == nil {
			t.Errorf("%s: symlink was archived", ext)
		}
		// A second extraction keeps existing files unless asked to overwrite.
		os.WriteFile(filepath.Join(root, "restore/public_html/index.php"), []byte("changed"), 0o644)
		res, _ = s.Extract(context.Background(), "backup"+ext, "restore", false, &log)
		if len(res.Existing) == 0 || readFile(root, "restore/public_html/index.php") != "changed" {
			t.Errorf("%s: existing file replaced without overwrite: %+v", ext, res)
		}
		s.Extract(context.Background(), "backup"+ext, "restore", true, &log)
		if readFile(root, "restore/public_html/index.php") != "<?php echo 'hi';" {
			t.Errorf("%s: overwrite did not replace", ext)
		}
		if _, err := s.Compress(context.Background(), []string{"public_html"}, "backup"+ext, &log); err == nil {
			t.Errorf("%s: existing archive overwritten", ext)
		}
		if _, err := s.Compress(context.Background(), []string{"public_html"}, "public_html/inside"+ext, &log); err == nil {
			t.Errorf("%s: archive inside the compressed folder accepted", ext)
		}
	}
}

func readFile(root, p string) string {
	b, _ := os.ReadFile(filepath.Join(root, p))
	return string(b)
}

func TestExtractRefusesHostileEntries(t *testing.T) {
	s, root := openSite(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"../../escape.txt", "/abs.txt", "ok/../../../x.txt", "good/file.txt", ".nimvor-tmp-1"} {
		w, _ := zw.Create(name)
		w.Write([]byte("data"))
	}
	link := &zip.FileHeader{Name: "link"}
	link.SetMode(os.ModeSymlink | 0o777)
	w, _ := zw.CreateHeader(link)
	w.Write([]byte("/etc/passwd"))
	zw.Close()
	os.WriteFile(filepath.Join(root, "evil.zip"), buf.Bytes(), 0o644)

	var log bytes.Buffer
	res, err := s.Extract(context.Background(), "evil.zip", ".", false, &log)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 1 || readFile(root, "good/file.txt") != "data" {
		t.Errorf("result %+v", res)
	}
	for _, p := range []string{"escape.txt", "../escape.txt", "abs.txt", "x.txt", "link", ".nimvor-tmp-1"} {
		if _, err := os.Lstat(filepath.Join(root, p)); err == nil {
			t.Errorf("%s was written", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(root), "escape.txt")); err == nil {
		t.Error("escaped the website")
	}

	// A symlink already inside the site is not followed by extraction.
	os.Symlink("/tmp", filepath.Join(root, "tmp-link"))
	var tb bytes.Buffer
	gz := gzip.NewWriter(&tb)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "tmp-link/pwned", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.WriteHeader(&tar.Header{Name: "setuid", Mode: 0o4755, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.WriteHeader(&tar.Header{Name: "dev", Typeflag: tar.TypeChar, Mode: 0o666})
	tw.Close()
	gz.Close()
	os.WriteFile(filepath.Join(root, "evil.tar.gz"), tb.Bytes(), 0o644)
	_, err = s.Extract(context.Background(), "evil.tar.gz", ".", false, &log)
	if err == nil || !strings.Contains(err.Error(), "not a folder") {
		t.Errorf("writing below a symlink: err = %v", err)
	}
	if _, err := os.Stat("/tmp/pwned"); err == nil {
		os.Remove("/tmp/pwned")
		t.Fatal("wrote through a symlink")
	}
}

func TestExtractModesAndBomb(t *testing.T) {
	s, root := openSite(t)
	var tb bytes.Buffer
	tw := tar.NewWriter(&tb)
	tw.WriteHeader(&tar.Header{Name: "setuid", Mode: 0o4755, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.WriteHeader(&tar.Header{Name: "dev", Typeflag: tar.TypeChar, Mode: 0o666})
	tw.WriteHeader(&tar.Header{Name: "huge", Mode: 0o644, Size: maxArchiveBytes + 1, Typeflag: tar.TypeReg})
	tw.Flush()
	os.WriteFile(filepath.Join(root, "a.tar"), tb.Bytes(), 0o644)
	var log bytes.Buffer
	res, err := s.Extract(context.Background(), "a.tar", ".", false, &log)
	if err == nil || !strings.Contains(err.Error(), "8 GiB") {
		t.Errorf("oversized entry: %v %+v", err, res)
	}
	info, err := os.Stat(filepath.Join(root, "setuid"))
	if err != nil || info.Mode()&os.ModeSetuid != 0 || info.Mode().Perm() != 0o755 {
		t.Errorf("setuid: %v %v", info, err)
	}
	if _, err := os.Lstat(filepath.Join(root, "dev")); err == nil {
		t.Error("device node created")
	}
}
