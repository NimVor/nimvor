package hosting

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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
		SELECT s.id, s.name, s.kind, s.url, s.status, s.last_seen_at, s.agent_version,
		       (SELECT COUNT(*) FROM websites w WHERE w.server_id = s.id) AS websites
		FROM servers s ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		var s Server
		if err := rows.Scan(&s.ID, &s.Name, &s.Kind, &s.URL, &s.Status, &s.LastSeenAt, &s.AgentVersion, &s.Websites); err != nil {
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

// ServerInput is the payload for adding a managed remote server.
type ServerInput struct {
	Name string
	URL  string
	Kind string // "local" or "remote"
}

// Add inserts a new remote server row. Mirrors ServerService::add. The actual
// agent token exchange is not implemented; this is a minimal scaffold that
// records name/url/kind so the servers list works.
func (r *ServerRepository) Add(ctx context.Context, in ServerInput) (int64, error) {
	name := strings.TrimSpace(in.Name)
	url := strings.TrimSpace(in.URL)
	kind := in.Kind
	if kind != "remote" {
		kind = "remote"
	}
	if name == "" {
		return 0, fmt.Errorf("name is required")
	}
	if url == "" {
		return 0, fmt.Errorf("url is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO servers (name, kind, url, token_enc, fingerprint, status, created_at)
		 VALUES (?, ?, ?, '', '', 'unknown', ?)`,
		name, kind, url, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Remove deletes a managed server row (idempotent if not the local server).
// Mirrors ServerService::remove.
func (r *ServerRepository) Remove(ctx context.Context, id int64) error {
	if id == 1 {
		return ErrServerSelf
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM servers WHERE id = ? AND kind <> 'local'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrServerNotFound
	}
	return nil
}

// Check pings a server by attempting a trivial SELECT on its row; a real
// implementation would call the agent. Returns true when the row exists.
// Mirrors ServerService::check stub.
func (r *ServerRepository) Check(ctx context.Context, id int64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM servers WHERE id = ?`, id).Scan(&n)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
