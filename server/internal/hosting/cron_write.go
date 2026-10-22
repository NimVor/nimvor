package hosting

import (
	"context"
	"database/sql"
	"time"
)

// cronJobRow is a complete cron_jobs row (the display rows in CronRepository
// carry only the page's columns; the write path needs the rest for the
// revert).
type cronJobRow struct {
	ID          int64
	ServerID    int64
	WebsiteID   int64
	Schedule    string
	Command     string
	Description string
	Enabled     bool
	Email       string
	LastRunJob  string
	CreatedAt   string
	UpdatedAt   string
}

// AllFull lists the website's complete job rows, ordered by id
// (CronService::jobs + the columns the revert needs).
func (r *CronRepository) AllFull(ctx context.Context, websiteID int64) ([]cronJobRow, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, server_id, website_id, schedule, command, description, enabled, email,
		        last_run_job, created_at, updated_at
		 FROM cron_jobs WHERE website_id = ? ORDER BY id`, websiteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cronJobRow
	for rows.Next() {
		var j cronJobRow
		var enabled int
		if err := rows.Scan(&j.ID, &j.ServerID, &j.WebsiteID, &j.Schedule, &j.Command,
			&j.Description, &enabled, &j.Email, &j.LastRunJob, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		j.Enabled = enabled == 1
		out = append(out, j)
	}
	return out, rows.Err()
}

// FindFull returns one of the website's job rows by id (nil = not one of
// this website's jobs).
func (r *CronRepository) FindFull(ctx context.Context, websiteID, id int64) (*cronJobRow, error) {
	var j cronJobRow
	var enabled int
	err := r.db.QueryRowContext(ctx,
		`SELECT id, server_id, website_id, schedule, command, description, enabled, email,
		        last_run_job, created_at, updated_at
		 FROM cron_jobs WHERE id = ? AND website_id = ?`, id, websiteID).
		Scan(&j.ID, &j.ServerID, &j.WebsiteID, &j.Schedule, &j.Command, &j.Description,
			&enabled, &j.Email, &j.LastRunJob, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	j.Enabled = enabled == 1
	return &j, nil
}

// Insert adds a job; returns its id (CronService::save, the create branch).
func (r *CronRepository) Insert(ctx context.Context, site *Website, schedule, command, description, email, now string, enabled bool) (int64, error) {
	e := 0
	if enabled {
		e = 1
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO cron_jobs (website_id, server_id, schedule, command, description, enabled, email, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		site.ID, site.ServerID, schedule, command, description, e, email, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Update rewrites one of the website's jobs (CronService::save, the update
// branch).
func (r *CronRepository) Update(ctx context.Context, websiteID, id int64, schedule, command, description, email, now string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE cron_jobs SET schedule = ?, command = ?, description = ?, enabled = ?, email = ?, updated_at = ?
		 WHERE id = ? AND website_id = ?`,
		schedule, command, description, e, email, now, id, websiteID)
	return err
}

// SetEnabled flips one of the website's jobs (CronService::setEnabled).
func (r *CronRepository) SetEnabled(ctx context.Context, websiteID, id int64, enabled bool, now string) error {
	e := 0
	if enabled {
		e = 1
	}
	_, err := r.db.ExecContext(ctx,
		"UPDATE cron_jobs SET enabled = ?, updated_at = ? WHERE id = ? AND website_id = ?", e, now, id, websiteID)
	return err
}

// DeleteJob removes one of the website's jobs (CronService::delete).
func (r *CronRepository) DeleteJob(ctx context.Context, websiteID, id int64) error {
	_, err := r.db.ExecContext(ctx,
		"DELETE FROM cron_jobs WHERE id = ? AND website_id = ?", id, websiteID)
	return err
}

// SetLastRun records the agent job of a manual run (CronService::run).
func (r *CronRepository) SetLastRun(ctx context.Context, id int64, jobID string) error {
	_, err := r.db.ExecContext(ctx, "UPDATE cron_jobs SET last_run_job = ? WHERE id = ?", jobID, id)
	return err
}

// Restore puts the website's jobs back exactly as they were before a change
// whose agent apply failed (CronService::applyOrRevert, in one
// transaction). The PHP's restore INSERT omitted the email column, dropping
// the recipients on a revert; the port restores it.
func (r *CronRepository) Restore(ctx context.Context, websiteID int64, before []cronJobRow) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM cron_jobs WHERE website_id = ?", websiteID); err != nil {
		return err
	}
	for _, j := range before {
		e := 0
		if j.Enabled {
			e = 1
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO cron_jobs (id, website_id, server_id, schedule, command, description,
				enabled, email, last_run_job, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			j.ID, j.WebsiteID, j.ServerID, j.Schedule, j.Command, j.Description, e, j.Email,
			j.LastRunJob, j.CreatedAt, j.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// cronNow is the row timestamp (the PHP's gmdate('c')).
func cronNow() string { return time.Now().UTC().Format(time.RFC3339) }
