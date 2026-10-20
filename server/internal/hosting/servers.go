package hosting

import (
	"context"
	"database/sql"
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
