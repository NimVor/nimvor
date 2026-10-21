package hosting

import (
	"context"
	"database/sql"
)

// CronJob is a scheduled job belonging to a website. Mirrors the display shape
// of CronService::jobs.
type CronJob struct {
	ID          int64
	Schedule    string
	Command     string
	Description string
	Email       string
	Enabled     bool
}

// CronRepository reads a website's cron jobs. Access control is on the website
// (the caller resolves it with WebsiteRepository.Find under tenant scope); the
// jobs themselves hang off that website_id.
type CronRepository struct{ db *sql.DB }

// NewCronRepository wraps a database handle.
func NewCronRepository(db *sql.DB) *CronRepository { return &CronRepository{db: db} }

// Jobs lists the cron jobs for a website. Mirrors CronService::jobs.
func (r *CronRepository) Jobs(ctx context.Context, websiteID int64) ([]CronJob, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, schedule, command, description, email, enabled FROM cron_jobs WHERE website_id = ? ORDER BY id`, websiteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CronJob
	for rows.Next() {
		var c CronJob
		var enabled int
		if err := rows.Scan(&c.ID, &c.Schedule, &c.Command, &c.Description, &c.Email, &enabled); err != nil {
			return nil, err
		}
		c.Enabled = enabled == 1
		out = append(out, c)
	}
	return out, rows.Err()
}
