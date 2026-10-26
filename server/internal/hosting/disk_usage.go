package hosting

// This file ports the disk-usage service (DiskUsageService): the agent
// measures a website folder in a background job; the latest result per
// folder is kept in disk_usage_scans and settled from the job's status on
// each page view. The panel only orchestrates — the measurement, and all
// confinement to the website folder, happen in the agent.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
)

var (
	ErrDiskUsageRunning  = errors.New("This folder is being measured already.")
	ErrDiskUsageBadPath  = errors.New("That is not a folder inside the website folder.")
	ErrDiskUsageLongPath = errors.New("The folder path is too long.")
)

// DiskUsageCleanPath is the PHP cleanPath: a folder inside the website
// folder ("" for the website folder itself).
func DiskUsageCleanPath(path string) (string, error) {
	path = strings.ReplaceAll(path, "\\", "/")
	path = strings.Trim(path, " /")
	if path == "" {
		return "", nil
	}
	for _, p := range strings.Split(path, "/") {
		if p == "" || p == "." || p == ".." || strings.Contains(p, "\x00") {
			return "", ErrDiskUsageBadPath
		}
	}
	if len(path) > 1024 {
		return "", ErrDiskUsageLongPath
	}
	return path, nil
}

// DiskUsageEntry is one measured folder of the result.
type DiskUsageEntry struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"` // dir, files or mail
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
}

// DiskUsageResult is the cleaned result of a finished scan.
type DiskUsageResult struct {
	Entries   []DiskUsageEntry `json:"entries"`
	Bytes     int64            `json:"bytes"`
	Partial   bool             `json:"partial"`
	Truncated int64            `json:"truncated"`
}

// DiskUsageScan is one row of disk_usage_scans.
type DiskUsageScan struct {
	ID         int64
	WebsiteID  int64
	Path       string
	JobID      string
	Status     string
	Result     DiskUsageResult
	Error      string
	StartedAt  string
	FinishedAt *string
}

// DiskUsageService reads and settles the scan table.
type DiskUsageService struct{ db *sql.DB }

func NewDiskUsageService(db *sql.DB) *DiskUsageService { return &DiskUsageService{db: db} }

// Running is the running scan of a folder, if any (the newest).
func (s *DiskUsageService) Running(ctx context.Context, websiteID int64, path string) (*DiskUsageScan, error) {
	return s.scan(ctx, websiteID, path, "status = 'running'")
}

// Latest is the latest finished scan of a folder, with its entries.
func (s *DiskUsageService) Latest(ctx context.Context, websiteID int64, path string) (*DiskUsageScan, error) {
	return s.scan(ctx, websiteID, path, "status <> 'running'")
}

func (s *DiskUsageService) scan(ctx context.Context, websiteID int64, path, where string) (*DiskUsageScan, error) {
	var sc DiskUsageScan
	var resultJSON string
	var finishedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT id, website_id, path, job_id, status, result, error, started_at, finished_at
		FROM disk_usage_scans WHERE website_id = ? AND path = ? AND `+where+` ORDER BY id DESC LIMIT 1`,
		websiteID, path).Scan(&sc.ID, &sc.WebsiteID, &sc.Path, &sc.JobID, &sc.Status, &resultJSON, &sc.Error, &sc.StartedAt, &finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if finishedAt.Valid {
		sc.FinishedAt = &finishedAt.String
	}
	if err := json.Unmarshal([]byte(resultJSON), &sc.Result); err != nil {
		sc.Result = DiskUsageResult{Entries: []DiskUsageEntry{}}
	}
	return &sc, nil
}

// Start begins measuring a folder (the agent's background job) and records
// the scan as running; ErrDiskUsageRunning when one is already in flight.
// Mirrors DiskUsageService::scan.
func (s *DiskUsageService) Start(ctx context.Context, serverID, websiteID int64, domain, path string, agent AgentCaller) (string, error) {
	path, err := DiskUsageCleanPath(path)
	if err != nil {
		return "", err
	}
	if running, err := s.Running(ctx, websiteID, path); err != nil {
		return "", err
	} else if running != nil {
		return "", ErrDiskUsageRunning
	}
	res, err := agent.Call(ctx, "sites.disk_usage", map[string]any{"domain": domain, "path": path})
	if err != nil {
		return "", err
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return "", err
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO disk_usage_scans (server_id, website_id, path, job_id, status, started_at)
		VALUES (?, ?, ?, ?, 'running', ?)`,
		serverID, websiteID, path, out.JobID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return "", err
	}
	return out.JobID, nil
}

