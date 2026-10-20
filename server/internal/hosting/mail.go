package hosting

import (
	"context"
	"database/sql"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// MailDomain is an email domain joined to the website it belongs to (if any),
// with mailbox and alias counts. Mirrors the display shape of
// MailService::domains.
type MailDomain struct {
	ID        int64
	Domain    string
	WebsiteID sql.NullInt64
	Website   sql.NullString
	Mailboxes int
	Aliases   int
	CreatedAt string
}

// MailRepository reads email domains with the website-aware tenant scope (a
// mail domain an administrator created for a customer's website is visible to
// that customer too).
type MailRepository struct{ db *sql.DB }

// NewMailRepository wraps a database handle.
func NewMailRepository(db *sql.DB) *MailRepository { return &MailRepository{db: db} }

// Domains lists the email domains on a server visible to the user. Mirrors
// MailService::domains (scope on d.owner_id, extended to d.website_id).
func (r *MailRepository) Domains(ctx context.Context, serverID int64, u *identity.User) ([]MailDomain, error) {
	cond, params := WithWebsite(u, "d.owner_id", "d.website_id")
	args := append([]any{serverID}, params...)
	rows, err := r.db.QueryContext(ctx, `
		SELECT d.id, d.domain, d.website_id, w.domain AS website,
		       (SELECT COUNT(*) FROM mail_mailboxes m WHERE m.domain_id = d.id) AS mailboxes,
		       (SELECT COUNT(*) FROM mail_aliases a WHERE a.domain_id = d.id) AS aliases,
		       d.created_at
		FROM mail_domains d LEFT JOIN websites w ON w.id = d.website_id
		WHERE d.server_id = ? AND `+cond+` ORDER BY d.domain`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailDomain
	for rows.Next() {
		var d MailDomain
		if err := rows.Scan(&d.ID, &d.Domain, &d.WebsiteID, &d.Website, &d.Mailboxes, &d.Aliases, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
