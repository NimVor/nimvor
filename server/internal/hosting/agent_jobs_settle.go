package hosting

// The scheduler's half of JobTracker (JobTracker::settlePending and ::settle):
// jobs nobody watched are followed up, their outcome stored and, for the
// ones worth it, notified to whoever started them.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// jobSettleWindow is how long an unfinished job is followed before it is
// given up on.
const jobSettleWindow = 2 * 24 * time.Hour

// jobQuietOnSuccess are jobs whose success is not worth a notification
// (quick, and started by someone who watches them); failures always are.
var jobQuietOnSuccess = []string{"Cron: ", "Compress in ", "Extract in ", "Malware scan of "}

// JobNotify stores a job's notification (dedupe key first).
type JobNotify func(ctx context.Context, key string, websiteID, ownerID sql.NullInt64, severity, title, body, link string)

// SettlePending asks the agent for up to 200 jobs not yet settled, stores
// the outcome of the finished ones and notifies about it once. A job the
// agent forgot (it restarted) is recorded as "unknown"; one still not
// finished after two days is given up on. One line per finished job.
func (t *JobTracker) SettlePending(ctx context.Context, agent AgentCaller, serverID int64, now time.Time, notify JobNotify) []string {
	type pending struct {
		id          int64
		jobID, kind string
		targetType  string
		targetID    string
		createdAt   string
	}
	var list []pending
	rows, err := t.db.QueryContext(ctx,
		`SELECT id, job_id, kind, target_type, target_id, created_at FROM agent_jobs
		 WHERE server_id = ? AND settled_at IS NULL ORDER BY id LIMIT 200`, serverID)
	if err != nil {
		return []string{"jobs not checked: " + err.Error()}
	}
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.jobID, &p.kind, &p.targetType, &p.targetID, &p.createdAt); err != nil {
			rows.Close()
			return []string{"jobs not checked: " + err.Error()}
		}
		list = append(list, p)
	}
	rows.Close()

	stamp := now.UTC().Format("2006-01-02T15:04:05Z")
	var out []string
	for _, j := range list {
		res, err := agent.Call(ctx, "jobs.get", map[string]any{"id": j.jobID})
		if err != nil {
			if strings.Contains(err.Error(), "not_found") {
				// The agent restarted and forgot it: the outcome is unknown.
				_, _ = t.db.ExecContext(ctx, `UPDATE agent_jobs SET status = 'unknown', settled_at = ? WHERE id = ?`, stamp, j.id)
			} else if created, perr := time.Parse("2006-01-02T15:04:05Z", j.createdAt); perr == nil && created.Before(now.Add(-jobSettleWindow)) {
				_, _ = t.db.ExecContext(ctx, `UPDATE agent_jobs SET settled_at = ? WHERE id = ?`, stamp, j.id)
			}
			continue
		}
		status, errText := jobStatusOf(res)
		if status != "succeeded" && status != "failed" {
			continue
		}
		if _, err := t.db.ExecContext(ctx, `UPDATE agent_jobs SET status = ? WHERE id = ?`, status, j.id); err != nil {
			continue
		}
		t.settle(ctx, serverID, j.id, j.jobID, j.kind, j.targetType, j.targetID, status, errText, stamp, notify)
		out = append(out, fmt.Sprintf("Job %s (%s): %s.", j.jobID, j.kind, status))
	}
	return out
}

// settle records a finished job once and notifies about it: always on
// failure, on success unless it is a quick job.
func (t *JobTracker) settle(ctx context.Context, serverID, id int64, jobID, kind, targetType, targetID, status, errText, stamp string, notify JobNotify) {
	r, err := t.db.ExecContext(ctx, `UPDATE agent_jobs SET settled_at = ? WHERE id = ? AND settled_at IS NULL`, stamp, id)
	if err != nil {
		return
	}
	if n, _ := r.RowsAffected(); n == 0 || notify == nil {
		return // recorded by another request or pass
	}
	failed := status == "failed"
	if !failed {
		for _, p := range jobQuietOnSuccess {
			if strings.HasPrefix(kind, p) {
				return
			}
		}
	}
	var websiteID, ownerID sql.NullInt64
	link := ""
	switch targetType {
	case "website":
		var siteID int64
		if err := t.db.QueryRowContext(ctx, `SELECT id FROM websites WHERE id = ? AND server_id = ?`, targetID, serverID).Scan(&siteID); err != nil {
			return // the website is gone
		}
		websiteID = sql.NullInt64{Int64: siteID, Valid: true}
		link = "/sites/" + strconv.FormatInt(siteID, 10)
	case "database":
		err := t.db.QueryRowContext(ctx, `SELECT website_id, owner_id FROM databases WHERE id = ?`, targetID).Scan(&websiteID, &ownerID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return
		}
	}
	severity, title, body := "success", kind+" finished", ""
	if failed {
		severity, title, body = "error", kind+" failed", errText
		if body == "" {
			body = "The job failed. Open its log for details."
		}
	}
	notify(ctx, "job:"+strconv.FormatInt(serverID, 10)+":"+jobID, websiteID, ownerID, severity, title, body, link)
}

// jobStatusOf reads a jobs.get reply: the job's status and its error.
func jobStatusOf(raw []byte) (status, errText string) {
	var o struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return o.Status, o.Error
	}
	return "", ""
}
