package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/twofactor"
)

// secureCode returns a random 6-digit numeric string using crypto/rand
// (matches PHP random_int(100000,999999)).
func secureCode() string {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		// Fallback: extremely unlikely but keep the flow working.
		return "000000"
	}
	return fmt.Sprintf("%06d", 100000+n.Int64())
}

// Session keys. These live only in the Go panel's sealed cookie (not shared
// with the PHP file sessions), so the names need only be stable here. The
// fingerprint value, however, uses the same formula as AuthService.php so it
// binds to the account's password hash and session epoch identically.
const (
	keyUserID   = "_auth_user_id"
	keySID      = "_auth_sid"
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

	// IP-verification pending-sign-in keys.
	keyIPID     = "_auth_ip_id"
	keyIPUser   = "_auth_ip_username"
	keyIPFP     = "_auth_ip_fp"
	keyIPAt     = "_auth_ip_at"
	keyIPTries  = "_auth_ip_tries"
	keyIPCode   = "_auth_ip_code"
	keyIPIP     = "_auth_ip_ip"
	ipCodeTTL   = 900 // 15 minutes (LoginIpVerifier::CODE_TTL)
	ipCodeTries = 5

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
	msgInvalid               = "Invalid username or password."
	msgSuspended             = "This account is suspended. Please contact your hosting provider."
	msgSignInExpired         = "Your sign-in expired. Enter your password again."
	msgTwoFactorBadCode      = "That code is not valid. Check the time on your phone and try again."
	msgTooManyTwoFactorTries = "Too many failed attempts. Enter your password again."
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

	// ipVerifier stores/remembers verified IP addresses. When nil the
	// "limit sign-ins to verified IP addresses" policy is disabled.
	ipVerifier ipRememberer
	// mailSender is the SMTP mailer used to send the one-time code.
	mailSender ipMailSender
	// policyReader reads settings (policy_verified_ips, panel_name).
	policyReader ipPolicyReader
}

// ipRememberer stores verified IPs (security.LoginIpVerifier shape).
type ipRememberer interface {
	Verified(ctx context.Context, userID int64, ip string) bool
	Remember(ctx context.Context, userID int64, ip string) error
}

// ipMailSender sends plain-text email.
type ipMailSender interface {
	Send(ctx context.Context, to []string, subject, text string) error
	Configured(ctx context.Context) (bool, error)
}

// ipPolicyReader reads bool/string settings.
type ipPolicyReader interface {
	Bool(ctx context.Context, key string, def bool) (bool, error)
	String(ctx context.Context, key, def string) (string, error)
}

// WithIpVerification wires the "verified IP addresses" login gate.
func (s *Service) WithIpVerification(v ipRememberer, m ipMailSender, p ipPolicyReader) *Service {
	s.ipVerifier = v
	s.mailSender = m
	s.policyReader = p
	return s
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

	// Security-key-only accounts: enter a pending 2FA state (same as TOTP)
	// but leave TOTP disabled; the WebAuthn handler completes it. We do not
	// silently sign them in.
	var nKey int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?", row.ID).Scan(&nKey); err != nil {
		return "", false, err
	}
	if nKey > 0 {
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
		s.audit.Record(ctx, audit.Entry{UserID: &row.ID, Username: row.Username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(row.ID, 10), Result: audit.Success, IP: ip, Details: map[string]any{"step": "password", "two_factor": "security_key"}})
		return "", true, nil
	}

	if msg, err := s.finishOrVerifyIPStr(ctx, sess, row.ID, row.Username, hash, ip, nil); err != nil {
		return "", false, err
	} else if msg != "" {
		return msg, false, nil
	}
	return "", false, nil
}

// bruteForceRefusal mirrors AuthService::bruteForceRefusal (the HULK
// brute-force-protection refusal). The Go panel has no HULK port yet, so it
// never refuses; the login throttle above still applies.
func (s *Service) bruteForceRefusal(username, ip string) string { return "" }

