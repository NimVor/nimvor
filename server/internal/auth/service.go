package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"strconv"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/twofactor"
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

	key2FAID     = "_auth_2fa_id"
	key2FAUser   = "_auth_2fa_username"
	key2FAFP     = "_auth_2fa_fp"
	key2FAAt     = "_auth_2fa_at"
	key2FATries  = "_auth_2fa_tries"
	pendingTTL   = 300 // seconds allowed between the password and the second factor
	pendingTries = 5

	// keyAuthConfirmedAt is set on the session by ConfirmPassword; the
	// confirmed window lasts ConfirmTTL seconds (mirrors CONFIRMED_KEY
	// and CONFIRM_TTL in the PHP AuthService).
	keyAuthConfirmedAt = "_auth_confirmed_at"
	ConfirmTTL         = 15 * 60
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
	// msgSecondFactorUnavailable is shown when a user whose second factor is a
	// security key (not yet ported to the Go panel) tries to sign in. It must
	// never silently sign them in without the factor.
	msgSecondFactorUnavailable = "Two-factor sign-in is required for this account and is not yet available here. Use the existing panel to sign in."
	msgSignInExpired           = "Your sign-in expired. Enter your password again."
	msgTwoFactorBadCode        = "That code is not valid. Check the time on your phone and try again."
	msgTooManyTwoFactorTries   = "Too many failed attempts. Enter your password again."
)

// Service is the session authentication service. It is stateless across
// requests: every method takes the per-request *session.Session.
type Service struct {
	db              *sql.DB
	users           *identity.Repository
	throttle        *Throttle
	twoFactor       *twofactor.Service
	audit           *audit.Logger
	idleTimeout     int64
	absoluteTimeout int64
	now             func() int64
}

// NewService builds the auth service with the PHP defaults (30-minute idle,
// 12-hour absolute timeout).
func NewService(db *sql.DB, users *identity.Repository, throttle *Throttle, twoFactor *twofactor.Service, auditLog *audit.Logger) *Service {
	return &Service{db: db, users: users, throttle: throttle, twoFactor: twoFactor, audit: auditLog, idleTimeout: 1800, absoluteTimeout: 43200, now: unixNow}
}

// pendingSignin is a sign-in that passed the password and waits for the
// second factor (AuthService::PENDING_KEY payload).
type pendingSignin struct {
	id       int64
	username string
	fp       string
	at       int64
	tries    int
}

// Attempt verifies a username/password. On success it either signs the
// session in, or — when the account has TOTP two-factor — records a pending
// sign-in (pending==true) and leaves the session unsigned in; the second
// factor is completed with CompleteTwoFactor. It returns a user-facing error
// message (empty on success/pending). Mirrors AuthService::attempt.
func (s *Service) Attempt(ctx context.Context, sess *session.Session, username, password, ip string) (string, bool, error) {
	username = trimSpace(username)

	if msg := s.bruteForceRefusal(username, ip); msg != "" {
		return msg, false, nil
	}
	retry, err := s.throttle.RetryAfter(ctx, username, ip)
	if err != nil {
		return "", false, err
	}
	if retry > 0 {
		mins := (retry + 59) / 60
		s.audit.Record(ctx, audit.Entry{Username: username, Action: "auth.login", TargetType: "user", TargetID: username, Result: audit.Denied, IP: ip, Details: map[string]any{"reason": "throttled"}})
		return "Too many failed attempts. Try again in " + strconv.FormatInt(mins, 10) + " minute(s).", false, nil
	}

	var row *identity.AuthRow
	if username != "" {
		row, err = s.users.FindForAuth(ctx, username)
		if err != nil && !errors.Is(err, identity.ErrNotFound) {
			return "", false, err
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
		s.audit.Record(ctx, audit.Entry{UserID: &row.ID, Username: row.Username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(row.ID, 10), Result: audit.Denied, IP: ip, Details: map[string]any{"reason": "suspended"}})
		return msgSuspended, false, nil
	}
	if row == nil || !valid || !row.IsActive {
		if err := s.throttle.RecordFailure(ctx, username, ip); err != nil {
			return "", false, err
		}
		reason := "unknown_user"
		if row != nil {
			if valid {
				reason = "inactive"
			} else {
				reason = "bad_password"
			}
		}
		e := audit.Entry{Username: username, Action: "auth.login", TargetType: "user", TargetID: username, Result: audit.Failure, IP: ip, Details: map[string]any{"reason": reason}}
		if row != nil {
			e.UserID = &row.ID
			e.Username = row.Username
			e.TargetID = strconv.FormatInt(row.ID, 10)
		}
		s.audit.Record(ctx, e)
		return msgInvalid, false, nil
	}

	// Opportunistically upgrade a legacy/out-of-parameter hash before the
	// pending sign-in is fingerprinted, so the fingerprint binds to the new
	// hash (AuthService::attempt does the rehash before the second-factor
	// check).
	if identity.NeedsRehash(hash) {
		if nh, herr := identity.HashPassword(password); herr == nil {
			if uerr := s.users.UpdatePassword(ctx, row.ID, nh); uerr == nil {
				hash = nh
			}
		}
	}

	// TOTP two-factor: the password is right, but the session is not signed
	// in until the second factor is given.
	if enabled, err := s.twoFactor.Enabled(ctx, row.ID); err != nil {
		return "", false, err
	} else if enabled {
		fp, err := s.fingerprint(ctx, hash, row.ID)
		if err != nil {
			return "", false, err
		}
		sess.Regenerate()
		sess.Set(key2FAID, strconv.FormatInt(row.ID, 10))
		sess.Set(key2FAUser, row.Username)
		sess.Set(key2FAFP, fp)
		sess.Set(key2FAAt, strconv.FormatInt(s.now(), 10))
		sess.Set(key2FATries, "0")
		s.audit.Record(ctx, audit.Entry{UserID: &row.ID, Username: row.Username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(row.ID, 10), Result: audit.Success, IP: ip, Details: map[string]any{"step": "password", "two_factor": "required"}})
		return "", true, nil
	}

	// Security-key-only users (WebAuthn is a later batch): refuse rather
	// than bypass the factor.
	var nKey int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?", row.ID).Scan(&nKey); err != nil {
		return "", false, err
	}
	if nKey > 0 {
		s.audit.Record(ctx, audit.Entry{UserID: &row.ID, Username: row.Username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(row.ID, 10), Result: audit.Denied, IP: ip, Details: map[string]any{"reason": "security_key_not_available"}})
		return msgSecondFactorUnavailable, false, nil
	}

	if err := s.finishLogin(ctx, sess, row.ID, row.Username, hash, ip, nil); err != nil {
		return "", false, err
	}
	return "", false, nil
}

