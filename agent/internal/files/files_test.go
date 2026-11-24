package files

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func setup(t *testing.T) (*testhost.Host, *actions.Registry, string) {
	h := testhost.New(t)
	root := filepath.Join(h.Base, "home/nimvor/site.test")
	for _, d := range []string{"public_html/css", "logs", "tmp"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "public_html/index.php"), []byte("<?php echo 'hi';"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/secret"), []byte("root only"), 0o600)
	os.Symlink("/etc", filepath.Join(root, "public_html/etc-link"))
	os.Symlink(filepath.Join(h.Base, "etc/secret"), filepath.Join(root, "public_html/secret-link"))
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return h, r, root
}

func do(t *testing.T, r *actions.Registry, name string, params map[string]any) (any, error) {
	t.Helper()
	params["domain"] = "site.test"
	b, _ := json.Marshal(params)
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a.Handler(context.Background(), b)
}

func TestListReadWrite(t *testing.T) {
	_, r, root := setup(t)
	res, err := do(t, r, "files.list", map[string]any{"path": "public_html"})
	if err != nil {
		t.Fatal(err)
	}
	entries := res.([]Entry)
	if entries[0].Name != "css" || entries[0].Type != "dir" {
		t.Errorf("dirs first: %+v", entries)
	}
	read, err := do(t, r, "files.read", map[string]any{"path": "public_html/index.php"})
	if err != nil || read.(*ReadResult).Content != "<?php echo 'hi';" {
		t.Fatalf("read: %v %v", read, err)
	}
	mtime := read.(*ReadResult).ModTime
	if _, err := do(t, r, "files.write", map[string]any{"path": "public_html/index.php", "content": "v2", "expected_mtime": mtime}); err != nil {
		t.Fatal(err)
	}
	if _, err := do(t, r, "files.write", map[string]any{"path": "public_html/index.php", "content": "v3", "expected_mtime": mtime.Add(-time.Hour)}); err == nil {
		t.Error("stale write accepted")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "public_html/index.php")); string(b) != "v2" {
		t.Errorf("content = %q", b)
	}
}

func TestConfinement(t *testing.T) {
	h, r, _ := setup(t)
	for _, p := range []string{"../../etc/passwd", "public_html/etc-link/passwd", "public_html/secret-link", "/../x"} {
		if _, err := do(t, r, "files.read", map[string]any{"path": p}); err == nil {
			t.Errorf("read %s escaped the site root", p)
		}
		if _, err := do(t, r, "files.write", map[string]any{"path": p, "content": "pwned"}); err == nil {
			t.Errorf("write %s escaped the site root", p)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(h.Base, "etc/secret")); string(b) != "root only" {
		t.Fatal("file outside the root was modified")
	}
	if _, err := do(t, r, "files.list", map[string]any{"path": "public_html/etc-link"}); err == nil {
		t.Error("listed a directory outside the root via symlink")
	}
	if _, err := do(t, r, "files.read", map[string]any{"domain": "", "path": "x"}); err == nil {
		t.Error("empty domain accepted")
	}
}

func TestProtectedAndOperations(t *testing.T) {
	_, r, root := setup(t)
	for _, p := range []string{"", ".", "public_html", "logs"} {
		if _, err := do(t, r, "files.delete", map[string]any{"paths": []string{p}}); err == nil {
			t.Errorf("deleted protected %q", p)
		}
	}
	if _, err := do(t, r, "files.copy", map[string]any{"from": "public_html", "to": "public_html/css/copy"}); err == nil {
		t.Error("copied a folder into itself")
	}
	if _, err := do(t, r, "files.mkdir", map[string]any{"path": "public_html/new"}); err != nil {
		t.Fatal(err)
	}
	if _, err := do(t, r, "files.copy", map[string]any{"from": "public_html/index.php", "to": "public_html/new/index.php"}); err != nil {
		t.Fatal(err)
	}
	if _, err := do(t, r, "files.rename", map[string]any{"from": "public_html/new", "to": "public_html/renamed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := do(t, r, "files.chmod", map[string]any{"path": "public_html/renamed/index.php", "mode": "4755"}); err == nil {
		t.Error("setuid accepted")
	}
	if _, err := do(t, r, "files.chmod", map[string]any{"path": "public_html/renamed/index.php", "mode": "600"}); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(root, "public_html/renamed/index.php"))
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", info.Mode())
	}
	res, _ := do(t, r, "files.search", map[string]any{"query": "INDEX"})
	if len(res.(map[string]any)["results"].([]Hit)) != 2 {
		t.Errorf("search: %+v", res)
	}
	if _, err := do(t, r, "files.delete", map[string]any{"paths": []string{"public_html/renamed"}}); err != nil {
		t.Fatal(err)
	}
	// The symlink to /etc is deleted as a link; /etc itself is untouched.
	if _, err := do(t, r, "files.delete", map[string]any{"paths": []string{"public_html/etc-link"}}); err != nil {
		t.Fatal(err)
	}
}

