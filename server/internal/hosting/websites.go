package hosting

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// Website is a hosting website row joined to its owner, with the derived
// account-suspended and bandwidth-blocked flags. The first twelve fields cover
// the websites list and overview (scanWebsite); the remaining columns are the
// full row hydrated by FindFull for the write path (WebsiteService), as in
// WebsiteRepository::hydrate.
type Website struct {
	ID               int64
	ServerID         int64
	OwnerID          sql.NullInt64
	Domain           string
	Status           string
	Kind             string
	SystemUser       string
	HTTPS            bool
	Enabled          bool
	OwnerName        sql.NullString
	AccountSuspended bool
	BandwidthBlocked bool

	// Full row (FindFull).
	Aliases          []string
	Root             string
	DocumentRoot     string
	AppRuntime       string
	PhpVersion       string
	PhpSettings      PhpSettings
	ProxyPass        string
	Redirects        []Redirect
	ClientMaxBodyMB  int
	ForceHTTPS       bool
	HSTS             bool
	SSLMode          string
	CertFile         string
	KeyFile          string
	SecurityHeaders  bool
	DirectoryListing bool
	IPv4             sql.NullString
	IPv6             sql.NullString
	ParentID         sql.NullInt64
	LastError        string
	BandwidthApplied bool
	CreatedAt        string
	UpdatedAt        string
}

// bandwidthBlocked is UserRepository::BANDWIDTH_BLOCKED — the account (or its
// reseller) is over its monthly bandwidth this month.
const bandwidthBlockedExpr = `(u.bw_blocked_month = strftime('%Y-%m','now') OR EXISTS (SELECT 1 FROM users bp WHERE bp.id = u.parent_id AND bp.bw_blocked_month = strftime('%Y-%m','now')))`

// websiteSelect lists the display columns plus the derived flags. It mirrors
// the SELECT in WebsiteRepository::all/find.
const websiteSelect = `SELECT w.id, w.server_id, w.owner_id, w.domain, w.status, w.kind, w.system_user, w.https, w.enabled,
	u.username AS owner_name, ` + suspendedUserExpr + ` AS account_suspended, ` + bandwidthBlockedExpr + ` AS bandwidth_blocked
	FROM websites w LEFT JOIN users u ON u.id = w.owner_id`

// suspendedUserExpr is UserRepository::SUSPENDED evaluated on the joined owner
// (alias u), used for the account_suspended column of a website.
const suspendedUserExpr = `(u.suspended_at IS NOT NULL OR EXISTS (SELECT 1 FROM users sp WHERE sp.id = u.parent_id AND sp.suspended_at IS NOT NULL))`

// WebsiteRepository reads websites with tenant scoping applied.
type WebsiteRepository struct{ db *sql.DB }

// NewWebsiteRepository wraps a database handle.
func NewWebsiteRepository(db *sql.DB) *WebsiteRepository { return &WebsiteRepository{db: db} }

// All returns the websites on a server visible to the user, ordered by domain.
// Visibility is enforced in SQL via Scope — the user can never widen it with a
// request parameter. Mirrors WebsiteRepository::all.
func (r *WebsiteRepository) All(ctx context.Context, serverID int64, u *identity.User) ([]Website, error) {
	cond, params := Condition(u, "w.owner_id")
	args := append([]any{serverID}, params...)
	rows, err := r.db.QueryContext(ctx,
		websiteSelect+" WHERE w.server_id = ? AND "+cond+" ORDER BY w.domain", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Website
	for rows.Next() {
		w, err := scanWebsite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

// Find returns one website by id on a server, scoped to the user. A website
// outside the user's tenant reads as not found — the id alone never grants
// access. A nil user means no scoping (internal/system use). Mirrors
// WebsiteRepository::find.
func (r *WebsiteRepository) Find(ctx context.Context, serverID, id int64, u *identity.User) (*Website, error) {
	cond, params := "1 = 1", []any(nil)
	if u != nil {
		cond, params = Condition(u, "w.owner_id")
	}
	args := append([]any{serverID, id}, params...)
	w, err := scanWebsite(r.db.QueryRowContext(ctx,
		websiteSelect+" WHERE w.server_id = ? AND w.id = ? AND "+cond, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebsiteNotFound
	}
	return w, err
}

// ErrWebsiteNotFound is returned when no website matches within the user's scope.
var ErrWebsiteNotFound = errors.New("hosting: website not found")

// FindByDomain returns the website with that primary domain on the server,
// unscoped (internal use: the caller checks visibility separately, as in
// LogsController::allowed). Mirrors WebsiteRepository::findByDomain.
func (r *WebsiteRepository) FindByDomain(ctx context.Context, serverID int64, domain string) (*Website, error) {
	w, err := scanWebsite(r.db.QueryRowContext(ctx,
		websiteSelect+" WHERE w.server_id = ? AND w.domain = ?", serverID, domain))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrWebsiteNotFound
	}
	return w, err
}

// Children returns the websites created from a parent website (subdomains and
// addon domains), ordered by domain. The caller resolves the parent under
// tenant scope first. Mirrors WebsiteRepository::children.
func (r *WebsiteRepository) Children(ctx context.Context, serverID, parentID int64) ([]Website, error) {
	rows, err := r.db.QueryContext(ctx,
		websiteSelect+" WHERE w.server_id = ? AND w.parent_id = ? ORDER BY w.domain", serverID, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Website
	for rows.Next() {
		wsite, err := scanWebsite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *wsite)
	}
	return out, rows.Err()
}

func scanWebsite(row interface{ Scan(...any) error }) (*Website, error) {
	var w Website
	var https, enabled int
	var suspended, bwBlocked int
	if err := row.Scan(&w.ID, &w.ServerID, &w.OwnerID, &w.Domain, &w.Status, &w.Kind, &w.SystemUser,
		&https, &enabled, &w.OwnerName, &suspended, &bwBlocked); err != nil {
		return nil, err
	}
	w.HTTPS = https == 1
	w.Enabled = enabled == 1
	w.AccountSuspended = suspended == 1
	w.BandwidthBlocked = bwBlocked == 1
	return &w, nil
}
