package hosting

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Server is a managed server for the admin list. The agent token (token_enc)
// is never exposed.
type Server struct {
	ID           int64
	Name         string
	Kind         string
	URL          string
	Status       string
	LastSeenAt   sql.NullString
	AgentVersion string
	Websites     int
	Fingerprint  string
	LastError    string
}

// ServerRepository reads the managed servers.
type ServerRepository struct{ db *sql.DB }

// NewServerRepository wraps a database handle.
func NewServerRepository(db *sql.DB) *ServerRepository { return &ServerRepository{db: db} }

// All lists every managed server with its website count. Viewing servers is a
// staff permission (servers.manage), so there is no tenant scoping. Mirrors the
// listing in ServerService. The encrypted agent token is deliberately omitted.
func (r *ServerRepository) All(ctx context.Context) ([]Server, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.name, s.kind, s.url, s.status, s.last_seen_at, s.agent_version, s.fingerprint, s.last_error,
		       (SELECT COUNT(*) FROM websites w WHERE w.server_id = s.id) AS websites
		FROM servers s ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		var s Server
		if err := rows.Scan(&s.ID, &s.Name, &s.Kind, &s.URL, &s.Status, &s.LastSeenAt, &s.AgentVersion, &s.Fingerprint, &s.LastError, &s.Websites); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ErrServerNotFound is returned when a managed server doesn't exist.
var (
	ErrServerNotFound = errors.New("server not found")
	ErrServerSelf     = errors.New("the local server cannot be removed")
)

// ServerRemoveError is a reason a server cannot be removed, worded for the user.
type ServerRemoveError struct{ Msg string }

func (e *ServerRemoveError) Error() string { return e.Msg }

// FailureState is the state recorded for a failed check: why the agent could
// not be used, never "online" (ServerService::failureState). code is the
// agent error code.
func FailureState(code string) string {
	switch code {
	case "unauthorized":
		return "auth_failed"
	case "agent_pin_mismatch":
		return "certificate_mismatch"
	case "agent_forbidden":
		return "refused"
	case "agent_timeout":
		return "timeout"
	}
	return "offline"
}

// Server returns one server's name and kind (ErrServerNotFound when none).
func (r *ServerRepository) Server(ctx context.Context, id int64) (name, kind string, err error) {
	err = r.db.QueryRowContext(ctx, `SELECT name, kind FROM servers WHERE id = ?`, id).Scan(&name, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrServerNotFound
	}
	return
}

// NameTaken reports whether a server has that name.
func (r *ServerRepository) NameTaken(ctx context.Context, name string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM servers WHERE name = ?`, name).Scan(&n)
	return n > 0, err
}

// AddRemote records a connected remote server: its agent address, the pin of
// its key and its token, encrypted by the caller (ServerService::add).
func (r *ServerRepository) AddRemote(ctx context.Context, name, url, pin, tokenEnc, version string) (int64, error) {
	now := time.Now().UTC().Format("2006-01-02T15:04:05Z")
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO servers (name, kind, url, token_enc, fingerprint, status, last_seen_at, agent_version, created_at)
		 VALUES (?, 'remote', ?, ?, ?, 'online', ?, ?, ?)`, name, url, tokenEnc, pin, now, version, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// RecordCheck stores the outcome of a check: the state, and the version the
// agent reported (empty keeps the stored one on a failure).
func (r *ServerRepository) RecordCheck(ctx context.Context, id int64, state, lastError, version string, reached bool) error {
	if reached {
		_, err := r.db.ExecContext(ctx, `UPDATE servers SET status = ?, last_seen_at = ?, last_error = ?, agent_version = ? WHERE id = ?`,
			state, time.Now().UTC().Format("2006-01-02T15:04:05Z"), lastError, version, id)
		return err
	}
	if len(lastError) > 300 {
		lastError = lastError[:300]
	}
	_, err := r.db.ExecContext(ctx, `UPDATE servers SET status = ?, last_error = ? WHERE id = ?`, state, lastError, id)
	return err
}

// serverOwnedTables hold rows of one server, deleted with it.
var serverOwnedTables = []string{"cron_jobs", "workers", "databases", "database_users", "backups", "backup_schedules",
	"agent_jobs", "discovery_snapshots", "metric_samples", "alerts"}

// Remove forgets a remote server and the records kept for it. A server that
// still has websites in WhaCP stays (ServerService::remove).
func (r *ServerRepository) Remove(ctx context.Context, id int64) error {
	if id == 1 {
		return ErrServerSelf
	}
	_, kind, err := r.Server(ctx, id)
	if err != nil {
		return err
	}
	if kind != "remote" {
		return &ServerRemoveError{"Only remote servers can be removed."}
	}
	var sites int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM websites WHERE server_id = ?`, id).Scan(&sites); err != nil {
		return err
	}
	if sites > 0 {
		return &ServerRemoveError{"This server still has websites in WhaCP. Delete or move them first."}
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range serverOwnedTables {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+t+" WHERE server_id = ?", id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM servers WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
