package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
)

// fakeDrive implements the parts of the Drive v3 API the client uses.
type fakeDrive struct {
	mu       sync.Mutex
	files    map[string]*fakeFile
	sessions map[string]*fakeFile
	next     int
	token    string
	revoked  bool
	chunks   int
}

type fakeFile struct {
	ID, Name, Parent, Key string
	Data                  []byte
	Size                  int64
}

func newFakeDrive(t *testing.T) (*fakeDrive, *httptest.Server) {
	f := &fakeDrive{files: map[string]*fakeFile{}, sessions: map[string]*fakeFile{}, token: "tok-1"}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	oldTok, oldAPI := driveTokenURL, driveAPI
	driveTokenURL, driveAPI = srv.URL+"/token", srv.URL
	t.Cleanup(func() { driveTokenURL, driveAPI = oldTok, oldAPI })
	return f, srv
}

func (f *fakeDrive) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/token" {
		r.ParseForm()
		if f.revoked || r.Form.Get("refresh_token") != "refresh-1" || r.Form.Get("client_secret") != "secret" {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": f.token, "expires_in": 3600})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.token && !strings.HasPrefix(r.URL.Path, "/session/") {
		w.WriteHeader(401)
		return
	}
	switch {
	case r.Method == "POST" && r.URL.Path == "/drive/v3/files":
		var m map[string]any
		json.NewDecoder(r.Body).Decode(&m)
		f.next++
		id := fmt.Sprintf("folder%d", f.next)
		f.files[id] = &fakeFile{ID: id, Name: m["name"].(string)}
		json.NewEncoder(w).Encode(map[string]string{"id": id})
	case r.Method == "POST" && r.URL.Path == "/upload/drive/v3/files":
		var m struct {
			Name    string            `json:"name"`
			Parents []string          `json:"parents"`
			Props   map[string]string `json:"appProperties"`
		}
		json.NewDecoder(r.Body).Decode(&m)
		size, _ := strconv.ParseInt(r.Header.Get("X-Upload-Content-Length"), 10, 64)
		f.next++
		sid := fmt.Sprintf("s%d", f.next)
		f.sessions[sid] = &fakeFile{ID: fmt.Sprintf("file%d", f.next), Name: m.Name, Parent: m.Parents[0], Key: m.Props["whacp_key"], Size: size}
		w.Header().Set("Location", "http://"+r.Host+"/session/"+sid)
	case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/session/"):
		s := f.sessions[strings.TrimPrefix(r.URL.Path, "/session/")]
		b, _ := io.ReadAll(r.Body)
		f.chunks++
		s.Data = append(s.Data, b...)
		if int64(len(s.Data)) < s.Size {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(s.Data)-1))
			w.WriteHeader(308)
			return
		}
		f.files[s.ID] = s
		w.WriteHeader(200)
	case r.Method == "GET" && r.URL.Path == "/drive/v3/files":
		q := r.URL.Query().Get("q")
		var out []map[string]any
		for _, x := range f.files {
			if x.Key == "" || !strings.Contains(q, "'"+x.Parent+"' in parents") {
				continue
			}
			if strings.Contains(q, "value='") && !strings.Contains(q, "value='"+x.Key+"'") {
				continue
			}
			out = append(out, map[string]any{"id": x.ID, "name": x.Name, "appProperties": map[string]string{"whacp_key": x.Key}})
		}
		json.NewEncoder(w).Encode(map[string]any{"files": out})
	case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/drive/v3/files/"):
		x := f.files[strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")]
		if x == nil {
			w.WriteHeader(404)
			return
		}
		w.Write(x.Data)
	case r.Method == "DELETE":
		delete(f.files, strings.TrimPrefix(r.URL.Path, "/drive/v3/files/"))
		w.WriteHeader(204)
	default:
		w.WriteHeader(400)
	}
}

