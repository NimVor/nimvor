package hosting

import (
	"context"
	"database/sql"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// Database is a managed database joined to the website it belongs to (if any).
// Mirrors the display columns of DatabaseRepository::databases.
type Database struct {
	ID        int64
	Name      string
	OwnerID   sql.NullInt64
	WebsiteID sql.NullInt64
	Website   sql.NullString // the owning website's domain, if attached
	CreatedAt string
}

// DatabaseRepository reads databases with tenant scoping applied.
type DatabaseRepository struct{ db *sql.DB }

// NewDatabaseRepository wraps a database handle.
func NewDatabaseRepository(db *sql.DB) *DatabaseRepository { return &DatabaseRepository{db: db} }

// All lists databases on a server visible to the user, optionally restricted to
// one website. Scope is applied in SQL. Mirrors DatabaseRepository::databases.
func (r *DatabaseRepository) All(ctx context.Context, serverID int64, u *identity.User, websiteID *int64) ([]Database, error) {
	cond, params := Condition(u, "d.owner_id")
	q := `SELECT d.id, d.name, d.owner_id, d.website_id, w.domain AS website, d.created_at
	      FROM databases d LEFT JOIN websites w ON w.id = d.website_id
	      WHERE d.server_id = ? AND ` + cond
	args := append([]any{serverID}, params...)
	if websiteID != nil {
		q += " AND d.website_id = ?"
		args = append(args, *websiteID)
	}
	q += " ORDER BY d.name"
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Database
	for rows.Next() {
		var d Database
		if err := rows.Scan(&d.ID, &d.Name, &d.OwnerID, &d.WebsiteID, &d.Website, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
