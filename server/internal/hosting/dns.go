package hosting

import (
	"context"
	"database/sql"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// Zone is a DNS zone joined to the website it belongs to (if any), with a
// record count. Mirrors the display shape of DnsService::zones.
type Zone struct {
	ID        int64
	Name      string
	WebsiteID sql.NullInt64
	Website   sql.NullString // the attached website's domain, if any
	Records   int
	CreatedAt string
}

// DnsRepository reads DNS zones with tenant scoping applied. Zones use the
// website-aware scope: a zone an administrator created for a customer's
// website is visible to that customer too (Scope.WithWebsite).
type DnsRepository struct{ db *sql.DB }

// NewDnsRepository wraps a database handle.
func NewDnsRepository(db *sql.DB) *DnsRepository { return &DnsRepository{db: db} }

// Zones lists the DNS zones on a server visible to the user. Mirrors
// DnsService::zones (scope on z.owner_id, extended to z.website_id).
func (r *DnsRepository) Zones(ctx context.Context, serverID int64, u *identity.User) ([]Zone, error) {
	cond, params := WithWebsite(u, "z.owner_id", "z.website_id")
	args := append([]any{serverID}, params...)
	rows, err := r.db.QueryContext(ctx, `
		SELECT z.id, z.name, z.website_id, w.domain AS website,
		       (SELECT COUNT(*) FROM dns_records rr WHERE rr.zone_id = z.id) AS records, z.created_at
		FROM dns_zones z LEFT JOIN websites w ON w.id = z.website_id
		WHERE z.server_id = ? AND `+cond+` ORDER BY z.name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Zone
	for rows.Next() {
		var z Zone
		if err := rows.Scan(&z.ID, &z.Name, &z.WebsiteID, &z.Website, &z.Records, &z.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}
