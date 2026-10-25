package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// servicesIndex GET /services — lists systemd units and the WhaCP-managed
// services (agent, panel). Mirrors ServerController::services
// (services, services.view).
func (s *Server) servicesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Services",
		"Services":   nil,
		"Whacp":      nil,
		"AgentError": nil,
		"Success":    "",
		"Error":      "",
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	if raw, err := s.agent.Call(r.Context(), "services.status", nil, 30*time.Second); err == nil {
		var svc any
		_ = json.Unmarshal(raw, &svc)
		data["Services"] = svc
	} else {
		data["AgentError"] = err.Error()
	}
	// Older agents may lack whacp.services; an empty object is fine.
	if raw, err := s.agent.Call(r.Context(), "whacp.services", nil, 10*time.Second); err == nil {
		var wh any
		_ = json.Unmarshal(raw, &wh)
		data["Whacp"] = wh
	}
	s.render(w, "server_services", data)
}