// bruteForceRefusal mirrors AuthService::bruteForceRefusal (the HULK
// brute-force-protection refusal). The Go panel has no HULK port yet, so it
// never refuses; the login throttle above still applies.
func (s *Service) bruteForceRefusal(username, ip string) string { return "" }

// finishLogin records the success, writes the signed-in session and audits it.
func (s *Service) finishLogin(ctx context.Context, sess *session.Session, id int64, username, hash, ip string, details map[string]any) error {
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
	s.clearTwoFactorKeys(sess)
	now := s.now()
	sess.Set(keyUserID, strconv.FormatInt(id, 10))
	sess.Set(keyFP, fp)
	sess.Set(keyLoginAt, strconv.FormatInt(now, 10))
	sess.Set(keyActivity, strconv.FormatInt(now, 10))
	if details == nil {
		details = map[string]any{}
	}
	s.audit.Record(ctx, audit.Entry{UserID: &id, Username: username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(id, 10), Result: audit.Success, IP: ip, Details: details})
	return nil
}

// PendingTwoFactor reports whether a sign-in is waiting for its second
// factor (and not expired). AuthService::pendingTwoFactor.
func (s *Service) PendingTwoFactor(sess *session.Session) bool {
	if sess.Get(key2FAID) == "" {
		return false
	}
	if s.now()-atoi64(sess.Get(key2FAAt)) > pendingTTL {
		s.clearTwoFactorKeys(sess)
		return false
	}
	return true
}

// PendingUsername returns the username of the pending sign-in, or "".
func (s *Service) PendingUsername(sess *session.Session) string {
	if !s.PendingTwoFactor(sess) {
		return ""
	}
	return sess.Get(key2FAUser)
}

// PendingMethods reports which second factors the pending sign-in may use.
func (s *Service) PendingMethods(ctx context.Context, sess *session.Session) (totp, key bool) {
	if !s.PendingTwoFactor(sess) {
		return false, false
	}
	id := atoi64(sess.Get(key2FAID))
	totp, _ = s.twoFactor.Enabled(ctx, id)
	var nKey int
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?", id).Scan(&nKey)
	return totp, nKey > 0
}

// CancelTwoFactor abandons a pending sign-in (the "cancel" button).
func (s *Service) CancelTwoFactor(sess *session.Session) { s.clearTwoFactorKeys(sess) }

func (s *Service) clearTwoFactorKeys(sess *session.Session) {
	for _, k := range []string{key2FAID, key2FAUser, key2FAFP, key2FAAt, key2FATries} {
		sess.Remove(k)
	}
}

