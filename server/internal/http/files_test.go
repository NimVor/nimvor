package http

// This suite drives the website file manager (FilesController): the
// manager and editor pages (starting path, tenant scoping, the retention
// note), the operation endpoint (op dispatch, the agent parameters, the
// audit for changing ops, job tracking for extract/compress, the agent
// error pass-through), chunked upload and the chunked download.

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// filesEnv seeds an administrator, two customers (bob owns site 10, alice
// owns site 20, both files.manage) and a viewer (carol, no files.manage),
// plus the trash_retention_days setting. The fake agent answers the
// files.* actions.
func filesEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'administrator','Admin','all',''),(2,'customer','Customer','own',''),(3,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'files.manage','fm','files')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(2,1),(2,2),(2,3),(3,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'alice','c@x','Alice',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(4,'carol','d@x','Carol',?,3,1,'2026-01-01T00:00:00Z','','',1)`,
		loginHash, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at) VALUES
		(10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(20,1,3,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES
		(1,10,'bob.example',1),(1,20,'alice.example',1)`)
	exec(`INSERT INTO settings (key,value,updated_at,updated_by) VALUES ('trash_retention_days','30','',NULL)`)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sa, socket, tokenFile := startSitesAgent(t, false)
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

// postMultipart is a multipart/form-data client (chunked uploads).
func postMultipart(t *testing.T, h http.Handler, path string, fields map[string]string, fileField, fileName, fileContent string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile(fileField, fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte(fileContent)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestFilesShowPage(t *testing.T) {
	h, sa, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/files", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/10/files = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`data-start="public_html"`,
		`data-api="/sites/10/files/api"`,
		`data-site="bob.example"`,
		`data-upload="/sites/10/files/upload"`,
		`data-download="/sites/10/files/download"`,
		`data-edit="/sites/10/files/edit"`,
		"after 30 days they are deleted automatically",
		"File manager",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("manager page missing %q", want)
		}
	}
	if len(sa.called()) != 0 {
		t.Errorf("the manager page must not call the agent: %v", sa.called())
	}
}

func TestFilesShowStartPath(t *testing.T) {
	h, _, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")

	if rr := get(t, h, "/sites/10/files?path=app", bob); rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `data-start="app"`) {
		t.Errorf("?path=app must open the manager in app/ (got %d)", rr.Code)
	}
	// A traversal (or unprintable) start point is ignored, not followed.
	for _, bad := range []string{"../etc", "a/../b"} {
		rr := get(t, h, "/sites/10/files?path="+bad, bob)
		if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `data-start="public_html"`) {
			t.Errorf("?path=%s must fall back to the document root (got %d)", bad, rr.Code)
		}
	}
}

func TestFilesShowForeignAndDenied(t *testing.T) {
	h, _, _ := filesEnv(t)
	alice := loginAsUser(t, h, "alice")
	if rr := get(t, h, "/sites/10/files", alice); rr.Code != http.StatusNotFound {
		t.Fatalf("foreign manager page = %d, want 404", rr.Code)
	}
	carol := loginAsUser(t, h, "carol")
	if rr := get(t, h, "/sites/10/files", carol); rr.Code != http.StatusForbidden {
		t.Fatalf("viewer manager page = %d, want 403", rr.Code)
	}
}

func TestFilesEditPage(t *testing.T) {
	h, _, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/files/edit?path=app/config.php", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET edit = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`data-path="app/config.php"`,
		`data-back="/sites/10/files?path=app"`,
		`data-download="/sites/10/files/download?path=app/config.php"`,
		"bob.example/app/config.php",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("editor page missing %q", want)
		}
	}
	// Refusals: empty path, traversal, foreign site.
	if rr := get(t, h, "/sites/10/files/edit?path=", bob); rr.Code != http.StatusNotFound {
		t.Errorf("empty path = %d, want 404", rr.Code)
	}
	if rr := get(t, h, "/sites/10/files/edit?path=a/../b", bob); rr.Code != http.StatusNotFound {
		t.Errorf("traversal path = %d, want 404", rr.Code)
	}
	alice := loginAsUser(t, h, "alice")
	if rr := get(t, h, "/sites/10/files/edit?path=x.php", alice); rr.Code != http.StatusNotFound {
		t.Errorf("foreign edit = %d, want 404", rr.Code)
	}
}

func TestFilesApiList(t *testing.T) {
	h, sa, db := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/files")

	rr := postJSON(t, h, "/sites/10/files/api", map[string]any{"op": "list", "path": ""}, bob, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api list = %d: %s", rr.Code, rr.Body.String())
	}
	if got := agentParam(t, sa, "files.list", "domain"); got != "bob.example" {
		t.Fatalf("files.list domain = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action LIKE 'files.%'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("read-only op audited: %d rows %v", n, err)
	}
}

func TestFilesApiUnknownOp(t *testing.T) {
	h, sa, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/files")

	rr := postJSON(t, h, "/sites/10/files/api", map[string]any{"op": "nope"}, bob, token)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown op = %d, want 400", rr.Code)
	}
	if len(sa.called()) != 0 {
		t.Fatalf("agent called for an unknown op: %v", sa.called())
	}
}

