package http

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// workersEnv builds a server with an administrator, a reseller (dave, no
// customers) and two customers (bob, alice), each owning one website and —
// when withWorkers — one worker. The fake agent answers workers.status with
// a single active unit (whacp-worker-bob-app.service), so only bob's worker
// has a live state. Returns the handler, the admin cookie, the agent and DB.
func workersEnv(t *testing.T, withWorkers bool) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
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
		(1,'administrator','Admin','all',''),
		(2,'reseller','Reseller','own',''),
		(3,'customer','Customer','own',''),
		(5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'workers.manage','wm','auto')`)
	// Customers manage the workers of their own websites (as in the panel);
	// the viewer sees websites but holds no workers permission.
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(2,1),(2,2),(2,3),(3,1),(3,2),(3,3),(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','adm@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,3,1,'2026-01-01T00:00:00Z','','',1),
		(3,'alice','a@x','Alice',?,3,1,'2026-01-01T00:00:00Z','','',1),
		(4,'dave','d@x','Dave',?,2,1,'2026-01-01T00:00:00Z','','',NULL),
		(5,'eve','e@x','Eve',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(20,1,3,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active','',''),
		(30,1,4,'dave.example','u_dave','/home/whacp/dave.example','/home/whacp/dave.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES
		(1,10,'bob.example',1),(1,20,'alice.example',1),(1,30,'dave.example',1)`)
	if withWorkers {
		exec(`INSERT INTO workers (id, server_id, website_id, slug, name, command, restart, enabled, created_at, updated_at)
			VALUES (1, 1, 10, 'bob-app', 'Bob queue', 'php artisan queue:work', 'always', 1, '', ''),
			(2, 1, 20, 'alice-app', 'Alice queue', 'php artisan queue:work', 'always', 1, '', ''),
			(3, 1, 30, 'dave-app', 'Dave queue', 'node app.js', 'always', 1, '', '')`)
	}

	sa, socket, tokenFile := startSitesAgent(t, false)
	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	return h, loginAs(t, h, "admin"), sa, db
}

// TestWorkersIndexRequiresPermission: a role without workers.manage (viewer)
// is refused the global list.
func TestWorkersIndexRequiresPermission(t *testing.T) {
	h, _, _, _ := workersEnv(t, true)
	eve := loginAs(t, h, "eve")
	if rr := get(t, h, "/workers", eve); rr.Code != http.StatusForbidden {
		t.Fatalf("GET /workers (viewer) = %d, want 403", rr.Code)
	}
}

// TestWorkersIndexTenantScope: each tenant sees only the workers of the
// websites they can see — never other customers' or the reseller's.
func TestWorkersIndexTenantScope(t *testing.T) {
	h, _, _, _ := workersEnv(t, true)
	bob := loginAs(t, h, "bob")
	rr := get(t, h, "/workers", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /workers (customer) = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Bob queue", `href="/sites/10/workers"`, "bob.example"} {
		if !strings.Contains(body, want) {
			t.Fatalf("bob's page missing %q", want)
		}
	}
	for _, not := range []string{"Alice queue", "Dave queue", "alice.example", "dave.example"} {
		if strings.Contains(body, not) {
			t.Fatalf("bob's page leaked %q", not)
		}
	}

	dave := loginAs(t, h, "dave")
	rr = get(t, h, "/workers", dave)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /workers (reseller) = %d", rr.Code)
	}
	body = rr.Body.String()
	if !strings.Contains(body, "Dave queue") || !strings.Contains(body, `href="/sites/30/workers"`) {
		t.Fatalf("dave's page missing his worker: %s", body)
	}
	for _, not := range []string{"Bob queue", "Alice queue"} {
		if strings.Contains(body, not) {
			t.Fatalf("dave's page leaked %q", not)
		}
	}
}

// TestWorkersIndexPage: the administrator sees every worker with the website
// link, the unit, the command and the live state (unknown when the agent
// report has no row for the unit).
func TestWorkersIndexPage(t *testing.T) {
	h, admin, sa, _ := workersEnv(t, true)
	rr := get(t, h, "/workers", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /workers = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Bob queue", "Alice queue", "Dave queue",
		`whacp-worker-bob-app.service`, `whacp-worker-alice-app.service`, `whacp-worker-dave-app.service`,
		`href="/sites/10/workers"`, `href="/sites/20/workers"`, `href="/sites/30/workers"`,
		"php artisan queue:work", "node app.js",
		// Only bob's unit is in the agent report: active with the detail.
		"active", "2/2 up", "12.0 MB",
		"unknown",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	// The agent was asked once for the whole list, not once per website.
	n := 0
	for _, a := range sa.called() {
		if a == "workers.status" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("workers.status calls = %d, want 1", n)
	}
}

// TestWorkersIndexEmpty: no workers anywhere — the empty state.
func TestWorkersIndexEmpty(t *testing.T) {
	h, admin, _, _ := workersEnv(t, false)
	rr := get(t, h, "/workers", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /workers = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "No workers yet.") {
		t.Fatalf("empty state missing: %s", rr.Body.String())
	}
}
