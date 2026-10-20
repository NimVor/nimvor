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

func adminEnv(t *testing.T, loginAs string) http.Handler {
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
	exec(`INSERT INTO roles (id,name,label,scope,require_two_factor,is_system,created_at) VALUES
		(1,'administrator','Administrator','all',1,1,''),(2,'reseller','Reseller','own',0,1,'')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'users.view','v','Admin'),(2,'plans.manage','p','Admin')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(2,1),(2,2)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'reseller','r@x','Reseller',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash)
	// A global plan (no owner) and a reseller-owned plan.
	exec(`INSERT INTO plans (id,owner_id,name,max_websites,max_databases,disk_quota_mb,bandwidth_mb,created_at,updated_at) VALUES
		(1,NULL,'GlobalPlan',10,20,1000,5000,'',''),
		(2,2,'ResellerPlan',5,5,500,1000,'','')`)

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

func TestRolesListShowsSystemRoles(t *testing.T) {
	h := adminEnv(t, "admin")
	body := get(t, h, "/roles", nil).Body.String()
	if !strings.Contains(body, "Administrator") || !strings.Contains(body, "Reseller") {
		t.Fatalf("roles page missing roles:\n%s", body)
	}
	if !strings.Contains(body, "built-in") {
		t.Fatalf("roles page missing system marker:\n%s", body)
	}
}

func TestPlansAdminSeesGlobalAndReseller(t *testing.T) {
	h := adminEnv(t, "admin")
	body := get(t, h, "/plans", nil).Body.String()
	if !strings.Contains(body, "GlobalPlan") || !strings.Contains(body, "ResellerPlan") {
		t.Fatalf("admin should see all plans:\n%s", body)
	}
}

func TestPlansResellerSeesOnlyOwn(t *testing.T) {
	h := adminEnv(t, "reseller")
	body := get(t, h, "/plans", nil).Body.String()
	if !strings.Contains(body, "ResellerPlan") {
		t.Fatalf("reseller missing own plan:\n%s", body)
	}
	if strings.Contains(body, "GlobalPlan") {
		t.Fatalf("reseller must not see the global plan:\n%s", body)
	}
}
