package http

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/twofactor"
)

// freshCode computes a valid TOTP for [secret] on the current time step.
// The ±1-step window keeps it valid for ~60s, well beyond one test request.
func freshCode(t *testing.T, secret string) string {
	t.Helper()
	step := time.Now().Unix() / twofactor.Period
	code, err := twofactor.Code(secret, step)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// enable2FAForUser turns two-factor on for the test user directly in the
// database (same zero key testConfig uses for the panel key file).
func enable2FAForUser(t *testing.T, db *sql.DB) string {
	t.Helper()
	var key [32]byte
	tf := twofactor.New(db, crypto.NewWithKey(key))
	secret, err := twofactor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	codes, err := tf.Enable(context.Background(), 1, secret, freshCode(t, secret))
	if err != nil {
		t.Fatal(err)
	}
	_ = codes
	return secret
}

// loginPasswordStep posts the password for the two-factor test user and
// returns the session cookie at the pending (second-factor) step.
func loginPasswordStep(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"root"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login/two-factor" {
		t.Fatalf("login POST = %d loc=%q, want 303 /login/two-factor", rr.Code, rr.Header().Get("Location"))
	}
	return nextCookie(t, rr, cookie)
}

func getTwoFactorPage(t *testing.T, h http.Handler, cookie *http.Cookie) (string, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/login/two-factor", nil)
	req.AddCookie(cookie)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /login/two-factor = %d, want 200:\n%s", rr.Code, rr.Body.String())
	}
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token in two-factor form:\n%s", rr.Body.String())
	}
	return rr.Body.String(), m[1]
}

