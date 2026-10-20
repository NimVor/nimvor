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

func TestAuditViewerStaffOnlyAndLists(t *testing.T) {
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
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'audit.view','View audit','Administration')`)
	// Only the admin role holds audit.view.
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'cust','c@x','Cust',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash)
	exec(`INSERT INTO audit_logs (user_id,username,action,target_type,target_id,result,ip,details,created_at)
		VALUES (1,'admin','database.create','database','shop','success','10.0.0.1','{}','2026-01-01T00:00:00Z')`)

	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()

	login := func(who string) *http.Cookie {
		c, tok := getLogin(t, h)
		rr := postForm(h, "/login", url.Values{"_token": {tok}, "username": {who}, "password": {loginPassword}}, c)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("login %s = %d", who, rr.Code)
		}
		return rr.Result().Cookies()[0]
	}

	// Admin sees the audit entry.
	body := get(t, h, "/audit", login("admin")).Body.String()
	if !strings.Contains(body, "database.create") || !strings.Contains(body, "shop") {
		t.Fatalf("admin audit page missing the entry:\n%s", body)
	}
	// Customer (no audit.view) is forbidden.
	if rr := get(t, h, "/audit", login("cust")); rr.Code != http.StatusForbidden {
		t.Fatalf("customer /audit = %d, want 403", rr.Code)
	}
}
