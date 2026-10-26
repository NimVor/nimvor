package auth

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/twofactor"
)

// A known PHP-generated argon2id hash of "correct horse battery staple".
const (
	testPassword = "correct horse battery staple"
	testHash     = "$argon2id$v=19$m=65536,t=4,p=1$a2l2TkJFMDJkS09HYTY3Ug$3jdx5kNrMWKaMSPWkDAAShBk+JlgksFdItavD55w/ts"
)

func setup(t *testing.T) (*Service, *sql.DB, *fakeClock) {
	t.Helper()
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
	if _, err := db.Exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,created_at,updated_at)
		VALUES (1,'root','root@example.test',?,1,1,'2026-01-01T00:00:00Z','','')`, testHash); err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{t: 1_700_000_000}
	repo := identity.NewRepository(db)
	thr := NewThrottle(db)
	thr.now = clk.now
	var key [32]byte
	tf := twofactor.New(db, crypto.NewWithKey(key))
	tf = tf.WithClock(clk.now)
	svc := NewService(db, repo, thr, tf, audit.New(db, slog.New(slog.DiscardHandler)))
	svc.now = clk.now
	return svc, db, clk
}

type fakeClock struct{ t int64 }

func (c *fakeClock) now() int64 { return c.t }

func newSession() *session.Session {
	return &session.Session{Values: map[string]string{}}
}

// login signs the test user in with the password and fails the test on any
// refusal, pending state or error.
func login(t *testing.T, svc *Service, sess *session.Session, ip string) {
	t.Helper()
	msg, pending, err := svc.Attempt(context.Background(), sess, "root", testPassword, ip)
	if err != nil || pending || msg != "" {
		t.Fatalf("login failed: pending=%v msg=%q err=%v", pending, msg, err)
	}
}

func TestAttemptSuccessSignsIn(t *testing.T) {
	svc, _, _ := setup(t)
	sess := newSession()
	login(t, svc, sess, "203.0.113.1")
	u, err := svc.User(context.Background(), sess)
	if err != nil {
		t.Fatal(err)
	}
	if u == nil || u.ID != 1 {
		t.Fatalf("not signed in: %+v", u)
	}
}

func TestAttemptWrongPassword(t *testing.T) {
	svc, _, _ := setup(t)
	sess := newSession()
	msg, pending, err := svc.Attempt(context.Background(), sess, "root", "nope", "203.0.113.2")
	if err != nil || pending {
		t.Fatalf("err=%v pending=%v", err, pending)
	}
	if msg != msgInvalid {
		t.Fatalf("msg = %q, want %q", msg, msgInvalid)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("must not be signed in after a wrong password")
	}
}

func TestAttemptUnknownUserSameMessage(t *testing.T) {
	svc, _, _ := setup(t)
	sess := newSession()
	msg, _, err := svc.Attempt(context.Background(), sess, "ghost", "whatever", "203.0.113.3")
	if err != nil {
		t.Fatal(err)
	}
	if msg != msgInvalid {
		t.Fatalf("unknown user msg = %q, want generic", msg)
	}
}

func TestAttemptInactiveUser(t *testing.T) {
	svc, db, _ := setup(t)
	if _, err := db.Exec(`UPDATE users SET is_active = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	sess := newSession()
	msg, _, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.4")
	if msg != msgInvalid {
		t.Fatalf("inactive msg = %q", msg)
	}
}

