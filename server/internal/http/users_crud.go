package http

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// usersCreate GET /users/new — new-panel-user form. Shows available roles
// and plans from existing repositories with a friendly stub notice until
// UserService::create is fully ported.
func (s *Server) usersCreate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	roles, _ := s.roles.All(r.Context())
	plans, _ := hosting.NewPlanRepository(s.db).Visible(r.Context(), user)
	data := map[string]any{
		"CSRF":    session.CSRFToken(sess),
		"User":    user,
		"Title":   "New panel user",
		"Roles":   roles,
		"Plans":   plans,
		"EditID":  int64(0),
		"Error":   flashStr(sess, "error"),
		"Success": flashStr(sess, "success"),
		"Stub":    true,
	}
	s.render(w, "users_form", data)
}

// usersStore POST /users — creates a panel user. Stub (UserService::create
// is heavy).
func (s *Server) usersStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Panel-user creation is not yet ported in the Go panel.")
	http.Redirect(w, r, "/users/new", http.StatusSeeOther)
}

// usersEdit GET /users/{id}/edit — edit-panel-user form. Best-effort loads
// the user row and shows roles/plans; the heavy edit fields (usage,
// twoFactor, hostingOptions, limitRows) are stubbed out.
func (s *Server) usersEdit(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	me := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	repo := identity.NewRepository(s.db)
	target, err := repo.FindByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || target == nil {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	roles, _ := s.roles.All(r.Context())
	plans, _ := hosting.NewPlanRepository(s.db).Visible(r.Context(), me)
	data := map[string]any{
		"CSRF":     session.CSRFToken(sess),
		"User":     me,
		"Title":    "Edit " + target.Username,
		"EditID":   id,
		"EditUser": target,
		"Roles":    roles,
		"Plans":    plans,
		"Error":    flashStr(sess, "error"),
		"Success":  flashStr(sess, "success"),
		"Stub":     true,
	}
	s.render(w, "users_form", data)
}

// usersUpdate POST /users/{id} — updates a panel user. Stub.
func (s *Server) usersUpdate(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Panel-user updates are not yet ported in the Go panel.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// usersDestroy POST /users/{id}/delete — deletes a panel user. Fully
// implemented: canManage check, self-delete refusal, last-admin check,
// DELETE the row, audit user.delete. Mirrors UsersController::destroy +
// UserService::delete.
func (s *Server) usersDestroy(w http.ResponseWriter, r *http.Request) {
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
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	repo := identity.NewRepository(s.db)
	target, err := repo.FindByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || target == nil {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	// Permission: staff can manage any user; resellers only their own customers.
	if !canManageUser(actor, target) {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if id == actor.ID {
		sess.Flash("error", "You cannot delete your own account. Ask another administrator to remove it.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if target.RoleName == "admin" && target.IsActive {
		n, err := repo.CountActiveWithRole(r.Context(), "admin")
		if err != nil {
			s.log.Error("count admins", "error", err)
			sess.Flash("error", "Could not check administrator count.")
			http.Redirect(w, r, "/users", http.StatusSeeOther)
			return
		}
		if n <= 1 {
			sess.Flash("error", "The last administrator cannot be deleted. Promote another user to Administrator first.")
			http.Redirect(w, r, "/users", http.StatusSeeOther)
			return
		}
	}
	// Clean up webauthn credentials and API tokens; FK cascades handle most
	// other relations but be explicit where we have no cascade.
	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM webauthn_credentials WHERE user_id = ?", id); err != nil {
		s.log.Error("delete webauthn creds on user delete", "error", err)
	}
	if err := repo.Delete(r.Context(), id); err != nil {
		s.log.Error("delete user", "error", err)
		sess.Flash("error", "Could not delete the user.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	uid := actor.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "user.delete", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
		Result: audit.Success, Details: map[string]any{"username": target.Username},
		UserID: &uid, Username: actor.Username,
	})
	sess.Flash("success", "User deleted.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// canManageUser reports whether actor may manage target (matches
// UserService::canManage).
func canManageUser(actor, target *identity.User) bool {
	if actor == nil || target == nil {
		return false
	}
	// Administrator / server-wide staff can manage anyone whose role scope
	// is not higher than their own. Use RoleScope: "all" = staff/admins,
	// "own" = reseller.
	if actor.RoleScope == "all" {
		return true
	}
	if actor.RoleScope == "own" {
		return target.ParentID.Valid && target.ParentID.Int64 == actor.ID
	}
	return false
}

// usersResetTwoFactor POST /users/{id}/two-factor/reset — disables TOTP
// and removes all WebAuthn credentials for a user (admin action). Fully
// implemented: twofactor.Service.Disable + DELETE webauthn_credentials +
// audit user.two_factor_reset. Mirrors UsersController::resetTwoFactor.
func (s *Server) usersResetTwoFactor(w http.ResponseWriter, r *http.Request) {
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
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	target, err := identity.NewRepository(s.db).FindByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || target == nil {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if !hosting.IsAll(actor) {
		sess.Flash("error", "You may not do this.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if err := s.twoFactor.Disable(r.Context(), id); err != nil {
		s.log.Error("reset 2fa", "error", err)
		sess.Flash("error", "Could not disable two-factor for this user.")
		http.Redirect(w, r, "/users/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), "DELETE FROM webauthn_credentials WHERE user_id = ?", id); err != nil {
		s.log.Error("delete webauthn creds on reset", "error", err)
	}
	uid := actor.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "user.two_factor_reset", TargetType: "user",
		TargetID: strconv.FormatInt(id, 10), Result: audit.Success,
		Details: map[string]any{"username": target.Username},
		UserID:  &uid, Username: actor.Username,
	})
	sess.Flash("success", "Two-factor sign-in (app and security keys) is off for "+target.Username+". Ask them to turn it on again.")
	http.Redirect(w, r, "/users/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
}

// usersSuspend POST /users/{id}/suspend — suspends a hosting account. Stub.
func (s *Server) usersSuspend(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Account suspension is not yet ported in the Go panel.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// usersUnsuspend POST /users/{id}/unsuspend — stub.
func (s *Server) usersUnsuspend(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Account unsuspension is not yet ported in the Go panel.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// usersPlan GET /users/{id}/plan — plan change page stub.
func (s *Server) usersPlan(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	me := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	target, err := identity.NewRepository(s.db).FindByID(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || target == nil {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	plans, _ := hosting.NewPlanRepository(s.db).Visible(r.Context(), me)
	data := map[string]any{
		"CSRF":    session.CSRFToken(sess),
		"User":    me,
		"Title":   "Change plan for " + target.Username,
		"Target":  target,
		"Plans":   plans,
		"Error":   flashStr(sess, "error"),
		"Success": flashStr(sess, "success"),
		"Stub":    true,
	}
	s.render(w, "users_plan", data)
}

// usersChangePlan POST /users/{id}/plan — stub.
func (s *Server) usersChangePlan(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Plan changes are not yet ported in the Go panel.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// usersSaveLimits POST /users/{id}/limits — stub.
func (s *Server) usersSaveLimits(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Per-account resource limits are not yet ported in the Go panel.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// usersSaveOptions POST /users/{id}/hosting-options — stub.
func (s *Server) usersSaveOptions(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Hosting options are not yet ported in the Go panel.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// usersResetBandwidth POST /users/{id}/bandwidth/reset — stub.
func (s *Server) usersResetBandwidth(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Bandwidth reset is not yet ported in the Go panel.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// flashStr pulls a flash value without consuming it (for forms rendered
// after redirect that want to surface the flash once; our PullFlash in
// other handlers removes it, here we mirror that by pulling).
func flashStr(sess *session.Session, key string) string {
	v, _ := sess.PullFlash(key)
	return v
}
