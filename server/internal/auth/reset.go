package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// Reset-link limits (PasswordReset constants).
const (
	resetTTL         = 1800 // seconds a reset link stays valid
	perUserResetHour = 3    // requests per account per hour
	perIPResetHour   = 10   // requests per IP per hour, known accounts or not
)

var resetTokenRe = regexp.MustCompile(`^[a-f0-9]{64}$`)

// MailSender is the outgoing-mail dependency of ResetService. The
// mail.Mailer satisfies it; tests substitute a recorder.
type MailSender interface {
	Configured(ctx context.Context) (bool, error)
	Send(ctx context.Context, to []string, subject, text string) error
}

// FieldErrors carries the per-field user-facing problems from Reset
// (PHP's ValidationException payload). An empty map means the password was
// changed.
type FieldErrors map[string]string

// ResetService is the Go port of Security\PasswordReset — "forgot password"
// by email. Requests always get the same answer whether or not the account
// exists. Tokens are 256-bit, stored only as SHA-256 hashes, valid for 30
// minutes, single-use, and all of a user's tokens die when one is used.
// Links are built from the configured public panel URL, never from the
// request's Host header. Two-factor sign-in, when on, is still required
// afterwards.
type ResetService struct {
	db       *sql.DB
	users    *identity.Repository
	mailer   MailSender
	settings *settings.Repository
	policy   *security.Policy
	audit    *audit.Logger
	log      *slog.Logger
	now      func() int64
}

func NewResetService(db *sql.DB, users *identity.Repository, mailer MailSender, st *settings.Repository, policy *security.Policy, auditLog *audit.Logger, log *slog.Logger) *ResetService {
	return &ResetService{
		db: db, users: users, mailer: mailer, settings: st,
		policy: policy, audit: auditLog, log: log, now: unixNow,
	}
}

func (s *ResetService) WithClock(now func() int64) *ResetService {
	s.now = now
	return s
}

// Available reports whether reset by email can work: the mailer is
// configured and a public panel URL is set (PasswordReset::available).
func (s *ResetService) Available(ctx context.Context) (bool, error) {
	ok, err := s.mailer.Configured(ctx)
	if err != nil || !ok {
		return false, err
	}
	url, err := s.settings.String(ctx, "panel_url", "")
	return url != "", err
}