func TestChunkedUploadAndDownload(t *testing.T) {
	_, r, root := setup(t)
	parts := []string{strings.Repeat("a", 1000), strings.Repeat("b", 500)}
	off := int64(0)
	for i, p := range parts {
		res, err := do(t, r, "files.upload", map[string]any{"path": "public_html/big.bin", "offset": off, "data": base64.StdEncoding.EncodeToString([]byte(p)), "final": i == len(parts)-1})
		if err != nil {
			t.Fatal(err)
		}
		off = res.(map[string]any)["size"].(int64)
	}
	b, _ := os.ReadFile(filepath.Join(root, "public_html/big.bin"))
	if len(b) != 1500 {
		t.Fatalf("uploaded size %d", len(b))
	}
	if _, err := do(t, r, "files.upload", map[string]any{"path": "public_html/x", "offset": 99, "data": "YQ=="}); err == nil {
		t.Error("offset mismatch accepted")
	}
	res, err := do(t, r, "files.download", map[string]any{"path": "public_html/big.bin", "offset": 1000})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	data, _ := base64.StdEncoding.DecodeString(m["data"].(string))
	if string(data) != parts[1] || m["eof"] != true {
		t.Fatalf("download = %d bytes eof=%v", len(data), m["eof"])
	}
}

// A copy or move onto a name that is taken is refused, unless the caller chose to replace it; the replaced file goes
// to the website's trash so the choice can be undone.
func TestCopyAndMoveReplaceOnlyWhenAsked(t *testing.T) {
	_, r, root := setup(t)
	write := func(p, c string) { os.WriteFile(filepath.Join(root, p), []byte(c), 0o644) }
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(root, p)); return string(b) }
	write("public_html/a.txt", "new")
	write("public_html/b.txt", "old")

	for _, op := range []string{"files.copy", "files.rename"} {
		_, err := do(t, r, op, map[string]any{"from": "public_html/a.txt", "to": "public_html/b.txt"})
		var ae *actions.Error
		if !errors.As(err, &ae) || ae.Status != 409 {
			t.Fatalf("%s onto an existing name: want a 409 conflict, got %v", op, err)
		}
		if read("public_html/b.txt") != "old" {
			t.Fatalf("%s changed the file without being asked", op)
		}
	}

	if _, err := do(t, r, "files.copy", map[string]any{"from": "public_html/a.txt", "to": "public_html/b.txt", "overwrite": true}); err != nil {
		t.Fatal(err)
	}
	if read("public_html/b.txt") != "new" || read("public_html/a.txt") != "new" {
		t.Fatal("the copy did not replace the file, or lost the original")
	}
	list, err := do(t, r, "files.trash_list", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	items := list.(map[string]any)["items"].([]TrashItem)
	if len(items) != 1 || items[0].Path != "public_html/b.txt" {
		t.Fatalf("the replaced file is not in the trash: %+v", items)
	}

	// A move replaces too, and a folder can replace a file.
	write("public_html/c.txt", "moved")
	if _, err := do(t, r, "files.rename", map[string]any{"from": "public_html/c.txt", "to": "public_html/b.txt", "overwrite": true}); err != nil {
		t.Fatal(err)
	}
	if read("public_html/b.txt") != "moved" {
		t.Fatal("the move did not replace the file")
	}
	if _, err := os.Stat(filepath.Join(root, "public_html/c.txt")); err == nil {
		t.Fatal("the moved file is still at its old place")
	}
	if _, err := do(t, r, "files.rename", map[string]any{"from": "public_html/css", "to": "public_html/b.txt", "overwrite": true}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(root, "public_html/b.txt")); err != nil || !fi.IsDir() {
		t.Fatal("the folder did not replace the file")
	}

	// Nothing may replace itself, something inside it, or a top-level folder.
	for _, c := range []map[string]any{
		{"from": "public_html/a.txt", "to": "public_html/a.txt", "overwrite": true},
		{"from": "public_html/b.txt/x", "to": "public_html/b.txt", "overwrite": true},
		{"from": "public_html/a.txt", "to": "public_html", "overwrite": true},
		{"from": "public_html/a.txt", "to": "logs", "overwrite": true},
	} {
		if _, err := do(t, r, "files.copy", c); err == nil {
			t.Errorf("accepted %v", c)
		}
	}
	if read("public_html/a.txt") != "new" {
		t.Fatal("a refused replacement changed the source")
	}
}