func TestAttemptSuspendedUser(t *testing.T) {
	svc, db, _ := setup(t)
	if _, err := db.Exec(`UPDATE users SET suspended_at = '2026-01-01T00:00:00Z' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	sess := newSession()
	msg, pending, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.5")
	if pending || msg != msgSuspended {
		t.Fatalf("suspended msg = %q pending=%v, want %q", msg, pending, msgSuspended)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("suspended user must not be signed in")
	}
}

// A TOTP-enabled account gets a pending sign-in (not signed in, not refused).
func TestAttemptTwoFactorUserBecomesPending(t *testing.T) {
	svc, _, clk := setup(t)
	enable2FA(t, svc, clk)
	sess := newSession()
	msg, pending, err := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.6")
	if err != nil || msg != "" {
		t.Fatalf("pending expected, got msg=%q err=%v", msg, err)
	}
	if !pending {
		t.Fatal("expected a pending two-factor sign-in")
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("a 2FA-enabled user must NOT be signed in by the password-only path")
	}
	if !svc.PendingTwoFactor(sess) || svc.PendingUsername(sess) != "root" {
		t.Fatal("PendingTwoFactor/PendingUsername do not reflect the pending sign-in")
	}
}

// A security-key-only account (WebAuthn not ported yet) is refused, never
// signed in.
func TestAttemptKeyOnlyUserRefused(t *testing.T) {
	svc, db, _ := setup(t)
	if _, err := db.Exec(`INSERT INTO webauthn_credentials (user_id, credential_id, public_key, name, created_at)
		VALUES (1,'cred1','key','yubikey','2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	sess := newSession()
	msg, pending, err := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.12")
	if err != nil || !pending || msg != "" {
		t.Fatalf("key-only user: msg=%q pending=%v err=%v, want a pending security-key step", msg, pending, err)
	}
	if id := svc.PendingSigninID(sess); id != 1 {
		t.Fatalf("pending sign-in id = %d, want 1", id)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("a key-only user must NOT be signed in without the key")
	}
}

func TestThrottleLocksOutAfterFailures(t *testing.T) {
	svc, _, _ := setup(t)
	ip := "203.0.113.7"
	// 5 per-user failures is the cap.
	for i := 0; i < 5; i++ {
		if _, _, err := svc.Attempt(context.Background(), newSession(), "root", "bad", ip); err != nil {
			t.Fatal(err)
		}
	}
	msg, _, _ := svc.Attempt(context.Background(), newSession(), "root", testPassword, ip)
	if msg == "" || msg == msgInvalid {
		t.Fatalf("expected throttle message, got %q", msg)
	}
}

func TestIdleTimeoutSignsOut(t *testing.T) {
	svc, _, clk := setup(t)
	sess := newSession()
	login(t, svc, sess, "203.0.113.8")
	clk.t += svc.idleTimeout + 1
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("idle timeout must sign the session out")
	}
}

func TestPasswordChangeInvalidatesSession(t *testing.T) {
	svc, db, _ := setup(t)
	sess := newSession()
	login(t, svc, sess, "203.0.113.9")
	// Change the password hash out from under the session.
	if _, err := db.Exec(`UPDATE users SET password_hash = '$2y$12$differenthashvaluethatdoesntmatterxxxxxxxxxxxxxxxxxxxxx' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("changing the password must invalidate the fingerprint")
	}
}

func TestSessionEpochBumpInvalidatesSession(t *testing.T) {
	svc, db, _ := setup(t)
	sess := newSession()
	login(t, svc, sess, "203.0.113.10")
	if _, err := db.Exec(`UPDATE users SET session_epoch = session_epoch + 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("bumping session_epoch must end the session")
	}
}

func TestLogout(t *testing.T) {
	svc, _, _ := setup(t)
	sess := newSession()
	login(t, svc, sess, "203.0.113.11")
	svc.Logout(context.Background(), sess, &identity.User{ID: 1, Username: "root"})
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("after logout the session must be empty")
	}
}

// enable2FA turns two-factor on for user 1 using a valid code at the fake
// clock; it returns the secret so tests can compute valid codes.
func enable2FA(t *testing.T, svc *Service, clk *fakeClock) string {
	t.Helper()
	secret, err := twofactor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	step := clk.t / twofactor.Period
	code, err := twofactor.Code(secret, step)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.twoFactor.Enable(context.Background(), 1, secret, code); err != nil {
		t.Fatal(err)
	}
	// Enabling consumed the current TOTP step (replay protection), so time moves
	// on before the test signs in with a fresh code.
	clk.t += twofactor.Period
	return secret
}

func TestCompleteTwoFactorSignsIn(t *testing.T) {
	svc, _, clk := setup(t)
	secret := enable2FA(t, svc, clk)
	sess := newSession()
	if _, pending, err := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.20"); err != nil || !pending {
		t.Fatalf("pending expected: %v", err)
	}
	code, err := twofactor.Code(secret, clk.t/twofactor.Period)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := svc.CompleteTwoFactor(context.Background(), sess, code, "203.0.113.20")
	if err != nil || msg != "" {
		t.Fatalf("complete failed: %q %v", msg, err)
	}
	u, err := svc.User(context.Background(), sess)
	if err != nil || u == nil || u.ID != 1 {
		t.Fatalf("not signed in after 2FA: %v %+v", err, u)
	}
	if svc.PendingTwoFactor(sess) {
		t.Fatal("pending state must be cleared after a completed sign-in")
	}
}

