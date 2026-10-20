package http

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// A PHP-generated argon2id hash of "correct horse battery staple".
const (
	loginPassword = "correct horse battery staple"
	loginHash     = "$argon2id$v=19$m=65536,t=4,p=1$a2l2TkJFMDJkS09HYTY3Ug$3jdx5kNrMWKaMSPWkDAAShBk+JlgksFdItavD55w/ts"
)

func serverWithUser(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (1,'administrator','Administrator','all','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (1,'root','root@example.test','Root User',?,1,1,'2026-01-01T00:00:00Z','','')`, loginHash); err != nil {
		t.Fatal(err)
	}
	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), db
}

var csrfRe = regexp.MustCompile(`name="_token" value="([0-9a-f]{64})"`)

// getLogin returns the session cookie and the CSRF token from the form.
func getLogin(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/login", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /login = %d", rr.Code)
	}
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token in login form:\n%s", rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("GET /login set no session cookie")
	}
	return cookies[0], m[1]
}

func postForm(h http.Handler, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "203.0.113.50:40000"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestLoginFlowEndToEnd(t *testing.T) {
	h, _ := serverWithUser(t)
	cookie, token := getLogin(t, h)

	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"root"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("login POST = %d loc=%q, want 303 /", rr.Code, rr.Header().Get("Location"))
	}
	authed := rr.Result().Cookies()
	if len(authed) == 0 {
		t.Fatal("successful login set no new session cookie")
	}

	// Follow to the dashboard with the authenticated cookie.
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(authed[0])
	dash := httptest.NewRecorder()
	h.ServeHTTP(dash, req)
	if dash.Code != http.StatusOK {
		t.Fatalf("dashboard = %d, want 200", dash.Code)
	}
	if !strings.Contains(dash.Body.String(), "Root User") {
		t.Fatalf("dashboard does not show the signed-in user:\n%s", dash.Body.String())
	}
}

func TestDashboardRequiresAuth(t *testing.T) {
	h, _ := serverWithUser(t)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("anon dashboard = %d loc=%q, want redirect to /login", rr.Code, rr.Header().Get("Location"))
	}
}

func TestLoginRejectsBadCSRF(t *testing.T) {
	h, _ := serverWithUser(t)
	cookie, _ := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {"deadbeef"}, "username": {"root"}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bad-CSRF login = %d, want 400", rr.Code)
	}
	// And it must not have signed anyone in: no auth cookie that lands us on /.
	for _, c := range rr.Result().Cookies() {
		req := httptest.NewRequest("GET", "/", nil)
		req.AddCookie(c)
		d := httptest.NewRecorder()
		h.ServeHTTP(d, req)
		if d.Code == http.StatusOK {
			t.Fatal("a CSRF-rejected login produced a usable session")
		}
	}
}

func TestLoginWrongPasswordShowsError(t *testing.T) {
	h, _ := serverWithUser(t)
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"root"}, "password": {"wrong"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("wrong-password = %d loc=%q, want 303 /login", rr.Code, rr.Header().Get("Location"))
	}
	// Follow the redirect with the flash cookie: the error must render.
	flash := rr.Result().Cookies()
	req := httptest.NewRequest("GET", "/login", nil)
	if len(flash) > 0 {
		req.AddCookie(flash[0])
	}
	d := httptest.NewRecorder()
	h.ServeHTTP(d, req)
	if !strings.Contains(d.Body.String(), "Invalid username or password") {
		t.Fatalf("login page did not show the error flash:\n%s", d.Body.String())
	}
}

func TestLogoutEndsSession(t *testing.T) {
	h, _ := serverWithUser(t)
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {"root"}, "password": {loginPassword}}, cookie)
	authed := rr.Result().Cookies()[0]

	// Dashboard renders a logout form with a fresh token.
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(authed)
	dash := httptest.NewRecorder()
	h.ServeHTTP(dash, req)
	m := csrfRe.FindStringSubmatch(dash.Body.String())
	if m == nil {
		t.Fatal("no logout CSRF token on the dashboard")
	}
	// Use any session cookie the dashboard refreshed, else the login one.
	logoutCookie := authed
	if cs := dash.Result().Cookies(); len(cs) > 0 {
		logoutCookie = cs[0]
	}
	out := postForm(h, "/logout", url.Values{"_token": {m[1]}}, logoutCookie)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/login" {
		t.Fatalf("logout = %d loc=%q", out.Code, out.Header().Get("Location"))
	}
	// The cleared cookie must no longer reach the dashboard.
	cleared := out.Result().Cookies()
	req2 := httptest.NewRequest("GET", "/", nil)
	if len(cleared) > 0 {
		req2.AddCookie(cleared[0])
	}
	d2 := httptest.NewRecorder()
	h.ServeHTTP(d2, req2)
	if d2.Code == http.StatusOK {
		t.Fatal("session still valid after logout")
	}
}
