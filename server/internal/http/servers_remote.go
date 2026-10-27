package http

// Several servers from one panel: the selected server travels in the request
// context, and the agent client every service holds (s.agent) sends each call
// to that server — the local agent over its Unix socket, or a remote agent
// over TLS pinned to its key (ServerContext, ServerService and
// RemoteTransport in the PHP panel).

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// serverSessionKey is the session value holding the selected server.
const serverSessionKey = "_server_id"

// withServer returns ctx working on the server.
func withServer(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxServer, id)
}

// serverIDFrom is the server ctx works on (the local server, 1, by default).
func serverIDFrom(ctx context.Context) int64 {
	if id, ok := ctx.Value(ctxServer).(int64); ok && id > 0 {
		return id
	}
	return 1
}

// serverChoice is a server a user may switch to.
type serverChoice struct {
	ID   int64
	Name string
}

// visibleServers are the servers a user may switch to: all of them for
// "all"-scope roles, otherwise this server plus the servers hosting their (or
// their customers') websites.
func (s *Server) visibleServers(ctx context.Context, u *identity.User) []serverChoice {
	q, args := `SELECT id, name FROM servers ORDER BY id`, []any(nil)
	if u == nil || u.RoleScope != "all" {
		cond, params := hosting.Condition(u, "w.owner_id")
		q = `SELECT id, name FROM servers WHERE id = 1 OR id IN (SELECT w.server_id FROM websites w WHERE ` + cond + `) ORDER BY id`
		args = params
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []serverChoice
	for rows.Next() {
		var c serverChoice
		if rows.Scan(&c.ID, &c.Name) == nil {
			out = append(out, c)
		}
	}
	return out
}

// activeServer is the server the session works on: the one it switched to
// while the user may still use it, otherwise the local server.
func (s *Server) activeServer(ctx context.Context, sess *session.Session, u *identity.User) int64 {
	if u == nil || sess == nil {
		return 1
	}
	id, err := strconv.ParseInt(sess.Get(serverSessionKey), 10, 64)
	if err != nil || id <= 1 {
		return 1
	}
	for _, c := range s.visibleServers(ctx, u) {
		if c.ID == id {
			return id
		}
	}
	return 1
}

// agentRouter sends each call to the agent of the server in its context.
type agentRouter struct {
	s     *Server
	local *agent.Client

	mu      sync.Mutex
	remotes map[int64]remoteEntry
}

type remoteEntry struct {
	key    string
	client *agent.Client
}

func newAgentRouter(s *Server, local *agent.Client) *agentRouter {
	return &agentRouter{s: s, local: local, remotes: map[int64]remoteEntry{}}
}

// slowAgentCall is how long an agent call may take before it is logged: the
// panel's own pages are slow when the agent is.
const slowAgentCall = 2 * time.Second

// Call implements hosting.AgentCaller; calls slower than slowAgentCall are
// logged with their action, so a slow page can be traced to what it waits for.
func (a *agentRouter) Call(ctx context.Context, action string, params any, timeout ...time.Duration) (json.RawMessage, error) {
	start := time.Now()
	raw, err := a.call(ctx, action, params, timeout...)
	if d := time.Since(start); d >= slowAgentCall && a.s.log != nil {
		a.s.log.Warn("slow agent call", "action", action, "server", serverIDFrom(ctx), "ms", d.Milliseconds(), "failed", err != nil)
	}
	return raw, err
}

func (a *agentRouter) call(ctx context.Context, action string, params any, timeout ...time.Duration) (json.RawMessage, error) {
	id := serverIDFrom(ctx)
	if id == 1 {
		return a.local.Call(ctx, action, params, timeout...)
	}
	c, err := a.remote(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return a.local.Call(ctx, action, params, timeout...)
	}
	return c.Call(ctx, action, params, timeout...)
}

// remote is the client of a remote server (nil for a local one).
func (a *agentRouter) remote(ctx context.Context, id int64) (*agent.Client, error) {
	var kind, url, tokenEnc, pin string
	err := a.s.db.QueryRowContext(ctx, `SELECT kind, url, token_enc, fingerprint FROM servers WHERE id = ?`, id).Scan(&kind, &url, &tokenEnc, &pin)
	if err != nil {
		return nil, &agent.Error{Code: "agent_unavailable", Message: "That server does not exist.", Status: 502}
	}
	if kind != "remote" {
		return nil, nil
	}
	key := url + "\x00" + pin + "\x00" + tokenEnc
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.remotes[id]; ok && e.key == key {
		return e.client, nil
	}
	token, err := a.s.crypto.Decrypt(tokenEnc)
	if err != nil {
		return nil, &agent.Error{Code: "agent_unavailable", Message: "The remote server's token cannot be read.", Details: "Add the server again.", Status: 502}
	}
	c := agent.NewRemote(url, pin, string(token), 30*time.Second)
	a.remotes[id] = remoteEntry{key: key, client: c}
	return c, nil
}

// forget drops a server's cached client.
func (a *agentRouter) forget(id int64) {
	a.mu.Lock()
	delete(a.remotes, id)
	a.mu.Unlock()
}

// Health is the selected server's agent health.
func (a *agentRouter) Health(ctx context.Context) (json.RawMessage, error) {
	id := serverIDFrom(ctx)
	if id == 1 {
		return a.local.Health(ctx)
	}
	c, err := a.remote(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return a.local.Health(ctx)
	}
	return c.Health(ctx)
}