func TestCompleteTwoFactorBadCodeRejected(t *testing.T) {
	svc, _, clk := setup(t)
	enable2FA(t, svc, clk)
	sess := newSession()
	if _, pending, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.21"); !pending {
		t.Fatal("pending expected")
	}
	msg, err := svc.CompleteTwoFactor(context.Background(), sess, "000000", "203.0.113.21")
	if err != nil {
		t.Fatal(err)
	}
	if msg != msgTwoFactorBadCode {
		t.Fatalf("bad code msg = %q", msg)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("must not be signed in with a bad code")
	}
	if !svc.PendingTwoFactor(sess) {
		t.Fatal("pending must survive a failed attempt (tries remain)")
	}
}

func TestCompleteTwoFactorTooManyTriesCancels(t *testing.T) {
	svc, _, clk := setup(t)
	enable2FA(t, svc, clk)
	sess := newSession()
	if _, pending, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.22"); !pending {
		t.Fatal("pending expected")
	}
	// Different valid-looking codes are impossible; use the same wrong code
	// pendingTries times — the throttle also counts these, but the per-signin
	// try cap fires first.
	for i := 0; i < pendingTries; i++ {
		msg, _ := svc.CompleteTwoFactor(context.Background(), sess, "000000", "203.0.113.22")
		if i == pendingTries-1 && msg != msgTooManyTwoFactorTries {
			t.Fatalf("last attempt msg = %q, want too-many", msg)
		}
	}
	if svc.PendingTwoFactor(sess) {
		t.Fatal("pending must be cancelled after too many failed tries")
	}
}

func TestCompleteTwoFactorExpiredAfterTTL(t *testing.T) {
	svc, _, clk := setup(t)
	enable2FA(t, svc, clk)
	sess := newSession()
	if _, pending, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.23"); !pending {
		t.Fatal("pending expected")
	}
	clk.t += pendingTTL + 1
	msg, err := svc.CompleteTwoFactor(context.Background(), sess, "123456", "203.0.113.23")
	if err != nil {
		t.Fatal(err)
	}
	if msg != msgSignInExpired {
		t.Fatalf("expired msg = %q", msg)
	}
	if svc.PendingTwoFactor(sess) {
		t.Fatal("pending must be gone after the TTL")
	}
}

func TestCompleteTwoFactorInvalidatedByPasswordChange(t *testing.T) {
	svc, db, clk := setup(t)
	enable2FA(t, svc, clk)
	sess := newSession()
	if _, pending, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.24"); !pending {
		t.Fatal("pending expected")
	}
	// A password change out from under the pending sign-in changes the
	// fingerprint: the pending state must die, not be completed.
	if _, err := db.Exec(`UPDATE users SET password_hash = '$2y$12$differenthashvaluethatdoesntmatterxxxxxxxxxxxxxxxxxxxxx' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	msg, err := svc.CompleteTwoFactor(context.Background(), sess, "123456", "203.0.113.24")
	if err != nil {
		t.Fatal(err)
	}
	if msg != msgSignInExpired {
		t.Fatalf("invalidated pending msg = %q, want expired", msg)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("must not be signed in through an invalidated pending sign-in")
	}
}

func TestCompleteTwoFactorRecoveryCode(t *testing.T) {
	svc, _, clk := setup(t)
	enable2FA(t, svc, clk)
	sess := newSession()
	if _, pending, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.25"); !pending {
		t.Fatal("pending expected")
	}
	codes, err := svc.twoFactor.RegenerateRecovery(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	msg, err := svc.CompleteTwoFactor(context.Background(), sess, codes[0], "203.0.113.25")
	if err != nil || msg != "" {
		t.Fatalf("recovery code sign-in failed: %q %v", msg, err)
	}
	if u, _ := svc.User(context.Background(), sess); u == nil {
		t.Fatal("not signed in after a recovery code")
	}
}

func TestCancelTwoFactor(t *testing.T) {
	svc, _, clk := setup(t)
	enable2FA(t, svc, clk)
	sess := newSession()
	if _, pending, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.26"); !pending {
		t.Fatal("pending expected")
	}
	svc.CancelTwoFactor(sess)
	if svc.PendingTwoFactor(sess) {
		t.Fatal("cancel must clear the pending sign-in")
	}
}
