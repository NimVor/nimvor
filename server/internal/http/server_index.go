package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// serverIndex GET /server — administrator overview of the server (OS, kernel,
// memory, uptime, CPU, disks, network, firewall). Mirrors
// ServerController::index (server, server.view).
func (s *Server) serverIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Server",
		"Info":       nil,
		"Metrics":    nil,
		"Firewall":   nil,
		"Settings":   nil,
		"Timezones":  []any{},
		"Graphical":  nil,
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
	infoRaw, err := s.agent.Call(r.Context(), "system.info", nil, 30*time.Second)
	if err != nil {
		data["AgentError"] = err.Error()
		s.render(w, "server_index", data)
		return
	}
	metricsRaw, err := s.agent.Call(r.Context(), "system.metrics", nil, 30*time.Second)
	if err != nil {
		data["AgentError"] = err.Error()
		_ = json.Unmarshal(infoRaw, &data["Info"])
		s.render(w, "server_index", data)
		return
	}
	fwRaw, err := s.agent.Call(r.Context(), "discovery.firewall", nil, 30*time.Second)
	if err == nil {
		var fw any
		_ = json.Unmarshal(fwRaw, &fw)
		data["Firewall"] = fw
	}
	// Best-effort settings/zone/graphical; missing actions simply leave them nil.
	if raw, err := s.agent.Call(r.Context(), "server.settings", nil, 15*time.Second); err == nil {
		var v any
		_ = json.Unmarshal(raw, &v)
		data["Settings"] = v
	}
	if raw, err := s.agent.Call(r.Context(), "server.timezones", nil, 15*time.Second); err == nil {
		var v []any
		_ = json.Unmarshal(raw, &v)
		data["Timezones"] = v
	}
	if raw, err := s.agent.Call(r.Context(), "server.graphical", nil, 15*time.Second); err == nil {
		var v any
		_ = json.Unmarshal(raw, &v)
		data["Graphical"] = v
	}
	var info, metrics any
	_ = json.Unmarshal(infoRaw, &info)
	_ = json.Unmarshal(metricsRaw, &metrics)
	data["Info"] = info
	data["Metrics"] = metrics
	s.render(w, "server_index", data)
}
