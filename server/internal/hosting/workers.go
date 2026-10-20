package hosting

import (
	"context"
	"database/sql"
)

// Worker is a background worker belonging to a website.
type Worker struct {
	ID      int64
	Name    string
	Command string
	Runtime string
	Procs   int
	Enabled bool
}

// WorkerRepository reads a website's background workers. Access control is on
// the website (resolved by the caller under tenant scope).
type WorkerRepository struct{ db *sql.DB }

// NewWorkerRepository wraps a database handle.
func NewWorkerRepository(db *sql.DB) *WorkerRepository { return &WorkerRepository{db: db} }

// List returns the workers for a website.
func (r *WorkerRepository) List(ctx context.Context, websiteID int64) ([]Worker, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, command, runtime, procs, enabled FROM workers WHERE website_id = ? ORDER BY id`, websiteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Worker
	for rows.Next() {
		var wk Worker
		var enabled int
		if err := rows.Scan(&wk.ID, &wk.Name, &wk.Command, &wk.Runtime, &wk.Procs, &enabled); err != nil {
			return nil, err
		}
		wk.Enabled = enabled == 1
		out = append(out, wk)
	}
	return out, rows.Err()
}
