package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// accountsCreate GET /accounts/new — new-account form page. Renders the
// real form (domain, username, contact, password, plan, reseller/www
// toggles) backed by hosting.AccountService::create.
// Mirrors AccountsController::create (accounts.create, users.manage).
func (s *Server) accountsCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":        session.CSRFToken(sess),
		"User":        user,
		"Title":       "New account",
		"Plans":       nil,
		"Error":       "",
		"Success":     "",
		"Stub":        false,
		"CanReseller": hosting.IsAll(user),
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

// accountsStore POST /accounts — creates a new hosting account. Wires to
// hosting.AccountService::create which provisions the panel login, plan,
// hosting options, initial website (with www alias), DNS zone and SSL,
// rolling back the login if website creation fails.
// Mirrors AccountsController::store.
func (s *Server) accountsStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	actor := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	svc := s.accountService(r, actor)
	acct, err := svc.Create(r.Context(), postValues(r), actor)
	if err != nil {
		var ve *hosting.ValidationError
		msg := err.Error()
		if errors.As(err, &ve) {
			for _, m := range ve.Fields {
				msg = m
				break
			}
		}
		sess.Flash("error", msg)
		http.Redirect(w, r, "/accounts/new", http.StatusSeeOther)
		return
	}
	if acct.Password.Valid {
		sess.Flash("success", "Account "+acct.Username+" created. Generated password: "+acct.Password.String)
	} else {
		sess.Flash("success", "Account "+acct.Username+" created.")
	}
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// accountService builds hosting.AccountService with per-request audit hooks
// (same pattern as websiteService/websiteAuditHook).
func (s *Server) accountService(r *http.Request, user *identity.User) *hosting.AccountService {
	users := identity.NewRepository(s.db)
	roles := authz.NewRoleRepository(s.db)
	plans := hosting.NewPlanRepository(s.db)
	limits := hosting.NewPlanLimits(s.db)
	usvc := hosting.NewUserService(users, roles, s.policy, plans, limits, s.accountAuditHook(r, user))
	ws := hosting.NewWebsiteService(
		hosting.NewWebsiteRepository(s.db), s.agent, s.features, limits,
		hosting.NewDnsRepository(s.db), s.settings, s.websiteAuditHook(r, user),
		s.currentServerID(r),
	)
	return hosting.NewAccountService(usvc, users, ws, hosting.NewAccountOptionStore(s.db), s.db, s.accountAuditHook(r, user))
}

func (s *Server) accountAuditHook(r *http.Request, user *identity.User) hosting.Auditor {
	return func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		if targetType == "" {
			targetType = "user"
		}
		uid := user.ID
		s.audit.Record(ctx, audit.Entry{
			UserID:     &uid,
			Username:   user.Username,
			Action:     action,
			TargetType: targetType,
			TargetID:   target,
			Result:     result,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    details,
		})
	}
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
