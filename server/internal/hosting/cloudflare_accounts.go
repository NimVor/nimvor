package hosting

// The server-wide Cloudflare account (cloudflare_accounts): one API token
// per server, encrypted, for the administrators' CDN/DNS management.

import (
	"context"
	"database/sql"
)

// CloudflareAccount is one row of cloudflare_accounts (TokenEnc is the
// encrypted token; the token itself is never read back into views).
type CloudflareAccount struct {
	ServerID     int64
	TokenEnc     string
	AccountEmail string
	VerifiedAt   string
}

// FindCloudflareAccount returns the stored account (nil when absent).
func FindCloudflareAccount(ctx context.Context, db *sql.DB, serverID int64) (*CloudflareAccount, error) {
	var c CloudflareAccount
	var verifiedAt sql.NullString
	err := db.QueryRowContext(ctx, `SELECT server_id, token_enc, account_email, verified_at FROM cloudflare_accounts WHERE server_id = ?`, serverID).
		Scan(&c.ServerID, &c.TokenEnc, &c.AccountEmail, &verifiedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	c.VerifiedAt = verifiedAt.String
	return &c, nil
}

// SaveCloudflareAccount upserts the account (a re-connect replaces it).
func SaveCloudflareAccount(ctx context.Context, db *sql.DB, c *CloudflareAccount, now string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO cloudflare_accounts (server_id, token_enc, verified_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(server_id) DO UPDATE SET
			token_enc = excluded.token_enc,
			verified_at = excluded.verified_at,
			updated_at = excluded.updated_at`,
		c.ServerID, c.TokenEnc, now, now, now)
	return err
}

// DeleteCloudflareAccount removes the stored account.
func DeleteCloudflareAccount(ctx context.Context, db *sql.DB, serverID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM cloudflare_accounts WHERE server_id = ?`, serverID)
	return err
}