// finishOrVerifyIP signs the session in, unless the verified-IP policy is
// enabled and the address isn't already trusted: then a random six-digit
// code is emailed and the sign-in pends until verifyIp submits it.
// Mirrors AuthService::finishOrVerifyIp.
func (s *Service) finishOrVerifyIP(ctx context.Context, sess *session.Session, id int64, username, hash, ip string, details map[string]any) error {
	if !s.ipRequired(ctx, id, ip) {
		return s.finishLogin(ctx, sess, id, username, hash, ip, details)
	}
	s.clearTwoFactorKeys(sess)
	s.clearIpKeys(sess)
	var email string
	s.db.QueryRowContext(ctx, "SELECT email FROM users WHERE id = ?", id).Scan(&email)
	email = strings.TrimSpace(strings.ToLower(email))
	configured, _ := s.mailSender.Configured(ctx)
	if email == "" || !configured {
		reason := "your account has no email address"
		if configured {
			reason = "the panel cannot send email"
		}
		uid := id
		s.audit.Record(ctx, audit.Entry{UserID: &uid, Username: username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(id, 10), Result: audit.Denied, IP: ip, Details: map[string]any{"reason": "ip_not_verified", "ip": ip}})
		return &userError{"Sign-ins from new IP addresses must be verified by email, but " + reason + ". Ask your administrator to verify this address for you."}
	}
	code := secureCode()
	panelName, _ := s.policyReader.String(ctx, "panel_name", "WhaCP")
	body := strings.Join([]string{
		"Someone (hopefully you) signed in to the " + panelName + " account \"" + username + "\" from the IP address " + ip + ", which has not been used before.",
		"",
		"To allow it, enter this code within 15 minutes: " + code,
		"",
		"If this was not you, do not enter the code and change your password.",
	}, "\n")
	if err := s.mailSender.Send(ctx, []string{email}, panelName+": verify a sign-in from a new IP address", body); err != nil {
		uid := id
		s.audit.Record(ctx, audit.Entry{UserID: &uid, Username: username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(id, 10), Result: audit.Denied, IP: ip, Details: map[string]any{"reason": "ip_code_email_failed", "ip": ip}})
		return &userError{"The verification email could not be sent. Try again later or ask your administrator to verify this address."}
	}
	fp, err := s.fingerprint(ctx, hash, id)
	if err != nil {
		return err
	}
	codeHash := sha256Hex("whacp-login-ip|" + code)
	sess.Regenerate()
	sess.Set(keyIPID, strconv.FormatInt(id, 10))
	sess.Set(keyIPUser, username)
	sess.Set(keyIPFP, fp)
	sess.Set(keyIPAt, strconv.FormatInt(s.now(), 10))
	sess.Set(keyIPTries, "0")
	sess.Set(keyIPCode, codeHash)
	sess.Set(keyIPIP, ip)
	if details != nil {
		if dj, err := json.Marshal(details); err == nil {
			sess.Set("_auth_ip_details", string(dj))
		}
	}
	uid := id
	s.audit.Record(ctx, audit.Entry{UserID: &uid, Username: username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(id, 10), Result: audit.Success, IP: ip, Details: map[string]any{"step": "password", "ip_verification": "required", "ip": ip}})
	return nil
}

func (s *Service) ipRequired(ctx context.Context, userID int64, ip string) bool {
	if s.ipVerifier == nil || s.mailSender == nil || s.policyReader == nil {
		return false
	}
	on, _ := s.policyReader.Bool(ctx, "policy_verified_ips", false)
	if !on {
		return false
	}
	return !s.ipVerifier.Verified(ctx, userID, ip)
}

// PendingIpVerification reports whether the session is waiting for an
// emailed IP verification code (AuthService::pendingIpVerification).
func (s *Service) PendingIpVerification(sess *session.Session) bool {
	id := sess.Get(keyIPID)
	if id == "" {
		return false
	}
	if s.now()-atoi64(sess.Get(keyIPAt)) > ipCodeTTL {
		s.clearIpKeys(sess)
		return false
	}
	return true
}

// CancelIpVerification drops an IP-verification pending sign-in.
func (s *Service) CancelIpVerification(sess *session.Session) {
	s.clearIpKeys(sess)
}

