package http

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// dbEnv seeds two tenants with a website and a database each, plus a plan for
// custA whose feature list disables mysql (so databases.view is withheld).
func dbEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sql.DB) {
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
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'administrator','Admin','all',''),(2,'customer','Customer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','View sites','Hosting'),(2,'databases.view','View dbs','Hosting')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(2,1),(2,2)`)
	// Plan 1 disables mysql; assigned to custA only.
	exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES (1,'nodb','','')`)
	exec(`INSERT INTO feature_list_disabled (list_id,feature) VALUES (1,'mysql')`)
	exec(`INSERT INTO plans (id,name,feature_list_id,created_at,updated_at) VALUES (1,'NoDB',1,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'custA','ca@x','Customer A',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'custB','cb@x','Customer B',?,2,1,'2026-01-01T00:00:00Z','','',NULL)`, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,status,system_user,root,document_root,kind,https,enabled,created_at,updated_at) VALUES
		(10,1,2,'a.example','active','ua','/srv/a','/srv/a/pub','php',1,1,'',''),
		(11,1,3,'b.example','active','ub','/srv/b','/srv/b/pub','php',1,1,'','')`)
	exec(`INSERT INTO databases (id,server_id,website_id,owner_id,name,created_at) VALUES
		(100,1,10,2,'a_main','2026-01-01'),
		(101,1,11,3,'b_main','2026-01-01')`)

	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {loginAs}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d", loginAs, rr.Code)
	}
	return h, rr.Result().Cookies()[0], db
}

func TestWebsiteShowScopedAndForeignIs404(t *testing.T) {
	h, cookie, db := dbEnv(t, "custB")
	// The hub (/sites/{id}) stays tenant-scoped: own site (11) is viewable
	// and shows its database.
	own := get(t, h, "/sites/11", cookie)
	if own.Code != http.StatusOK || !strings.Contains(own.Body.String(), "b.example") || !strings.Contains(own.Body.String(), "b_main") {
		t.Fatalf("custB own site = %d, body missing expected content:\n%s", own.Code, own.Body.String())
	}
	// Another tenant's site id must be 404 (not a leak, not a 403 oracle diff —
	// PHP returns not-found for out-of-scope ids).
	foreign := get(t, h, "/sites/10", cookie)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("custB viewing custA's site = %d, want 404", foreign.Code)
	}
	if strings.Contains(foreign.Body.String(), "a.example") || strings.Contains(foreign.Body.String(), "a_main") {
		t.Fatalf("TENANT LEAK on the website hub:\n%s", foreign.Body.String())
	}

	// /websites/{id} shows a discovered website (discovery id). A managed
	// website's numeric id is not a discovery id: 404, and the page never
	// reveals tenant data.
	seedDiscoverySnapshot(t, db, `{
	  "websites":{"websites":[{"id":"abc123","domain":"old.test","aliases":[],"kind":"php","enabled":true,
	    "document_root":"/var/www/old","document_root_exists":true,"config_files":["/etc/nginx/sites-available/old"]}],
	    "total":1,"catch_all_vhosts":0,"warnings":[]},
	  "cron":{"jobs":[]}
	}`)
	rr := get(t, h, "/websites/10", cookie)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("numeric id on the discovery page = %d, want 404", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "a.example") || strings.Contains(rr.Body.String(), "a_main") {
		t.Fatalf("TENANT LEAK on the discovery detail page:\n%s", rr.Body.String())
	}
	// The discovered website resolves for any viewer (websites.view, as in PHP).
	rr = get(t, h, "/websites/abc123", cookie)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "old.test") {
		t.Fatalf("discovered website detail = %d", rr.Code)
	}
}

func TestDatabasesListTenantScoped(t *testing.T) {
	h, cookie, _ := dbEnv(t, "custB")
	body := get(t, h, "/databases", cookie).Body.String()
	if !strings.Contains(body, "b_main") {
		t.Fatalf("custB missing own database:\n%s", body)
	}
	if strings.Contains(body, "a_main") {
		t.Fatalf("TENANT LEAK: custB saw another tenant's database:\n%s", body)
	}
}

func TestDatabasesListWithheldByDisabledMysqlFeature(t *testing.T) {
	// custA's plan disables mysql, which withholds databases.view -> 403.
	h, cookie, _ := dbEnv(t, "custA")
	if rr := get(t, h, "/databases", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("/databases for a plan with mysql disabled = %d, want 403", rr.Code)
	}
}

func TestAdminDatabasesSeesAll(t *testing.T) {
	h, cookie, _ := dbEnv(t, "admin")
	body := get(t, h, "/databases", cookie).Body.String()
	if !strings.Contains(body, "a_main") || !strings.Contains(body, "b_main") {
		t.Fatalf("admin should see all databases:\n%s", body)
	}
}
