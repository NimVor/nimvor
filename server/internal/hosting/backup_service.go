package hosting

// This file ports the website (per-site) subset of the panel's
// BackupService (panel/app/Services/BackupService.php): the data side of
// the per-website backups tab (Websites → site → Backups). It starts
// backups, updates running ones from their agent jobs, lists and finds
// rows, restores parts, deletes, and streams stored parts to the browser.
// The server-wide backups page, remote-storage administration, the
// encryption key and account backups are separate areas (later batches).
//
// A backup row starts with status 'running' and the agent's job id.
// Refresh() polls jobs.get for every running row and promotes it to
// 'completed' (storing the agent's manifest, size and whether it is
// encrypted/kept local) or 'failed'. The parts (the files tarball plus one
// SQL dump per database) are read from the stored manifest.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
)

// BackupPart is one part of a backup (a manifest.files entry).
type BackupPart struct {
	Name     string
	Kind     string // files | database | config
	Database string
	Size     int64
}

// Backup is one backups row, hydrated the way the panel's
// BackupService::hydrate does: the manifest decoded, the parts extracted,
// local/encrypted coerced to bools.
type Backup struct {
	ID          int64
	WebsiteID   sql.NullInt64
	ScheduleID  sql.NullInt64
	Kind        string
	Domain      string
	BackupID    string
	Label       string
	Manifest    map[string]any
	Size        int64
	Encrypted   bool
	StorageID   sql.NullInt64
	StorageName string
	Local       bool
	Status      string
	Error       string
	JobID       string
	CreatedAt   string
	FinishedAt  sql.NullString
	Parts       []BackupPart
}

// CreateSiteOptions is what the "Back up now" form submits.
type CreateSiteOptions struct {
	Files     bool
	Databases bool
	Encrypt   bool
	// StorageID picks a remote storage for an extra copy; administrators
	// only (remote storage is a server-wide setting).
	StorageID sql.NullInt64
	KeepLocal bool
	Label     string
	// ScheduleID is set when the scheduler (not a person) starts the run.
	ScheduleID sql.NullInt64
}

// BackupService manages this server's backups table for websites.
type BackupService struct {
	db       *sql.DB
	agent    AgentCaller
	crypto   *crypto.Crypto
	serverID int64
	audit    Auditor
}

// NewBackupService builds the service.
func NewBackupService(db *sql.DB, ag AgentCaller, c *crypto.Crypto, serverID int64, audit Auditor) *BackupService {
	return &BackupService{db: db, agent: ag, crypto: c, serverID: serverID, audit: audit}
}

// backupColumns is the SELECT list every query here uses (with the
// storage name), in scanBackupRow's scan order.
const backupColumns = `b.id, b.website_id, b.schedule_id, b.kind, b.domain, b.backup_id, b.label,
	b.manifest, b.size, b.encrypted, b.storage_id, s.name, b.local, b.status, b.error, b.job_id, b.created_at, b.finished_at`

// scanBackupRow decodes one row into a hydrated Backup.
func scanBackupRow(row interface{ Scan(...any) error }) (Backup, error) {
	var (
		b           Backup
		website     sql.NullInt64
		sched       sql.NullInt64
		storage     sql.NullInt64
		manifest    string
		finished    sql.NullString
		storageName sql.NullString
		encrypted   int
		local       int
	)
	if err := row.Scan(&b.ID, &website, &sched, &b.Kind, &b.Domain, &b.BackupID, &b.Label,
		&manifest, &b.Size, &encrypted, &storage, &storageName, &local,
		&b.Status, &b.Error, &b.JobID, &b.CreatedAt, &finished); err != nil {
		return b, err
	}
	b.WebsiteID, b.ScheduleID, b.StorageID, b.FinishedAt = website, sched, storage, finished
	b.StorageName = storageName.String
	b.Encrypted = encrypted == 1
	b.Local = local == 1
	b.Manifest = map[string]any{}
	if json.Unmarshal([]byte(manifest), &b.Manifest) != nil {
		b.Manifest = map[string]any{}
	}
	b.Parts = []BackupPart{}
	if files, ok := b.Manifest["files"].([]any); ok {
		for _, f := range files {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			p := BackupPart{Name: asStr(fm["name"]), Kind: asStr(fm["kind"]), Database: asStr(fm["database"])}
			if n, ok := fm["size"].(float64); ok {
				p.Size = int64(n)
			}
			b.Parts = append(b.Parts, p)
		}
	}
	return b, nil
}

func asStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ListSite lists a website's backups, newest first (PHP's list($site)).
func (s *BackupService) ListSite(ctx context.Context, websiteID int64) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+backupColumns+` FROM backups b LEFT JOIN backup_storages s ON s.id = b.storage_id
		 WHERE b.website_id = ? ORDER BY b.id DESC LIMIT 100`, websiteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backup
	for rows.Next() {
		b, err := scanBackupRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// FindSite looks up one of a website's backups (nil = not found).
func (s *BackupService) FindSite(ctx context.Context, websiteID, id int64) (*Backup, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+backupColumns+` FROM backups b LEFT JOIN backup_storages s ON s.id = b.storage_id
		 WHERE b.id = ? AND b.website_id = ?`, id, websiteID)
	b, err := scanBackupRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// SiteDatabases lists the database names linked to a website (PHP's
// siteDatabases): what "Back up the databases" would cover.
func (s *BackupService) SiteDatabases(ctx context.Context, websiteID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM databases WHERE website_id = ? ORDER BY name`, websiteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// CreateSite starts a website backup (agent job) and stores the running
// row. Mirrors createSite; returns the agent job id.
func (s *BackupService) CreateSite(ctx context.Context, site *Website, opts CreateSiteOptions, actorID sql.NullInt64) (string, error) {
	dbs := []string{}
	if opts.Databases {
		d, err := s.SiteDatabases(ctx, site.ID)
		if err != nil {
			return "", err
		}
		dbs = d
	}
	if !opts.Files && len(dbs) == 0 {
		return "", &UserError{What: "Nothing to back up.", Why: "Choose the website files, or link a database to this website."}
	}
	label := trimLabel(opts.Label)
	params := map[string]any{
		"domain":    site.Domain,
		"user":      site.SystemUser,
		"root":      site.Root,
		"files":     opts.Files,
		"databases": dbs,
		"encrypt":   opts.Encrypt,
		"label":     label,
		"meta":      map[string]any{"website": Spec(site, false), "panel_version": 1},
	}
	if opts.StorageID.Valid {
		remote, err := s.ActiveStorageConfig(ctx, opts.StorageID.Int64)
		if err != nil {
			return "", err
		}
		params["remote"] = remote
		params["keep_local"] = opts.KeepLocal
	}
	var res struct {
		JobID string `json:"job_id"`
	}
	if err := s.callAgent(ctx, "backup.create", params, nil, &res); err != nil {
		return "", err
	}
	enc := 0
	if opts.Encrypt {
		enc = 1
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO backups (server_id, website_id, schedule_id, kind, domain, label, encrypted, storage_id, status, job_id, user_id, created_at)
		 VALUES (?, ?, ?, 'site', ?, ?, ?, ?, 'running', ?, ?, ?)`,
		s.serverID, site.ID, nullOrZero(opts.ScheduleID), site.Domain, label, enc, nullOrZero(opts.StorageID), res.JobID, nullOrZero(actorID), backupNow()); err != nil {
		return "", err
	}
	s.audit(ctx, "backup.create", "website", fmt.Sprintf("%d", site.ID), "success", map[string]any{
		"domain": site.Domain, "files": opts.Files, "databases": dbs, "remote": opts.StorageID.Valid,
	})
	return res.JobID, nil
}

// RestoreSite restores selected parts of a completed website backup
// (agent job). Mirrors restore; returns the agent job id.
func (s *BackupService) RestoreSite(ctx context.Context, site *Website, b *Backup, files bool, databases []string, confirm string) (string, error) {
	if b.Status != "completed" || b.Kind != "site" {
		return "", &UserError{What: "Only completed website backups can be restored."}
	}
	if confirm != site.Domain {
		return "", &UserError{What: "Type the domain name to confirm the restore."}
	}
	available := map[string]bool{}
	for _, p := range b.Parts {
		if p.Kind == "database" {
			available[p.Database] = true
		}
	}
	dm := map[string]string{}
	for _, d := range databases {
		if !available[d] {
			return "", &UserError{What: "The backup does not contain database " + d + "."}
		}
		dm[d] = d
	}
	if !files && len(dm) == 0 {
		return "", &UserError{What: "Choose what to restore."}
	}
	params := map[string]any{
		"domain": site.Domain, "user": site.SystemUser, "root": site.Root,
		"id": b.BackupID, "files": files, "databases": dm, "safety_backup": files,
	}
	if b.StorageID.Valid {
		remote, err := s.StorageConfig(ctx, b.StorageID.Int64)
		if err != nil {
			return "", err
		}
		params["remote"] = remote
	}
	var res struct {
		JobID string `json:"job_id"`
	}
	if err := s.callAgent(ctx, "backup.restore", params, nil, &res); err != nil {
		return "", err
	}
	s.audit(ctx, "backup.restore", "website", fmt.Sprintf("%d", site.ID), "success", map[string]any{
		"domain": site.Domain, "backup": b.BackupID, "files": files, "databases": dm,
	})
	return res.JobID, nil
}

