// Package auth ports the panel's session authentication: the login attempt
// state machine, brute-force throttling and the signed-in-user resolution with
// idle/absolute timeouts and password-change invalidation. It is the Go
// counterpart of Security\AuthService, Security\LoginThrottle and
// Repositories\LoginAttemptRepository.
//
// Scope note: this batch ports the password path. Two-factor, security keys,
// emailed-IP verification and the HULK brute-force integration are NOT yet
// ported — a user who has them configured in the shared database must not be
// allowed to bypass them, so AuthService refuses to complete a password-only
// sign-in for such a user (see Service.Attempt).
package auth

import (
	"context"
	"database/sql"
	"time"
)

// attemptStore is the login_attempts table access, ported verbatim from
// LoginAttemptRepository.php.
type attemptStore struct{ db *sql.DB }

func (s attemptStore) record(ctx context.Context, username, ip string, succeeded bool, t int64) error {
	if len(username) > 190 {
		username = username[:190]
	}
	v := 0
	if succeeded {
		v = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO login_attempts (username, ip, succeeded, attempted_at) VALUES (?, ?, ?, ?)`,
		username, ip, v, t)
	return err
}

func (s attemptStore) failuresForIP(ctx context.Context, ip string, since int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM login_attempts WHERE ip = ? AND succeeded = 0 AND attempted_at >= ?`, ip, since).Scan(&n)
	return n, err
}

func (s attemptStore) failuresForUser(ctx context.Context, username string, since int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM login_attempts WHERE username = ? AND succeeded = 0 AND attempted_at >= ?`, username, since).Scan(&n)
	return n, err
}

func (s attemptStore) oldestFailureForIP(ctx context.Context, ip string, since int64) (int64, bool, error) {
	var v sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MIN(attempted_at) FROM login_attempts WHERE ip = ? AND succeeded = 0 AND attempted_at >= ?`, ip, since).Scan(&v)
	if err != nil {
		return 0, false, err
	}
	return v.Int64, v.Valid, nil
}

func (s attemptStore) clearFailures(ctx context.Context, username, ip string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM login_attempts WHERE succeeded = 0 AND (username = ? OR ip = ?)`, username, ip)
	return err
}

func (s attemptStore) prune(ctx context.Context, before int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM login_attempts WHERE attempted_at < ?`, before)
	return err
}

// Throttle limits failed logins per IP and per username in a sliding window.
// Defaults match LoginThrottle.php (10 per IP, 5 per user, 15-minute window).
type Throttle struct {
	store      attemptStore
	maxPerIP   int
	maxPerUser int
	window     int64
	now        func() int64
}

// NewThrottle builds a Throttle over the given database.
func NewThrottle(db *sql.DB) *Throttle {
	return &Throttle{store: attemptStore{db}, maxPerIP: 10, maxPerUser: 5, window: 900, now: unixNow}
}

func unixNow() int64 { return time.Now().Unix() }

// RetryAfter returns the seconds until another attempt is allowed (0 = now).
func (t *Throttle) RetryAfter(ctx context.Context, username, ip string) (int64, error) {
	now := t.now()
	since := now - t.window
	ipFails, err := t.store.failuresForIP(ctx, ip, since)
	if err != nil {
		return 0, err
	}
	blocked := ipFails >= t.maxPerIP
	if !blocked && username != "" {
		userFails, err := t.store.failuresForUser(ctx, username, since)
		if err != nil {
			return 0, err
		}
		blocked = userFails >= t.maxPerUser
	}
	if !blocked {
		return 0, nil
	}
	oldest, ok, err := t.store.oldestFailureForIP(ctx, ip, since)
	if err != nil {
		return 0, err
	}
	if !ok {
		oldest = now
	}
	retry := oldest + t.window - now
	if retry < 60 {
		retry = 60
	}
	return retry, nil
}

// RecordFailure logs a failed attempt and prunes rows older than a day.
func (t *Throttle) RecordFailure(ctx context.Context, username, ip string) error {
	now := t.now()
	if err := t.store.record(ctx, username, ip, false, now); err != nil {
		return err
	}
	return t.store.prune(ctx, now-86400)
}

// RecordSuccess logs a success and clears the account's/IP's failures.
func (t *Throttle) RecordSuccess(ctx context.Context, username, ip string) error {
	if err := t.store.record(ctx, username, ip, true, t.now()); err != nil {
		return err
	}
	return t.store.clearFailures(ctx, username, ip)
}
