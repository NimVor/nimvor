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

// subdomainsEnv seeds a customer with websites.manage and a website that has a
// subdomain child. featureListID (0 = none) can disable the subdomains feature.
func subdomainsEnv(t *testing.T, disableSubdomains bool) (http.Handler, *http.Cookie) {
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
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (2,'customer','Customer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'websites.manage','v','H'),(2,'websites.view','v','H')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (2,1),(2,2)`)
	var planID any
	if disableSubdomains {
		exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES (1,'nosub','','')`)
		exec(`INSERT INTO feature_list_disabled (list_id,feature) VALUES (1,'subdomains')`)
		exec(`INSERT INTO plans (id,name,feature_list_id,created_at,updated_at) VALUES (1,'P',1,'','')`)
		planID = 1
	}
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'cust','c@x','Cust',?,2,1,'2026-01-01T00:00:00Z','','',?)`, loginHash, planID)
	exec(`INSERT INTO websites (id,server_id,owner_id,parent_id,domain,status,system_user,root,document_root,kind,created_at,updated_at) VALUES
		(10,1,1,NULL,'a.example','active','ua','/srv/a','/srv/a/pub','php','',''),
		(12,1,1,10,'blog.a.example','active','ua','/srv/a/blog','/srv/a/blog','php','','')`)

	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	c, tok := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {tok}, "username": {"cust"}, "password": {loginPassword}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rr.Code)
	}
	return h, rr.Result().Cookies()[0]
}

func TestSubdomainsListed(t *testing.T) {
	h, cookie := subdomainsEnv(t, false)
	body := get(t, h, "/sites/10/subdomains", cookie).Body.String()
	if !strings.Contains(body, "blog.a.example") {
		t.Fatalf("subdomain not listed:\n%s", body)
	}
}

func TestSubdomainsRouteGatedByDisabledFeature(t *testing.T) {
	// websites.manage is held, but the subdomains feature is off on the plan:
	// the route-name feature gate must 403 (first live use of requireFeatureRoute).
	h, cookie := subdomainsEnv(t, true)
	if rr := get(t, h, "/sites/10/subdomains", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("/sites/10/subdomains with subdomains feature off = %d, want 403", rr.Code)
	}
}
