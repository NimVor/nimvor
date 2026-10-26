package http

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func mailEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sql.DB) {
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
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'mail.manage','Mail','Hosting')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(2,1)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'custA','ca@x','Customer A',?,2,1,'2026-01-01T00:00:00Z','',''),
		(3,'custB','cb@x','Customer B',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,status,system_user,root,document_root,kind,created_at,updated_at) VALUES
		(10,1,2,'a.example','active','ua','/srv/a','/srv/a/pub','php','','')`)
	// d1 owned by custA; d2 admin-owned but attached to custA's website; d3 custB's.
	exec(`INSERT INTO mail_domains (id,server_id,domain,website_id,owner_id,created_at) VALUES
		(1,1,'a.example',NULL,2,''),
		(2,1,'mx-a.example',10,1,''),
		(3,1,'b.example',NULL,3,'')`)
	// Two mailboxes + one alias on d1, to prove the counts.
	exec(`INSERT INTO mail_mailboxes (id,domain_id,local,created_at) VALUES (1,1,'info',''),(2,1,'sales','')`)
	exec(`INSERT INTO mail_aliases (id,domain_id,local,destinations,created_at) VALUES (1,1,'all','info@a.example','')`)

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

func TestMailDomainsWebsiteAwareScopeAndCounts(t *testing.T) {
	h, cookie, _ := mailEnv(t, "custA")
	body := get(t, h, "/mail", cookie).Body.String()
	if !pageHas(body, "a.example") || !pageHas(body, "mx-a.example") {
		t.Fatalf("custA missing own/attached mail domains:\n%s", body)
	}
	if pageHas(body, "b.example") {
		t.Fatalf("TENANT LEAK: custA saw another tenant's mail domain:\n%s", body)
	}
	// a.example shows 2 mailboxes and 1 alias.
	if !pageHas(body, "<td>2</td>") || !pageHas(body, "<td>1</td>") {
		t.Fatalf("mailbox/alias counts missing:\n%s", body)
	}
}

func TestMailAdminSeesAll(t *testing.T) {
	h, cookie, _ := mailEnv(t, "admin")
	body := get(t, h, "/mail", cookie).Body.String()
	for _, d := range []string{"a.example", "mx-a.example", "b.example"} {
		if !pageHas(body, d) {
			t.Fatalf("admin missing mail domain %s", d)
		}
	}
}

func TestMailRequiresPermission(t *testing.T) {
	h, cookie, db := mailEnv(t, "custA")
	if _, err := db.Exec(`DELETE FROM role_permissions WHERE role_id = 2`); err != nil {
		t.Fatal(err)
	}
	if rr := get(t, h, "/mail", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("/mail without mail.manage = %d, want 403", rr.Code)
	}
}