// Delete removes a backup: its agent copy (local and, when the row points
// at a storage that still exists, the remote one) and the row. Mirrors
// delete.
func (s *BackupService) Delete(ctx context.Context, b *Backup) error {
	if b.Status == "running" {
		return &UserError{What: "This backup is still running."}
	}
	if b.Status == "completed" {
		params := map[string]any{}
		if b.Kind == "server" {
			params["kind"] = "server"
		} else {
			params["domain"] = b.Domain
		}
		params["id"] = b.BackupID
		if b.StorageID.Valid {
			if remote, err := s.StorageConfig(ctx, b.StorageID.Int64); err == nil {
				params["remote"] = remote
			}
			// storage removed: only the local copy can be deleted
		}
		if _, err := s.agent.Call(ctx, "backup.delete", params, 300*time.Second); err != nil {
			var ae *agent.Error
			if !(errors.As(err, &ae) && ae.Code == "not_found") {
				return err
			}
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM backups WHERE id = ?`, b.ID); err != nil {
		return err
	}
	target, id := "website", ""
	if b.WebsiteID.Valid {
		id = fmt.Sprintf("%d", b.WebsiteID.Int64)
	} else {
		target = "server"
	}
	s.audit(ctx, "backup.delete", target, id, "success", map[string]any{"backup": b.BackupID, "domain": b.Domain})
	return nil
}

// BackupChunk is one decoded chunk of a stored part.
type BackupChunk struct {
	Data []byte
	Size int64
	EOF  bool
}

// Download starts streaming one stored part: it validates the part, makes
// the first agent call and returns the first chunk plus a function that
// fetches further chunks by byte offset (the handler streams them with the
// same loop the PHP stream() uses). Mirrors download.
func (s *BackupService) Download(ctx context.Context, b *Backup, file string) (*BackupChunk, func(int64) (*BackupChunk, error), error) {
	found := false
	for _, p := range b.Parts {
		if p.Name == file {
			found = true
			break
		}
	}
	if !found || !b.Local {
		why := ""
		if !b.Local {
			why = "This backup is kept in remote storage only; download it from there."
		}
		return nil, nil, &UserError{What: "That file is not stored on this server.", Why: why}
	}
	kind, domain := "", ""
	if b.Kind == "server" {
		kind = "server"
	} else {
		domain = b.Domain
	}
	next := func(offset int64) (*BackupChunk, error) {
		var out struct {
			Data string `json:"data"`
			Size int64  `json:"size"`
			EOF  bool   `json:"eof"`
		}
		if err := s.callAgent(ctx, "backup.download", map[string]any{"kind": kind, "domain": domain, "id": b.BackupID, "file": file, "offset": offset}, durPtr(120*time.Second), &out); err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(out.Data)
		if err != nil {
			return nil, &UserError{What: "The backup part could not be read."}
		}
		return &BackupChunk{Data: data, Size: out.Size, EOF: out.EOF}, nil
	}
	first, err := next(0)
	if err != nil {
		return nil, nil, err
	}
	target, id := "website", ""
	if b.WebsiteID.Valid {
		id = fmt.Sprintf("%d", b.WebsiteID.Int64)
	} else {
		target = "server"
	}
	s.audit(ctx, "backup.download", target, id, "success", map[string]any{"backup": b.BackupID, "file": file})
	return first, next, nil
}

// StorageConfig rebuilds a stored remote's agent "remote" parameter (PHP's
// storageConfig): the sealed config decrypted, its type defaulted to the
// row's kind.
func (s *BackupService) StorageConfig(ctx context.Context, id int64) (map[string]any, error) {
	var kind, enc string
	err := s.db.QueryRowContext(ctx, `SELECT kind, config_enc FROM backup_storages WHERE id = ?`, id).Scan(&kind, &enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &UserError{What: "The remote storage no longer exists."}
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.crypto.Decrypt(enc)
	if err != nil {
		return nil, &UserError{What: "The remote storage's settings could not be read."}
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(plain, &cfg); err != nil {
		return nil, &UserError{What: "The remote storage's settings could not be read."}
	}
	if _, ok := cfg["type"]; !ok {
		cfg["type"] = kind
	}
	return cfg, nil
}

// storageOffError is what new-backup calls get when the storage is not
// turned on (PHP's activeStorageConfig).
var storageOffError = &UserError{
	What: "That remote storage is turned off.",
	Why:  "Only storages that are turned on receive new backups. Test it and turn it on under Backups → Remote storage, or choose another.",
}

// ActiveStorageConfig is StorageConfig plus the "turned on" check (new
// backups may only go to active storages).
func (s *BackupService) ActiveStorageConfig(ctx context.Context, id int64) (map[string]any, error) {
	var active int
	err := s.db.QueryRowContext(ctx, `SELECT active FROM backup_storages WHERE id = ?`, id).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || active != 1 {
		return nil, storageOffError
	}
	if err != nil {
		return nil, err
	}
	return s.StorageConfig(ctx, id)
}

// Storage is one backup_storages row for the "Copy to" select (admins).
type Storage struct {
	ID     int64
	Name   string
	Kind   string
	Active bool
}

// StorageLabels is the PHP STORAGE_TYPES label map for the select.
var StorageLabels = map[string]string{
	"s3":     "S3-compatible bucket",
	"sftp":   "SFTP server",
	"ftp":    "FTP server (FTPS)",
	"gdrive": "Google Drive",
	"local":  "Local folder (mounted disk or NAS)",
}

// Storages lists the server's storages ordered by name.
func (s *BackupService) Storages(ctx context.Context) ([]Storage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, kind, active FROM backup_storages ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Storage
	for rows.Next() {
		var (
			st     Storage
			active int
		)
		if err := rows.Scan(&st.ID, &st.Name, &st.Kind, &active); err != nil {
			return nil, err
		}
		st.Active = active == 1
		out = append(out, st)
	}
	return out, rows.Err()
}

// Refresh updates every running backup from its agent job (PHP's
// refresh). A lost job (agent restarted) marks the row failed; a finished
// job stores the agent's manifest. The caller treats a refresh as best
// effort and ignores the returned error.
func (s *BackupService) Refresh(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id, job_id FROM backups WHERE server_id = ? AND status = 'running'`, s.serverID)
	if err != nil {
		return err
	}
	type jobRef struct {
		id    int64
		jobID string
	}
	var refs []jobRef
	for rows.Next() {
		var j jobRef
		if err := rows.Scan(&j.id, &j.jobID); err != nil {
			rows.Close()
			return err
		}
		refs = append(refs, j)
	}
	rows.Close()
	for _, j := range refs {
		var job struct {
			Status string          `json:"status"`
			Error  string          `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := s.callAgent(ctx, "jobs.get", map[string]any{"id": j.jobID}, nil, &job); err != nil {
			var ae *agent.Error
			if errors.As(err, &ae) && ae.Code == "not_found" {
				if _, err := s.db.ExecContext(ctx,
					`UPDATE backups SET status = 'failed', error = ?, finished_at = ? WHERE id = ?`,
					"The job was lost (agent restarted).", backupNow(), j.id); err != nil {
					return err
				}
			}
			continue
		}
		switch job.Status {
		case "failed":
			if _, err := s.db.ExecContext(ctx,
				`UPDATE backups SET status = 'failed', error = ?, finished_at = ? WHERE id = ?`,
				trimRunes(job.Error, 500), backupNow(), j.id); err != nil {
				return err
			}
		case "succeeded":
			// The agent's manifest minus its meta (the website spec is the
			// panel's own data and would only bloat the row).
			m := map[string]any{}
			if err := json.Unmarshal(job.Result, &m); err != nil {
				m = map[string]any{}
			}
			delete(m, "meta")
			mj, _ := json.Marshal(m)
			backupID, _ := m["id"].(string)
			size := int64(0)
			if n, ok := m["size"].(float64); ok {
				size = int64(n)
			}
			encrypted := false
			if e, ok := m["encrypted"].(bool); ok {
				encrypted = e
			}
			local := false
			if l, ok := m["local"].(bool); ok {
				local = l
			}
			if _, err := s.db.ExecContext(ctx,
				`UPDATE backups SET status = 'completed', backup_id = ?, manifest = ?, size = ?, encrypted = ?, local = ?, finished_at = ? WHERE id = ?`,
				backupID, string(mj), size, backupBoolInt(encrypted), backupBoolInt(local), backupNow(), j.id); err != nil {
				return err
			}
		}
	}
	return nil
}

// callAgent invokes an action and decodes the envelope's "data" into out
// (nil = ignore the payload).
func (s *BackupService) callAgent(ctx context.Context, action string, params any, timeout *time.Duration, out any) error {
	var (
		raw json.RawMessage
		err error
	)
	if timeout != nil {
		raw, err = s.agent.Call(ctx, action, params, *timeout)
	} else {
		raw, err = s.agent.Call(ctx, action, params)
	}
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	// agent.Client.Call already unwraps the {success,data} envelope: raw is
	// the action's payload.
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// backupNow is the row timestamp (the PHP's gmdate('c')).
func backupNow() string { return time.Now().UTC().Format(time.RFC3339) }

// nullOrZero passes NULL for unset values, 0 otherwise (SQLite treats a
// zero schedule_id as "no schedule" for our purposes only when it is
// NULL, so unset stays NULL).
func nullOrZero(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

func trimLabel(l string) string {
	r := []rune(strings.TrimSpace(l))
	if len(r) > 100 {
		return string(r[:100])
	}
	return string(r)
}

func trimRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func backupBoolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
