package http

// This suite drives the website's Memcached page (SitesController::memcached):
// the connection details + the service state, the not-installed / stopped
// states, the agent error, the role split and the 404s.

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// cacheEnv seeds an administrator (everything), a customer (bob,
// websites.view + logs.view) who owns website 10 (bob.example) and a
// viewer (carol, logs.view only).
func cacheEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own',''),(3,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'logs.view','lv','logs'),(4,'redis.manage','rm','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(1,4),(2,1),(2,3),(3,3)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','',''),
		(3,'carol','c@x','Carol',?,3,1,'2026-01-01T00:00:00Z','','')`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','','')`)

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

func TestSiteMemcachedPage(t *testing.T) {
	h, sa, _ := cacheEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/memcached", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/memcached = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>Memcached · bob.example · WhaCP</title>",
		"Memcached is a fast in-memory cache",
		"Memcached is <strong>running</strong>",
		"text-bg-success",
		"Connect your application",
		"<code>127.0.0.1</code>",
		"<code>11211</code>",
		"None (local connections only)",
		"$m->addServer('127.0.0.1', 11211);",
		"Memcached has no per-site separation",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, banned := range []string{
		"Memcached is not available", "not running", "Memcached state unknown",
	} {
		if strings.Contains(page, banned) {
			t.Errorf("page unexpectedly shows %q", banned)
		}
	}
	if got := agentParam(t, sa, "memcached.status", "nonexistent"); got != nil {
		t.Errorf("memcached.status takes no params, got %v", got)
	}
}

func TestSiteMemcachedCustomPort(t *testing.T) {
	h, sa, _ := cacheEnv(t)
	sa.mu.Lock()
	sa.mcPort = 11212
	sa.mu.Unlock()
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/memcached", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/memcached = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "<code>11212</code>") {
		t.Errorf("page lacks the custom port 11212")
	}
}

func TestSiteMemcachedNotInstalled(t *testing.T) {
	h, sa, _ := cacheEnv(t)
	sa.mu.Lock()
	sa.mcInstalled = false
	sa.mu.Unlock()
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/memcached", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/memcached = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"Memcached is not available on this server",
		"Ask your administrator to enable Memcached",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "Connect your application") {
		t.Errorf("connection card shown although not installed")
	}
}

func TestSiteMemcachedStopped(t *testing.T) {
	h, sa, _ := cacheEnv(t)
	sa.mu.Lock()
	sa.mcActive = false
	sa.mu.Unlock()
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/memcached", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/memcached = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	if !strings.Contains(page, "Memcached is <strong>not running</strong>") || !strings.Contains(page, "text-bg-danger") {
		t.Errorf("page lacks the stopped state: %s", rr.Body.String())
	}
}

func TestSiteMemcachedAgentDown(t *testing.T) {
	h, sa, _ := cacheEnv(t)
	sa.mu.Lock()
	sa.failActions = map[string]string{"memcached.status": "the agent refused"}
	sa.mu.Unlock()
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/memcached", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/memcached = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"Memcached state unknown.",
		"the agent refused",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(page, "Connect your application") {
		t.Errorf("connection card shown although the state is unknown")
	}
}

func TestSiteMemcachedForbidden(t *testing.T) {
	h, _, _ := cacheEnv(t)
	carol := loginAsUser(t, h, "carol")
	rr := get(t, h, "/sites/10/memcached", carol)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("/sites/10/memcached (carol) = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestSiteMemcachedNotFound(t *testing.T) {
	h, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/99/memcached", admin)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/sites/99/memcached = %d: %s", rr.Code, rr.Body.String())
	}
	rr = get(t, h, "/sites/x/memcached", admin)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/sites/x/memcached = %d: %s", rr.Code, rr.Body.String())
	}
}

// postRedis mirrors the browser's JSON call (postJSON sends the token the
// same way whacp.js does).
func postRedis(t *testing.T, h http.Handler, path string, body map[string]any, cookie *http.Cookie, token string) *httptest.ResponseRecorder {
	t.Helper()
	return postJSON(t, h, path, body, cookie, token)
}
