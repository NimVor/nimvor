package hosting

import (
	"context"
	"database/sql"
	"time"

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

// MailDomainFull is a mail domain row (the domain page + the e-mail records
// it needs published).
type MailDomainFull struct {
	ID           int64
	Domain       string
	WebsiteID    sql.NullInt64
	OwnerID      sql.NullInt64
	Website      sql.NullString
	DkimSelector string
	DkimTxt      string
	Routing      string
	CreatedAt    string
}

// Domain returns one mail domain scoped to the user (MailService::domain).
func (r *MailRepository) Domain(ctx context.Context, serverID, id int64, u *identity.User) (*MailDomainFull, error) {
	cond, params := WithWebsite(u, "d.owner_id", "d.website_id")
	args := append([]any{id, serverID}, params...)
	var d MailDomainFull
	err := r.db.QueryRowContext(ctx,
		`SELECT d.id, d.domain, d.website_id, d.owner_id, w.domain, d.dkim_selector, d.dkim_txt, d.routing, d.created_at
		 FROM mail_domains d LEFT JOIN websites w ON w.id = d.website_id
		 WHERE d.id = ? AND d.server_id = ? AND `+cond, args...).
		Scan(&d.ID, &d.Domain, &d.WebsiteID, &d.OwnerID, &d.Website, &d.DkimSelector, &d.DkimTxt, &d.Routing, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// DomainForWebsite returns the mail domain attached to a website, or nil
// (MailService::forWebsite).
func (r *MailRepository) DomainForWebsite(ctx context.Context, websiteID int64) (*MailDomainFull, error) {
	var d MailDomainFull
	err := r.db.QueryRowContext(ctx,
		`SELECT id, domain, website_id, owner_id, dkim_selector, dkim_txt, routing, created_at
		 FROM mail_domains WHERE website_id = ?`, websiteID).
		Scan(&d.ID, &d.Domain, &d.WebsiteID, &d.OwnerID, &d.DkimSelector, &d.DkimTxt, &d.Routing, &d.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// DomainTaken reports whether e-mail is already hosted for the domain.
func (r *MailRepository) DomainTaken(ctx context.Context, serverID int64, domain string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM mail_domains WHERE server_id = ? AND domain = ?`, serverID, domain).Scan(&n)
	return n > 0, err
}

// InsertDomain stores a new mail domain (MailService::addDomain).
func (r *MailRepository) InsertDomain(ctx context.Context, serverID int64, domain string, websiteID, ownerID *int64, dkimSelector, dkimTxt string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO mail_domains (server_id, domain, website_id, owner_id, dkim_selector, dkim_txt, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		serverID, domain, websiteID, ownerID, dkimSelector, dkimTxt, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Mailbox is one mail_mailboxes row.
type Mailbox struct {
	ID      int64
	Local   string
	QuotaMB int
}

// MailboxList lists a domain's mailboxes (MailService::mailboxes).
func (r *MailRepository) MailboxList(ctx context.Context, domainID int64) ([]Mailbox, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, local, quota_mb FROM mail_mailboxes WHERE domain_id = ? ORDER BY local`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mailbox
	for rows.Next() {
		var m Mailbox
		if err := rows.Scan(&m.ID, &m.Local, &m.QuotaMB); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Alias is one mail_aliases row.
type Alias struct {
	ID           int64
	Local        string
	Destinations string
}

// AliasList lists a domain's forwarders (MailService::aliases).
func (r *MailRepository) AliasList(ctx context.Context, domainID int64) ([]Alias, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, local, destinations FROM mail_aliases WHERE domain_id = ? ORDER BY local = '', local`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alias
	for rows.Next() {
		var a Alias
		if err := rows.Scan(&a.ID, &a.Local, &a.Destinations); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
