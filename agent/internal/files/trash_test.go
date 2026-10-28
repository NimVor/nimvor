package files

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func items(t *testing.T, res any) []TrashItem {
	t.Helper()
	b, _ := json.Marshal(res.(map[string]any)["items"])
	var out []TrashItem
	json.Unmarshal(b, &out)
	return out
}

// Deleting moves to the trash; restore puts it back (a new name when the
// old one is taken, missing folders recreated); permanent delete and
// emptying remove for good.
func TestTrashRoundTrip(t *testing.T) {
	h, r, root := setup(t)
	os.MkdirAll(filepath.Join(root, "public_html/blog/img"), 0o755)
	os.WriteFile(filepath.Join(root, "public_html/blog/img/a.png"), []byte("12345"), 0o644)
	os.WriteFile(filepath.Join(root, "public_html/blog/post.txt"), []byte("abc"), 0o640)

	res, err := do(t, r, "files.delete", map[string]any{"paths": []string{"public_html/blog", "public_html/index.php"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["trashed"] != true || res.(map[string]any)["deleted"] != 2 {
		t.Fatalf("%v", res)
	}
	if _, err := os.Lstat(filepath.Join(root, "public_html/blog")); !os.IsNotExist(err) {
		t.Fatal("still in place")
	}
	info, err := os.Stat(filepath.Join(root, TrashDir))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("trash folder: %v %v", info, err)
	}
	// Hidden from the file manager, and not reachable through it.
	list, _ := do(t, r, "files.list", map[string]any{"path": ""})
	if b, _ := json.Marshal(list); strings.Contains(string(b), TrashDir) {
		t.Fatal("trash listed")
	}
	if _, err := do(t, r, "files.list", map[string]any{"path": TrashDir}); err == nil {
		t.Fatal("trash browsable")
	}

	tl, _ := do(t, r, "files.trash_list", map[string]any{})
	got := items(t, tl)
	if len(got) != 2 {
		t.Fatalf("%+v", got)
	}
	var blog, index TrashItem
	for _, it := range got {
		if it.Path == "public_html/blog" {
			blog = it
		} else {
			index = it
		}
	}
	if blog.Type != "dir" || blog.Size != 8 || index.Type != "file" || !index.DeletedAt.Equal(h.Env.Now()) {
		t.Fatalf("%+v %+v", blog, index)
	}

	// A new index.php meanwhile: the old one comes back beside it.
	os.WriteFile(filepath.Join(root, "public_html/index.php"), []byte("new"), 0o644)
	os.RemoveAll(filepath.Join(root, "public_html"))
	os.MkdirAll(filepath.Join(root, "public_html"), 0o755)
	os.WriteFile(filepath.Join(root, "public_html/index.php"), []byte("new"), 0o644)
	rs, err := do(t, r, "files.trash_restore", map[string]any{"ids": []string{blog.ID, index.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(rs); !strings.Contains(string(b), `"path":"public_html/index (restored).php"`) || !strings.Contains(string(b), `"path":"public_html/blog"`) {
		t.Fatalf("%s", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "public_html/blog/img/a.png")); string(b) != "12345" {
		t.Fatal("folder not restored with its content")
	}
	if info, _ := os.Stat(filepath.Join(root, "public_html/blog/post.txt")); info.Mode().Perm() != 0o640 {
		t.Fatal("mode not kept")
	}
	if tl, _ := do(t, r, "files.trash_list", map[string]any{}); len(items(t, tl)) != 0 {
		t.Fatal("restored items still in the trash")
	}

	// Permanent: straight away, nothing in the trash.
	if res, _ := do(t, r, "files.delete", map[string]any{"paths": []string{"public_html/blog/post.txt"}, "permanent": true}); res.(map[string]any)["trashed"] != false {
		t.Fatalf("%v", res)
	}
	if tl, _ := do(t, r, "files.trash_list", map[string]any{}); len(items(t, tl)) != 0 {
		t.Fatal("permanent delete went to the trash")
	}

	// Delete one item for good, empty by age, empty all.
	do(t, r, "files.delete", map[string]any{"paths": []string{"public_html/blog/img"}})
	do(t, r, "files.delete", map[string]any{"paths": []string{"public_html/index.php"}})
	tl, _ = do(t, r, "files.trash_list", map[string]any{})
	all := items(t, tl)
	if _, err := do(t, r, "files.trash_delete", map[string]any{"ids": []string{all[0].ID}}); err != nil {
		t.Fatal(err)
	}
	if res, _ := do(t, r, "files.trash_empty", map[string]any{"older_than_days": 30}); res.(map[string]any)["deleted"] != 0 {
		t.Fatalf("recent items purged: %v", res)
	}
	if res, _ := do(t, r, "files.trash_empty", map[string]any{}); res.(map[string]any)["deleted"] != 1 {
		t.Fatalf("%v", res)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, TrashDir)); len(entries) != 0 {
		t.Fatalf("left: %v", entries)
	}
}

// What the account can put in its trash folder cannot lead a restore or a
// delete outside the website folder or onto reserved names.
func TestTrashDistrustsItsContent(t *testing.T) {
	h, r, root := setup(t)
	do(t, r, "files.delete", map[string]any{"paths": []string{"public_html/index.php"}})
	tl, _ := do(t, r, "files.trash_list", map[string]any{})
	it := items(t, tl)[0]
	meta := filepath.Join(root, TrashDir, it.ID, "meta.json")
	for _, bad := range []string{"../../../etc/evil", "/etc/evil", ".nimvor-trash/x", "public_html", ""} {
		b, _ := json.Marshal(TrashItem{Path: bad})
		os.WriteFile(meta, b, 0o600)
		if _, err := do(t, r, "files.trash_restore", map[string]any{"ids": []string{it.ID}}); err == nil {
			t.Errorf("restored to %q", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(h.Base, "etc/evil")); err == nil {
		t.Fatal("wrote outside the website")
	}
	for _, bad := range []string{"../../etc", "x/../../y", "20260901T120000Z-zzzzzzzz"} {
		if _, err := do(t, r, "files.trash_delete", map[string]any{"ids": []string{bad}}); err == nil {
			t.Errorf("accepted id %q", bad)
		}
	}
	// A trash folder replaced by a link is refused, not followed.
	os.RemoveAll(filepath.Join(root, TrashDir))
	os.Symlink("public_html", filepath.Join(root, TrashDir))
	if _, err := do(t, r, "files.delete", map[string]any{"paths": []string{"public_html/css"}}); err == nil || !strings.Contains(err.Error(), "not a folder") {
		t.Fatalf("%v", err)
	}
}
