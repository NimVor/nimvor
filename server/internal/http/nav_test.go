package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func navEnv(t *testing.T, loginAs string) http.Handler {
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
		(1,'websites.view','v','H'),(2,'databases.view','v','H'),(3,'audit.view','v','A'),(4,'users.view','v','A')`)
	// Admin holds all; customer holds only the two hosting view perms.
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(1,4),(2,1),(2,2)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'cust','c@x','Cust',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash)

	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	c, tok := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {tok}, "username": {loginAs}, "password": {loginPassword}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d", loginAs, rr.Code)
	}
	return &loggedIn{h: h, cookie: rr.Result().Cookies()[0]}
}

func TestDashboardNavGatedByPermission(t *testing.T) {
	// Customer: sees Websites + Databases + Profile, not Users or Audit.
	cust := navEnv(t, "cust")
	body := get(t, cust, "/", nil).Body.String()
	if !strings.Contains(body, `href="/websites"`) || !strings.Contains(body, `href="/databases"`) || !strings.Contains(body, `href="/profile"`) {
		t.Fatalf("customer nav missing allowed links:\n%s", body)
	}
	if strings.Contains(body, `href="/users"`) || strings.Contains(body, `href="/audit"`) {
		t.Fatalf("customer nav shows staff-only links:\n%s", body)
	}

	// Admin: sees Users and Audit.
	admin := navEnv(t, "admin")
	abody := get(t, admin, "/", nil).Body.String()
	if !strings.Contains(abody, `href="/users"`) || !strings.Contains(abody, `href="/audit"`) {
		t.Fatalf("admin nav missing staff links:\n%s", abody)
	}
}
