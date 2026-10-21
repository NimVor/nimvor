package auth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/mchkhawarshad/whacp/server/internal/audit"
	"github.com/mchkhawarshad/whacp/server/internal/database"
	"github.com/mchkhawarshad/whacp/server/internal/identity"
	"github.com/mchkhawarshad/whacp/server/internal/security"
	"github.com/mchkhawarshad/whacp/server/internal/settings"
)

// fakeMail records what ResetService asked it to send.
type fakeMail struct {
	configured bool
	err        error
	sent       int
	to         []string
	subject    string
	body       string
}

func (f *fakeMail) Configured(ctx context.Context) (bool, error) { return f.configured, nil }
func (f *fakeMail) Send(ctx context.Context, to []string, subject, text string) error {
	f.sent++
	f.to, f.subject, f.body = to, subject, text
	return f.err
}

var rsClk = int64(1_700_000_000)

func rsNow() int64 { return rsClk }

// goodPassword passes the default policy (strength 100, 17 distinct mixed
// runes) and contains no test username.
const goodPassword = "Abcdefghijkl1234!"

// setupResetDB migrates a fresh in-memory DB with an active user (id 1) and a
// suspended one (id 2), and returns the reset service wired to a fake mailer.
func setupResetDB(t *testing.T, mail *fakeMail) (*ResetService, *sql.DB) {
	t.Helper()
	rsClk = 1_700_000_000
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES (1,'administrator','Admin','all','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,suspended_at,created_at,updated_at)
		VALUES (1,'root','root@example.test','HASH',1,1,'2026-01-01T00:00:00Z',NULL,'','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,suspended_at,created_at,updated_at)
		VALUES (2,'billy','billy@example.test','HASH',1,1,'2026-01-01T00:00:00Z','2026-01-02T00:00:00Z','','')`); err != nil {
		t.Fatal(err)
	}
	st := settings.NewRepository(db)
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('panel_url', ?, '')`, `"https://panel.example.test"`); err != nil {
		t.Fatal(err)
	}
	svc := NewResetService(
		db, identity.NewRepository(db), mail, st,
		security.NewPolicy(st), audit.New(db, slog.New(slog.DiscardHandler)),
		slog.New(slog.DiscardHandler),
	).WithClock(rsNow)
	return svc, db
}

// auditRows returns the audit rows for one action as "result details".
func auditRows(t *testing.T, db *sql.DB, action string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT result, details FROM audit_logs WHERE action = ? ORDER BY id`, action)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var result, details string
		if err := rows.Scan(&result, &details); err != nil {
			t.Fatal(err)
		}
		out = append(out, result+" "+details)
	}
	return out
}

// countReason counts audit rows whose details carry the given reason.
func countReason(rows []string, reason string) int {
	n := 0
	for _, r := range rows {
		if strings.Contains(r, `"reason":"`+reason+`"`) {
			n++
		}
	}
	return n
}

func resetTokensFor(t *testing.T, db *sql.DB, userID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM password_resets WHERE user_id = ? AND token_hash IS NOT NULL AND used_at IS NULL`, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

var tokenRe = regexp.MustCompile(`[0-9a-f]{64}`)

func TestResetAvailable(t *testing.T) {
	ctx := context.Background()
	svc1, _ := setupResetDB(t, &fakeMail{configured: false})
	if ok, err := svc1.Available(ctx); err != nil || ok {
		t.Fatalf("Available(mailer off) = %v, %v; want false", ok, err)
	}
	svc2, db2 := setupResetDB(t, &fakeMail{configured: true})
	if ok, err := svc2.Available(ctx); err != nil || !ok {
		t.Fatalf("Available = %v, %v; want true", ok, err)
	}
	// A missing panel URL disables it too (links would otherwise point at
	// the request host, which the service must never do).
	if _, err := db2.Exec(`DELETE FROM settings WHERE key = 'panel_url'`); err != nil {
		t.Fatal(err)
	}
	if ok, err := svc2.Available(ctx); err != nil || ok {
		t.Fatalf("Available(no panel_url) = %v, %v; want false", ok, err)
	}
}

func TestRequestUnknownUserSendsNothing(t *testing.T) {
	mail := &fakeMail{configured: true}
	svc, db := setupResetDB(t, mail)
	ctx := context.Background()
	if err := svc.Request(ctx, "ghost", "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	if mail.sent != 0 {
		t.Fatalf("mail sent to unknown user: %+v", mail)
	}
	if n := resetTokensFor(t, db, 1); n != 0 {
		t.Fatalf("tokens created for a known user: %d", n)
	}
	rows := auditRows(t, db, "auth.password_reset_request")
	if len(rows) != 1 || rows[0] != `failure {"reason":"unknown_user"}` {
		t.Fatalf("audit = %v", rows)
	}
}

func TestRequestSuspendedUserSendsNothing(t *testing.T) {
	mail := &fakeMail{configured: true}
	svc, db := setupResetDB(t, mail)
	if err := svc.Request(context.Background(), "billy", "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	if mail.sent != 0 {
		t.Fatal("mail sent to a suspended user")
	}
	rows := auditRows(t, db, "auth.password_reset_request")
	if len(rows) != 1 || rows[0] != `failure {"reason":"inactive"}` {
		t.Fatalf("audit = %v", rows)
	}
}

func TestRequestEmailsLink(t *testing.T) {
	mail := &fakeMail{configured: true}
	svc, db := setupResetDB(t, mail)
	ctx := context.Background()
	if err := svc.Request(ctx, "root", "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	if mail.sent != 1 || len(mail.to) != 1 || mail.to[0] != "root@example.test" {
		t.Fatalf("mail = %+v", mail)
	}
	if mail.subject != "WhaCP: reset your password" {
		t.Fatalf("subject = %q", mail.subject)
	}
	m := tokenRe.FindString(mail.body)
	if m == "" || !strings.Contains(mail.body, "https://panel.example.test/password/reset/"+m) {
		t.Fatalf("body missing the link:\n%s", mail.body)
	}
	if !strings.Contains(mail.body, "The request came from 10.0.0.9.") {
		t.Fatalf("body missing the IP line:\n%s", mail.body)
	}
	user, err := svc.UserFor(ctx, m)
	if err != nil || user == nil || user.ID != 1 {
		t.Fatalf("UserFor = %v, %v", user, err)
	}
	rows := auditRows(t, db, "auth.password_reset_request")
	if len(rows) != 1 || rows[0] != "success {}" {
		t.Fatalf("audit = %v", rows)
	}
}

// TestRequestThrottlesUserThenIP walks one account past both limits:
// 3 requests/hour per account, 10/hour per IP.
func TestRequestThrottlesUserThenIP(t *testing.T) {
	mail := &fakeMail{configured: true}
	svc, db := setupResetDB(t, mail)
	ctx := context.Background()
	ip := "10.0.0.9"
	for i := 0; i < 11; i++ {
		if err := svc.Request(ctx, "root", ip); err != nil {
			t.Fatal(err)
		}
	}
	// Requests 1–3 sent mail; 4–10 hit the per-user limit; 11+ the IP limit.
	if mail.sent != 3 {
		t.Fatalf("mail sent = %d, want 3", mail.sent)
	}
	rows := auditRows(t, db, "auth.password_reset_request")
	if n := countReason(rows, "throttled_user"); n != 7 {
		t.Errorf("throttled_user = %d, want 7 (requests 4..10): %v", n, rows)
	}
	if n := countReason(rows, "throttled_ip"); n != 1 {
		t.Errorf("throttled_ip = %d, want 1 (request 11): %v", n, rows)
	}
	// The IP counter also counts unknown accounts.
	if err := svc.Request(ctx, "nobody-here", ip); err != nil {
		t.Fatal(err)
	}
	rows = auditRows(t, db, "auth.password_reset_request")
	last := rows[len(rows)-1]
	if !strings.Contains(last, "throttled_ip") {
		t.Errorf("unknown-account request on a throttled IP = %q, want throttled_ip", last)
	}
}

func TestRequestTTLExpires(t *testing.T) {
	mail := &fakeMail{configured: true}
	svc, _ := setupResetDB(t, mail)
	ctx := context.Background()
	if err := svc.Request(ctx, "root", "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	token := tokenRe.FindString(mail.body)
	rsClk += resetTTL - 1
	if u, err := svc.UserFor(ctx, token); err != nil || u == nil {
		t.Fatalf("still valid inside the TTL: %v, %v", u, err)
	}
	rsClk += 2 // now past the TTL
	if u, err := svc.UserFor(ctx, token); err != nil || u != nil {
		t.Fatalf("expired token accepted: %v, %v", u, err)
	}
}

func TestRequestMailFailureIsSwallowed(t *testing.T) {
	mail := &fakeMail{configured: true, err: errors.New("smtp down")}
	svc, db := setupResetDB(t, mail)
	if err := svc.Request(context.Background(), "root", "10.0.0.9"); err != nil {
		t.Fatalf("mail failure must not surface: %v", err)
	}
	rows := auditRows(t, db, "auth.password_reset_request")
	if len(rows) != 1 || rows[0] != `failure {"reason":"mail_failed"}` {
		t.Fatalf("audit = %v", rows)
	}
}

func TestResetAppliesNewPasswordAndKillsAllTokens(t *testing.T) {
	mail := &fakeMail{configured: true}
	svc, db := setupResetDB(t, mail)
	ctx := context.Background()
	// Two live tokens for the same account.
	for i := 0; i < 2; i++ {
		if err := svc.Request(ctx, "root", "10.0.0.9"); err != nil {
			t.Fatal(err)
		}
	}
	if n := resetTokensFor(t, db, 1); n != 2 {
		t.Fatalf("live tokens = %d, want 2", n)
	}
	tokenA := tokenRe.FindString(mail.body) // the newest (second) email
	if fe, err := svc.Reset(ctx, tokenA, goodPassword, goodPassword); err != nil {
		t.Fatal(err)
	} else if len(fe) != 0 {
		t.Fatalf("Reset = %v", fe)
	}
	if u, _ := svc.UserFor(ctx, tokenA); u != nil {
		t.Fatal("used token still valid")
	}
	if n := resetTokensFor(t, db, 1); n != 0 {
		t.Fatalf("tokens still live after use: %d", n)
	}
	// The new password verifies; the old one does not.
	repo := identity.NewRepository(db)
	hash, err := repo.PasswordHash(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !identity.VerifyPassword(hash, goodPassword) {
		t.Fatal("new password does not verify")
	}
	if identity.VerifyPassword(hash, "oldpassword12345") {
		t.Fatal("old password still verifies")
	}
	rows := auditRows(t, db, "user.password_reset")
	if len(rows) != 1 || rows[0] != `success {"via":"email"}` {
		t.Fatalf("audit = %v", rows)
	}
}

func TestResetFieldErrors(t *testing.T) {
	mail := &fakeMail{configured: true}
	svc, db := setupResetDB(t, mail)
	ctx := context.Background()
	if err := svc.Request(ctx, "root", "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	token := tokenRe.FindString(mail.body)

	// Malformed token: rejected without touching the database.
	if fe, err := svc.Reset(ctx, "nope", goodPassword, goodPassword); err != nil {
		t.Fatal(err)
	} else if fe["token"] == "" {
		t.Fatalf("malformed token accepted: %v", fe)
	}

	// Weak password (strength 62 < 65 under the default policy).
	weak := "The password is too weak (strength 62, at least 65 is required). Make it longer or mix upper and lower case, digits and symbols."
	if fe, err := svc.Reset(ctx, token, "abcdefghij1234", "abcdefghij1234"); err != nil {
		t.Fatal(err)
	} else if fe["password"] != weak {
		t.Fatalf("weak password = %v", fe)
	}

	// Username in password (base rule).
	if fe, err := svc.Reset(ctx, token, "SuperRoot123!secret", "SuperRoot123!secret"); err != nil {
		t.Fatal(err)
	} else if fe["password"] != "The password must not contain the username." {
		t.Fatalf("username-in-password = %v", fe)
	}

	// Mismatched confirmation.
	if fe, err := svc.Reset(ctx, token, goodPassword, goodPassword+"x"); err != nil {
		t.Fatal(err)
	} else if fe["password_confirmation"] != "The passwords do not match." {
		t.Fatalf("mismatch = %v", fe)
	}

	// The token is still usable after rejected attempts.
	if u, err := svc.UserFor(ctx, token); err != nil || u == nil {
		t.Fatalf("token invalidated by rejected attempts: %v, %v", u, err)
	}
	// And the password was never changed.
	hash, _ := identity.NewRepository(db).PasswordHash(ctx, 1)
	if hash != "HASH" {
		t.Fatal("password changed by a rejected reset")
	}
}
