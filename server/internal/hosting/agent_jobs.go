package hosting

// This file ports the write path of Services\JobTracker: remembering which
// panel user started which agent job, so job logs are only shown to people
// entitled to see them. Polling (jobs.log), settle-on-finish and the
// notifications ride with the jobs log page + notifications batches.

import (
	"context"
	"database/sql"
	"regexp"
)

// JobIDRe is the agent's job id shape (8 random bytes, hex — 16 lowercase
// hex digits). JobTracker::track ignores ids that do not match.
var JobIDRe = regexp.MustCompile(`^[a-f0-9]{16}$`)

// JobTracker records started agent jobs (the agent_jobs table).
type JobTracker struct{ db *sql.DB }

// NewJobTracker wraps a database handle.
func NewJobTracker(db *sql.DB) *JobTracker { return &JobTracker{db: db} }

// Track records a started agent job (INSERT OR IGNORE — a job started by
// two requests is recorded once). A job id not shaped like the agent's
// 16-hex id is ignored, as in PHP. Mirrors JobTracker::track.
func (t *JobTracker) Track(ctx context.Context, serverID int64, jobID, kind, targetType, targetID string, userID *int64) error {
	if !JobIDRe.MatchString(jobID) {
		return nil
	}
	_, err := t.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO agent_jobs (server_id, job_id, kind, target_type, target_id, user_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
		serverID, jobID, kind, targetType, targetID, userID)
	return err
}
