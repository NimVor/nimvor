package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// memcachedStatusOf is the agent's memcached.status.
func (s *Server) memcachedStatusOf(ctx context.Context) (map[string]any, *agent.Error) {
	res, err := s.agent.Call(ctx, "memcached.status", map[string]any{}, 30*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			return nil, ae
		}
		s.log.Error("memcached status", "error", err)
		return nil, &agent.Error{Code: "agent_error", Message: "The WhaCP agent could not be reached."}
	}
	var out map[string]any
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, &agent.Error{Code: "agent_bad_response", Message: "The WhaCP agent answered with an unreadable state."}
	}
	return out, nil
}

// siteMemcached renders the website's Memcached page: the connection
// details + the service state (SitesController::memcached — websites.view).
// Memcached is a shared, local, unauthenticated cache, so the page is
// informational — there is no per-site isolation to manage.
func (s *Server) siteMemcached(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.memcachedNotFound(w)
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(ctx, s.currentServerID(r), id, user)
	if errors.Is(err, hosting.ErrWebsiteNotFound) || site == nil {
		s.memcachedNotFound(w)
		return
	}
	if err != nil {
		s.log.Error("find website", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	status, ae := s.memcachedStatusOf(ctx)
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Site":       site,
		"AgentError": "",
		"Installed":  false,
		"Running":    false,
		"Port":       11211,
	}
	if ae != nil {
		data["AgentError"] = ae.Message
	} else if status != nil {
		installed, _ := status["installed"].(bool)
		active, _ := status["active"].(bool)
		if cfg, ok := status["settings"].(map[string]any); ok {
			if port, ok := cfg["port"].(float64); ok && port > 0 {
				data["Port"] = int(port)
			}
		}
		_, hasErr := status["error"]
		data["Installed"] = installed
		data["Running"] = installed && active && !hasErr
	}
	s.render(w, "sites_memcached", data)
}

func (s *Server) memcachedNotFound(w http.ResponseWriter) {
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Website not found",
		"Message": "This website does not exist or you do not have access to it.",
	}, http.StatusNotFound)
}