// Refresh settles this website's running scans from the agent's job status
// and prunes old finished scans (the five latest per folder are kept).
// Mirrors DiskUsageService::refresh: a job that is gone (agent restarted)
// is failed; anything else waits for the next view.
func (s *DiskUsageService) Refresh(ctx context.Context, serverID, websiteID int64, ag AgentCaller) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, job_id FROM disk_usage_scans WHERE website_id = ? AND server_id = ? AND status = 'running'`,
		websiteID, serverID)
	if err != nil {
		return err
	}
	type pending struct {
		id    int64
		jobID string
	}
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.jobID); err != nil {
			rows.Close()
			return err
		}
		todo = append(todo, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range todo {
		res, err := ag.Call(ctx, "jobs.get", map[string]any{"id": p.jobID})
		if err != nil {
			var ae *agent.Error
			if errors.As(err, &ae) && ae.Code == "not_found" {
				s.finish(ctx, p.id, "failed", DiskUsageResult{}, "The scan was interrupted (the agent restarted).")
			}
			continue
		}
		var job struct {
			Status string          `json:"status"`
			Error  string          `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(res, &job); err != nil {
			continue
		}
		switch job.Status {
		case "failed":
			msg := job.Error
			if msg == "" {
				msg = "The scan failed."
			}
			s.finish(ctx, p.id, "failed", DiskUsageResult{}, msg)
		case "succeeded":
			var result struct {
				Entries []struct {
					Name  string `json:"name"`
					Kind  string `json:"kind"`
					Bytes int64  `json:"bytes"`
					Files int64  `json:"files"`
				} `json:"entries"`
				Bytes     int64 `json:"bytes"`
				Partial   bool  `json:"partial"`
				Truncated int64 `json:"truncated"`
			}
			if len(job.Result) > 0 {
				_ = json.Unmarshal(job.Result, &result)
			}
			clean := DiskUsageResult{Entries: []DiskUsageEntry{}, Bytes: result.Bytes, Partial: result.Partial, Truncated: result.Truncated}
			for i, e := range result.Entries {
				if i == 300 {
					break
				}
				kind := e.Kind
				if kind != "dir" && kind != "files" && kind != "mail" {
					kind = "dir"
				}
				name := e.Name
				if r := []rune(name); len(r) > 255 {
					name = string(r[:255])
				}
				b, f := e.Bytes, e.Files
				if b < 0 {
					b = 0
				}
				if f < 0 {
					f = 0
				}
				clean.Entries = append(clean.Entries, DiskUsageEntry{Name: name, Kind: kind, Bytes: b, Files: f})
			}
			if clean.Bytes < 0 {
				clean.Bytes = 0
			}
			if clean.Truncated < 0 {
				clean.Truncated = 0
			}
			s.finish(ctx, p.id, "done", clean, "")
		}
	}
	// Keep the five latest scans of each folder.
	_, err = s.db.ExecContext(ctx, `
		DELETE FROM disk_usage_scans WHERE website_id = ? AND status <> 'running'
		AND id NOT IN (SELECT id FROM disk_usage_scans d
			WHERE d.website_id = disk_usage_scans.website_id AND d.path = disk_usage_scans.path
			ORDER BY id DESC LIMIT 5)`, websiteID)
	return err
}

// finish settles one scan (only while it is still running) with the
// cleaned result JSON. Mirrors DiskUsageService::finish.
func (s *DiskUsageService) finish(ctx context.Context, id int64, status string, result DiskUsageResult, msg string) {
	if msgR := []rune(msg); len(msgR) > 1000 {
		msg = string(msgR[:1000])
	}
	resultJSON, _ := json.Marshal(result)
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.ExecContext(ctx, `
		UPDATE disk_usage_scans SET status = ?, result = ?, error = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`, status, string(resultJSON), msg, now, id)
}
