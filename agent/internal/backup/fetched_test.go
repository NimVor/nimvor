package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
)

const testHostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

func sftpRemote(t *testing.T) Remote {
	t.Helper()
	r := Remote{Type: "sftp", Host: "files.example.com", Username: "bk", Password: "S3cret-pass", HostKey: testHostKey}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestFetchPathIsValidated(t *testing.T) {
	for _, p := range []string{"/backups/site.tar.gz", "site.tar.gz", "dir/with space/a b.zip", "/a/b.c"} {
		if err := validFetchPath(p); err != nil {
			t.Errorf("%q refused: %v", p, err)
		}
	}
	for _, p := range []string{"", "/backups/", "../x", "/a/../b", "a\nb", "a\x00b", strings.Repeat("a", 1025)} {
		if err := validFetchPath(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func TestFetchURLEscapesEverySegmentAndKeepsAbsoluteAndRelativeApart(t *testing.T) {
	sf := &fileRemote{R: sftpRemote(t)}
	cases := map[string]string{
		"/backups/site.tar.gz": "sftp://files.example.com:22/backups/site.tar.gz",
		"site.tar.gz":          "sftp://files.example.com:22/~/site.tar.gz",
		"/my dir/a b.zip":      "sftp://files.example.com:22/my%20dir/a%20b.zip",
		"d/../x":               "", // refused earlier; never reaches here
	}
	for in, want := range cases {
		if want == "" {
			continue
		}
		if got := sf.fetchURL(in); got != want {
			t.Errorf("sftp %q = %q, want %q", in, got, want)
		}
	}
	// A query or fragment character in a name is data, not syntax.
	if got := sf.fetchURL("/a/b?c#d.tar"); got != "sftp://files.example.com:22/a/b%3Fc%23d.tar" {
		t.Errorf("escape = %q", got)
	}
	ftp := Remote{Type: "ftp", Host: "ftp.example.com", Username: "bk", Password: "p"}
	ftp.Validate()
	ff := &fileRemote{R: ftp}
	if got := ff.fetchURL("/backups/a.tar"); got != "ftp://ftp.example.com:21//backups/a.tar" {
		t.Errorf("ftp absolute = %q", got)
	}
	if got := ff.fetchURL("a.tar"); got != "ftp://ftp.example.com:21/a.tar" {
		t.Errorf("ftp relative = %q", got)
	}
}

func TestLocalFetchNameIsAlwaysAFlatSafeName(t *testing.T) {
	cases := map[string]string{
		"/backups/site.tar.gz":   "site.tar.gz",
		"dir/My Site (1).zip":    "My-Site-1-.zip",
		"/x/.hidden":             "hidden",
		"/x/..":                  "fetched-file",
		"/x/日本語.tar":             "tar",
		"/x/a;rm -rf *.sql":      "a-rm--rf-.sql",
		strings.Repeat("a", 300): strings.Repeat("a", 150),
	}
	for in, want := range cases {
		got := localFetchName(in)
		if got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
		if !fetchedNameRe.MatchString(got) {
			t.Errorf("%q → %q is not an accepted name", in, got)
		}
	}
}

func TestFetchRequestNeedsPinnedKeyAndTLS(t *testing.T) {
	ok := FetchRequest{Remote: sftpRemote(t), Path: "/a/b.tar"}
	if err := ok.validate(); err != nil {
		t.Fatal(err)
	}
	noKey := ok
	noKey.Remote.HostKey = ""
	if err := noKey.validate(); err == nil || !strings.Contains(err.Error(), "host key") {
		t.Errorf("unpinned SFTP accepted: %v", err)
	}
	plain := FetchRequest{Remote: Remote{Type: "ftp", Host: "h.example.com", Username: "bk", Password: "p", TLS: "none"}, Path: "a.tar"}
	if err := plain.validate(); err == nil || !strings.Contains(err.Error(), "FTPS") {
		t.Errorf("plain FTP accepted: %v", err)
	}
	for _, rt := range []string{"s3", "local", "gdrive", ""} {
		bad := ok
		bad.Remote = Remote{Type: rt, Path: "/mnt/x"}
		if err := bad.validate(); err == nil {
			t.Errorf("type %q accepted", rt)
		}
	}
	for _, n := range []string{"../x", "a/b", ".hidden", "x.partial", "a b"} {
		bad := ok
		bad.Name = n
		if err := bad.validate(); err == nil {
			t.Errorf("name %q accepted", n)
		}
	}
}

func TestFetchRemoteSavesTheFileInTheFetchedFolderOnly(t *testing.T) {
	h, s := siteHost(t)
	body := "BACKUP-BYTES"
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "curl" {
			return body, nil, true
		}
		return "", nil, false
	}
	in := FetchRequest{Remote: sftpRemote(t), Path: "/srv/backups/../x"}
	if err := in.validate(); err == nil {
		t.Fatal("path with .. accepted")
	}
	in.Path = "/srv/backups/site 1.tar.gz"
	var log bytes.Buffer
	res, err := s.FetchRemote(context.Background(), in, &log)
	if err != nil {
		t.Fatalf("fetch: %v\n%s", err, log.String())
	}
	sum := sha256.Sum256([]byte(body))
	if res["name"] != "site-1.tar.gz" || res["size"] != int64(len(body)) || res["sha256"] != hex.EncodeToString(sum[:]) {
		t.Fatalf("result = %v", res)
	}
	p := filepath.Join(h.Base, "var/backups/nimvor/fetched/site-1.tar.gz")
	if got, _ := os.ReadFile(p); string(got) != body {
		t.Fatalf("file = %q", got)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	// Nothing else is left in the folder (no partial file).
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Errorf("folder = %v", entries)
	}
	// The password never appears in a command line or in the log.
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "S3cret") {
			t.Fatalf("password in arguments: %v", c)
		}
	}
	if strings.Contains(log.String(), "S3cret") {
		t.Error("password in log")
	}
	// The same file fetched again gets its own name, not an overwrite.
	res2, err := s.FetchRemote(context.Background(), in, &log)
	if err != nil || res2["name"] != "site-1-1.tar.gz" {
		t.Fatalf("second fetch = %v, %v", res2, err)
	}
	if got, _ := os.ReadFile(p); string(got) != body {
		t.Error("first file was overwritten")
	}
}

func TestFetchRemoteLeavesNothingBehindWhenItFailsOrIsEmpty(t *testing.T) {
	h, s := siteHost(t)
	in := FetchRequest{Remote: sftpRemote(t), Path: "/nope.tar"}
	dir := filepath.Join(h.Base, "var/backups/nimvor/fetched")

	h.Fail["curl"] = "curl: (78) Access denied: S3cret-pass"
	_, err := s.FetchRemote(context.Background(), in, &bytes.Buffer{})
	if err == nil || strings.Contains(err.Error(), "S3cret-pass") {
		t.Fatalf("failure = %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left after failure: %v", entries)
	}

	delete(h.Fail, "curl")
	_, err = s.FetchRemote(context.Background(), in, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("empty file accepted: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left after empty: %v", entries)
	}
}

func TestDeleteFetchedIsConfinedAndRepeatable(t *testing.T) {
	h, s := siteHost(t)
	dir := filepath.Join(h.Base, "var/backups/nimvor/fetched")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "a.tar"), []byte("a"), 0o600)
	os.WriteFile(filepath.Join(dir, "b.tar"), []byte("b"), 0o600)
	outside := filepath.Join(h.Base, "var/backups/nimvor/keep.txt")
	os.WriteFile(outside, []byte("keep"), 0o600)
	os.Symlink(outside, filepath.Join(dir, "link"))

	res, err := s.DeleteFetched([]string{"a.tar", "b.tar", "gone.tar"})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r["deleted"] != true {
			t.Errorf("not deleted: %v", r)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "a.tar")); err == nil {
		t.Error("a.tar left")
	}
	// A symbolic link is removed as a link; its target is untouched.
	if _, err := s.DeleteFetched([]string{"link"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Error("the file a link pointed to was deleted")
	}
	// The action refuses names that are not plain file names.
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	act, err := r.Lookup("backup.fetched_delete")
	if err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{{"../keep.txt"}, {"a/b"}, {".hidden"}, {}, {"ok.tar", "../x"}} {
		p, _ := json.Marshal(map[string]any{"names": names})
		if _, err := act.Handler(context.Background(), p); err == nil {
			t.Errorf("names %v accepted", names)
		}
	}
	if b, _ := os.ReadFile(outside); string(b) != "keep" {
		t.Error("file outside the folder deleted")
	}
}

