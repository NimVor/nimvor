package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// setupIndex GET /setup — the server setup wizard. The PHP wizard drives
// ServerSetupService which installs packages, configures services, and
// creates a default plan; that orchestration is not ported to Go yet.
// This handler returns a friendly page with live component/metric data
// from the agent, mirroring the PHP page's data shape where possible so
// the UI is wired but steps don't run. Mirrors SetupController::index.
func (s *Server) setupIndex(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Setup wizard",
		"Success":    "",
		"Error":      "",
		"Stub":       true,
		"Components": nil,
		"MemoryMB":   0,
		"AgentError": nil,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	// Mark the wizard as seen (same as PHP index()).
	if seen, _ := s.settings.Bool(r.Context(), "setup_wizard_seen", false); !seen {
		_ = s.settings.Set(r.Context(), "setup_wizard_seen", true, user.ID)
	}
	if raw, err := s.agent.Call(r.Context(), "components.status", nil, 60*time.Second); err == nil {
		var c any
		_ = json.Unmarshal(raw, &c)
		data["Components"] = c
	} else {
		data["AgentError"] = err.Error()
	}
	if raw, err := s.agent.Call(r.Context(), "system.metrics", nil, 30*time.Second); err == nil {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil {
			if mem, ok := m["memory"].(map[string]any); ok {
				if tb, ok := mem["total_bytes"].(float64); ok {
					data["MemoryMB"] = int(tb) / (1024 * 1024)
				}
			}
		}
	}
	s.render(w, "setup_index", data)
}

// setupStep POST /setup/step — runs one wizard step (JSON). Stub: returns
// a JSON error because ServerSetupService::runStep is not yet ported.
// Mirrors SetupController::step.
func (s *Server) setupStep(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"error":   map[string]any{"message": "The setup wizard step runner is not yet ported in the Go panel."},
	})
}

// setupDismiss POST /setup/dismiss — closes the wizard so the dashboard
// no longer nags; fully implemented (just settings + audit). Mirrors
// SetupController::dismiss (setup.dismiss, server.manage).
func (s *Server) setupDismiss(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if err := s.settings.Set(r.Context(), "setup_wizard_seen", true, user.ID); err != nil {
		s.log.Error("set setup_wizard_seen", "error", err)
	}
	if err := s.settings.Set(r.Context(), "setup_wizard_done", true, user.ID); err != nil {
		s.log.Error("set setup_wizard_done", "error", err)
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "server.setup_dismiss", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "The setup wizard is closed. Reopen it any time under Server → Setup wizard.")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
