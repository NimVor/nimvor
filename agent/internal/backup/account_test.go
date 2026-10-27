package backup

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/execx"
)

func TestKeyExportImport(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	if _, err := ExportKey(key, "short"); err == nil {
		t.Fatal("a short passphrase was accepted")
	}
	kf, err := ExportKey(key, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := json.Marshal(kf)
	if bytes.Contains(doc, key) || strings.Contains(string(doc), string(key)) {
		t.Fatal("the key file holds the key in clear")
	}
	got, err := UnwrapKey(doc, "correct horse battery staple")
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("unwrap: %v", err)
	}
	if _, err := UnwrapKey(doc, "wrong horse battery staple"); !errors.Is(err, ErrKeyPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	forged := *kf
	forged.KeyID = "0000000000000000"
	fdoc, _ := json.Marshal(forged)
	if _, err := UnwrapKey(fdoc, "correct horse battery staple"); err == nil {
		t.Fatal("a key file with a changed key id was accepted")
	}
	slow := *kf
	slow.Iterations = 100_000_000
	sdoc, _ := json.Marshal(slow)
	if _, err := UnwrapKey(sdoc, "correct horse battery staple"); err == nil {
		t.Fatal("an absurd iteration count was accepted")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "state", "backup.key")
	if already, err := InstallKey(path, key); err != nil || already {
		t.Fatalf("install: %v %v", already, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %v", info.Mode())
	}
	if already, err := InstallKey(path, key); err != nil || !already {
		t.Fatalf("same key again: %v %v", already, err)
	}
	other := make([]byte, 32)
	rand.Read(other)
	if _, err := InstallKey(path, other); err == nil {
		t.Fatal("a different key replaced the server's key")
	}
	if b, _ := os.ReadFile(path); !bytes.Equal(b, key) {
		t.Fatal("the key file was changed")
	}
}

// An account backup made on one server is uploaded to another, refused
// there until the first server's key is imported, then imported and
// restored; changed files are refused.
func TestAccountBackupOntoANewServer(t *testing.T) {
	ctx := context.Background()
	var log bytes.Buffer
	h1, s1 := siteHost(t)
	cred1, _, _ := h1.Env.Credential("example")
	req := AccountRequest{Username: "example", Label: "before termination", Meta: json.RawMessage(`{"user":{"username":"example"}}`),
		Sites: []SiteRequest{{Domain: "example.com", User: "example", Root: "/home/nimvor/example.com", Files: true, Databases: []string{"shop"}}}}
	if err := req.validate(s1); err != nil {
		t.Fatal(err)
	}
	if !req.Sites[0].Encrypt {
		t.Fatal("account backups must be encrypted")
	}
	m, file, err := s1.CreateAccount(ctx, req, map[string]*execx.Credential{"example": cred1}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if len(m.Sites) != 1 || m.Mac == "" || !strings.HasPrefix(file, "/var/backups/nimvor/accounts/example/") || !strings.HasSuffix(file, AccountSuffix) {
		t.Fatalf("manifest %+v file %s", m, file)
	}
	files := s1.AccountFiles()
	if len(files) != 1 || files[0].Where != "backups" || files[0].Problem != "" || files[0].Manifest == nil {
		t.Fatalf("files = %+v", files)
	}
	raw, err := os.ReadFile(filepath.Join(h1.Base, file))
	if err != nil {
		t.Fatal(err)
	}

	// The new server: upload in two chunks.
	h2, s2 := siteHost(t)
	half := len(raw) / 2
	if _, _, err := s2.UploadAccountChunk("example.nimvor-account", 0, raw[:half], false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s2.UploadAccountChunk("example.nimvor-account", 5, raw[half:], true); err == nil {
		t.Fatal("a chunk at the wrong offset was accepted")
	}
	_, up, err := s2.UploadAccountChunk("example.nimvor-account", int64(half), raw[half:], true)
	if err != nil || up != "/var/backups/nimvor/incoming/example.nimvor-account" {
		t.Fatalf("upload: %v %s", err, up)
	}
	if _, err := s2.InspectAccount(up); err == nil || !strings.Contains(err.Error(), "cannot be restored") {
		t.Fatalf("inspect without the key: %v", err)
	}
	if _, _, err := s2.ImportAccount(ctx, up, &log); err == nil {
		t.Fatal("imported without the key")
	}

	// Import the first server's key (via a key file and its passphrase).
	k1, _ := os.ReadFile(s1.keyReal())
	kf, _ := ExportKey(k1, "a long passphrase here")
	doc, _ := json.Marshal(kf)
	key, err := UnwrapKey(doc, "a long passphrase here")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallKey(s2.keyReal(), key); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.InspectAccount(up); err != nil {
		t.Fatalf("inspect with the key: %v", err)
	}
	got, sites, err := s2.ImportAccount(ctx, up, &log)
	if err != nil {
		t.Fatalf("import: %v\n%s", err, log.String())
	}
	if got.Username != "example" || len(sites) != 1 || sites[0]["imported"] != true {
		t.Fatalf("import result %+v %+v", got, sites)
	}
	id := m.Sites[0].ID
	if !h2.Exists("var/backups/nimvor/sites/example.com/" + id + "/manifest.json") {
		t.Fatal("the website backup was not imported")
	}
	cred2, _, _ := h2.Env.Credential("example")
	if _, err := s2.Restore(ctx, RestoreRequest{Domain: "example.com", User: "example", Root: "/home/nimvor/example.com", ID: id, Files: true, Databases: map[string]string{"shop": "shop"}}, cred2, &log); err != nil {
		t.Fatalf("restore: %v\n%s", err, log.String())
	}
	// Importing again keeps what is there.
	if _, sites, err := s2.ImportAccount(ctx, up, &log); err != nil || sites[0]["imported"] != false {
		t.Fatalf("second import: %v %+v", err, sites)
	}

	// A changed description (another account name) is refused.
	write := func(name string, b []byte) string {
		p := filepath.Join(h2.Base, "var/backups/nimvor/incoming", name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return "/var/backups/nimvor/incoming/" + name
	}
	forged := rewriteTar(t, raw, func(name string, b []byte) []byte {
		if name == "account.json" {
			return bytes.Replace(b, []byte(`"username": "example"`), []byte(`"username": "mallory"`), 1)
		}
		return b
	})
	if _, err := s2.InspectAccount(write("forged.nimvor-account", forged)); err == nil || !strings.Contains(err.Error(), "cannot be restored") {
		t.Fatalf("forged description: %v", err)
	}
	// A changed part is refused and nothing is left behind.
	h3, s3 := siteHost(t)
	k3, _ := os.ReadFile(s2.keyReal())
	InstallKey(s3.keyReal(), k3)
	damaged := rewriteTar(t, raw, func(name string, b []byte) []byte {
		if strings.HasSuffix(name, "files.tar.gz.enc") {
			b = append([]byte{}, b...)
			b[len(b)-3] ^= 0xff
		}
		return b
	})
	os.MkdirAll(filepath.Join(h3.Base, "var/backups/nimvor/incoming"), 0o700)
	p3 := filepath.Join(h3.Base, "var/backups/nimvor/incoming/damaged.nimvor-account")
	os.WriteFile(p3, damaged, 0o600)
	if _, _, err := s3.ImportAccount(ctx, "/var/backups/nimvor/incoming/damaged.nimvor-account", &log); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("damaged part: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(h3.Base, "var/backups/nimvor/sites/example.com"))
	if len(entries) != 0 {
		t.Fatalf("left behind: %v", entries)
	}
}

func TestAccountPathIsConfined(t *testing.T) {
	h, s := siteHost(t)
	os.MkdirAll(filepath.Join(h.Base, "var/backups/nimvor/incoming"), 0o700)
	os.WriteFile(filepath.Join(h.Base, "etc/x.nimvor-account"), []byte("x"), 0o600)
	os.Symlink(filepath.Join(h.Base, "etc/x.nimvor-account"), filepath.Join(h.Base, "var/backups/nimvor/incoming/link.nimvor-account"))
	for _, p := range []string{
		"/etc/passwd",
		"/etc/x.nimvor-account",
		"/var/backups/nimvor/incoming/../../../../etc/x.nimvor-account",
		"/var/backups/nimvor/incoming/link.nimvor-account",
		"var/backups/nimvor/incoming/a.nimvor-account",
	} {
		if _, err := s.accountPath(p); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
	if err := s.RemoveAccountFile("/mnt/disk/a.nimvor-account"); err == nil {
		t.Error("a file in a mounted folder may not be deleted")
	}
	for _, name := range []string{"../x.nimvor-account", "x.tar", ".x.nimvor-account", "a/b.nimvor-account"} {
		if _, _, err := s.UploadAccountChunk(name, 0, []byte("x"), true); err == nil {
			t.Errorf("upload name %q accepted", name)
		}
	}
}

// rewriteTar copies a tar, passing each regular file through fn.
func rewriteTar(t *testing.T, in []byte, fn func(name string, b []byte) []byte) []byte {
	t.Helper()
	tr := tar.NewReader(bytes.NewReader(in))
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		b = fn(h.Name, b)
		h.Size = int64(len(b))
		tw.WriteHeader(h)
		tw.Write(b)
	}
	tw.Close()
	return out.Bytes()
}
