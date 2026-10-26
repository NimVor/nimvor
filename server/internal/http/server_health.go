package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// serverHealth GET /server/health — runs server health checks via the agent
// and renders the result. The PHP HealthService aggregates discovery, package,
// SSL and service checks; we approximate by calling agent `server.health`
// when available and falling back to a minimal summary based on system.info /
// services.status. Mirrors ServerHealthController::index.
func (s *Server) serverHealth(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Server health",
		"Result":     nil,
		"CheckedAt":  time.Now().UTC().Format("2006-01-02 15:04"),
		"AgentError": nil,
	}
	// Try the agent health endpoint; if the agent doesn't support it we just
	// render whatever we could gather (empty result).
	if raw, err := s.agent.Call(r.Context(), "server.health", nil, 60*time.Second); err == nil {
		var res any
		_ = json.Unmarshal(raw, &res)
		data["Result"] = res
	} else {
		// Fall back to a quick composite status from system.info + services.status
		info, ierr := s.agent.Call(r.Context(), "system.info", nil, 15*time.Second)
		svc, serr := s.agent.Call(r.Context(), "services.status", nil, 15*time.Second)
		if ierr != nil && serr != nil {
			data["AgentError"] = err.Error()
		} else {
			data["Result"] = map[string]any{
				"info":     jsonOrRaw(info),
				"services": jsonOrRaw(svc),
			}
		}
	}
	s.render(w, "server_health", data)
}

func jsonOrRaw(b json.RawMessage) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err == nil {
		return v
	}
	return string(b)
}
