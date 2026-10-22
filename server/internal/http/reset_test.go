package http

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/auth"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// resetMail records what the reset service asked it to send.
type resetMail struct {
	configured bool
	err        error
	sent       int
	to         []string
	subject    string
	body       string
}

func (f *resetMail) Configured(ctx context.Context) (bool, error) { return f.configured, nil }
func (f *resetMail) Send(ctx context.Context, to []string, subject, text string) error {
	f.sent++
	f.to, f.subject, f.body = to, subject, text
	return f.err
}

// resetTestServer builds a server with the test user and a reset service
// wired to a fake mailer. panelURL empty disables the feature (no public URL).
func resetTestServer(t *testing.T, mail *resetMail, panelURL string) (*Server, *sql.DB) {
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
	if panelURL != "" {
		if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('panel_url', ?, '')`, `"`+panelURL+`"`); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(testConfig(t), db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	st := settings.NewRepository(db)
	s.resets = auth.NewResetService(db, identity.NewRepository(db), mail, st, security.NewPolicy(st), s.audit, s.log)
	return s, db
}

var resetTokenRe = regexp.MustCompile(`/password/reset/([0-9a-f]{64})`)

func TestForgotPasswordFlow(t *testing.T) {
	mail := &resetMail{configured: true}
	s, _ := resetTestServer(t, mail, "https://panel.example.test")
	h := s.Handler()

	// 1. The form renders with a CSRF token.
	rr := getWith(t, h, "/password/forgot", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /password/forgot = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `action="/password/forgot"`) {
		t.Fatalf("forgot form missing:\n%s", rr.Body.String())
	}
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token in forgot form")
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("GET /password/forgot set no session cookie")
	}
	cookie := cookies[0]

	// 2. Bad CSRF is rejected.
	rr = postForm(h, "/password/forgot", url.Values{"_token": {"not-a-token"}, "identifier": {"root"}}, cookie)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST with bad CSRF = %d, want 400", rr.Code)
	}

	// 3. A valid request emails the link and redirects to /login.
	rr = postForm(h, "/password/forgot", url.Values{"_token": {m[1]}, "identifier": {"root"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("POST forgot = %d loc %q, want 303 /login", rr.Code, rr.Header().Get("Location"))
	}
	if mail.sent != 1 || len(mail.to) != 1 || mail.to[0] != "root@example.test" {
		t.Fatalf("mail = sent %d to %v", mail.sent, mail.to)
	}
	tokenMatch := resetTokenRe.FindStringSubmatch(mail.body)
	if tokenMatch == nil {
		t.Fatalf("no reset link in the email body:\n%s", mail.body)
	}
	token := tokenMatch[1]
	if !strings.Contains(mail.body, "https://panel.example.test/password/reset/"+token) {
		t.Fatalf("link not built from the configured panel URL:\n%s", mail.body)
	}
	cookie = nextCookie(t, rr, cookie)

	// 4. A malformed token is a 404.
	rr = getWith(t, h, "/password/reset/nope", cookie)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET bad token = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "expired or was already used") {
		t.Fatalf("bad-token page missing the explanation:\n%s", rr.Body.String())
	}

	// 5. A valid token renders the new-password form.
	rr = getWith(t, h, "/password/reset/"+token, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET valid token = %d, want 200:\n%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `action="/password/reset/`+token+`"`) {
		t.Fatalf("reset form missing:\n%s", rr.Body.String())
	}
	m2 := csrfRe.FindStringSubmatch(rr.Body.String())
	if m2 == nil {
		t.Fatalf("no CSRF token in reset form")
	}
	cookie = nextCookie(t, rr, cookie)

	// 6. A mismatched confirmation redirects back with the error flashed.
	rr = postForm(h, "/password/reset/"+token, url.Values{
		"_token": {m2[1]}, "password": {goodPasswordHTTP}, "password_confirmation": {goodPasswordHTTP + "x"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/password/reset/"+token {
		t.Fatalf("POST mismatch = %d loc %q, want 303 back to the form", rr.Code, rr.Header().Get("Location"))
	}
	cookie = nextCookie(t, rr, cookie)
	rr = getWith(t, h, "/password/reset/"+token, cookie)
	if !strings.Contains(rr.Body.String(), "The passwords do not match.") {
		t.Fatalf("flashed error not shown:\n%s", rr.Body.String())
	}
	cookie = nextCookie(t, rr, cookie)

	// 7. A valid reset redirects to /login.
	rr = postForm(h, "/password/reset/"+token, url.Values{
		"_token": {m2[1]}, "password": {goodPasswordHTTP}, "password_confirmation": {goodPasswordHTTP},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("POST reset = %d loc %q, want 303 /login", rr.Code, rr.Header().Get("Location"))
	}

	// 8. The new password signs in (the old one is gone).
	cookie2, csrf2 := getLogin(t, h)
	rr = postForm(h, "/login", url.Values{"_token": {csrf2}, "username": {"root"}, "password": {goodPasswordHTTP}}, cookie2)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("login with new password = %d loc %q, want 303 /", rr.Code, rr.Header().Get("Location"))
	}
}

// goodPasswordHTTP passes the default policy and contains no username.
const goodPasswordHTTP = "Abcdefghijkl1234!"

func TestForgotPasswordUnavailable(t *testing.T) {
	s, _ := resetTestServer(t, &resetMail{configured: false}, "")
	h := s.Handler()
	rr := getWith(t, h, "/password/forgot", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /password/forgot = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Password reset by email is not set up on this panel") {
		t.Fatalf("unavailable notice missing:\n%s", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), `action="/password/forgot"`) {
		t.Fatalf("form rendered although the feature is unavailable:\n%s", rr.Body.String())
	}
}
