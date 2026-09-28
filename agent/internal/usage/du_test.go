package usage

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestDiskUsage(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "public_html", "big"), 0o755)
	os.WriteFile(filepath.Join(root, "public_html", "big", "a"), make([]byte, 5000), 0o644)
	os.WriteFile(filepath.Join(root, "public_html", "index.php"), make([]byte, 100), 0o644)
	os.MkdirAll(filepath.Join(root, "logs"), 0o755)
	os.WriteFile(filepath.Join(root, "logs", "access.log"), make([]byte, 700), 0o644)
	os.WriteFile(filepath.Join(root, "loose.txt"), make([]byte, 30), 0o644)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "huge"), make([]byte, 99999), 0o644)
	os.Symlink(outside, filepath.Join(root, "link"))
	mail := t.TempDir()
	os.WriteFile(filepath.Join(mail, "m"), make([]byte, 2000), 0o644)

	res, err := DiskUsage(context.Background(), root, "", mail, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	want := []DUEntry{{"public_html", "dir", 5100, 2}, {"mail", "mail", 2000, 1}, {"logs", "dir", 700, 1}, {"", "files", 30, 1}}
	if len(res.Entries) != len(want) {
		t.Fatalf("entries: %+v", res.Entries)
	}
	for i, w := range want {
		if res.Entries[i] != w {
			t.Errorf("entry %d: %+v, want %+v", i, res.Entries[i], w)
		}
	}
	if res.Bytes != 7830 {
		t.Fatalf("total %d (the link must not be followed)", res.Bytes)
	}
	for _, bad := range []string{"..", "a/../..", "/etc", "a//b", "./x"} {
		if _, err := cleanRel(bad); err == nil && bad != "/etc" {
			t.Errorf("%q accepted", bad)
		}
	}
	if p, err := cleanRel("/public_html/big/"); err != nil || p != "public_html/big" {
		t.Fatalf("clean: %q %v", p, err)
	}
}

func TestDiskUsageActionRefusesLinks(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	site, _ := h.Env.Root.Path(filepath.Join(h.Env.Cfg.SitesRoot, "shop.test"))
	os.MkdirAll(filepath.Join(site, "public_html"), 0o755)
	os.Symlink("/etc", filepath.Join(site, "etc-link"))
	call := func(path string) error {
		a, _ := r.Lookup("sites.disk_usage")
		b, _ := json.Marshal(map[string]string{"domain": "shop.test", "path": path})
		_, err := a.Handler(context.Background(), b)
		return err
	}
	if err := call("public_html"); err != nil {
		t.Fatalf("real folder: %v", err)
	}
	for _, p := range []string{"etc-link", "etc-link/ssl", "missing", "../other.test"} {
		if err := call(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}