// Request creates a reset link for the account matching identifier
// (username or email) and emails it. Every request counts against the IP
// limit — including unknown accounts — and the public answer is the same
// either way (anti-enumeration). A mail failure is audited and logged, not
// returned: the caller must not learn that the account exists. Mirrors
// PasswordReset::request.
func (s *ResetService) Request(ctx context.Context, identifier, ip string) error {
	now := s.now()
	identifier = strings.TrimSpace(identifier)
	if r := []rune(identifier); len(r) > 190 {
		identifier = string(r[:190])
	}
	available, err := s.Available(ctx)
	if err != nil {
		return err
	}
	if !available || identifier == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM password_resets WHERE created_at < ?`, now-86400); err != nil {
		return err
	}
	var perIP int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM password_resets WHERE ip = ? AND created_at > ?`, ip, now-3600).Scan(&perIP); err != nil {
		return err
	}
	row, err := s.users.FindForAuth(ctx, identifier)
	if err != nil && !errors.Is(err, identity.ErrNotFound) {
		return err
	}
	// Every request counts against the IP, including unknown accounts.
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO password_resets (user_id, token_hash, ip, created_at, expires_at) VALUES (?, NULL, ?, ?, ?)`,
		nil, ip, now, now); err != nil {
		return err
	}
	if perIP >= perIPResetHour {
		s.audit.Record(ctx, audit.Entry{
			Action: "auth.password_reset_request", TargetType: "user", TargetID: identifier,
			Username: identifier, Result: audit.Denied, IP: ip,
			Details: map[string]any{"reason": "throttled_ip"},
		})
		return nil
	}
	if row == nil || !row.IsActive || row.Suspended {
		reason := "unknown_user"
		if row != nil {
			reason = "inactive"
		}
		s.audit.Record(ctx, audit.Entry{
			Action: "auth.password_reset_request", TargetType: "user", TargetID: identifier,
			Username: identifier, Result: audit.Failure, IP: ip,
			Details: map[string]any{"reason": reason},
		})
		return nil
	}
	id := row.ID
	var perUser int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM password_resets WHERE user_id = ? AND created_at > ?`, id, now-3600).Scan(&perUser); err != nil {
		return err
	}
	if perUser >= perUserResetHour {
		s.audit.Record(ctx, audit.Entry{
			Action: "auth.password_reset_request", TargetType: "user", TargetID: fmt.Sprint(id),
			UserID: &id, Username: row.Username, Result: audit.Denied, IP: ip,
			Details: map[string]any{"reason": "throttled_user"},
		})
		return nil
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return err
	}
	token := hex.EncodeToString(tokenBytes)
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO password_resets (user_id, token_hash, ip, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		id, sha256hex(token), ip, now, now+resetTTL); err != nil {
		return err
	}
	user, err := s.users.Find(ctx, id)
	if err != nil {
		return err
	}
	name, err := s.settings.String(ctx, "panel_name", "WhaCP")
	if err != nil {
		return err
	}
	panelURL, err := s.settings.String(ctx, "panel_url", "")
	if err != nil {
		return err
	}
	body := strings.Join([]string{
		fmt.Sprintf("Someone (hopefully you) asked to reset the password of the %s account %q.", name, row.Username),
		"",
		"Choose a new password here within 30 minutes:",
		panelURL + "/password/reset/" + token,
		"",
		fmt.Sprintf("The request came from %s. If it was not you, ignore this email; your password stays unchanged.", ip),
	}, "\n")
	if err := s.mailer.Send(ctx, []string{user.Email}, fmt.Sprintf("%s: reset your password", name), body); err != nil {
		s.log.Error("password reset email failed", "user", id, "error", err.Error())
		s.audit.Record(ctx, audit.Entry{
			Action: "auth.password_reset_request", TargetType: "user", TargetID: fmt.Sprint(id),
			UserID: &id, Username: row.Username, Result: audit.Failure, IP: ip,
			Details: map[string]any{"reason": "mail_failed"},
		})
		return nil
	}
	s.audit.Record(ctx, audit.Entry{
		Action: "auth.password_reset_request", TargetType: "user", TargetID: fmt.Sprint(id),
		UserID: &id, Username: row.Username, Result: audit.Success, IP: ip,
	})
	return nil
}

// UserFor returns the account the token belongs to, or nil when the token is
// malformed, expired, already used, or the account no longer may act
// (PasswordReset::userFor).
func (s *ResetService) UserFor(ctx context.Context, token string) (*identity.User, error) {
	if !resetTokenRe.MatchString(token) {
		return nil, nil
	}
	var userID int64
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id FROM password_resets WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		sha256hex(token), s.now()).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	user, err := s.users.Find(ctx, userID)
	if errors.Is(err, identity.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !identity.CanAct(user) {
		return nil, nil
	}
	return user, nil
}

// Reset verifies the token and the new password, then applies it. The base
// and configured password rules run first, all in one transaction with the
// single-use marking: using one link kills every token of the account
// (PasswordReset::reset).
func (s *ResetService) Reset(ctx context.Context, token, password, confirmation string) (FieldErrors, error) {
	user, err := s.UserFor(ctx, token)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return FieldErrors{"token": "This link has expired or was already used. Ask for a new one."}, nil
	}
	weak, err := s.policy.Check(ctx, password, user.Username)
	if err != nil {
		return nil, err
	}
	if weak != "" {
		return FieldErrors{"password": weak}, nil
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(confirmation)) != 1 {
		return FieldErrors{"password_confirmation": "The passwords do not match."}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	hash, err := identity.HashPassword(password)
	if err != nil {
		return nil, err
	}
	stamp := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`,
		hash, stamp, stamp, user.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE password_resets SET used_at = ? WHERE user_id = ? AND used_at IS NULL`,
		s.now(), user.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	id := user.ID
	s.audit.Record(ctx, audit.Entry{
		Action: "user.password_reset", TargetType: "user", TargetID: fmt.Sprint(id),
		UserID: &id, Username: user.Username, Result: audit.Success,
		Details: map[string]any{"via": "email"},
	})
	return nil, nil
}

// sha256hex is the token-hash form stored in password_resets.token_hash.
func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
