package hosting

// The per-website Cloudflare connection (cloudflare_sites): the customer's
// own API token, encrypted, plus the zone the panel found for the
// website's domain.

import (
	"context"
	"database/sql"
)

// CloudflareSite is one row of cloudflare_sites (TokenEnc is the encrypted
// token).
type CloudflareSite struct {
	WebsiteID  int64
	TokenEnc   string
	ZoneID     string
	ZoneName   string
	VerifiedAt string
}

// FindCloudflareSite returns the stored connection (nil when absent).
func FindCloudflareSite(ctx context.Context, db *sql.DB, websiteID int64) (*CloudflareSite, error) {
	row := db.QueryRowContext(ctx, `SELECT website_id, token_enc, zone_id, zone_name, verified_at FROM cloudflare_sites WHERE website_id = ?`, websiteID)
	var c CloudflareSite
	err := row.Scan(&c.WebsiteID, &c.TokenEnc, &c.ZoneID, &c.ZoneName, &c.VerifiedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveCloudflareSite upserts the connection (a re-connect replaces it).
func SaveCloudflareSite(ctx context.Context, db *sql.DB, c *CloudflareSite, now string) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO cloudflare_sites (website_id, token_enc, zone_id, zone_name, verified_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(website_id) DO UPDATE SET
			token_enc = excluded.token_enc,
			zone_id = excluded.zone_id,
			zone_name = excluded.zone_name,
			verified_at = excluded.verified_at,
			updated_at = excluded.updated_at`,
		c.WebsiteID, c.TokenEnc, c.ZoneID, c.ZoneName, now, now, now)
	return err
}

// DeleteCloudflareSite removes the connection.
func DeleteCloudflareSite(ctx context.Context, db *sql.DB, websiteID int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM cloudflare_sites WHERE website_id = ?`, websiteID)
	return err
}
