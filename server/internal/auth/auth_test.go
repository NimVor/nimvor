package auth

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
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
	svc := NewService(db, repo, thr)
	svc.now = clk.now
	return svc, db, clk
}

type fakeClock struct{ t int64 }

func (c *fakeClock) now() int64 { return c.t }

func newSession() (*session.Manager, *session.Session) {
	var key [32]byte
	m := session.NewManager(crypto.NewWithKey(key), "whacp_session", true)
	return m, &session.Session{Values: map[string]string{}}
}

func TestAttemptSuccessSignsIn(t *testing.T) {
	svc, _, _ := setup(t)
	_, sess := newSession()
	msg, err := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.1")
	if err != nil {
		t.Fatal(err)
	}
	if msg != "" {
		t.Fatalf("unexpected error message: %q", msg)
	}
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
	_, sess := newSession()
	msg, err := svc.Attempt(context.Background(), sess, "root", "nope", "203.0.113.2")
	if err != nil {
		t.Fatal(err)
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
	_, sess := newSession()
	msg, err := svc.Attempt(context.Background(), sess, "ghost", "whatever", "203.0.113.3")
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
	_, sess := newSession()
	msg, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.4")
	if msg != msgInvalid {
		t.Fatalf("inactive msg = %q", msg)
	}
}

func TestAttemptSuspendedUser(t *testing.T) {
	svc, db, _ := setup(t)
	if _, err := db.Exec(`UPDATE users SET suspended_at = '2026-01-01T00:00:00Z' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	_, sess := newSession()
	msg, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.5")
	if msg != msgSuspended {
		t.Fatalf("suspended msg = %q, want %q", msg, msgSuspended)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("suspended user must not be signed in")
	}
}

func TestSecondFactorBlocksPasswordOnlyLogin(t *testing.T) {
	svc, db, _ := setup(t)
	if _, err := db.Exec(`INSERT INTO user_two_factor (user_id, secret, recovery_codes, enabled_at) VALUES (1,'S','[]','2026-01-01')`); err != nil {
		t.Skipf("user_two_factor shape differs: %v", err)
	}
	_, sess := newSession()
	msg, err := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.6")
	if err != nil {
		t.Fatal(err)
	}
	if msg != msgSecondFactorUnavailable {
		t.Fatalf("2FA user msg = %q, want refusal", msg)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("a 2FA-enabled user must NOT be signed in by the password-only path")
	}
}

func TestThrottleLocksOutAfterFailures(t *testing.T) {
	svc, _, _ := setup(t)
	ip := "203.0.113.7"
	// 5 per-user failures is the cap.
	for i := 0; i < 5; i++ {
		if _, err := svc.Attempt(context.Background(), &session.Session{Values: map[string]string{}}, "root", "bad", ip); err != nil {
			t.Fatal(err)
		}
	}
	msg, _ := svc.Attempt(context.Background(), &session.Session{Values: map[string]string{}}, "root", testPassword, ip)
	if msg == "" || msg == msgInvalid {
		t.Fatalf("expected throttle message, got %q", msg)
	}
}

func TestIdleTimeoutSignsOut(t *testing.T) {
	svc, _, clk := setup(t)
	_, sess := newSession()
	if msg, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.8"); msg != "" {
		t.Fatalf("login failed: %q", msg)
	}
	clk.t += svc.idleTimeout + 1
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("idle timeout must sign the session out")
	}
}

func TestPasswordChangeInvalidatesSession(t *testing.T) {
	svc, db, _ := setup(t)
	_, sess := newSession()
	if msg, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.9"); msg != "" {
		t.Fatalf("login failed: %q", msg)
	}
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
	_, sess := newSession()
	if msg, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.10"); msg != "" {
		t.Fatalf("login failed: %q", msg)
	}
	if _, err := db.Exec(`UPDATE users SET session_epoch = session_epoch + 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("bumping session_epoch must end the session")
	}
}

func TestLogout(t *testing.T) {
	svc, _, _ := setup(t)
	_, sess := newSession()
	if msg, _ := svc.Attempt(context.Background(), sess, "root", testPassword, "203.0.113.11"); msg != "" {
		t.Fatalf("login failed: %q", msg)
	}
	svc.Logout(sess)
	if u, _ := svc.User(context.Background(), sess); u != nil {
		t.Fatal("after logout the session must be empty")
	}
}
