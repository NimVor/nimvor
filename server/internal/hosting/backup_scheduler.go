package hosting

// This file ports the website (per-site) subset of the panel's
// BackupScheduler (panel/app/Services/BackupScheduler.php). This batch
// only needs the list (the backups tab shows which schedules a website
// has); creating and deleting schedules, plus the scheduler's run loop,
// are a later batch.

import (
	"context"
	"database/sql"
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

// BackupScheduler manages the backup_schedules table.
type BackupScheduler struct {
	db       *sql.DB
	serverID int64
}

// NewBackupScheduler builds the scheduler (serverID is this server).
func NewBackupScheduler(db *sql.DB, serverID int64) *BackupScheduler {
	return &BackupScheduler{db: db, serverID: serverID}
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
			sc        BackupSchedule
			storageName sql.NullString
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
