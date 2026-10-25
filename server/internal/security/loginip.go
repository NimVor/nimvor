package security

// This file ports LoginIpVerifier's address store: the verified IP
// addresses behind the policy "Limit logins to verified IP addresses"
// (Security → Configure Security Policies). The code-email half of the PHP
// class is not ported yet (the sign-in flow); these four operations are the
// panel's own management of the list.

import (
	"context"
	"database/sql"
	"time"
)

// LoginIp is one verified address of one user.
type LoginIp struct {
	ID         int64
	IP         string
	VerifiedAt string
	LastUsedAt string
}

// LoginIpVerifier wraps the login_ips table.
type LoginIpVerifier struct{ db *sql.DB }

func NewLoginIpVerifier(db *sql.DB) *LoginIpVerifier { return &LoginIpVerifier{db: db} }

// Verified reports whether the user already has this IP on their verified
// list (LoginIpVerifier::verified).
func (v *LoginIpVerifier) Verified(ctx context.Context, userID int64, ip string) bool {
	var id int64
	err := v.db.QueryRowContext(ctx, "SELECT id FROM login_ips WHERE user_id = ? AND ip = ?", userID, ip).Scan(&id)
	return err == nil
}

// Remember records an address as verified (or refreshes last_used_at).
func (v *LoginIpVerifier) Remember(ctx context.Context, userID int64, ip string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := v.db.ExecContext(ctx,
		`INSERT INTO login_ips (user_id, ip, verified_at, last_used_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT (user_id, ip) DO UPDATE SET last_used_at = excluded.last_used_at`,
		userID, ip, now, now)
	return err
}

// SeedFromLastLogins records the address every user last signed in from
// (when the policy is turned on, so nobody is locked out). Returns how many
// addresses were recorded.
func (v *LoginIpVerifier) SeedFromLastLogins(ctx context.Context) (int, error) {
	rows, err := v.db.QueryContext(ctx,
		`SELECT id, last_login_ip FROM users WHERE last_login_ip IS NOT NULL AND last_login_ip <> ''`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id int64
		var ip string
		if rows.Scan(&id, &ip) != nil {
			continue
		}
		if v.Remember(ctx, id, ip) != nil {
			return n, nil // best effort, as in PHP
		}
		n++
	}
	return n, nil
}

// List is the user's verified addresses, most recently used first.
func (v *LoginIpVerifier) List(ctx context.Context, userID int64) ([]LoginIp, error) {
	rows, err := v.db.QueryContext(ctx,
		`SELECT id, ip, verified_at, last_used_at FROM login_ips WHERE user_id = ?
		 ORDER BY COALESCE(last_used_at, verified_at) DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoginIp{}
	for rows.Next() {
		var li LoginIp
		var last sql.NullString
		if rows.Scan(&li.ID, &li.IP, &li.VerifiedAt, &last) != nil {
			continue
		}
		li.LastUsedAt = last.String
		out = append(out, li)
	}
	return out, nil
}

// Forget removes one of the user's verified addresses (by row id).
func (v *LoginIpVerifier) Forget(ctx context.Context, userID, id int64) error {
	_, err := v.db.ExecContext(ctx, `DELETE FROM login_ips WHERE user_id = ? AND id = ?`, userID, id)
	return err
}
