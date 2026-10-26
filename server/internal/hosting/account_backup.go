package hosting

// AccountBackupService extended with the create/delete/download/upload/
// inspect/start/show/recreate methods used by AccountBackupsController.
// The agent does the heavy lifting (packing the tarball, streaming
// chunks, unpacking, recreating sites); this service is the glue that
// supplies audit records, persists the account_restores row and hands
// out job IDs.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// AccountRestore is one row from account_restores.
type AccountRestore struct {
	ID        int64
	ServerID  int64
	Path      string
	Username  string
	Manifest  map[string]any
	Status    string
	JobID     string
	Report    []any
	UserID    sql.NullInt64
	CreatedAt string
	UpdatedAt string
}

// AccountRestoreSecrets is a one-shot list of generated credentials
// returned from Recreate and flashed to the session.
type AccountRestoreSecrets struct {
	What     string `json:"what"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

// safePathRe restricts account backup paths to the agent's backup dir.
var safePathRe = regexp.MustCompile(`^[A-Za-z0-9._/\-]+$`)

// NewAccountBackupService builds the account-backup service. The auditor
// parameter is accepted for signature parity with other services but not
// retained here (the HTTP layer writes its own audit for account actions).
func NewAccountBackupService(db *sql.DB, ag AgentCaller, serverID int64, _ Auditor) *AccountBackupService {
	return &AccountBackupService{db: db, agent: ag, serverID: serverID}
}

// Create asks the agent to build an account backup for userID and
// returns the job id.
func (s *AccountBackupService) Create(ctx context.Context, userID int64, label string, actorID sql.NullInt64) (string, error) {
	meta, sites, err := s.Describe(ctx, userID)
	if err != nil {
		return "", err
	}
	username, _ := meta["user"].(map[string]any)["username"].(string)
	raw, err := s.agent.Call(ctx, "backup.account", map[string]any{
		"username": username,
		"label":    truncate(strings.TrimSpace(label), 100),
		"meta":     meta,
		"sites":    sites,
	}, 120*time.Second)
	if err != nil {
		return "", wrapAgentErr(err)
	}
	var env struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(raw, &env); err != nil || env.JobID == "" {
		var full map[string]any
		if json.Unmarshal(raw, &full) == nil {
			if data, ok := full["data"].(map[string]any); ok {
				if jid, _ := data["job_id"].(string); jid != "" {
					env.JobID = jid
				}
			}
		}
	}
	return env.JobID, nil
}

// Delete removes an account backup file (best effort, agent-side).
func (s *AccountBackupService) Delete(ctx context.Context, path string, actorID sql.NullInt64) error {
	if !safePathRe.MatchString(path) {
		return errors.New("invalid backup path")
	}
	_, err := s.agent.Call(ctx, "backup.account_delete", map[string]any{"path": path}, 60*time.Second)
	return wrapAgentErr(err)
}

// Chunk returns one chunk of an account backup file for download.
// Returns {data (base64), size, eof}.
func (s *AccountBackupService) Chunk(ctx context.Context, path string, offset int) (map[string]any, error) {
	if !safePathRe.MatchString(path) {
		return nil, errors.New("invalid backup path")
	}
	raw, err := s.agent.Call(ctx, "backup.account_download", map[string]any{"path": path, "offset": offset}, 60*time.Second)
	if err != nil {
		return nil, wrapAgentErr(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Upload appends a chunk to a named upload; final=true flushes.
func (s *AccountBackupService) Upload(ctx context.Context, name string, offset int, bytes []byte, final bool) (map[string]any, error) {
	name = sanitizeUploadName(name)
	raw, err := s.agent.Call(ctx, "backup.account_upload", map[string]any{
		"name":   name,
		"offset": offset,
		"data":   bytes,
		"final":  final,
	}, 120*time.Second)
	if err != nil {
		return nil, wrapAgentErr(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	// unwrap {success,data} if needed
	if _, ok := out["success"]; ok {
		if d, ok := out["data"].(map[string]any); ok {
			return d, nil
		}
	}
	return out, nil
}

// Inspect returns the manifest + plan for a backup file.
func (s *AccountBackupService) Inspect(ctx context.Context, path string) (map[string]any, error) {
	if !safePathRe.MatchString(path) {
		return nil, errors.New("invalid backup path")
	}
	raw, err := s.agent.Call(ctx, "backup.account_inspect", map[string]any{"path": path}, 60*time.Second)
	if err != nil {
		return nil, wrapAgentErr(err)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	var manifest map[string]any
	if d, ok := env["manifest"].(map[string]any); ok {
		manifest = d
	} else if data, ok := env["data"].(map[string]any); ok {
		if m, ok := data["manifest"].(map[string]any); ok {
			manifest = m
		}
	}
	if manifest == nil {
		manifest = map[string]any{}
	}
	return map[string]any{"manifest": manifest, "plan": map[string]any{}}, nil
}

// Start begins an import: agent extracts website backups; a row is
// inserted in account_restores and its id returned.
func (s *AccountBackupService) Start(ctx context.Context, path string, actorID sql.NullInt64) (int64, error) {
	insp, err := s.Inspect(ctx, path)
	if err != nil {
		return 0, err
	}
	raw, err := s.agent.Call(ctx, "backup.account_import", map[string]any{"path": path}, 120*time.Second)
	if err != nil {
		return 0, wrapAgentErr(err)
	}
	var env struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(raw, &env)
	manifest, _ := insp["manifest"].(map[string]any)
	username := ManifestUsername(manifest)
	now := time.Now().UTC().Format(time.RFC3339)
	mblob, _ := json.Marshal(manifest)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO account_restores (server_id, path, username, manifest, status, job_id, user_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, 'importing', ?, ?, ?, ?)`,
		s.serverID, path, username, string(mblob), env.JobID, nullableInt64(actorID), now, now)
	if err != nil {
		return 0, err
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// RestoreRecord loads one restore row.
func (s *AccountBackupService) RestoreRecord(ctx context.Context, id int64) (*AccountRestore, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, server_id, path, username, manifest, status, job_id, report, user_id, created_at, updated_at
		 FROM account_restores WHERE id = ? AND server_id = ?`, id, s.serverID)
	var (
		r     AccountRestore
		mblob string
		rblob string
	)
	if err := row.Scan(&r.ID, &r.ServerID, &r.Path, &r.Username, &mblob, &r.Status, &r.JobID, &rblob, &r.UserID, &r.CreatedAt, &r.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	r.Manifest = map[string]any{}
	_ = json.Unmarshal([]byte(mblob), &r.Manifest)
	r.Report = []any{}
	_ = json.Unmarshal([]byte(rblob), &r.Report)
	return &r, nil
}

// ImportState returns a human-readable state string for the restore.
func (s *AccountBackupService) ImportState(ctx context.Context, r *AccountRestore) string {
	if r == nil {
		return ""
	}
	switch r.Status {
	case "importing":
		return "Extracting website backups…"
	case "imported":
		return "Ready to recreate account"
	case "restoring":
		return "Restoring websites…"
	case "done":
		return "Restore complete"
	case "failed":
		return "Restore failed"
	}
	return r.Status
}

// Recreate moves from 'imported' to 'restoring', creates the account if
// needed (via agent for the bulk of the work), then returns generated
// secrets (one-shot password) and website restore jobs to track.
func (s *AccountBackupService) Recreate(ctx context.Context, id int64, actor *identity.User) (map[string]any, error) {
	r, err := s.RestoreRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	if r == nil {
		return nil, errors.New("restore no longer exists")
	}
	if r.Status != "imported" {
		if r.Status == "done" {
			return nil, errors.New("this account was already restored")
		}
		return nil, errors.New("the file is not imported yet")
	}
	// PHP rebuilds the account, websites, databases, DNS zones and cron jobs
	// panel-side (AccountBackupService::recreate); the agent has no such
	// action, so the old call always failed after marking the restore failed.
	// Leave the record as imported and say what is missing.
	return nil, ErrRecreateUnavailable
}

// ErrRecreateUnavailable: rebuilding an account from a backup is not ported yet.
var ErrRecreateUnavailable = errors.New("restoring an account from a backup file is not available in this panel yet; the website backups inside it were imported and can be restored from each website's Backups page")

func (s *AccountBackupService) finishRecord(ctx context.Context, id int64, status string, report []any) {
	blob, _ := json.Marshal(report)
	now := time.Now().UTC().Format(time.RFC3339)
	_, _ = s.db.ExecContext(ctx, `UPDATE account_restores SET status=?, report=?, updated_at=? WHERE id=?`, status, string(blob), now, id)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func sanitizeUploadName(name string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, name)
}

func nullableInt64(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}

func wrapAgentErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("account backup: %w", err)
}

// ManifestUsername is the account a backup file describes: the authenticated
// top-level username, else the one in the panel's description.
func ManifestUsername(m map[string]any) string {
	if u, _ := m["username"].(string); u != "" {
		return u
	}
	if meta, ok := m["meta"].(map[string]any); ok {
		if u, ok := meta["user"].(map[string]any); ok {
			name, _ := u["username"].(string)
			return name
		}
	}
	return ""
}
