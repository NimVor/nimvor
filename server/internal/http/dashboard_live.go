package http

import (
	"encoding/json"
	"net/http"
	"time"
)

// dashboardLive returns live metrics for the dashboard (polled with fetch()).
// Mirrors DashboardController::live (GET /dashboard/live, dashboard.view).
// Services are only included when the caller may view them (services.view),
// matching the PHP $this->view->can() gate.
func (s *Server) dashboardLive(w http.ResponseWriter, r *http.Request) {
	metricsRaw, err := s.agent.Call(r.Context(), "system.metrics", nil, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"error":   map[string]any{"message": err.Error()},
		})
		return
	}
	historyRaw, err := s.agent.Call(r.Context(), "system.metrics_history", nil, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"success": false,
			"error":   map[string]any{"message": err.Error()},
		})
		return
	}
	var services any
	if s.can(r, "services.view") {
		if svcRaw, err := s.agent.Call(r.Context(), "services.status", nil, 10*time.Second); err == nil {
			_ = json.Unmarshal(svcRaw, &services)
		}
	}
	var metrics, history any
	_ = json.Unmarshal(metricsRaw, &metrics)
	_ = json.Unmarshal(historyRaw, &history)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":  true,
		"metrics":  metrics,
		"history":  history,
		"services": services,
	})
}
