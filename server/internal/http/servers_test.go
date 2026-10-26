package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func TestServersListStaffOnly(t *testing.T) {
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
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'servers.manage','v','Server')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'cust','c@x','Cust',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash)
	// A remote server in addition to the migration-seeded local one (id 1).
	// token_enc must never render.
	exec(`INSERT INTO servers (id,name,kind,url,token_enc,status,created_at) VALUES
		(2,'edge1','remote','https://edge1:9443','ANOTHER-SECRET','online','')`)

	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	login := func(u string) *http.Cookie {
		c, tok := getLogin(t, h)
		rr := postForm(h, "/login", url.Values{"_token": {tok}, "username": {u}, "password": {loginPassword}}, c)
		return rr.Result().Cookies()[0]
	}

	body := get(t, h, "/servers", login("admin")).Body.String()
	if !pageHas(body, "This server") || !pageHas(body, "edge1") {
		t.Fatalf("admin servers page missing servers:\n%s", body)
	}
	if pageHas(body, "ANOTHER-SECRET") {
		t.Fatalf("SECURITY: servers page leaked the encrypted agent token")
	}
	if rr := get(t, h, "/servers", login("cust")); rr.Code != http.StatusForbidden {
		t.Fatalf("customer /servers = %d, want 403", rr.Code)
	}
}