func TestLoginTwoFactorEndToEnd(t *testing.T) {
	h, db := serverWithUser(t)
	secret := enable2FAForUser(t, db)

	// Password step: redirected to the second factor, NOT signed in.
	cookie := loginPasswordStep(t, h)
	if rr := getWith(t, h, "/", cookie); rr.Code != http.StatusSeeOther {
		t.Fatalf("dashboard with a pending sign-in = %d, want 303 to /login", rr.Code)
	}

	// The step renders with the code form and the pending user.
	body, token := getTwoFactorPage(t, h, cookie)
	if !strings.Contains(body, `name="code"`) || !strings.Contains(body, "root") {
		t.Fatalf("two-factor page missing form/user:\n%s", body)
	}

	// A wrong code keeps the sign-in pending and shows an error.
	rr := postForm(h, "/login/two-factor", url.Values{"_token": {token}, "code": {"000000"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login/two-factor" {
		t.Fatalf("bad code POST = %d loc=%q, want 303 /login/two-factor", rr.Code, rr.Header().Get("Location"))
	}
	cookie = nextCookie(t, rr, cookie)
	body, token = getTwoFactorPage(t, h, cookie)
	if !strings.Contains(body, "not valid") {
		t.Fatalf("bad-code error not shown:\n%s", body)
	}

	// The correct code completes the sign-in.
	rr = postForm(h, "/login/two-factor", url.Values{"_token": {token}, "code": {freshCode(t, secret)}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("good code POST = %d loc=%q, want 303 /", rr.Code, rr.Header().Get("Location"))
	}
	signed := rr.Result().Cookies()
	if len(signed) == 0 {
		t.Fatal("completed sign-in set no session cookie")
	}
	if rr := getWith(t, h, "/", signed[0]); rr.Code != http.StatusOK {
		t.Fatalf("dashboard after 2FA = %d, want 200", rr.Code)
	}
}

func TestLoginTwoFactorRecoveryCode(t *testing.T) {
	h, db := serverWithUser(t)
	enable2FAForUser(t, db)
	var key [32]byte
	tf := twofactor.New(db, crypto.NewWithKey(key))
	codes, err := tf.RegenerateRecovery(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}

	cookie := loginPasswordStep(t, h)
	_, token := getTwoFactorPage(t, h, cookie)
	rr := postForm(h, "/login/two-factor", url.Values{"_token": {token}, "code": {codes[0]}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("recovery code POST = %d loc=%q, want 303 /", rr.Code, rr.Header().Get("Location"))
	}
	signed := rr.Result().Cookies()
	if rr := getWith(t, h, "/", signed[0]); rr.Code != http.StatusOK {
		t.Fatalf("dashboard after recovery-code sign-in = %d", rr.Code)
	}
}

func TestLoginTwoFactorCancel(t *testing.T) {
	h, db := serverWithUser(t)
	enable2FAForUser(t, db)
	cookie := loginPasswordStep(t, h)
	_, token := getTwoFactorPage(t, h, cookie)

	rr := postForm(h, "/login/two-factor", url.Values{"_token": {token}, "cancel": {"1"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("cancel POST = %d loc=%q, want 303 /login", rr.Code, rr.Header().Get("Location"))
	}
	// The pending sign-in is gone: the step bounces back to the password form.
	c := rr.Result().Cookies()
	if len(c) > 0 {
		cookie = c[0]
	}
	rr = getWith(t, h, "/login/two-factor", cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("two-factor step after cancel = %d loc=%q, want 303 /login", rr.Code, rr.Header().Get("Location"))
	}
	if rr = getWith(t, h, "/", cookie); rr.Code != http.StatusSeeOther {
		t.Fatalf("dashboard after cancel = %d, want 303 /login", rr.Code)
	}
}

func TestLoginTwoFactorBadCSRF(t *testing.T) {
	h, db := serverWithUser(t)
	enable2FAForUser(t, db)
	cookie := loginPasswordStep(t, h)
	rr := postForm(h, "/login/two-factor", url.Values{"_token": {"deadbeef"}, "code": {"123456"}}, cookie)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad CSRF POST = %d, want 400", rr.Code)
	}
	if rr := getWith(t, h, "/", cookie); rr.Code != http.StatusSeeOther {
		t.Fatalf("must not be signed in after a bad-CSRF two-factor POST (%d)", rr.Code)
	}
}

func TestLoginTwoFactorStepWithoutPending(t *testing.T) {
	h, _ := serverWithUser(t)
	rr := getWith(t, h, "/login/two-factor", nil)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("two-factor step without pending = %d loc=%q, want 303 /login", rr.Code, rr.Header().Get("Location"))
	}
}

func TestLoginKeyOnlyUserRefused(t *testing.T) {
	h, db := serverWithUser(t)
	if _, err := db.Exec(`INSERT INTO webauthn_credentials (user_id, credential_id, public_key, name, created_at)
		VALUES (1,'cred1','key','yubikey','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"root"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("key-only login = %d loc=%q, want 303 /login (refusal)", rr.Code, rr.Header().Get("Location"))
	}
	c := rr.Result().Cookies()
	if len(c) > 0 {
		cookie = c[0]
	}
	rr = getWith(t, h, "/login", cookie)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "Two-factor sign-in is required") {
		t.Fatalf("refusal message not shown:\n%s", rr.Body.String())
	}
	if rr = getWith(t, h, "/", cookie); rr.Code != http.StatusSeeOther {
		t.Fatalf("key-only user signed in without the key (dashboard %d)", rr.Code)
	}
}

func TestLoginTwoFactorInvalidatedByPasswordChange(t *testing.T) {
	h, db := serverWithUser(t)
	secret := enable2FAForUser(t, db)
	cookie := loginPasswordStep(t, h)
	_, token := getTwoFactorPage(t, h, cookie)
	// Rotate the password out from under the pending sign-in.
	if _, err := db.Exec(`UPDATE users SET password_hash = '$2y$12$differenthashvaluethatdoesntmatterxxxxxxxxxxxxxxxxxxxxx' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	rr := postForm(h, "/login/two-factor", url.Values{"_token": {token}, "code": {freshCode(t, secret)}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("invalidated pending = %d loc=%q, want 303 /login", rr.Code, rr.Header().Get("Location"))
	}
	c := rr.Result().Cookies()
	if len(c) > 0 {
		cookie = c[0]
	}
	if rr = getWith(t, h, "/", cookie); rr.Code != http.StatusSeeOther {
		t.Fatalf("signed in through an invalidated pending sign-in (dashboard %d)", rr.Code)
	}
}

// ---- profile self-service --------------------------------------------------

func signInProfile(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"root"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("login = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login set no session cookie")
	}
	return cookies[0]
}

func profilePage(t *testing.T, h http.Handler, cookie *http.Cookie) (string, string) {
	t.Helper()
	rr := getWith(t, h, "/profile", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /profile = %d:\n%s", rr.Code, rr.Body.String())
	}
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token in profile page:\n%s", rr.Body.String())
	}
	return rr.Body.String(), m[1]
}

func postProfile2FA(t *testing.T, h http.Handler, path, token string, extra url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"_token": {token}}
	for k, v := range extra {
		form[k] = v
	}
	return postForm(h, path, form, cookie)
}

func TestProfileTwoFactorSetupAndEnable(t *testing.T) {
	h, _ := serverWithUser(t)
	cookie := signInProfile(t, h)

	body, token := profilePage(t, h, cookie)
	if !strings.Contains(body, "Two-factor sign-in is off") {
		t.Fatalf("profile should say 2FA is off:\n%s", body)
	}

	// Start the setup: the staged secret is shown on the profile page.
	rr := postProfile2FA(t, h, "/profile/two-factor/setup", token, nil, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("setup POST = %d", rr.Code)
	}
	cookie = nextCookie(t, rr, cookie)
	body, token = profilePage(t, h, cookie)
	secretRe := regexp.MustCompile(`<code class="font-monospace">([A-Z2-7]{32})</code>`)
	m := secretRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("staged secret not shown:\n%s", body)
	}
	secret := m[1]
	if !strings.Contains(body, "otpauth://totp/") {
		t.Fatalf("otpauth URI not shown:\n%s", body)
	}

	// Enable with a valid code and the current password.
	rr = postProfile2FA(t, h, "/profile/two-factor/enable", token,
		url.Values{"code": {freshCode(t, secret)}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("enable POST = %d", rr.Code)
	}
	cookie = nextCookie(t, rr, cookie)
	body, _ = profilePage(t, h, cookie)
	if !strings.Contains(body, "Two-factor sign-in is on") {
		t.Fatalf("profile should say 2FA is on:\n%s", body)
	}
	if !strings.Contains(body, "Recovery codes") || !strings.Contains(body, "work once") {
		t.Fatalf("one-time recovery codes not shown:\n%s", body)
	}
}

func TestProfileTwoFactorEnableNeedsSetupAndPassword(t *testing.T) {
	h, _ := serverWithUser(t)
	cookie := signInProfile(t, h)
	_, token := profilePage(t, h, cookie)

	// No setup started.
	rr := postProfile2FA(t, h, "/profile/two-factor/enable", token,
		url.Values{"code": {"123456"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("enable POST = %d", rr.Code)
	}
	cookie = nextCookie(t, rr, cookie)
	body, token := profilePage(t, h, cookie)
	if !strings.Contains(body, "Start the setup again.") {
		t.Fatalf("setup-required error not shown:\n%s", body)
	}

	// Setup started, wrong current password.
	rr = postProfile2FA(t, h, "/profile/two-factor/setup", token, nil, cookie)
	cookie = nextCookie(t, rr, cookie)
	body, token = profilePage(t, h, cookie)
	m := regexp.MustCompile(`<code class="font-monospace">([A-Z2-7]{32})</code>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("staged secret not shown:\n%s", body)
	}
	rr = postProfile2FA(t, h, "/profile/two-factor/enable", token,
		url.Values{"code": {freshCode(t, m[1])}, "password": {"wrong"}}, cookie)
	cookie = nextCookie(t, rr, cookie)
	body, _ = profilePage(t, h, cookie)
	if !strings.Contains(body, "Your current password is not correct.") {
		t.Fatalf("wrong-password error not shown:\n%s", body)
	}
}

func TestProfileTwoFactorEnableRejectsBadCode(t *testing.T) {
	h, _ := serverWithUser(t)
	cookie := signInProfile(t, h)
	_, token := profilePage(t, h, cookie)
	postProfile2FA(t, h, "/profile/two-factor/setup", token, nil, cookie)
	body, token := profilePage(t, h, cookie)
	m := regexp.MustCompile(`<code class="font-monospace">([A-Z2-7]{32})</code>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("staged secret not shown:\n%s", body)
	}
	rr := postProfile2FA(t, h, "/profile/two-factor/enable", token,
		url.Values{"code": {"000000"}, "password": {loginPassword}}, cookie)
	cookie = nextCookie(t, rr, cookie)
	body, _ = profilePage(t, h, cookie)
	if !strings.Contains(body, "That code is not valid") {
		t.Fatalf("bad-code error not shown:\n%s", body)
	}
	if !strings.Contains(body, "Two-factor sign-in is off") {
		t.Fatalf("2FA must still be off after a bad code:\n%s", body)
	}
}

func TestProfileTwoFactorDisable(t *testing.T) {
	h, db := serverWithUser(t)
	cookie := signInProfile(t, h)

	// Enable it first (directly in the DB, then refresh the page).
	enable2FAForUser(t, db)
	body, token := profilePage(t, h, cookie)
	if !strings.Contains(body, "Two-factor sign-in is on") {
		t.Fatalf("2FA should be on:\n%s", body)
	}

	// Wrong password does not disable.
	rr := postProfile2FA(t, h, "/profile/two-factor/disable", token,
		url.Values{"password": {"wrong"}}, cookie)
	cookie = nextCookie(t, rr, cookie)
	body, token = profilePage(t, h, cookie)
	if !strings.Contains(body, "Your current password is not correct.") || !strings.Contains(body, "is on") {
		t.Fatalf("disable with wrong password succeeded or error missing:\n%s", body)
	}

	// Correct password disables.
	rr = postProfile2FA(t, h, "/profile/two-factor/disable", token,
		url.Values{"password": {loginPassword}}, cookie)
	cookie = nextCookie(t, rr, cookie)
	body, _ = profilePage(t, h, cookie)
	if !strings.Contains(body, "Two-factor sign-in is off") {
		t.Fatalf("2FA must be off after disable:\n%s", body)
	}

	// Recovery while off is refused.
	rr = postProfile2FA(t, h, "/profile/two-factor/recovery", token,
		url.Values{"password": {loginPassword}}, cookie)
	cookie = nextCookie(t, rr, cookie)
	body, _ = profilePage(t, h, cookie)
	if !strings.Contains(body, "Two-factor sign-in is off.") {
		t.Fatalf("recovery while off not refused:\n%s", body)
	}
}

func TestProfileTwoFactorRecoveryRegeneratesCodes(t *testing.T) {
	h, db := serverWithUser(t)
	cookie := signInProfile(t, h)
	enable2FAForUser(t, db)
	_, token := profilePage(t, h, cookie)

	rr := postProfile2FA(t, h, "/profile/two-factor/recovery", token,
		url.Values{"password": {loginPassword}}, cookie)
	cookie = nextCookie(t, rr, cookie)
	body, _ := profilePage(t, h, cookie)
	if !strings.Contains(body, "New recovery codes created") {
		t.Fatalf("regeneration success not shown:\n%s", body)
	}
	pre := regexp.MustCompile(`<pre class="recovery-list">([^<]+)</pre>`).FindStringSubmatch(body)
	if pre == nil {
		t.Fatalf("new recovery codes not shown:\n%s", body)
	}
	codes := strings.Split(strings.TrimSpace(pre[1]), "\n")
	if len(codes) != 10 {
		t.Fatalf("want 10 recovery codes, got %d", len(codes))
	}
	// The freshly shown code verifies as a recovery code.
	var key [32]byte
	tf := twofactor.New(db, crypto.NewWithKey(key))
	if method, err := tf.Verify(context.Background(), 1, codes[0]); err != nil || method != "recovery" {
		t.Fatalf("shown recovery code does not verify: %q %v", method, err)
	}
}

func TestProfileTwoFactorSetupRequiresCSRF(t *testing.T) {
	h, _ := serverWithUser(t)
	cookie := signInProfile(t, h)
	rr := postForm(h, "/profile/two-factor/setup", url.Values{"_token": {"deadbeef"}}, cookie)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad CSRF setup POST = %d, want 400", rr.Code)
	}
}

// ---- small request helpers --------------------------------------------------

func getWith(t *testing.T, h http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func nextCookie(t *testing.T, rr *httptest.ResponseRecorder, fallback *http.Cookie) *http.Cookie {
	t.Helper()
	for i := len(rr.Result().Cookies()) - 1; i >= 0; i-- {
		if rr.Result().Cookies()[i].Name == "whacp_session" {
			return rr.Result().Cookies()[i]
		}
	}
	return fallback
}

