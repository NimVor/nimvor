package hosting

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The scheduler pass of BackupScheduler (run / runAllSites / isDue / lastSlot /
// prune): start due backups, move all-websites rounds along, and delete the
// oldest completed backups beyond each schedule's retention.

// allSitesParallel is how many websites an all-websites round backs up at once.
const allSitesParallel = 2

// ScheduleRunner is what a pass needs besides the schedules themselves.
type ScheduleRunner struct {
	Backups *BackupService
	Sites   *WebsiteRepository
	Jobs    *JobTracker
	// Notify records a notification (NotificationService::notify).
	Notify func(ctx context.Context, websiteID sql.NullInt64, severity, title, body, link, key string)
	Now    func() time.Time
}

// LastSlot is the most recent scheduled time at or before now.
func LastSlot(s BackupSchedule, now time.Time) time.Time {
	slot := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, 0, 0, 0, now.Location())
	if s.Frequency == "weekly" {
		diff := (int(now.Weekday()) - s.Weekday + 7) % 7
		slot = slot.AddDate(0, 0, -diff)
	}
	if slot.After(now) {
		if s.Frequency == "weekly" {
			slot = slot.AddDate(0, 0, -7)
		} else {
			slot = slot.AddDate(0, 0, -1)
		}
	}
	return slot
}

func parseStamp(v string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}

// IsDue says whether the schedule's latest slot has not been run yet. A new
// schedule waits for its first slot instead of running at once.
func IsDue(s BackupSchedule, now time.Time) bool {
	if !s.Enabled {
		return false
	}
	slot := LastSlot(s, now)
	created, _ := parseStamp(s.CreatedAt)
	if !s.LastRunAt.Valid {
		return !slot.Before(created)
	}
	last, ok := parseStamp(s.LastRunAt.String)
	return !ok || last.Before(slot)
}

func ucfirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Run is one pass; it returns log lines.
func (sc *BackupScheduler) Run(ctx context.Context, r ScheduleRunner) []string {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	_ = r.Backups.Refresh(ctx)
	var log []string
	list, err := sc.ListServerAll(ctx)
	if err != nil {
		return []string{"Backup schedules could not be read: " + err.Error()}
	}
	for _, s := range list {
		if s.AllSites {
			log = append(log, sc.runAllSites(ctx, r, s, now)...)
			continue
		}
		if !IsDue(s, now) {
			continue
		}
		stamp := now.Format(time.RFC3339)
		_, _ = sc.db.ExecContext(ctx, `UPDATE backup_schedules SET last_run_at = ? WHERE id = ?`, stamp, s.ID)
		storage := s.StorageID
		keepLocal := s.KeepLocal
		off := false
		if storage.Valid {
			if active, _ := r.Backups.StorageActive(ctx, storage.Int64); !active {
				storage, keepLocal, off = sql.NullInt64{}, true, true
			}
		}
		label := ucfirst(s.Frequency) + " backup"
		var job, what string
		var runErr error
		if s.Kind == "server" {
			job, runErr = r.Backups.CreateServer(ctx, ServerCreateOptions{Encrypt: s.Encrypt, StorageID: storage, KeepLocal: keepLocal, Label: label, ScheduleID: sql.NullInt64{Int64: s.ID, Valid: true}}, sql.NullInt64{})
			what = "server configuration"
			if runErr == nil {
				_ = r.Jobs.Track(ctx, sc.serverID, job, "Scheduled backup of the server configuration", "server", strconv.FormatInt(sc.serverID, 10), nil)
			}
		} else {
			var site *Website
			site, runErr = r.Sites.FindFull(ctx, sc.serverID, s.WebsiteID.Int64, nil)
			if runErr == nil {
				what = site.Domain
				job, runErr = r.Backups.CreateSite(ctx, site, CreateSiteOptions{Files: s.IncludeFiles, Databases: s.IncludeDatabases, Encrypt: s.Encrypt,
					StorageID: storage, KeepLocal: keepLocal, Label: label, ScheduleID: sql.NullInt64{Int64: s.ID, Valid: true}}, sql.NullInt64{})
			}
			if runErr == nil {
				_ = r.Jobs.Track(ctx, sc.serverID, job, "Scheduled backup of "+what, "website", strconv.FormatInt(site.ID, 10), nil)
			}
		}
		if runErr != nil {
			msg := problem(runErr)
			_, _ = sc.db.ExecContext(ctx, `UPDATE backup_schedules SET last_status = ? WHERE id = ?`, "failed: "+truncate(msg, 200), s.ID)
			if sc.audit != nil {
				sc.audit(ctx, "backup.scheduled", "backup_schedule", strconv.FormatInt(s.ID, 10), "failure", map[string]any{"error": msg})
			}
			link, site := "/backups", sql.NullInt64{}
			if s.Kind != "server" && s.WebsiteID.Valid {
				var n int
				_ = sc.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM websites WHERE id = ?`, s.WebsiteID.Int64).Scan(&n)
				if n > 0 {
					site = s.WebsiteID
				}
				link = fmt.Sprintf("/sites/%d/backups", s.WebsiteID.Int64)
			}
			if r.Notify != nil {
				r.Notify(ctx, site, "error", "Scheduled backup could not start", msg, link, fmt.Sprintf("schedule:%d:%s", s.ID, now.Format("200601021504")))
			}
			log = append(log, fmt.Sprintf("Schedule %d failed: %s", s.ID, msg))
			continue
		}
		_, _ = sc.db.ExecContext(ctx, `UPDATE backup_schedules SET last_status = 'started' WHERE id = ?`, s.ID)
		line := fmt.Sprintf("Started backup of %s (job %s).", what, job)
		if off {
			line += " Its remote storage is turned off: kept on this server only."
		}
		log = append(log, line)
	}
	return append(log, sc.Prune(ctx, r)...)
}

// ListServerAll is every schedule of this server (websites' and the server's).
func (sc *BackupScheduler) ListServerAll(ctx context.Context) ([]BackupSchedule, error) {
	rows, err := sc.db.QueryContext(ctx, scheduleColumns+` WHERE sc.server_id = ? ORDER BY sc.id`, sc.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupSchedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// runAllSites: when due a round starts; each pass then starts the backups of
// the websites not yet backed up in this round, allSitesParallel at a time,
// until every website has one.
func (sc *BackupScheduler) runAllSites(ctx context.Context, r ScheduleRunner, s BackupSchedule, now time.Time) []string {
	if IsDue(s, now) {
		s.LastRunAt = sql.NullString{String: now.Format(time.RFC3339), Valid: true}
		s.LastStatus = "running"
		_, _ = sc.db.ExecContext(ctx, `UPDATE backup_schedules SET last_run_at = ?, last_status = 'running' WHERE id = ?`, s.LastRunAt.String, s.ID)
	}
	if !s.LastRunAt.Valid || !strings.HasPrefix(s.LastStatus, "running") {
		return nil
	}
	started, ok := parseStamp(s.LastRunAt.String)
	if !ok {
		return nil
	}
	since := started.UTC().Format(time.RFC3339)
	var running int
	_ = sc.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backups WHERE schedule_id = ? AND status = 'running'`, s.ID).Scan(&running)
	var todo []int64
	rows, err := sc.db.QueryContext(ctx,
		`SELECT w.id FROM websites w WHERE w.server_id = ? AND NOT EXISTS (SELECT 1 FROM backups b WHERE b.schedule_id = ? AND b.website_id = w.id AND b.created_at >= ?) ORDER BY w.id`,
		sc.serverID, s.ID, since)
	if err != nil {
		return []string{fmt.Sprintf("All-websites schedule %d: %v", s.ID, err)}
	}
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			todo = append(todo, id)
		}
	}
	rows.Close()
	if len(todo) == 0 && running == 0 {
		by := map[string]int{}
		if rr, err := sc.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM backups WHERE schedule_id = ? AND created_at >= ? GROUP BY status`, s.ID, since); err == nil {
			for rr.Next() {
				var st string
				var n int
				if rr.Scan(&st, &n) == nil {
					by[st] = n
				}
			}
			rr.Close()
		}
		status := fmt.Sprintf("%d website(s) backed up", by["completed"])
		if by["failed"] > 0 {
			status += fmt.Sprintf(", %d failed", by["failed"])
		}
		_, _ = sc.db.ExecContext(ctx, `UPDATE backup_schedules SET last_status = ? WHERE id = ?`, status, s.ID)
		if by["failed"] > 0 && r.Notify != nil {
			r.Notify(ctx, sql.NullInt64{}, "error", fmt.Sprintf("Scheduled backup of every website: %d failed", by["failed"]),
				"See the backups of the websites concerned.", "/backups", fmt.Sprintf("schedule:%d:%s", s.ID, since[:16]))
		}
		return []string{fmt.Sprintf("All-websites schedule %d: round finished.", s.ID)}
	}
	storage, keepLocal := s.StorageID, s.KeepLocal
	if storage.Valid {
		if active, _ := r.Backups.StorageActive(ctx, storage.Int64); !active {
			storage, keepLocal = sql.NullInt64{}, true
		}
	}
	label := ucfirst(s.Frequency) + " backup (every website)"
	var log []string
	batch := todo
	if n := allSitesParallel - running; n < len(batch) {
		if n < 0 {
			n = 0
		}
		batch = batch[:n]
	}
	for _, id := range batch {
		site, err := r.Sites.FindFull(ctx, sc.serverID, id, nil)
		if err != nil || site == nil {
			continue
		}
		job, err := r.Backups.CreateSite(ctx, site, CreateSiteOptions{Files: s.IncludeFiles, Databases: s.IncludeDatabases, Encrypt: s.Encrypt,
			StorageID: storage, KeepLocal: keepLocal, Label: label, ScheduleID: sql.NullInt64{Int64: s.ID, Valid: true}}, sql.NullInt64{})
		if err != nil {
			// Recorded as a failed backup of this round, so the round moves on.
			stamp := time.Now().UTC().Format(time.RFC3339)
			_, _ = sc.db.ExecContext(ctx,
				`INSERT INTO backups (server_id, website_id, schedule_id, kind, domain, label, status, error, created_at, finished_at) VALUES (?, ?, ?, 'site', ?, ?, 'failed', ?, ?, ?)`,
				sc.serverID, site.ID, s.ID, site.Domain, label, truncate(problem(err), 500), stamp, stamp)
			log = append(log, fmt.Sprintf("Backup of %s could not start: %s", site.Domain, problem(err)))
			continue
		}
		_ = r.Jobs.Track(ctx, sc.serverID, job, "Scheduled backup of "+site.Domain, "website", strconv.FormatInt(site.ID, 10), nil)
		log = append(log, fmt.Sprintf("Started backup of %s (job %s).", site.Domain, job))
	}
	return log
}

// Prune deletes completed backups beyond each schedule's retention (kept per
// website for an all-websites schedule).
func (sc *BackupScheduler) Prune(ctx context.Context, r ScheduleRunner) []string {
	list, err := sc.ListServerAll(ctx)
	if err != nil {
		return nil
	}
	var log []string
	type old struct{ id, site int64 }
	for _, s := range list {
		var olds []old
		var rows *sql.Rows
		if s.AllSites {
			rows, err = sc.db.QueryContext(ctx,
				`SELECT id, website_id FROM (SELECT id, website_id, ROW_NUMBER() OVER (PARTITION BY website_id ORDER BY id DESC) AS n FROM backups WHERE schedule_id = ? AND status = 'completed') WHERE n > ?`,
				s.ID, s.Retention)
		} else {
			rows, err = sc.db.QueryContext(ctx,
				`SELECT id, COALESCE(website_id, 0) FROM backups WHERE schedule_id = ? AND status = 'completed' ORDER BY id DESC LIMIT -1 OFFSET ?`, s.ID, s.Retention)
		}
		if err != nil {
			continue
		}
		for rows.Next() {
			var o old
			var site sql.NullInt64
			if rows.Scan(&o.id, &site) == nil {
				o.site = site.Int64
				olds = append(olds, o)
			}
		}
		rows.Close()
		for _, o := range olds {
			var b *Backup
			var ferr error
			if s.Kind == "server" && o.site == 0 {
				b, ferr = r.Backups.FindServer(ctx, o.id)
			} else {
				b, ferr = r.Backups.FindSite(ctx, o.site, o.id)
			}
			if ferr != nil || b == nil {
				continue
			}
			if derr := r.Backups.Delete(ctx, b); derr != nil {
				log = append(log, fmt.Sprintf("Retention: could not delete backup %s: %s", b.BackupID, problem(derr)))
				continue
			}
			log = append(log, fmt.Sprintf("Retention: deleted backup %s.", b.BackupID))
		}
	}
	return log
}
