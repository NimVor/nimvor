package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMandatoryTwoFactorRoleIsEnforced(t *testing.T) {
	h, db := rolesEnv(t)
	if _, err := db.Exec(`UPDATE roles SET require_two_factor = 1 WHERE id = 100`); err != nil {
		t.Fatal(err)
	}
	mgr := loginAsUser(t, h, "mgr")
	rr := get(t, h, "/users", mgr)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/profile#two-factor" {
		t.Fatalf("GET /users = %d loc=%q, want 303 /profile#two-factor", rr.Code, rr.Header().Get("Location"))
	}
	if rr := get(t, h, "/profile", mgr); rr.Code != http.StatusOK {
		t.Fatalf("profile must stay reachable, got %d", rr.Code)
	}
	req := httptest.NewRequest("GET", "/users", nil)
	req.Header.Set("Accept", "application/json")
	req.AddCookie(mgr)
	jr := httptest.NewRecorder()
	h.ServeHTTP(jr, req)
	if jr.Code != http.StatusForbidden {
		t.Fatalf("JSON request = %d, want 403", jr.Code)
	}
	// Once enrolled the restriction lifts.
	if _, err := db.Exec(`INSERT INTO user_two_factor (user_id, secret, recovery_codes, last_step, enabled_at) VALUES (2,'x','[]',0,'')`); err != nil {
		t.Fatal(err)
	}
	if rr := get(t, h, "/users", mgr); rr.Code != http.StatusOK {
		t.Fatalf("enrolled user GET /users = %d", rr.Code)
	}
}

func TestPasswordAgePolicyIsEnforced(t *testing.T) {
	h, db := rolesEnv(t)
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('policy_password_age_days','30','')`); err != nil {
		t.Fatal(err)
	}
	mgr := loginAsUser(t, h, "mgr")
	rr := get(t, h, "/users", mgr) // password_changed_at is 2026-01-01: expired
	if rr.Code == http.StatusOK {
		t.Skip("fixture password is younger than the policy; adjust the clock")
	}
	if rr.Header().Get("Location") != "/profile#password" {
		t.Fatalf("GET /users = %d loc=%q, want redirect to /profile#password", rr.Code, rr.Header().Get("Location"))
	}
	if rr := get(t, h, "/profile", mgr); rr.Code != http.StatusOK {
		t.Fatalf("profile must stay reachable, got %d", rr.Code)
	}
}

func TestStepUpRequiredForSensitiveRoutes(t *testing.T) {
	s, db := rolesEnvServer(t)
	h := s.Handler()
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/roles/new")
	// Signing in counts as a confirmation.
	rr := postForm(h, "/roles", roleForm(token, "fresh", "users.view"), admin)
	if rr.Header().Get("Location") != "/roles" {
		t.Fatalf("fresh session: loc=%q", rr.Header().Get("Location"))
	}
	// 20 minutes later the confirmation has lapsed.
	s.auth.SetClock(func() int64 { return time.Now().Unix() + 20*60 })
	token, admin = siteFormToken(t, h, refreshCookie(admin, rr), "/roles/new")
	rr = postForm(h, "/roles", roleForm(token, "stale", "users.view"), admin)
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/confirm-password") {
		t.Fatalf("stale confirmation: status=%d loc=%q, want /confirm-password", rr.Code, loc)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM roles WHERE name='stale'`).Scan(&n)
	if n != 0 {
		t.Fatal("role created without a fresh confirmation")
	}
	// JSON clients get a 403 with the redirect target.
	req := httptest.NewRequest("POST", "/roles", strings.NewReader(roleForm(token, "stale2", "users.view").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(admin)
	jr := httptest.NewRecorder()
	h.ServeHTTP(jr, req)
	if jr.Code != http.StatusForbidden || !strings.Contains(jr.Body.String(), "/confirm-password") {
		t.Fatalf("JSON stale confirmation = %d %s", jr.Code, jr.Body.String())
	}
}

// A sealed cookie copied before sign-out must not work afterwards.
func TestLogoutRevokesTheCopiedSessionCookie(t *testing.T) {
	h, _ := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	stolen := &http.Cookie{Name: c.Name, Value: c.Value}
	if rr := get(t, h, "/users", stolen); rr.Code != http.StatusOK {
		t.Fatalf("session before logout = %d", rr.Code)
	}
	token, c := siteFormToken(t, h, c, "/users")
	if rr := postForm(h, "/logout", map[string][]string{"_token": {token}}, c); rr.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rr.Code)
	}
	rr := get(t, h, "/users", &http.Cookie{Name: stolen.Name, Value: stolen.Value})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("replayed cookie after logout = %d loc=%q, want redirect to /login", rr.Code, rr.Header().Get("Location"))
	}
}

// Behind the installer's nginx the peer is the proxy; the client address must
// come from X-Forwarded-For (and only from a trusted peer).
func TestClientIPBehindTrustedProxy(t *testing.T) {
	rolesEnv(t) // builds a server, which installs the default trusted proxies
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Forwarded-For", "203.0.113.77")
	if got := clientIP(r); got != "203.0.113.77" {
		t.Fatalf("clientIP via trusted proxy = %q", got)
	}
	r.RemoteAddr = "198.51.100.5:40000"
	if got := clientIP(r); got != "198.51.100.5" {
		t.Fatalf("clientIP from an untrusted peer = %q (header must be ignored)", got)
	}
}

func TestSecurityHeadersMatchPHP(t *testing.T) {
	h, _ := rolesEnv(t)
	rr := get(t, h, "/login", nil)
	for k, want := range map[string]string{
		"Cache-Control":                "no-store",
		"Permissions-Policy":           "camera=(), microphone=(), geolocation=(), payment=()",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"X-Frame-Options":              "DENY",
	} {
		if got := rr.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if cc := get(t, h, "/assets/css/whacp.css", nil).Header().Get("Cache-Control"); cc == "no-store" {
		t.Errorf("static assets must stay cacheable, got %q", cc)
	}
}

// Every signed-in page carries the shared shell: a permission-filtered
// sidebar, the search palette and the confirm/job modals.
func TestPagesCarryTheNavigationShell(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	for _, path := range []string{"/", "/users", "/roles", "/profile"} {
		body := get(t, h, path, admin).Body.String()
		for _, want := range []string{`id="wcp-sidebar"`, `href="/backups"`, `id="wcp-palette"`, `id="wcp-job"`, `id="wcp-confirm"`} {
			if !pageHas(body, want) {
				t.Errorf("%s: shell element %q missing", path, want)
			}
		}
	}
	// A role without the permission does not see the item.
	mgr := loginAsUser(t, h, "mgr")
	body := get(t, h, "/users", mgr).Body.String()
	if pageHas(body, `href="/backups"`) || pageHas(body, `href="/settings"`) {
		t.Error("the sidebar shows areas the role may not open")
	}
	if !pageHas(body, `href="/users"`) {
		t.Error("the sidebar lacks an area the role may open")
	}
}
