package http

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// rolesEnv seeds the real permission catalogue and default roles, plus an
// "mgr" user whose custom role may manage roles/users but nothing else.
func rolesEnv(t *testing.T) (http.Handler, *sql.DB) {
	s, db := rolesEnvServer(t)
	return s.Handler(), db
}

func rolesEnvServer(t *testing.T) (*Server, *sql.DB) {
	s, db, _ := rolesEnvServerAgent(t, false)
	return s, db
}

// rolesEnvServerAgent is rolesEnvServer, optionally with the fake agent running.
func rolesEnvServerAgent(t *testing.T, withAgent bool) (*Server, *sql.DB, *sitesAgent) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := authz.SyncDefaults(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,description,is_system,scope,created_at) VALUES (100,'rolemgr','Role manager','',0,'all','')`)
	exec(`INSERT INTO role_permissions (role_id, permission_id) SELECT 100, id FROM permissions WHERE name IN ('roles.manage','users.view','dashboard.view')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (1,'admin','a@x','Admin',?,(SELECT id FROM roles WHERE name='admin'),1,'2026-01-01T00:00:00Z','',''),
		       (2,'mgr','m@x','Mgr',?,100,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash)
	cfg := testConfig(t)
	var sa *sitesAgent
	if withAgent {
		var socket, tokenFile string
		sa, socket, tokenFile = startSitesAgent(t, false)
		cfg.AgentSocket, cfg.AgentTokenFile = socket, tokenFile
	}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s, db, sa
}

func roleForm(token, name string, perms ...string) url.Values {
	return url.Values{"_token": {token}, "name": {name}, "label": {"Label " + name}, "description": {"d"}, "permissions": perms}
}

func rolePermCount(t *testing.T, db *sql.DB, name string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRoleManagerCannotGrantPermissionsTheyLack(t *testing.T) {
	h, db := rolesEnv(t)
	mgr := loginAsUser(t, h, "mgr")
	token, mgr := siteFormToken(t, h, mgr, "/roles/new")
	rr := postForm(h, "/roles", roleForm(token, "escalated", "server.manage", "roles.manage"), mgr)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /roles = %d", rr.Code)
	}
	if n := rolePermCount(t, db, "escalated"); n != 0 {
		t.Fatalf("role created with %d permissions the actor lacks", n)
	}
	var cnt int
	_ = db.QueryRow(`SELECT COUNT(*) FROM roles WHERE name='escalated'`).Scan(&cnt)
	if cnt != 0 {
		t.Fatal("role was created")
	}
	// Self-grant through an update of its own role is refused too.
	token, mgr = siteFormToken(t, h, refreshCookie(mgr, rr), "/roles/100/edit")
	rr = postForm(h, "/roles/100", roleForm(token, "rolemgr", "roles.manage", "users.view", "dashboard.view", "server.manage"), mgr)
	if n := rolePermCount(t, db, "rolemgr"); n != 3 {
		t.Fatalf("own role now has %d permissions, want 3", n)
	}
}

func TestRoleManagerMayGrantWhatTheyHold(t *testing.T) {
	h, db := rolesEnv(t)
	mgr := loginAsUser(t, h, "mgr")
	token, mgr := siteFormToken(t, h, mgr, "/roles/new")
	postForm(h, "/roles", roleForm(token, "helper", "users.view", "dashboard.view"), mgr)
	if n := rolePermCount(t, db, "helper"); n != 2 {
		t.Fatalf("helper has %d permissions, want 2", n)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'role.create'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("role.create audit rows = %d, %v", n, err)
	}
}

func TestAdministratorRoleCannotBeChanged(t *testing.T) {
	h, db := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	before := rolePermCount(t, db, "admin")
	var id int64
	_ = db.QueryRow(`SELECT id FROM roles WHERE name='admin'`).Scan(&id)
	token, admin := siteFormToken(t, h, admin, "/roles")
	postForm(h, "/roles/"+itoa(id), roleForm(token, "admin", "dashboard.view"), admin)
	if after := rolePermCount(t, db, "admin"); after != before {
		t.Fatalf("Administrator permissions changed: %d -> %d", before, after)
	}
}

func TestRoleValidationAndUnknownPermissions(t *testing.T) {
	h, db := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/roles/new")
	for _, name := range []string{"Bad Name", "ab", "1abc"} {
		postForm(h, "/roles", roleForm(token, name, "users.view"), admin)
	}
	postForm(h, "/roles", roleForm(token, "okname", "no.such.permission"), admin)
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM roles WHERE is_system = 0`).Scan(&n)
	if n != 1 { // only the seeded rolemgr
		t.Fatalf("invalid roles were created: %d custom roles", n)
	}
}
