package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// limitsIndex GET /limits — shows cgroup-style resource limits per account.
// The PHP page calls ResourceLimitService::status / ::desired and
// DiskQuotaService::status; those services are not ported to Go yet, so
// this handler returns a friendly page indicating the feature is not yet
// available while the route itself is registered so the nav link works.
// Mirrors LimitsController::index (limits, server.view).
func (s *Server) limitsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Resource limits",
		"Success":    "",
		"Error":      "",
		"Status":     nil,
		"AgentError": nil,
		"NotReady":   true,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	// Best-effort: try calling resource-limits.status on the agent; if it
	// doesn't exist the page will fall back to the "not yet ported" card.
	if raw, err := s.agent.Call(r.Context(), "resource_limits.status", nil, 60*time.Second); err == nil {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		data["Status"] = parsed
		data["NotReady"] = false
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "limits_index", data)
}

// limitsApply POST /limits/apply — asks the agent to reconcile account
// cgroup limits with their plans. Stub: returns a friendly error because
// ResourceLimitService.reconcile has not been ported.
// Mirrors LimitsController::apply (limits.apply, server.manage).
func (s *Server) limitsApply(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Resource-limit reconciliation is not yet ported in the Go panel.")
	http.Redirect(w, r, "/limits", http.StatusSeeOther)
}

// limitsEnableQuota POST /limits/quota/enable — kicks off a background job
// that turns on disk quotas for websites' file systems. Stub: returns a
// friendly error because DiskQuotaService.enable is not yet ported.
// Mirrors LimitsController::enableQuota (limits.quota.enable, server.manage).
func (s *Server) limitsEnableQuota(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Disk-quota enablement is not yet ported in the Go panel.")
	http.Redirect(w, r, "/limits#disk-quotas", http.StatusSeeOther)
}
