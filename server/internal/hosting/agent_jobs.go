package hosting

// This file ports Services\JobTracker: remembering which panel user started
// which agent job (so job logs are only shown to people entitled to see
// them) and the read path — finding a job, its per-user visibility, and
// polling its log (jobs.log) with settle-on-finish. The settle's
// notification and scan-on-extract halves ride with the notifications and AV
// batches.

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
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

// AgentJob is one row of agent_jobs.
type AgentJob struct {
	ID         int64
	ServerID   int64
	JobID      string
	Kind       string
	TargetType string
	TargetID   string
	UserID     *int64
	Status     string
	SettledAt  *string
	CreatedAt  string
}

// Find loads a tracked job (server-scoped). nil when there is no such row.
// Mirrors JobTracker::find.
func (t *JobTracker) Find(ctx context.Context, serverID int64, jobID string) (*AgentJob, error) {
	var j AgentJob
	var settled sql.NullString
	err := t.db.QueryRowContext(ctx,
		`SELECT id, server_id, job_id, kind, target_type, target_id, user_id, status, settled_at, created_at
		 FROM agent_jobs WHERE server_id = ? AND job_id = ?`, serverID, jobID).
		Scan(&j.ID, &j.ServerID, &j.JobID, &j.Kind, &j.TargetType, &j.TargetID, &j.UserID, &j.Status, &settled, &j.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if settled.Valid {
		j.SettledAt = &settled.String
	}
	return &j, nil
}

// RecentFor lists a target's newest tracked jobs, newest first, as the
// website advanced page's "Recent tasks" card shows them
// (JobTracker::recentFor).
func (t *JobTracker) RecentFor(ctx context.Context, serverID int64, targetType, targetID string, limit int) ([]AgentJob, error) {
	rows, err := t.db.QueryContext(ctx,
		`SELECT j.id, j.server_id, j.job_id, j.kind, j.target_type, j.target_id, j.user_id,
		        j.status, j.settled_at, j.created_at, u.username
		 FROM agent_jobs j LEFT JOIN users u ON u.id = j.user_id
		 WHERE j.server_id = ? AND j.target_type = ? AND j.target_id = ?
		 ORDER BY j.id DESC LIMIT ?`, serverID, targetType, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AgentJob{}
	for rows.Next() {
		var j AgentJob
		var settled sql.NullString
		var username sql.NullString
		if rows.Scan(&j.ID, &j.ServerID, &j.JobID, &j.Kind, &j.TargetType, &j.TargetID,
			&j.UserID, &j.Status, &settled, &j.CreatedAt, &username) != nil {
			continue
		}
		if settled.Valid {
			j.SettledAt = &settled.String
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// VisibleTo reports whether the user may follow the job: "all"-scope roles
// see every job, others their own and (resellers) their customers' jobs, and
// a job on a website the user may see (e.g. a restore an administrator ran on
// it). Mirrors JobTracker::visibleTo.
func (t *JobTracker) VisibleTo(ctx context.Context, job *AgentJob, u *identity.User) (bool, error) {
	if IsAll(u) || (job.UserID != nil && u != nil && *job.UserID == u.ID) {
		return true, nil
	}
	if job.UserID != nil && u != nil {
		var n int
		if err := t.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM users WHERE id = ? AND parent_id = ?", *job.UserID, u.ID).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	if job.TargetType == "website" && u != nil {
		cond, params := Condition(u, "owner_id")
		args := append([]any{job.TargetID, job.ServerID}, params...)
		var n int
		if err := t.db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM websites WHERE id = ? AND server_id = ? AND "+cond, args...).Scan(&n); err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

// Poll fetches the job's log since offset, updates the stored status, and
// settles a finished job once (settled_at). The settle's notification and
// scan-on-extract halves ride with the notifications and AV batches, and the
// discovery invalidation on done is a no-op until the discovery service is
// ported. Returns the agent's reply for the caller to merge into its
// response. Mirrors JobTracker::poll.
func (t *JobTracker) Poll(ctx context.Context, agent AgentCaller, serverID int64, jobID string, offset int) (json.RawMessage, error) {
	if offset < 0 {
		offset = 0
	}
	res, err := agent.Call(ctx, "jobs.log", map[string]any{"id": jobID, "offset": offset})
	if err != nil {
		return nil, err
	}
	var out struct {
		Done bool `json:"done"` // (discovery invalidation — no-op for now)
		Job  struct {
			Status string `json:"status"`
		} `json:"job"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return res, nil // unexpected reply shape: pass through untouched
	}
	if out.Job.Status != "" {
		if _, err := t.db.ExecContext(ctx,
			"UPDATE agent_jobs SET status = ? WHERE server_id = ? AND job_id = ?", out.Job.Status, serverID, jobID); err != nil {
			return nil, err
		}
	}
	if out.Job.Status == "succeeded" || out.Job.Status == "failed" {
		if _, err := t.db.ExecContext(ctx,
			`UPDATE agent_jobs SET settled_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
			 WHERE server_id = ? AND job_id = ? AND settled_at IS NULL`, serverID, jobID); err != nil {
			return nil, err
		}
	}
	return res, nil
}
