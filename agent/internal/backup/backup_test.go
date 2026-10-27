package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestEncryptRoundTripAndTamper(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	for _, size := range []int{0, 1, chunkSize - 1, chunkSize, chunkSize + 1, 3*chunkSize + 17} {
		plain := make([]byte, size)
		rand.Read(plain)
		var buf bytes.Buffer
		w, _ := NewEncryptWriter(&buf, key)
		w.Write(plain)
		w.Close()
		enc := buf.Bytes()
		r, err := NewDecryptReader(bytes.NewReader(enc), key)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(r)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("size %d: roundtrip failed: %v", size, err)
		}
		bad := map[string][]byte{
			"truncated": enc[:len(enc)-20],
			"appended":  append(append([]byte{}, enc...), 1, 2, 3),
			"flipped":   func() []byte { b := append([]byte{}, enc...); b[len(b)-5] ^= 1; return b }(),
		}
		if size > chunkSize {
			// Dropping the final chunk entirely must not look like EOF.
			bad["chunk dropped"] = enc[:len(magic)+4+4+chunkSize+16]
		}
		for name, b := range bad {
			r, err := NewDecryptReader(bytes.NewReader(b), key)
			if err == nil {
				_, err = io.ReadAll(r)
			}
			if err == nil {
				t.Errorf("size %d: %s stream accepted", size, name)
			}
		}
		other := make([]byte, 32)
		r, _ = NewDecryptReader(bytes.NewReader(enc), other)
		if _, err := io.ReadAll(r); err == nil {
			t.Errorf("size %d: wrong key accepted", size)
		}
	}
}

// The example "GET Object" request from the AWS SigV4 documentation.
func TestSigV4MatchesAWSExample(t *testing.T) {
	s := &S3{R: Remote{Endpoint: "https://s3.amazonaws.com", Region: "us-east-1", Bucket: "examplebucket", AccessKey: "AKIAIOSFODNN7EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
		Now: func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) }}
	u := s.objectURL("test.txt", nil)
	req, _ := http.NewRequest("GET", u.String(), nil)
	req.URL = u
	req.Header.Set("Range", "bytes=0-9")
	s.sign(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Fatalf("Authorization =\n%s\nwant\n%s", got, want)
	}
}

func TestRemoteValidation(t *testing.T) {
	good := Remote{Endpoint: "https://s3.example.com", Bucket: "my-backups", AccessKey: "a", SecretKey: "b", Prefix: "/srv1/"}
	if err := good.Validate(); err != nil || good.Prefix != "srv1" || good.Region != "us-east-1" {
		t.Fatalf("good remote: %v %+v", err, good)
	}
	for _, r := range []Remote{
		{Endpoint: "http://s3.example.com", Bucket: "b-1", AccessKey: "a", SecretKey: "b"},
		{Endpoint: "https://s3.example.com/path", Bucket: "b-1", AccessKey: "a", SecretKey: "b"},
		{Endpoint: "https://user:pw@s3.example.com", Bucket: "b-1", AccessKey: "a", SecretKey: "b"},
		{Endpoint: "https://s3.example.com", Bucket: "B", AccessKey: "a", SecretKey: "b"},
		{Endpoint: "https://s3.example.com", Bucket: "b-1", AccessKey: "a", SecretKey: "b", Prefix: "../x"},
		{Endpoint: "https://s3.example.com", Bucket: "b-1"},
	} {
		if r.Validate() == nil {
			t.Errorf("accepted %+v", r)
		}
	}
	local := Remote{Endpoint: "http://127.0.0.1:9000", Bucket: "b-1", AccessKey: "a", SecretKey: "b"}
	if err := local.Validate(); err != nil {
		t.Errorf("local http MinIO rejected: %v", err)
	}
}

// fakeS3 is an in-memory, path-style S3 endpoint.
type fakeS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
	uploads map[string]map[int][]byte
}

