package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// Session keys. These live only in the Go panel's sealed cookie (not shared
// with the PHP file sessions), so the names need only be stable here. The
// fingerprint value, however, uses the same formula as AuthService.php so it
// binds to the account's password hash and session epoch identically.
const (
	keyUserID   = "_auth_user_id"
	keyFP       = "_auth_fp"
	keyLoginAt  = "_auth_login_at"
	keyActivity = "_auth_last_activity"
)

// dummyHash is a real argon2id hash of a random, discarded value (same cost as
// user hashes), verified against when the user does not exist so that login
// timing does not reveal which usernames are valid. Copied from
// AuthService::DUMMY_HASH.
const dummyHash = "$argon2id$v=19$m=65536,t=4,p=1$dVkvM1B6dzNvTFI1bW40Rg$4ObwPqsBPsJMDj4LO19/WrcHGZflXqksE8+8FUXeSAc"

// User-facing messages, matching AuthService.php wording.
const (
	msgInvalid   = "Invalid username or password."
	msgSuspended = "This account is suspended. Please contact your hosting provider."
	// msgSecondFactorUnavailable is shown when a user who has a second factor
	// configured tries to sign in through the not-yet-ported Go panel. It must
	// never silently sign them in without the factor.
	msgSecondFactorUnavailable = "Two-factor sign-in is required for this account and is not yet available here. Use the existing panel to sign in."
)

// Service is the session authentication service. It is stateless across
// requests: every method takes the per-request *session.Session.
type Service struct {
	db              *sql.DB
	users           *identity.Repository
	throttle        *Throttle
	idleTimeout     int64
	absoluteTimeout int64
	now             func() int64
}

// NewService builds the auth service with the PHP defaults (30-minute idle,
// 12-hour absolute timeout).
func NewService(db *sql.DB, users *identity.Repository, throttle *Throttle) *Service {
	return &Service{db: db, users: users, throttle: throttle, idleTimeout: 1800, absoluteTimeout: 43200, now: unixNow}
}

// Attempt verifies a username/password and, on success, signs the session in.
// It returns a user-facing error message, or "" on success. Mirrors
// AuthService::attempt for the password path.
func (s *Service) Attempt(ctx context.Context, sess *session.Session, username, password, ip string) (string, error) {
	username = trimSpace(username)

	retry, err := s.throttle.RetryAfter(ctx, username, ip)
	if err != nil {
		return "", err
	}
	if retry > 0 {
		mins := (retry + 59) / 60
		return "Too many failed attempts. Try again in " + strconv.FormatInt(mins, 10) + " minute(s).", nil
	}

	var row *identity.AuthRow
	if username != "" {
		row, err = s.users.FindForAuth(ctx, username)
		if err != nil && err != identity.ErrNotFound {
			return "", err
		}
	}
	hash := dummyHash
	if row != nil {
		hash = row.PasswordHash
	}
	valid := identity.VerifyPassword(hash, password)

	// Correct password on a suspended-but-active account: say why, without
	// counting a failure.
	if row != nil && valid && row.IsActive && row.Suspended {
		return msgSuspended, nil
	}
	if row == nil || !valid || !row.IsActive {
		if err := s.throttle.RecordFailure(ctx, username, ip); err != nil {
			return "", err
		}
		return msgInvalid, nil
	}

	// Password is correct. If the account has a second factor configured, the
	// Go panel cannot complete the sign-in yet — refuse rather than bypass it.
	has2fa, err := s.hasSecondFactor(ctx, row.ID)
	if err != nil {
		return "", err
	}
	if has2fa {
		return msgSecondFactorUnavailable, nil
	}

	// Opportunistically upgrade a legacy/out-of-parameter hash.
	if identity.NeedsRehash(hash) {
		if nh, herr := identity.HashPassword(password); herr == nil {
			if uerr := s.users.UpdatePassword(ctx, row.ID, nh); uerr == nil {
				hash = nh
			}
		}
	}

	if err := s.finishLogin(ctx, sess, row.ID, row.Username, hash, ip); err != nil {
		return "", err
	}
	return "", nil
}

// finishLogin records the success and writes the signed-in session.
func (s *Service) finishLogin(ctx context.Context, sess *session.Session, id int64, username, hash, ip string) error {
	if err := s.throttle.RecordSuccess(ctx, username, ip); err != nil {
		return err
	}
	if err := s.users.RecordLogin(ctx, id, ip); err != nil {
		return err
	}
	fp, err := s.fingerprint(ctx, hash, id)
	if err != nil {
		return err
	}
	sess.Regenerate()
	now := s.now()
	sess.Set(keyUserID, strconv.FormatInt(id, 10))
	sess.Set(keyFP, fp)
	sess.Set(keyLoginAt, strconv.FormatInt(now, 10))
	sess.Set(keyActivity, strconv.FormatInt(now, 10))
	return nil
}

// User returns the signed-in user, or nil. It enforces the idle and absolute
// timeouts and invalidates the session when the password hash or session epoch
// changed (the fingerprint no longer matches). Mirrors AuthService::user.
func (s *Service) User(ctx context.Context, sess *session.Session) (*identity.User, error) {
	idStr := sess.Get(keyUserID)
	if idStr == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		sess.Invalidate()
		return nil, nil
	}
	now := s.now()
	loginAt := atoi64(sess.Get(keyLoginAt))
	activity := atoi64(sess.Get(keyActivity))
	if now-activity > s.idleTimeout || now-loginAt > s.absoluteTimeout {
		sess.Invalidate()
		return nil, nil
	}
	user, err := s.users.Find(ctx, id)
	if err != nil && err != identity.ErrNotFound {
		return nil, err
	}
	hash, err := s.users.PasswordHash(ctx, id)
	if err != nil {
		return nil, err
	}
	if hash == "" || !identity.CanAct(user) {
		sess.Invalidate()
		return nil, nil
	}
	want, err := s.fingerprint(ctx, hash, id)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(sess.Get(keyFP))) != 1 {
		sess.Invalidate()
		return nil, nil
	}
	sess.Set(keyActivity, strconv.FormatInt(now, 10))
	return user, nil
}

// Logout clears the session.
func (s *Service) Logout(sess *session.Session) { sess.Invalidate() }

// fingerprint binds the session to the account's password hash and session
// epoch, so either change ends the session. Identical formula to
// AuthService::fingerprint.
func (s *Service) fingerprint(ctx context.Context, hash string, id int64) (string, error) {
	epoch, err := s.users.SessionEpoch(ctx, id)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("whacp-session|" + hash + "|" + strconv.FormatInt(epoch, 10)))
	return hex.EncodeToString(sum[:]), nil
}

// hasSecondFactor reports whether the user has TOTP or a security key
// registered (user_two_factor or webauthn_credentials). Used to refuse a
// password-only sign-in until the second-factor flow is ported.
func (s *Service) hasSecondFactor(ctx context.Context, id int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM user_two_factor WHERE user_id = ?)
		     + (SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?)`, id, id).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func atoi64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

func trimSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
