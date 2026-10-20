package http

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// buildGuarded returns a server (with two test-only gated routes registered on
// its mux) and a logged-in session cookie for the seeded customer.
func buildGuarded(t *testing.T) (http.Handler, *http.Cookie) {
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
	// A customer (scope own) on a plan whose feature list disables mysql.
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (2,'customer','Customer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES (1,'databases.view','View','db'),(2,'mail.manage','Mail','mail')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (2,1),(2,2)`)
	exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES (1,'nodb','','')`)
	exec(`INSERT INTO feature_list_disabled (list_id,feature) VALUES (1,'mysql'),(1,'mongodb')`)
	exec(`INSERT INTO plans (id,name,feature_list_id,created_at,updated_at) VALUES (1,'Basic',1,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'cust','c@x','Cust',?,2,1,'2026-01-01T00:00:00Z','','',1)`, loginHash)

	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ok := func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("OK")) }
	// databases.view permission is withheld because the plan disables mysql.
	s.mux.HandleFunc("GET /t/db", s.guard("", "databases.view", ok))
	// mail.manage is held and not feature-gated here.
	s.mux.HandleFunc("GET /t/mail", s.guard("", "mail.manage", ok))
	// A mongodb-owned route is gated by the disabled feature.
	s.mux.HandleFunc("GET /t/mongo", s.guard("sites.mongo", "", ok))
	// A redis-owned route is NOT disabled.
	s.mux.HandleFunc("GET /t/redis", s.guard("sites.redis", "", ok))

	h := s.Handler()

	// Log in to obtain a session cookie.
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"cust"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login = %d", rr.Code)
	}
	return h, rr.Result().Cookies()[0]
}

func get(t *testing.T, h http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestPermissionWithheldByDisabledFeature(t *testing.T) {
	h, cookie := buildGuarded(t)
	// databases.view is withheld because the plan disables the mysql feature.
	if rr := get(t, h, "/t/db", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("/t/db = %d, want 403 (feature-withheld permission)", rr.Code)
	}
	// mail.manage is held and not gated -> allowed.
	if rr := get(t, h, "/t/mail", cookie); rr.Code != http.StatusOK {
		t.Fatalf("/t/mail = %d, want 200", rr.Code)
	}
}

func TestRouteGatedByDisabledFeature(t *testing.T) {
	h, cookie := buildGuarded(t)
	if rr := get(t, h, "/t/mongo", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("/t/mongo = %d, want 403 (mongodb feature disabled)", rr.Code)
	}
	if rr := get(t, h, "/t/redis", cookie); rr.Code != http.StatusOK {
		t.Fatalf("/t/redis = %d, want 200 (redis feature on)", rr.Code)
	}
}

func TestGuardedRouteRequiresAuth(t *testing.T) {
	h, _ := buildGuarded(t)
	// No cookie -> redirect to login, never 200.
	if rr := get(t, h, "/t/mail", nil); rr.Code == http.StatusOK {
		t.Fatal("guarded route served without authentication")
	}
}