// CompleteIpVerification checks the code submitted to POST /login/verify-ip
// and, on success, finishes the login. Returns a user-facing error
// message, or "" on success. Mirrors AuthService::completeIpVerification.
func (s *Service) CompleteIpVerification(ctx context.Context, sess *session.Session, code, ip string) (string, error) {
	if !s.PendingIpVerification(sess) {
		return "Your sign-in expired. Enter your password again.", nil
	}
	id := atoi64(sess.Get(keyIPID))
	username := sess.Get(keyIPUser)
	fp := sess.Get(keyIPFP)
	tries := int(atoi64(sess.Get(keyIPTries)))
	wantHash := sess.Get(keyIPCode)
	pendingIP := sess.Get(keyIPIP)
	user, err := s.users.Find(ctx, id)
	if err != nil && !errors.Is(err, identity.ErrNotFound) {
		return "", err
	}
	hash, err := s.users.PasswordHash(ctx, id)
	if err != nil {
		return "", err
	}
	retry, err := s.throttle.RetryAfter(ctx, username, ip)
	if err != nil {
		return "", err
	}
	if pendingIP != ip || retry > 0 || hash == "" || user == nil || !identity.CanAct(user) {
		s.clearIpKeys(sess)
		return "Your sign-in expired. Enter your password again.", nil
	}
	gotFP, err := s.fingerprint(ctx, hash, id)
	if err != nil {
		return "", err
	}
	if subtle.ConstantTimeCompare([]byte(gotFP), []byte(fp)) != 1 {
		s.clearIpKeys(sess)
		return "Your sign-in expired. Enter your password again.", nil
	}
	code = strings.TrimSpace(code)
	if len(code) > 20 {
		code = code[:20]
	}
	if subtle.ConstantTimeCompare([]byte(wantHash), []byte(sha256Hex("whacp-login-ip|"+code))) != 1 {
		if err := s.throttle.RecordFailure(ctx, username, ip); err != nil {
			return "", err
		}
		uid := id
		s.audit.Record(ctx, audit.Entry{UserID: &uid, Username: username, Action: "auth.login", TargetType: "user", TargetID: strconv.FormatInt(id, 10), Result: audit.Failure, IP: ip, Details: map[string]any{"reason": "bad_ip_code", "ip": ip}})
		tries++
		if tries >= ipCodeTries {
			s.clearIpKeys(sess)
			return "Too many failed attempts. Enter your password again.", nil
		}
		sess.Set(keyIPTries, strconv.Itoa(tries))
		return "That code is not valid.", nil
	}
	if err := s.ipVerifier.Remember(ctx, id, ip); err != nil {
		return "", err
	}
	var details map[string]any
	if raw := sess.Get("_auth_ip_details"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &details)
	}
	if details == nil {
		details = map[string]any{}
	}
	details["ip_verified"] = true
	s.clearIpKeys(sess)
	return "", s.finishLogin(ctx, sess, id, username, hash, ip, details)
}

func (s *Service) clearIpKeys(sess *session.Session) {
	sess.Remove(keyIPID)
	sess.Remove(keyIPUser)
	sess.Remove(keyIPFP)
	sess.Remove(keyIPAt)
	sess.Remove(keyIPTries)
	sess.Remove(keyIPCode)
	sess.Remove(keyIPIP)
	sess.Remove("_auth_ip_details")
}

// sha256Hex returns the hex SHA-256 of a string (matches LoginIpVerifier::hash).
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

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
	sess.Set(keySID, newSID())
	// Signing in counts as confirming the password (AuthService: CONFIRMED_KEY).
	sess.Set(keyAuthConfirmedAt, strconv.FormatInt(now, 10))
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
	return p, "", hash, nil
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
	s.clearTwoFactorKeys(sess)
	return s.finishOrVerifyIPStr(ctx, sess, p.id, p.username, hash, ip, map[string]any{"two_factor": method})
}

