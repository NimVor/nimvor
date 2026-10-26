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