func newFakeS3(t *testing.T) (*fakeS3, *httptest.Server) {
	f := &fakeS3{objects: map[string][]byte{}, uploads: map[string]map[int][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AK/") {
			http.Error(w, "<Error><Code>AccessDenied</Code></Error>", 403)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/bucket-1/")
		q := r.URL.Query()
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == "GET" && q.Get("list-type") == "2":
			var keys []string
			for k := range f.objects {
				if strings.HasPrefix(k, q.Get("prefix")) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			io.WriteString(w, "<ListBucketResult>")
			for _, k := range keys {
				io.WriteString(w, "<Contents><Key>"+k+"</Key></Contents>")
			}
			io.WriteString(w, "<IsTruncated>false</IsTruncated></ListBucketResult>")
		case r.Method == "POST" && q.Has("uploads"):
			f.uploads["u1"] = map[int][]byte{}
			io.WriteString(w, "<InitiateMultipartUploadResult><UploadId>u1</UploadId></InitiateMultipartUploadResult>")
		case r.Method == "PUT" && q.Get("uploadId") != "":
			var n int
			json.Unmarshal([]byte(q.Get("partNumber")), &n)
			f.uploads[q.Get("uploadId")][n] = body
			w.Header().Set("ETag", `"etag"`)
		case r.Method == "POST" && q.Get("uploadId") != "":
			parts := f.uploads[q.Get("uploadId")]
			var all []byte
			for i := 1; i <= len(parts); i++ {
				all = append(all, parts[i]...)
			}
			f.objects[key] = all
			io.WriteString(w, "<CompleteMultipartUploadResult/>")
		case r.Method == "PUT":
			f.objects[key] = body
		case r.Method == "GET":
			b, ok := f.objects[key]
			if !ok {
				http.Error(w, "<Error><Code>NoSuchKey</Code></Error>", 404)
				return
			}
			w.Write(b)
		case r.Method == "DELETE":
			delete(f.objects, key)
			w.WriteHeader(204)
		}
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func TestS3MultipartRoundTrip(t *testing.T) {
	f, srv := newFakeS3(t)
	r := Remote{Endpoint: srv.URL, Bucket: "bucket-1", AccessKey: "AK", SecretKey: "SK", PathStyle: true}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	c := NewS3(r)
	c.PartSize = 1000
	data := make([]byte, 3500)
	rand.Read(data)
	p := filepath.Join(t.TempDir(), "big")
	os.WriteFile(p, data, 0o600)
	if err := c.Put(context.Background(), "a/b c.bin", p); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.objects["a/b c.bin"], data) {
		t.Fatal("multipart upload corrupted data")
	}
	var buf bytes.Buffer
	if err := c.Get(context.Background(), "a/b c.bin", &buf); err != nil || !bytes.Equal(buf.Bytes(), data) {
		t.Fatalf("get: %v", err)
	}
	keys, _ := c.List(context.Background(), "a/")
	if len(keys) != 1 {
		t.Fatalf("keys = %v", keys)
	}
	bad := NewS3(Remote{Endpoint: srv.URL, Bucket: "bucket-1", AccessKey: "XX", SecretKey: "SK", PathStyle: true})
	if err := bad.Get(context.Background(), "a/b c.bin", io.Discard); err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("expected access denied, got %v", err)
	}
}

func siteHost(t *testing.T) (*testhost.Host, Service) {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "home/nimvor/example.com/public_html"), 0o755)
	return h, Service{Env: h.Env}
}

func TestSiteBackupEncryptedRemoteRestore(t *testing.T) {
	h, s := siteHost(t)
	f, srv := newFakeS3(t)
	remote := &Remote{Endpoint: srv.URL, Bucket: "bucket-1", AccessKey: "AK", SecretKey: "SK", PathStyle: true, Prefix: "srv1"}
	remote.Validate()
	cred, _, err := h.Env.Credential("example")
	if err != nil {
		t.Fatal(err)
	}
	keep := false
	var log bytes.Buffer
	m, err := s.CreateSite(context.Background(), SiteRequest{Domain: "example.com", User: "example", Root: "/home/nimvor/example.com", Files: true, Databases: []string{"shop"}, Encrypt: true, Remote: remote, KeepLocal: &keep, Meta: json.RawMessage(`{"php":"8.3"}`)}, cred, &log)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, log.String())
	}
	if len(m.Files) != 2 || !m.Encrypted || m.KeyID == "" || m.Local || m.Remote == nil {
		t.Fatalf("manifest = %+v", m)
	}
	tarCall := ""
	for _, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "tar ") {
			tarCall = c
		}
	}
	if !strings.Contains(tarCall, "--exclude=./logs") || !strings.Contains(tarCall, "--exclude=./backups") {
		t.Errorf("tar call = %q", tarCall)
	}
	for i, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "tar ") && (h.Runner.Opts[i].As == nil || h.Runner.Opts[i].As.UID == 0) {
			t.Error("tar must run as the website account")
		}
	}
	if _, ok := f.objects["srv1/sites/example.com/"+m.ID+"/files.tar.gz.enc"]; !ok {
		t.Fatalf("not uploaded: %v", f.objects)
	}
	if bytes.Contains(f.objects["srv1/sites/example.com/"+m.ID+"/db-shop.sql.gz.enc"], []byte("dump of")) {
		t.Fatal("remote copy is not encrypted")
	}
	dir := filepath.Join(h.Base, "var/backups/nimvor/sites/example.com", m.ID)
	if _, err := os.Stat(filepath.Join(dir, "files.tar.gz.enc")); err == nil {
		t.Error("local copy should have been removed")
	}
	list, _ := s.List("example.com")
	if len(list) != 1 || list[0].ID != m.ID {
		t.Fatalf("list = %+v", list)
	}

	// Restore both parts; they are downloaded, verified and decrypted.
	h.Runner.Stdins = nil
	res, err := s.Restore(context.Background(), RestoreRequest{Domain: "example.com", User: "example", Root: "/home/nimvor/example.com", ID: m.ID, Files: true, Databases: map[string]string{"shop": "shop_copy"}, Remote: remote}, cred, &log)
	if err != nil {
		t.Fatalf("restore: %v\n%s", err, log.String())
	}
	joined := strings.Join(h.Runner.Stdins, "|")
	if !strings.Contains(joined, "TAR-DATA:") || !strings.Contains(joined, "-- dump of shop") {
		t.Fatalf("restored streams = %q", joined)
	}
	if !h.Runner.Called("tar -C " + filepath.Join(h.Base, "home/nimvor/example.com") + " --no-same-owner -xf -") {
		t.Errorf("calls: %v", h.Runner.Calls)
	}
	if res["databases"].([]string)[0] != "shop_copy" {
		t.Errorf("res = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "files.tar.gz.enc")); err == nil {
		t.Error("downloaded copies should be cleaned up")
	}

	// A damaged remote copy is detected before anything is restored.
	f.objects["srv1/sites/example.com/"+m.ID+"/db-shop.sql.gz.enc"][20] ^= 1
	calls := len(h.Runner.Calls)
	if _, err := s.Restore(context.Background(), RestoreRequest{Domain: "example.com", User: "example", Root: "/home/nimvor/example.com", ID: m.ID, Files: true, Databases: map[string]string{"shop": "shop"}, Remote: remote}, cred, &log); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("expected checksum failure, got %v", err)
	}
	if len(h.Runner.Calls) != calls {
		t.Errorf("commands ran despite a damaged backup: %v", h.Runner.Calls[calls:])
	}

	if err := s.Delete(context.Background(), "example.com", m.ID, remote); err != nil {
		t.Fatal(err)
	}
	if len(f.objects) != 0 {
		t.Errorf("remote objects left: %v", f.objects)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("local backup directory left")
	}
}

