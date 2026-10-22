package hosting

// This file ports the website (per-site) subset of the panel's
// BackupScheduler (panel/app/Services/BackupScheduler.php): the list
// (the backups tab shows which schedules a website has), creating a
// schedule (validated like the panel: frequency / hour / weekday /
// retention / the storage turned on / files-or-databases) and deleting
// one. The scheduler's run loop (isDue / lastSlot / run / prune) belongs
// to the background-jobs follow-up.

import (
	"context"
	"database/sql"
	"fmt"
)

// BackupSchedule is one backup_schedules row (with the storage name).
type BackupSchedule struct {
	ID               int64
	WebsiteID        sql.NullInt64
	Kind             string // site | server
	Frequency        string // daily | weekly
	Hour             int
	Weekday          int
	IncludeFiles     bool
	IncludeDatabases bool
	Encrypt          bool
	StorageID        sql.NullInt64
	StorageName      string
	KeepLocal        bool
	Retention        int
	Enabled          bool
	LastRunAt        sql.NullString
	LastStatus       string
	CreatedAt        string
}

// ScheduleInput is the website's "Add schedule" form, already parsed to
// the panel's defaults (PHP's $input ?? fallbacks).
type ScheduleInput struct {
	Frequency        string
	Hour             int
	Weekday          int
	Retention        int
	IncludeFiles     bool
	IncludeDatabases bool
	Encrypt          bool
	StorageID        sql.NullInt64
	KeepLocal        bool
}

// BackupScheduler manages the backup_schedules table.
type BackupScheduler struct {
	db       *sql.DB
	serverID int64
	audit    Auditor
}

// NewBackupScheduler builds the scheduler (serverID is this server).
func NewBackupScheduler(db *sql.DB, serverID int64, audit Auditor) *BackupScheduler {
	return &BackupScheduler{db: db, serverID: serverID, audit: audit}
}

// ListSite lists a website's schedules (PHP's list($site)).
func (s *BackupScheduler) ListSite(ctx context.Context, websiteID int64) ([]BackupSchedule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT sc.id, sc.website_id, sc.kind, sc.frequency, sc.hour, sc.weekday,
		 sc.include_files, sc.include_databases, sc.encrypt, sc.storage_id, st.name,
		 sc.keep_local, sc.retention, sc.enabled, sc.last_run_at, sc.last_status, sc.created_at
		 FROM backup_schedules sc LEFT JOIN backup_storages st ON st.id = sc.storage_id
		 WHERE sc.website_id = ? ORDER BY sc.id`, websiteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupSchedule
	for rows.Next() {
		var (
			sc                             BackupSchedule
			storageName                    sql.NullString
			files, dbs, enc, keep, enabled int
		)
		if err := rows.Scan(&sc.ID, &sc.WebsiteID, &sc.Kind, &sc.Frequency, &sc.Hour, &sc.Weekday,
			&files, &dbs, &enc, &sc.StorageID, &storageName,
			&keep, &sc.Retention, &enabled, &sc.LastRunAt, &sc.LastStatus, &sc.CreatedAt); err != nil {
			return nil, err
		}
		sc.StorageName = storageName.String
		sc.IncludeFiles, sc.IncludeDatabases = files == 1, dbs == 1
		sc.Encrypt, sc.KeepLocal, sc.Enabled = enc == 1, keep == 1, enabled == 1
		out = append(out, sc)
	}
	return out, rows.Err()
}

// CreateSite validates and stores a schedule for one website (PHP's
// create($site, $input), site branch). Returns the schedule's id.
func (s *BackupScheduler) CreateSite(ctx context.Context, site *Website, in ScheduleInput) (int64, error) {
	ve := &ValidationError{}
	if in.Frequency != "daily" && in.Frequency != "weekly" {
		ve.Add("frequency", "Choose daily or weekly.")
	}
	if in.Hour < 0 || in.Hour > 23 {
		ve.Add("hour", "Choose an hour between 0 and 23.")
	}
	if in.Weekday < 0 || in.Weekday > 6 {
		ve.Add("weekday", "Choose a weekday.")
	}
	if in.Retention < 1 || in.Retention > 365 {
		ve.Add("retention", "Keep between 1 and 365 backups.")
	}
	if in.StorageID.Valid {
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM backup_storages WHERE id = ? AND active = 1`, in.StorageID.Int64).Scan(&n); err != nil {
			return 0, err
		}
		if n == 0 {
			ve.Add("storage_id", "Choose a storage that is turned on.")
		}
	}
	if !in.IncludeFiles && !in.IncludeDatabases {
		ve.Add("include_files", "Back up the files, the databases or both.")
	}
	if len(ve.Fields) > 0 {
		return 0, ve
	}
	// Without a remote storage the copy always stays on this server.
	keepLocal := 1
	if in.StorageID.Valid {
		keepLocal = boolInt(in.KeepLocal)
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO backup_schedules (server_id, website_id, kind, all_sites, frequency, hour, weekday, include_files, include_databases, encrypt, storage_id, keep_local, retention, enabled, created_at)
		 VALUES (?, ?, 'site', 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		s.serverID, site.ID, in.Frequency, in.Hour, in.Weekday, boolInt(in.IncludeFiles), boolInt(in.IncludeDatabases),
		boolInt(in.Encrypt), nullOrZero(in.StorageID), keepLocal, in.Retention, backupNow())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	s.audit(ctx, "backup.schedule_create", "website", fmt.Sprintf("%d", site.ID), "success", map[string]any{
		"frequency": in.Frequency, "hour": in.Hour, "retention": in.Retention,
	})
	return id, nil
}

// DeleteSite removes a website's schedule (PHP's delete($site, $id)).
// Existing backups are kept.
func (s *BackupScheduler) DeleteSite(ctx context.Context, websiteID, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM backup_schedules WHERE id = ? AND website_id = ?`, id, websiteID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return &UserError{What: "Schedule not found."}
	}
	s.audit(ctx, "backup.schedule_delete", "backup_schedule", fmt.Sprintf("%d", id), "success", nil)
	return nil
}
