package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
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
// that turns on disk quotas for websites' file systems via the agent's
// `quota.enable` action. Mirrors LimitsController::enableQuota
// (limits.quota.enable, server.manage).
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
	user := userFrom(r.Context())
	// quota.enable is a privileged server-management action.
	if !s.can(r, "server.manage") {
		sess.Flash("error", "Disk quotas are managed by server administrators.")
		http.Redirect(w, r, "/limits#disk-quotas", http.StatusSeeOther)
		return
	}
	raw, err := s.agent.Call(r.Context(), "quota.enable", map[string]any{}, 60*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/limits#disk-quotas", http.StatusSeeOther)
		return
	}
	var parsed struct {
		JobID string `json:"job_id"`
	}
	jobID := ""
	if err := json.Unmarshal(raw, &parsed); err == nil && parsed.JobID != "" {
		jobID = parsed.JobID
	}
	uid := user.ID
	if jobID != "" {
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, "Turn on disk quotas", "server", itoa(s.currentServerID(r)), &uid)
		sess.Flash("job", jobID)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "quota.enable", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: map[string]any{"job_id": jobID},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Turning disk quotas on. Follow the log below; the plans' disk space is enforced once they are on.")
	http.Redirect(w, r, "/limits#disk-quotas", http.StatusSeeOther)
}