// checkPending validates the pending sign-in (TTL, throttle, account still
// usable, fingerprint still matches). It returns the fields and the current
// password hash, or a user-facing message (AuthService::pending).
func (s *Service) checkPending(ctx context.Context, sess *session.Session, ip string) (*pendingSignin, string, string, error) {
	if !s.PendingTwoFactor(sess) {
		return nil, msgSignInExpired, "", nil
	}
	p := &pendingSignin{
		id:       atoi64(sess.Get(key2FAID)),
		username: sess.Get(key2FAUser),
		fp:       sess.Get(key2FAFP),
		at:       atoi64(sess.Get(key2FAAt)),
		tries:    int(atoi64(sess.Get(key2FATries))),
	}
	retry, err := s.throttle.RetryAfter(ctx, p.username, ip)
	if err != nil {
		return nil, "", "", err
	}
	if retry > 0 {
		s.clearTwoFactorKeys(sess)
		mins := (retry + 59) / 60
		return nil, "Too many failed attempts. Try again in " + strconv.FormatInt(mins, 10) + " minute(s).", "", nil
	}
	user, err := s.users.Find(ctx, p.id)
	if err != nil && !errors.Is(err, identity.ErrNotFound) {
		return nil, "", "", err
	}
	hash, err := s.users.PasswordHash(ctx, p.id)
	if err != nil {
		return nil, "", "", err
	}
	if user == nil || hash == "" || !identity.CanAct(user) {
		s.clearTwoFactorKeys(sess)
		return nil, msgSignInExpired, "", nil
	}
	want, err := s.fingerprint(ctx, hash, p.id)
	if err != nil {
		return nil, "", "", err
	}
	if subtle.ConstantTimeCompare([]byte(want), []byte(p.fp)) != 1 {
		s.clearTwoFactorKeys(sess)
		return nil, msgSignInExpired, "", nil
	}
	return p, "", "", nil
}

// CompleteTwoFactor verifies the second factor of a pending sign-in and, on
// success, signs the session in. It returns a user-facing error, or "" when
// signed in. Mirrors AuthService::completeTwoFactor.
func (s *Service) CompleteTwoFactor(ctx context.Context, sess *session.Session, code, ip string) (string, error) {
	p, msg, hash, err := s.checkPending(ctx, sess, ip)
	if err != nil || msg != "" {
		return msg, err
	}
	method, verr := s.twoFactor.Verify(ctx, p.id, code)
	if verr != nil {
		if errors.Is(verr, twofactor.ErrNotEnabled) {
			s.clearTwoFactorKeys(sess)
			return msgSignInExpired, nil
		}
		if !errors.Is(verr, twofactor.ErrBadCode) {
			return "", verr
		}
		return s.secondFactorFailed(ctx, sess, p, ip)
	}
	return "", s.finishLogin(ctx, sess, p.id, p.username, hash, ip, map[string]any{"two_factor": method})
}

// secondFactorFailed counts a failed second-factor attempt (throttle +
// audit) and cancels the pending sign-in after too many tries.
// AuthService::secondFactorFailed.
func (s *Service) secondFactorFailed(ctx context.Context, sess *session.Session, p *pendingSignin, ip string) (string, error) {
	if err := s.throttle.RecordFailure(ctx, p.username, ip); err != nil {
		return "", err
	}
	s.audit.Record(ctx, audit.Entry{UserID: &p.id, Username: p.username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(p.id, 10), Result: audit.Failure, IP: ip, Details: map[string]any{"reason": "bad_two_factor_code"}})
	p.tries++
	if p.tries >= pendingTries {
		s.clearTwoFactorKeys(sess)
		return msgTooManyTwoFactorTries, nil
	}
	sess.Set(key2FATries, strconv.Itoa(p.tries))
	return msgTwoFactorBadCode, nil
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
	if err != nil && !errors.Is(err, identity.ErrNotFound) {
		return nil, err
	}
	hash, err := s.users.PasswordHash(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil || hash == "" || !identity.CanAct(user) {
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

// Logout clears the session and records the sign-out (AuthService::logout).
func (s *Service) Logout(ctx context.Context, sess *session.Session, user *identity.User) {
	if user != nil {
		s.audit.Record(ctx, audit.Entry{UserID: &user.ID, Username: user.Username, Action: "auth.logout", TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success, IP: ""})
	}
	sess.Invalidate()
}

// ConfirmPassword re-verifies the signed-in user's password for a step-up
// gate (AuthService::confirmPassword). On success it stamps the session
// with keyAuthConfirmedAt; on failure it records the audit failure.
func (s *Service) ConfirmPassword(ctx context.Context, sess *session.Session, user *identity.User, password, ip string) bool {
	if user == nil {
		return false
	}
	if password == "" {
		return false
	}
	hash, err := s.users.PasswordHash(ctx, user.ID)
	if err != nil || hash == "" {
		return false
	}
	if !identity.VerifyPassword(hash, password) {
		uid := user.ID
		s.audit.Record(ctx, audit.Entry{
			UserID: &uid, Username: user.Username, Action: "auth.confirm",
			TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10),
			Result: audit.Failure, IP: ip,
		})
		return false
	}
	sess.Set(keyAuthConfirmedAt, strconv.FormatInt(s.now().Unix(), 10))
	uid := user.ID
	s.audit.Record(ctx, audit.Entry{
		UserID: &uid, Username: user.Username, Action: "auth.confirm",
		TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10),
		Result: audit.Success, IP: ip,
	})
	return true
}

// IsConfirmed reports whether the session has a recent confirm-password
// step-up (AuthService::passwordConfirmed).
func (s *Service) IsConfirmed(sess *session.Session) bool {
	ts := atoi64(sess.Get(keyAuthConfirmedAt))
	if ts == 0 {
		return false
	}
	return s.now().Unix()-ts <= ConfirmTTL
}

// now returns the current time (overridable in tests).
func (s *Service) now() time.Time { return time.Now() }

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
