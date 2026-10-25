package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// accountsCreate GET /accounts/new — new-account form page. The full account
// provisioning flow (username/password/plan/contact/website) is a large
// multi-step operation in AccountService; this handler renders a page with
// live plan list and a friendly notice until the service is ported.
// Mirrors AccountsController::create (accounts.create, users.manage).
func (s *Server) accountsCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":    session.CSRFToken(sess),
		"User":    user,
		"Title":   "New account",
		"Plans":   nil,
		"Error":   "",
		"Success": "",
		"Stub":    true,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	plans, err := hosting.NewPlanRepository(s.db).Visible(r.Context(), user)
	if err == nil {
		data["Plans"] = plans
	}
	s.render(w, "accounts_create", data)
}

// accountsStore POST /accounts — creates a new hosting account. Stub:
// flashes a friendly "not yet ported" error (AccountService::create is a
// heavy multi-step provisioning flow that creates a system user, plan
// assignment, website, DNS records etc.). Mirrors AccountsController::store.
func (s *Server) accountsStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Account creation is not yet ported in the Go panel (it provisions a system user, website and DNS zone).")
	http.Redirect(w, r, "/accounts/new", http.StatusSeeOther)
}

// accountsBandwidth GET /accounts/bandwidth — bandwidth usage report per
// account. Stub: best-effort calls agent `accounts.bandwidth` and falls
// back to a friendly notice. Mirrors AccountAdminController::bandwidth.
func (s *Server) accountsBandwidth(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Account bandwidth",
		"Status":     nil,
		"Stub":       true,
		"Error":      "",
		"Success":    "",
		"AgentError": nil,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	if raw, err := s.agent.Call(r.Context(), "accounts.bandwidth", nil, 60*time.Second); err == nil {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		data["Status"] = parsed
		data["Stub"] = false
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "accounts_bandwidth", data)
}

// accountsTerminate POST /accounts/terminate — starts termination of a
// hosting account (queued, confirm step first). Stub: flashes a friendly
// error because AccountAdminService::terminate is not ported.
// Mirrors AccountAdminController::terminate.
func (s *Server) accountsTerminate(w http.ResponseWriter, r *http.Request) {
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
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "accounts.terminate", TargetType: "user", TargetID: r.FormValue("user_id"),
		Result: audit.Failure, Details: map[string]any{"stub": true},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("error", "Account termination is not yet ported in the Go panel.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// accountsTerminateConfirm POST /accounts/terminate/confirm — confirms a
// pending termination. Stub, same reason as terminate.
func (s *Server) accountsTerminateConfirm(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Account termination confirmation is not yet ported in the Go panel.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}
