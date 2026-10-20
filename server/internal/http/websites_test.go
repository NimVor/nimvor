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

// websitesEnv seeds two tenants' websites and logs in as the chosen user,
// returning the handler and that user's session cookie.
func websitesEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sql.DB) {
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
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'websites.view','View','Hosting')`)
	// Both admin and customer hold websites.view.
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(2,1)`)
	// admin (1), customer A (2), customer B (3).
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'custA','ca@x','Customer A',?,2,1,'2026-01-01T00:00:00Z','',''),
		(3,'custB','cb@x','Customer B',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,status,system_user,root,document_root,kind,https,enabled,created_at,updated_at) VALUES
		(10,1,2,'alpha-a.example','active','ua','/srv/a','/srv/a/pub','php',1,1,'',''),
		(11,1,3,'beta-b.example','active','ub','/srv/b','/srv/b/pub','static',0,1,'','')`)

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

func TestWebsitesListScopedToTenant(t *testing.T) {
	// Customer A must see only their own site, never customer B's.
	h, cookie, _ := websitesEnv(t, "custA")
	body := get(t, h, "/websites", cookie).Body.String()
	if !strings.Contains(body, "alpha-a.example") {
		t.Fatalf("custA did not see their own website:\n%s", body)
	}
	if strings.Contains(body, "beta-b.example") {
		t.Fatalf("TENANT LEAK: custA saw another tenant's website:\n%s", body)
	}
}

func TestWebsitesListAdminSeesAll(t *testing.T) {
	h, cookie, _ := websitesEnv(t, "admin")
	body := get(t, h, "/websites", cookie).Body.String()
	if !strings.Contains(body, "alpha-a.example") || !strings.Contains(body, "beta-b.example") {
		t.Fatalf("admin should see every website:\n%s", body)
	}
}

func TestWebsitesListRequiresPermission(t *testing.T) {
	// Strip the permission from the customer role and confirm a 403.
	h, cookie, db := websitesEnv(t, "custA")
	if _, err := db.Exec(`DELETE FROM role_permissions WHERE role_id = 2`); err != nil {
		t.Fatal(err)
	}
	if rr := get(t, h, "/websites", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("/websites without websites.view = %d, want 403", rr.Code)
	}
}

func TestWebsitesListRequiresAuth(t *testing.T) {
	h, _, _ := websitesEnv(t, "admin")
	if rr := get(t, h, "/websites", nil); rr.Code == http.StatusOK {
		t.Fatal("/websites served without authentication")
	}
}