func TestFilesApiWriteAudited(t *testing.T) {
	h, sa, db := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/files")

	rr := postJSON(t, h, "/sites/10/files/api", map[string]any{
		"op": "write", "path": "hello.txt", "content": "hi", "expected_mtime": 1700000000,
	}, bob, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api write = %d: %s", rr.Code, rr.Body.String())
	}
	if agentParam(t, sa, "files.write", "content") != "hi" {
		t.Errorf("files.write content = %v", agentParam(t, sa, "files.write", "content"))
	}
	var targetType, targetID, result, details string
	err := db.QueryRow(`SELECT target_type, target_id, result, details FROM audit_logs WHERE action = 'files.write'`).
		Scan(&targetType, &targetID, &result, &details)
	if err != nil || targetType != "website" || targetID != "10" || result != "success" {
		t.Errorf("write audit = %q %q %q %v", targetType, targetID, result, err)
	}
	if !strings.Contains(details, `"path":"hello.txt"`) || !strings.Contains(details, `"domain":"bob.example"`) {
		t.Errorf("write audit details = %s", details)
	}
}

func TestFilesApiRenamePathDetail(t *testing.T) {
	h, _, db := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/files")

	rr := postJSON(t, h, "/sites/10/files/api", map[string]any{"op": "rename", "from": "a.txt", "to": "b.txt"}, bob, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api rename = %d: %s", rr.Code, rr.Body.String())
	}
	var details string
	err := db.QueryRow(`SELECT details FROM audit_logs WHERE action = 'files.rename'`).Scan(&details)
	if err != nil || !strings.Contains(details, `"path":"a.txt → b.txt"`) {
		t.Errorf("rename audit details = %q %v", details, err)
	}
}

func TestFilesApiExtractTracksJob(t *testing.T) {
	h, _, db := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/files")

	rr := postJSON(t, h, "/sites/10/files/api", map[string]any{"op": "extract", "path": "archive.zip", "dest": "out"}, bob, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("api extract = %d: %s", rr.Code, rr.Body.String())
	}
	var kind, targetType, targetID string
	err := db.QueryRow(`SELECT kind, target_type, target_id FROM agent_jobs WHERE job_id = '0123456789abcdef'`).
		Scan(&kind, &targetType, &targetID)
	if err != nil || kind != "Extract in bob.example" || targetType != "website" || targetID != "10" {
		t.Errorf("tracked job = %q %q %q %v", kind, targetType, targetID, err)
	}
}

func TestFilesApiAgentFailure(t *testing.T) {
	h, sa, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/files")
	sa.failActions = map[string]string{"files.write": "read-only file system"}

	rr := postJSON(t, h, "/sites/10/files/api", map[string]any{"op": "write", "path": "x", "content": "y"}, bob, token)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("api write (agent down) = %d, want 502", rr.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	e, _ := body["error"].(map[string]any)
	if body["success"] != false || e["message"] != "read-only file system" {
		t.Errorf("error body = %v", body)
	}
}

func TestFilesUpload(t *testing.T) {
	h, sa, db := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/files")

	rr := postMultipart(t, h, "/sites/10/files/upload", map[string]string{
		"_token": token, "path": "up/file.txt", "offset": "0", "final": "1",
	}, "chunk", "file.txt", "hello", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("upload = %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("upload reply: %v", err)
	}
	if d, _ := body["data"].(map[string]any); d["size"] != float64(5) {
		t.Errorf("upload data = %v", body["data"])
	}
	if agentParam(t, sa, "files.upload", "domain") != "bob.example" {
		t.Errorf("files.upload domain = %v", agentParam(t, sa, "files.upload", "domain"))
	}
	if agentParam(t, sa, "files.upload", "final") != true {
		t.Errorf("files.upload final = %v", agentParam(t, sa, "files.upload", "final"))
	}
	wantB64 := base64.StdEncoding.EncodeToString([]byte("hello"))
	if agentParam(t, sa, "files.upload", "data") != wantB64 {
		t.Errorf("files.upload data = %v", agentParam(t, sa, "files.upload", "data"))
	}
	var result, details string
	err := db.QueryRow(`SELECT result, details FROM audit_logs WHERE action = 'files.upload'`).Scan(&result, &details)
	if err != nil || result != "success" || !strings.Contains(details, `"size":5`) {
		t.Errorf("upload audit = %q %q %v", result, details, err)
	}
}

func TestFilesUploadRefused(t *testing.T) {
	h, sa, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/files")

	// No chunk file at all.
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("_token", token)
	_ = mw.WriteField("path", "up/file.txt")
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/sites/10/files/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(bob)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("upload without chunk = %d, want 400", rr.Code)
	}
	for _, a := range sa.called() {
		if a == "files.upload" {
			t.Fatalf("agent called for a chunkless upload: %v", sa.called())
		}
	}
}

func TestFilesDownload(t *testing.T) {
	h, _, db := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/files/download?path=hello.txt", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("download = %d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "hello world" {
		t.Errorf("download body = %q", rr.Body.String())
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="hello.txt"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	var result, details string
	err := db.QueryRow(`SELECT result, details FROM audit_logs WHERE action = 'files.download'`).Scan(&result, &details)
	if err != nil || result != "success" || !strings.Contains(details, `"path":"hello.txt"`) {
		t.Errorf("download audit = %q %q %v", result, details, err)
	}
	// No path: 404.
	if rr := get(t, h, "/sites/10/files/download", bob); rr.Code != http.StatusNotFound {
		t.Errorf("download without path = %d, want 404", rr.Code)
	}
}

func TestFilesApiRequiresCSRF(t *testing.T) {
	h, sa, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")

	rr := postJSON(t, h, "/sites/10/files/api", map[string]any{"op": "list"}, bob, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("api without token = %d, want 400", rr.Code)
	}
	if len(sa.called()) != 0 {
		t.Fatalf("agent called without a CSRF token: %v", sa.called())
	}
}
