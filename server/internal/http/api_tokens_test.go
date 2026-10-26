package http

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var shownTokenRe = regexp.MustCompile(`whacp_[a-f0-9]{40}`)

func apiGet(h http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestIssuedAPITokenAuthenticatesExpiresAndIsRateLimited(t *testing.T) {
	h, db := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/profile")
	rr := postForm(h, "/profile/tokens", map[string][]string{"_token": {token}, "name": {"ci"}, "ability": {"read"}, "expires_days": {"30"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", rr.Code)
	}
	page := get(t, h, "/profile", refreshCookie(admin, rr)).Body.String()
	shown := shownTokenRe.FindString(page)
	if shown == "" {
		t.Fatalf("the token was not shown on the profile page")
	}
	var expires *string
	if err := db.QueryRow(`SELECT expires_at FROM api_tokens WHERE name='ci'`).Scan(&expires); err != nil || expires == nil {
		t.Fatalf("expires_at = %v, %v; want a date", expires, err)
	}
	if rr := apiGet(h, "/api/v1/me", shown); rr.Code != http.StatusOK {
		t.Fatalf("token as displayed: %d %s", rr.Code, rr.Body.String())
	}
	if rr := apiGet(h, "/api/v1/me", "whacp_"+strings.Repeat("0", 40)); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unknown token = %d", rr.Code)
	}
	var failed int
	_ = db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'api.auth_failed'`).Scan(&failed)
	if failed != 1 {
		t.Fatalf("api.auth_failed rows = %d, want 1", failed)
	}
	limited := 0
	for i := 0; i < 130; i++ {
		if apiGet(h, "/api/v1/me", shown).Code == http.StatusTooManyRequests {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("no 429 after 130 requests in a minute")
	}
}

func TestAPITokenLimitsAndValidation(t *testing.T) {
	h, db := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	for i := 0; i < 22; i++ {
		token, c := siteFormToken(t, h, admin, "/profile")
		rr := postForm(h, "/profile/tokens", map[string][]string{"_token": {token}, "name": {"t"}, "ability": {"read"}}, c)
		admin = refreshCookie(c, rr)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE user_id = 1`).Scan(&n)
	if n != 20 {
		t.Fatalf("tokens = %d, want the cap of 20", n)
	}
	var exp *string
	_ = db.QueryRow(`SELECT expires_at FROM api_tokens WHERE user_id = 1 LIMIT 1`).Scan(&exp)
	if exp == nil {
		t.Fatal("default expiry must be 90 days, not never")
	}
}
