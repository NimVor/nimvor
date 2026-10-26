package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func notifyEnv(t *testing.T, loginAs string) http.Handler {
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
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'notifications.view','v','Overview')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(2,1)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'custA','ca@x','Customer A',?,2,1,'2026-01-01T00:00:00Z','',''),
		(3,'custB','cb@x','Customer B',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,status,system_user,root,document_root,kind,created_at,updated_at) VALUES
		(10,1,2,'a.example','active','ua','/srv/a','/srv/a/pub','php','',''),
		(11,1,3,'b.example','active','ub','/srv/b','/srv/b/pub','php','','')`)
	// n1: for custA's website; n2: owned by custA (no website); n3: for custB's website.
	exec(`INSERT INTO notifications (id,server_id,website_id,owner_id,kind,severity,title,dedupe_key,created_at) VALUES
		(1,1,10,NULL,'ssl','info','Cert for A','k1','2026-01-01'),
		(2,1,NULL,2,'backup','info','Backup for A','k2','2026-01-02'),
		(3,1,11,NULL,'ssl','info','Cert for B','k3','2026-01-03')`)

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

func TestNotificationsScopedToRecipient(t *testing.T) {
	h := notifyEnv(t, "custA")
	body := get(t, h, "/notifications", nil).Body.String()
	if !pageHas(body, "Cert for A") || !pageHas(body, "Backup for A") {
		t.Fatalf("custA missing own notifications:\n%s", body)
	}
	if pageHas(body, "Cert for B") {
		t.Fatalf("TENANT LEAK: custA saw another tenant's notification:\n%s", body)
	}
}

func TestNotificationsAdminSeesAll(t *testing.T) {
	h := notifyEnv(t, "admin")
	body := get(t, h, "/notifications", nil).Body.String()
	for _, n := range []string{"Cert for A", "Backup for A", "Cert for B"} {
		if !pageHas(body, n) {
			t.Fatalf("admin missing notification %q", n)
		}
	}
}

func TestNotificationsMarkAllReadScoped(t *testing.T) {
	h := notifyEnv(t, "custA")
	// Grab a CSRF token + refreshed cookie from the page.
	rr := get(t, h, "/notifications", nil)
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token on notifications page:\n%s", rr.Body.String())
	}
	cookie := h.(*loggedIn).cookie
	if cs := rr.Result().Cookies(); len(cs) > 0 {
		cookie = cs[0]
	}
	post := postForm(h.(*loggedIn).h, "/notifications/read", url.Values{"_token": {m[1]}}, cookie)
	if post.Code != http.StatusSeeOther {
		t.Fatalf("mark read = %d, want 303", post.Code)
	}
	// After marking read, the customer's own items render without the unread class.
	body := get(t, h, "/notifications", nil).Body.String()
	if pageHas(body, "is-unread") {
		t.Fatalf("notifications still unread after mark-all-read:\n%s", body)
	}
}

func TestNotificationsReadRejectsBadCSRF(t *testing.T) {
	h := notifyEnv(t, "custA")
	cookie := h.(*loggedIn).cookie
	post := postForm(h.(*loggedIn).h, "/notifications/read", url.Values{"_token": {"nope"}}, cookie)
	if post.Code != http.StatusBadRequest {
		t.Fatalf("bad-CSRF mark read = %d, want 400", post.Code)
	}
}