func TestDeleteFetchedWithoutTheFolderIsAnEmptySuccess(t *testing.T) {
	_, s := siteHost(t)
	res, err := s.DeleteFetched([]string{"a.tar"})
	if err != nil || len(res) != 1 || res[0]["deleted"] != true {
		t.Fatalf("res = %v, err = %v", res, err)
	}
}

func TestFetchActionsAreRegisteredAndOnlyDeletingAndFetchingAreMutating(t *testing.T) {
	h, _ := siteHost(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	want := map[string]bool{"backup.fetch_probe": false, "backup.fetch_remote": true, "backup.fetched_delete": true}
	for name, mutating := range want {
		a, err := r.Lookup(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if a.Mutating != mutating {
			t.Errorf("%s mutating = %v, want %v", name, a.Mutating, mutating)
		}
	}
	// A request with a bare password-less SFTP login or an s3 type is refused before any job starts.
	a, _ := r.Lookup("backup.fetch_remote")
	for _, p := range []string{
		`{"remote":{"type":"s3"},"path":"/a"}`,
		`{"remote":{"type":"sftp","host":"h.example.com","username":"bk","password":"p"},"path":"/a"}`,
		`{"remote":{"type":"ftp","host":"h.example.com","username":"bk","password":"p","tls":"none"},"path":"/a"}`,
	} {
		if _, err := a.Handler(context.Background(), json.RawMessage(p)); err == nil {
			t.Errorf("accepted %s", p)
		}
	}
}

func TestPreferredHostKeyIsStable(t *testing.T) {
	rsa := "ssh-rsa " + strings.Repeat("A", 60)
	ecdsa := "ecdsa-sha2-nistp256 " + strings.Repeat("B", 60)
	ed := "ssh-ed25519 " + strings.Repeat("C", 60)
	for _, order := range [][]string{{rsa, ecdsa, ed}, {ed, rsa, ecdsa}, {ecdsa, ed, rsa}} {
		if got := preferredHostKey(order); got != ed {
			t.Errorf("%v → %s", order, got)
		}
	}
	if got := preferredHostKey([]string{rsa, ecdsa}); got != ecdsa {
		t.Errorf("without ed25519 = %s", got)
	}
	if got := preferredHostKey([]string{rsa}); got != rsa {
		t.Errorf("only rsa = %s", got)
	}
}
