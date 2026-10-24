package hosting

// This file ports BackupScheduler (panel/app/Services/BackupScheduler.php, 296 LOC):
// list/create/delete for both website schedules (Websites → site → Backups)
// and the server-wide schedules (Server → Backups → Server configuration).
// The scheduler's run loop (isDue / lastSlot / run / prune) belongs to the
// background-jobs follow-up.

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
	AllSites         bool
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
	LastRunAtStr     string
	LastStatus       string
	CreatedAt        string
}

// ScheduleInput is the website's "Add schedule" form.
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

// ServerScheduleInput is the server page's schedule form.
type ServerScheduleInput struct {
	Scope            string // server | all_sites
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

func NewBackupScheduler(db *sql.DB, serverID int64, audit Auditor) *BackupScheduler {
	return &BackupScheduler{db: db, serverID: serverID, audit: audit}
}

const scheduleColumns = `SELECT sc.id, sc.website_id, sc.kind, COALESCE(sc.all_sites,0), sc.frequency, sc.hour, sc.weekday,
		sc.include_files, sc.include_databases, sc.encrypt, sc.storage_id, st.name,
		sc.keep_local, sc.retention, sc.enabled, sc.last_run_at, sc.last_status, sc.created_at
		FROM backup_schedules sc LEFT JOIN backup_storages st ON st.id = sc.storage_id`

func scanSchedule(row interface{ Scan(...any) error }) (BackupSchedule, error) {
	var (
		sc                             BackupSchedule
		storageName                    sql.NullString
		files, dbs, enc, keep, enabled, allSites int
	)
	if err := row.Scan(&sc.ID, &sc.WebsiteID, &sc.Kind, &allSites, &sc.Frequency, &sc.Hour, &sc.Weekday,
		&files, &dbs, &enc, &sc.StorageID, &storageName,
		&keep, &sc.Retention, &enabled, &sc.LastRunAt, &sc.LastStatus, &sc.CreatedAt); err != nil {
		return sc, err
	}
	sc.AllSites = allSites == 1
	sc.StorageName = storageName.String
	sc.IncludeFiles, sc.IncludeDatabases = files == 1, dbs == 1
	sc.Encrypt, sc.KeepLocal, sc.Enabled = enc == 1, keep == 1, enabled == 1
	if sc.LastRunAt.Valid {
		sc.LastRunAtStr = sc.LastRunAt.String
	}
	return sc, nil
}

// ListSite lists a website's schedules.
func (s *BackupScheduler) ListSite(ctx context.Context, websiteID int64) ([]BackupSchedule, error) {
	rows, err := s.db.QueryContext(ctx, scheduleColumns+` WHERE sc.website_id = ? ORDER BY sc.id`, websiteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupSchedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ListServer lists the server's schedules (PHP list(null)).
func (s *BackupScheduler) ListServer(ctx context.Context) ([]BackupSchedule, error) {
	rows, err := s.db.QueryContext(ctx, scheduleColumns+` WHERE sc.server_id = ? AND (sc.kind = 'server' OR sc.all_sites = 1) ORDER BY sc.id`, s.serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackupSchedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// CreateSite validates and stores a schedule for one website.
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
	id, _ := res.LastInsertId()
	s.audit(ctx, "backup.schedule_create", "website", fmt.Sprintf("%d", site.ID), "success", map[string]any{
		"frequency": in.Frequency, "hour": in.Hour, "retention": in.Retention,
	})
	return id, nil
}

// CreateServer validates and stores a server schedule.
func (s *BackupScheduler) CreateServer(ctx context.Context, in ServerScheduleInput) (int64, error) {
	ve := &ValidationError{}
	all := in.Scope == "all_sites"
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
	if all && !in.IncludeFiles && !in.IncludeDatabases {
		ve.Add("include_files", "Back up the files, the databases or both.")
	}
	if len(ve.Fields) > 0 {
		return 0, ve
	}
	kind := "server"
	allFlag := 0
	if all {
		kind = "site"
		allFlag = 1
	}
	keepLocal := 1
	if in.StorageID.Valid {
		keepLocal = boolInt(in.KeepLocal)
	}
	encrypt := 1
	if all && !in.Encrypt {
		encrypt = 0
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO backup_schedules (server_id, website_id, kind, all_sites, frequency, hour, weekday, include_files, include_databases, encrypt, storage_id, keep_local, retention, enabled, created_at)
		 VALUES (?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		s.serverID, kind, allFlag, in.Frequency, in.Hour, in.Weekday, boolInt(in.IncludeFiles), boolInt(in.IncludeDatabases), encrypt, nullOrZero(in.StorageID), keepLocal, in.Retention, backupNow())
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	target := "server"
	if all {
		target = "all_sites"
	}
	s.audit(ctx, "backup.schedule_create", target, fmt.Sprintf("%d", s.serverID), "success", map[string]any{
		"frequency": in.Frequency, "hour": in.Hour, "retention": in.Retention,
	})
	return id, nil
}

// DeleteSite removes a website's schedule.
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

// DeleteServer removes a server schedule.
func (s *BackupScheduler) DeleteServer(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM backup_schedules WHERE id = ? AND server_id = ? AND (kind = 'server' OR all_sites = 1)`, id, s.serverID)
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
