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

// dnsEnv seeds two tenants, a customer website, and DNS zones that exercise the
// website-aware scope, then logs in as the chosen user.
func dnsEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sql.DB) {
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
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'dns.manage','DNS','Hosting')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(2,1)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'custA','ca@x','Customer A',?,2,1,'2026-01-01T00:00:00Z','',''),
		(3,'custB','cb@x','Customer B',?,2,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash, loginHash)
	// custA owns website 10.
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,status,system_user,root,document_root,kind,created_at,updated_at) VALUES
		(10,1,2,'a.example','active','ua','/srv/a','/srv/a/pub','php','','')`)
	// Zones:
	//  z1 owned by custA directly.
	//  z2 owned by the ADMIN but attached to custA's website 10 -> visible to custA via WithWebsite.
	//  z3 owned by custB -> never visible to custA.
	exec(`INSERT INTO dns_zones (id,server_id,name,website_id,owner_id,primary_ns,hostmaster,created_at,updated_at) VALUES
		(1,1,'a.example',NULL,2,'ns1','hm','',''),
		(2,1,'extra-a.example',10,1,'ns1','hm','',''),
		(3,1,'b.example',NULL,3,'ns1','hm','','')`)

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

func TestDnsZonesWebsiteAwareScope(t *testing.T) {
	h, cookie, _ := dnsEnv(t, "custA")
	body := get(t, h, "/dns", cookie).Body.String()
	if !strings.Contains(body, "a.example") {
		t.Fatalf("custA missing own zone:\n%s", body)
	}
	// An admin-owned zone attached to custA's website is visible via WithWebsite.
	if !strings.Contains(body, "extra-a.example") {
		t.Fatalf("WithWebsite: zone attached to custA's website not visible:\n%s", body)
	}
	// custB's zone must never appear.
	if strings.Contains(body, "b.example") {
		t.Fatalf("TENANT LEAK: custA saw another tenant's zone:\n%s", body)
	}
}

func TestDnsZonesAdminSeesAll(t *testing.T) {
	h, cookie, _ := dnsEnv(t, "admin")
	body := get(t, h, "/dns", cookie).Body.String()
	for _, z := range []string{"a.example", "extra-a.example", "b.example"} {
		if !strings.Contains(body, z) {
			t.Fatalf("admin missing zone %s", z)
		}
	}
}

func TestDnsRequiresPermission(t *testing.T) {
	h, cookie, db := dnsEnv(t, "custA")
	if _, err := db.Exec(`DELETE FROM role_permissions WHERE role_id = 2`); err != nil {
		t.Fatal(err)
	}
	if rr := get(t, h, "/dns", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("/dns without dns.manage = %d, want 403", rr.Code)
	}
}
