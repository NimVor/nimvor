package hosting

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// ErrDatabaseNotFound is returned when no database matches within the user's scope.
var ErrDatabaseNotFound = errors.New("hosting: database not found")

// Database is a managed database joined to the website it belongs to (if any)
// and the logins granted on it. Mirrors the display columns of
// DatabaseRepository::databases (incl. the per-database grants join).
type Database struct {
	ID        int64
	Name      string
	OwnerID   sql.NullInt64
	WebsiteID sql.NullInt64
	Website   sql.NullString // the owning website's domain, if attached
	CreatedAt string
	Grants    []DBUserGrant
}

// Find returns one database by id on a server, scoped to the user. A database
// outside the user's tenant reads as ErrDatabaseNotFound. Mirrors
// DatabaseRepository::database.
func (r *DatabaseRepository) Find(ctx context.Context, serverID, id int64, u *identity.User) (*Database, error) {
	cond, params := Condition(u, "owner_id")
	args := append([]any{serverID, id}, params...)
	var d Database
	err := r.db.QueryRowContext(ctx,
		"SELECT id, name, owner_id, website_id, created_at FROM databases WHERE server_id = ? AND id = ? AND "+cond, args...).
		Scan(&d.ID, &d.Name, &d.OwnerID, &d.WebsiteID, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDatabaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// DeleteDatabase removes the database record. Mirrors DatabaseRepository::deleteDatabase.
func (r *DatabaseRepository) DeleteDatabase(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM databases WHERE id = ?", id)
	return err
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The grants join (DatabaseRepository::databases lists each database's
	// users); one query for the whole page.
	if len(out) > 0 {
		ids := make([]int64, len(out))
		for i := range out {
			ids[i] = out[i].ID
		}
		grants, err := r.DBGrantsFor(ctx, ids)
		if err != nil {
			return nil, err
		}
		for i := range out {
			out[i].Grants = grants[out[i].ID]
		}
	}
	return out, nil
}

// AllNames lists every database name on a server (all scope, no tenant
// filter). The page uses it to flag databases present on the server but not
// under WhaCP management, which is only shown to an all-scope viewer.
func (r *DatabaseRepository) AllNames(ctx context.Context, serverID int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		"SELECT name FROM databases WHERE server_id = ? ORDER BY name", serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