func TestDriveRoundTrip(t *testing.T) {
	f, _ := newFakeDrive(t)
	r := Remote{Type: "gdrive", ClientID: "cid", ClientSecret: "secret", RefreshToken: "refresh-1"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	d := NewDrive(r)
	id, err := d.CreateFolder(context.Background(), "WhaCP backups")
	if err != nil {
		t.Fatal(err)
	}
	r.FolderID = id
	d = NewDrive(r)
	d.ChunkSize = 256 << 10
	data := bytes.Repeat([]byte("0123456789abcdef"), 40000) // 640 KB → 3 chunks
	src := filepath.Join(t.TempDir(), "part")
	os.WriteFile(src, data, 0o600)
	ctx := context.Background()
	if err := d.Put(ctx, "srv/sites/a.test/1/files.tar.gz", src); err != nil {
		t.Fatal(err)
	}
	if f.chunks != 3 {
		t.Errorf("chunks = %d", f.chunks)
	}
	os.WriteFile(src, []byte("second version"), 0o600)
	if err := d.Put(ctx, "srv/sites/a.test/1/files.tar.gz", src); err != nil {
		t.Fatal(err)
	}
	d.Put(ctx, "srv/sites/a.test/1/manifest.json", src)
	d.Put(ctx, "srv/sites/b.test/2/manifest.json", src)
	keys, err := d.List(ctx, "srv/sites/a.test/1/")
	if err != nil || strings.Join(keys, ",") != "srv/sites/a.test/1/files.tar.gz,srv/sites/a.test/1/manifest.json" {
		t.Fatalf("list = %v %v", keys, err)
	}
	var out bytes.Buffer
	if err := d.Get(ctx, "srv/sites/a.test/1/files.tar.gz", &out); err != nil || out.String() != "second version" {
		t.Fatalf("get = %q %v (the old copy must be replaced)", out.String(), err)
	}
	for _, k := range keys {
		if err := d.Delete(ctx, k); err != nil {
			t.Fatal(err)
		}
	}
	if keys, _ := d.List(ctx, "srv/sites/a.test/1/"); len(keys) != 0 {
		t.Errorf("left after delete: %v", keys)
	}
	f.revoked = true
	if _, err := NewDrive(r).List(ctx, ""); err == nil || !strings.Contains(err.Error(), "connect Google Drive again") {
		t.Errorf("revoked token: %v", err)
	}
}

func TestRemoteValidationByType(t *testing.T) {
	good := []Remote{
		{Type: "sftp", Host: "backup.example.com", Username: "bk", Password: "p"},
		{Type: "sftp", Host: "192.0.2.10", Port: 2222, Username: "bk", PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nx\n-----END OPENSSH PRIVATE KEY-----"},
		{Type: "ftp", Host: "ftp.example.com", Username: "bk", Password: "p"},
		{Type: "ftp", Host: "ftp.example.com", Username: "bk", Password: "p", TLS: "implicit", TLSPin: "sha256//" + strings.Repeat("A", 43) + "="},
		{Type: "gdrive", ClientID: "c", ClientSecret: "s", RefreshToken: "r"},
		{Type: "local", Path: "/mnt/nas/whacp"},
	}
	for _, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []Remote{
		{Type: "sftp", Host: "bad host", Username: "bk", Password: "p"},
		{Type: "sftp", Host: "h", Username: "bk:x", Password: "p"},
		{Type: "sftp", Host: "h", Username: "bk"},
		{Type: "sftp", Host: "h", Username: "bk", Password: "p", HostKey: "SHA256:abc"},
		{Type: "ftp", Host: "h", Username: "bk", Password: "p", TLS: "maybe"},
		{Type: "ftp", Host: "h", Username: "bk"},
		{Type: "gdrive", ClientID: "c"},
		{Type: "local", Path: "/etc/whacp"},
		{Type: "local", Path: "/mnt"},
		{Type: "local", Path: "/mnt/../etc"},
		{Type: "sftp", Host: "h", Username: "bk", Password: "p", Prefix: "../x"},
		{Type: "dropbox"},
	}
	for _, r := range bad {
		if err := r.Validate(); err == nil {
			t.Errorf("accepted %+v", r)
		}
	}
}

func TestLocalDirectoryStorage(t *testing.T) {
	h, s := siteHost(t)
	os.MkdirAll(filepath.Join(h.Base, "mnt/nas"), 0o700)
	r := Remote{Type: "local", Path: "/mnt/nas", Prefix: "srv1"}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	st := r.Open(s.Env)
	src := filepath.Join(t.TempDir(), "x")
	os.WriteFile(src, []byte("backup data"), 0o600)
	ctx := context.Background()
	if err := st.Put(ctx, "srv1/sites/a/1/files.tar.gz", src); err != nil {
		t.Fatal(err)
	}
	keys, _ := st.List(ctx, "srv1/sites/a/1/")
	if strings.Join(keys, ",") != "srv1/sites/a/1/files.tar.gz" {
		t.Fatalf("keys = %v", keys)
	}
	var out bytes.Buffer
	st.Get(ctx, keys[0], &out)
	if out.String() != "backup data" {
		t.Errorf("get = %q", out.String())
	}
	st.Delete(ctx, keys[0])
	st.RemoveDir(ctx, "srv1/sites/a/1/")
	if _, err := os.Stat(filepath.Join(h.Base, "mnt/nas/srv1/sites/a/1")); err == nil {
		t.Error("folder left")
	}
	// A destination that is a symlink or missing is refused.
	os.Symlink("/tmp", filepath.Join(h.Base, "mnt/link"))
	if err := (&Remote{Type: "local", Path: "/mnt/link"}).Open(s.Env).Put(ctx, "k", src); err == nil {
		t.Error("symlinked destination accepted")
	}
	if err := (&Remote{Type: "local", Path: "/mnt/missing"}).Open(s.Env).Put(ctx, "k", src); err == nil || !strings.Contains(err.Error(), "mounted") {
		t.Errorf("missing destination: %v", err)
	}
	os.Chmod(filepath.Join(h.Base, "mnt/nas"), 0o777)
	if err := st.Put(ctx, "k", src); err == nil {
		t.Error("world-writable destination accepted")
	}
}

func TestFileRemoteRedactsPasswordAndPassesSecretsOnStdin(t *testing.T) {
	h, s := siteHost(t)
	h.Fail["curl"] = "curl: (67) Access denied for S3cret-pass"
	r := Remote{Type: "ftp", Host: "ftp.example.com", Username: "bk", Password: "S3cret-pass"}
	r.Validate()
	src := filepath.Join(t.TempDir(), "x")
	os.WriteFile(src, []byte("x"), 0o600)
	err := r.Open(s.Env).Put(context.Background(), "a/b", src)
	if err == nil || strings.Contains(err.Error(), "S3cret-pass") || !strings.Contains(err.Error(), "Access denied for ***") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range h.Runner.Calls {
		if strings.Contains(c, "S3cret") {
			t.Fatalf("password in curl arguments: %v", c)
		}
		if strings.HasPrefix(c, "curl ") && !strings.Contains(c, "--ssl-reqd") {
			t.Errorf("FTP without --ssl-reqd: %v", c)
		}
	}
	// An empty password (key-based SFTP) must not mangle messages.
	h.Fail["curl"] = "curl: (78) No such file"
	k := Remote{Type: "sftp", Host: "h.example.com", Username: "bk", PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----\nx\n-----END OPENSSH PRIVATE KEY-----", HostKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"}
	if err := k.Open(s.Env).Put(context.Background(), "a/b", src); err == nil || err.Error() != "curl: (78) No such file" {
		t.Errorf("message mangled: %v", err)
	}
}

func TestLocalBackupsFolderIsCreatedButMountPointsAreNot(t *testing.T) {
	h, s := siteHost(t)
	for _, p := range []string{"/backups", "/backup", "/srv/backups", "/backups/web1"} {
		if err := (&Remote{Type: "local", Path: p}).Validate(); err != nil {
			t.Errorf("%s refused: %v", p, err)
		}
	}
	for _, p := range []string{"/mnt", "/media", "/srv", "/backupsx", "/etc/backups"} {
		if err := (&Remote{Type: "local", Path: p}).Validate(); err == nil {
			t.Errorf("%s accepted", p)
		}
	}
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "x")
	os.WriteFile(src, []byte("backup data"), 0o600)

	// /backups/web1 does not exist: the storage test creates it (0700).
	reg := actions.NewRegistry(true)
	Register(reg, s.Env)
	a, _ := reg.Lookup("backup.remote_test")
	b, _ := json.Marshal(map[string]any{"remote": Remote{Type: "local", Path: "/backups/web1"}})
	res, err := a.Handler(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]any)["created"] != true {
		t.Errorf("result = %v", res)
	}
	for _, d := range []string{"backups", "backups/web1"} {
		fi, err := os.Stat(filepath.Join(h.Base, d))
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi, err)
		}
	}
	if err := (&Remote{Type: "local", Path: "/backups/web1"}).Open(s.Env).Put(ctx, "k", src); err != nil {
		t.Errorf("put into the created folder: %v", err)
	}
	// Existing: nothing created.
	if created, err := (&localDir{R: Remote{Type: "local", Path: "/backups/web1"}, Env: s.Env}).Ensure(); created || err != nil {
		t.Errorf("existing folder: %v %v", created, err)
	}
	// A missing folder under /mnt is never created (the disk may be unmounted).
	b, _ = json.Marshal(map[string]any{"remote": Remote{Type: "local", Path: "/mnt/nas2"}})
	if _, err := a.Handler(ctx, b); err == nil {
		t.Error("missing /mnt folder accepted")
	}
	if _, err := os.Stat(filepath.Join(h.Base, "mnt/nas2")); err == nil {
		t.Error("/mnt/nas2 was created")
	}
	// A symbolic link on the way is refused, not followed.
	os.MkdirAll(filepath.Join(h.Base, "srv"), 0o755)
	os.Symlink(t.TempDir(), filepath.Join(h.Base, "srv/backups"))
	if _, err := (&localDir{R: Remote{Type: "local", Path: "/srv/backups/x"}, Env: s.Env}).Ensure(); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("symlink: %v", err)
	}
}