func TestServerBackupSkipsSpecialFilesAndLinks(t *testing.T) {
	h, s := siteHost(t)
	os.Symlink("/etc/shadow", filepath.Join(h.Base, "etc/nginx/evil-link"))
	var log bytes.Buffer
	m, err := s.CreateServer(context.Background(), true, "nightly", nil, true, &log)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.Base, "var/backups/nimvor/server", m.ID)
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o700 {
		t.Errorf("backup dir mode = %v", info.Mode())
	}
	rd, done, err := s.open(m, filepath.Join(dir, m.Files[0].Name))
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	tr := tar.NewReader(rd)
	names := map[string]*tar.Header{}
	for {
		hdr, err := tr.Next()
		if err != nil {
			break
		}
		names[hdr.Name] = hdr
	}
	if names["etc/nginx/nginx.conf"] == nil {
		t.Fatalf("entries: %v", names)
	}
	if l := names["etc/nginx/evil-link"]; l == nil || l.Typeflag != tar.TypeSymlink || l.Size != 0 {
		t.Errorf("symlink should be stored as a link, got %+v", l)
	}
}

func TestActionsValidate(t *testing.T) {
	h, _ := siteHost(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	call := func(name, params string) error {
		a, _ := r.Lookup(name)
		_, err := a.Handler(context.Background(), json.RawMessage(params))
		return err
	}
	for _, c := range []struct{ name, params string }{
		{"backup.create", `{"domain":"example.com","user":"example","files":false}`},
		{"backup.create", `{"domain":"example.com","user":"example","root":"/etc","files":true}`},
		{"backup.create", `{"domain":"example.com","user":"root","files":true}`},
		{"backup.create", `{"domain":"example.com","user":"example","databases":["x;drop"]}`},
		{"backup.download", `{"domain":"example.com","id":"../../etc","file":"manifest.json"}`},
		{"backup.delete", `{"domain":"../x","id":"20260901T120000Z-abcdef"}`},
		{"backup.server", `{"encrypt":false,"remote":{"endpoint":"https://s3.example.com","bucket":"b-1","access_key":"a","secret_key":"b"}}`},
		// Kept only on this server: still refused (downloads, host backups).
		{"backup.server", `{"encrypt":false}`},
	} {
		if call(c.name, c.params) == nil {
			t.Errorf("%s accepted %s", c.name, c.params)
		}
	}
	_ = url.Values{}
}

func TestChunkOnlyServesManifestFiles(t *testing.T) {
	h, s := siteHost(t)
	cred, _, _ := h.Env.Credential("example")
	m, err := s.CreateSite(context.Background(), SiteRequest{Domain: "example.com", User: "example", Files: true}, cred, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	b, size, err := s.Chunk("example.com", m.ID, "files.tar.gz", 0)
	if err != nil || int64(len(b)) != size {
		t.Fatalf("chunk: %v", err)
	}
	gz, _ := gzip.NewReader(bytes.NewReader(b))
	plain, _ := io.ReadAll(gz)
	if !strings.HasPrefix(string(plain), "TAR-DATA:") {
		t.Errorf("content = %q", plain)
	}
	os.WriteFile(filepath.Join(h.Base, "var/backups/nimvor/sites/example.com", m.ID, "db-other.sql.gz"), []byte("x"), 0o600)
	if _, _, err := s.Chunk("example.com", m.ID, "db-other.sql.gz", 0); err == nil {
		t.Error("served a file that is not in the manifest")
	}
}