// finishOrVerifyIPStr is a string-returning wrapper around finishOrVerifyIP
// used by second-factor completions (which return "","nil" on success).
func (s *Service) finishOrVerifyIPStr(ctx context.Context, sess *session.Session, id int64, username, hash, ip string, details map[string]any) (string, error) {
	if err := s.finishOrVerifyIP(ctx, sess, id, username, hash, ip, details); err != nil {
		// A refusal meant for the person signing in is a message, not a server
		// failure; anything else is an internal error.
		var ue *userError
		if errors.As(err, &ue) {
			return ue.msg, nil
		}
		return "", err
	}
	return "", nil
}

// userError is a sign-in refusal whose text is shown to the user (PHP returned
// these as plain strings). Anything that is not a *userError is an internal error.
type userError struct{ msg string }

func (e *userError) Error() string { return e.msg }

// PendingSigninID returns the user ID of the pending second-factor sign-in,
// or 0 if no sign-in is pending. Used by WebAuthn handlers (which need to
// look up webauthn_credentials before the session is signed in).
func (s *Service) PendingSigninID(sess *session.Session) int64 {
	if !s.PendingTwoFactor(sess) {
		return 0
	}
	return atoi64(sess.Get(key2FAID))
}

// SecondFactorFailed counts a failed second-factor attempt for a non-TOTP
// factor (e.g. a bad WebAuthn assertion).
func (s *Service) SecondFactorFailed(ctx context.Context, sess *session.Session, ip string) (string, error) {
	p, msg, _, err := s.checkPending(ctx, sess, ip)
	if err != nil || msg != "" {
		return msg, err
	}
	return s.secondFactorFailed(ctx, sess, p, ip)
}

// CompleteSecondFactor signs the session in after a second factor that the
// provider verified itself (WebAuthn). reason is a short tag (e.g.
// "security_key"); details are merged into the audit entry.
func (s *Service) CompleteSecondFactor(ctx context.Context, sess *session.Session, ip, method string, details map[string]any) (string, error) {
	p, msg, hash, err := s.checkPending(ctx, sess, ip)
	if err != nil || msg != "" {
		return msg, err
	}
	if details == nil {
		details = map[string]any{}
	}
	details["two_factor"] = method
	return s.finishOrVerifyIPStr(ctx, sess, p.id, p.username, hash, ip, details)
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
	if sid := sess.Get(keySID); sid != "" {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM revoked_sessions WHERE sid = ?`, sid).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			sess.Invalidate()
			return nil, nil
		}
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

// RefreshOwnSession keeps the current session valid after a password
// change by bumping the session epoch (invalidating every other session)
// and rewriting the fingerprint bound to the new hash. Mirrors
// AuthService::refreshFingerprint.
func (s *Service) RefreshOwnSession(ctx context.Context, sess *session.Session, user *identity.User) error {
	if user == nil {
		return nil
	}
	if err := s.users.BumpSessionEpoch(ctx, user.ID); err != nil {
		return err
	}
	hash, err := s.users.PasswordHash(ctx, user.ID)
	if err != nil {
		return err
	}
	fp, err := s.fingerprint(ctx, hash, user.ID)
	if err != nil {
		return err
	}
	sess.Set(keyFP, fp)
	return nil
}

// Logout clears the session and records the sign-out (AuthService::logout).
func (s *Service) Logout(ctx context.Context, sess *session.Session, user *identity.User) {
	if user != nil {
		s.audit.Record(ctx, audit.Entry{UserID: &user.ID, Username: user.Username, Action: "auth.logout", TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success, IP: ""})
	}
	// A sealed cookie cannot be deleted from the client's copy: remember the
	// session id so a replayed cookie is refused.
	if sid := sess.Get(keySID); sid != "" {
		now := s.now()
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO revoked_sessions (sid, revoked_at) VALUES (?, ?)`, sid, now); err == nil {
			_, _ = s.db.ExecContext(ctx, `DELETE FROM revoked_sessions WHERE revoked_at < ?`, now-s.absoluteTimeout-3600)
		}
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
	sess.Set(keyAuthConfirmedAt, strconv.FormatInt(s.now(), 10))
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
	return s.now()-ts <= ConfirmTTL
}

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

// SetClock replaces the time source (tests).
func (s *Service) SetClock(now func() int64) { s.now = now }

func newSID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS entropy source failing is unrecoverable
	}
	return hex.EncodeToString(b)
}
