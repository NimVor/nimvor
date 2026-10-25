package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// usersUpdate POST /users/{id} — updates a panel user's email, display
// name, role, active flag and optional password. Mirrors
// UsersController::update / UserService::update (users.update, users.manage).
func (s *Server) usersUpdate(w http.ResponseWriter, r *http.Request) {
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
	if !canManageUser(repo, actor, target) {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	// Validate fields.
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	display := strings.TrimSpace(r.FormValue("display_name"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirmation")
	roleID, err := strconv.ParseInt(r.FormValue("role_id"), 10, 64)
	active := r.FormValue("is_active") == "1"
	errs := map[string]string{}
	if email == "" || !isEmail(email) || len(email) > 190 {
		errs["email"] = "Enter a valid email address."
	} else if taken, e := repo.EmailExists(r.Context(), email, id); e == nil && taken {
		errs["email"] = "This email address is already in use."
	}
	if len(display) > 100 {
		errs["display_name"] = "Use at most 100 printable characters."
	}
	// Validate role exists.
	var newRoleName string
	var newRoleScope string
	if roleID <= 0 {
		errs["role_id"] = "Choose a valid role."
	} else {
		rr := struct {
			Name  string
			Scope string
		}{}
		if e := s.db.QueryRowContext(r.Context(), "SELECT name, scope FROM roles WHERE id = ?", roleID).Scan(&rr.Name, &rr.Scope); e != nil {
			errs["role_id"] = "Choose a valid role."
		} else {
			newRoleName = rr.Name
			newRoleScope = rr.Scope
		}
	}
	// Self-disable guard.
	if id == actor.ID && !active {
		errs["is_active"] = "You cannot disable your own account."
	}
	// Password validation (only if a new password is supplied).
	pwHash := ""
	if password != "" {
		weak, wErr := s.policy.Check(r.Context(), password, target.Username)
		if wErr != nil {
			errs["password"] = "Password policy unavailable."
		} else if weak != "" {
			errs["password"] = weak
		} else if password != confirm {
			errs["password_confirmation"] = "The passwords do not match."
		} else {
			h, herr := identity.HashPassword(password)
			if herr != nil {
				errs["password"] = herr.Error()
			} else {
				pwHash = h
			}
		}
	}
	// Last-administrator guard: currently admin+active, new state isn't.
	wasAdmin := target.RoleName == "admin" && target.IsActive
	staysAdmin := newRoleName == "admin" && active
	if wasAdmin && !staysAdmin {
		n, _ := repo.CountActiveWithRole(r.Context(), "admin")
		if n <= 1 {
			errs["role_id"] = "At least one active administrator is required."
		}
	}
	if len(errs) > 0 {
		// Flash the first error (simple and matches the flash-redirect style
		// used elsewhere in the panel); the PHP controller uses ValidationException
		// to render per-field errors on the form, but Go flash stubs here don't
		// yet carry an errors map — keep it to a single friendly message.
		for _, m := range errs {
			sess.Flash("error", m)
			break
		}
		http.Redirect(w, r, "/users/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
		return
	}
	u := identity.UserUpdate{
		Email:       email,
		DisplayName: display,
		RoleID:      roleID,
		IsActive:    active,
		Password:    pwHash,
	}
	if err := repo.Update(r.Context(), id, u); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/users/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
		return
	}
	roleChanged := target.RoleID != roleID
	pwChanged := pwHash != ""
	uid := actor.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "user.update", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
		Result: audit.Success,
		Details: map[string]any{
			"username":         target.Username,
			"role_changed":     roleChanged,
			"active":           active,
			"password_changed": pwChanged,
		},
		UserID: &uid, Username: actor.Username,
	})
	sess.Flash("success", "User updated.")
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// isEmail is a light-touch email check — the Go stdlib has no built-in, and
// this mirrors filter_var(FILTER_VALIDATE_EMAIL) well enough for the form.
func isEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at < 1 || at == len(s)-1 {
		return false
	}
	host := s[at+1:]
	return strings.Contains(host, ".") && !strings.ContainsAny(s, " \t\r\n<>")
}

// canManageUser reports whether actor may manage target per the parent-chain
// rule in UserService::canManage: server-wide roles manage everyone, own-scope
// (reseller) roles manage only accounts whose ancestor (through parent_id) is
// the actor. The traversal uses repo.Find to walk up the chain safely.
func canManageUser(repo *identity.Repository, actor *identity.User, target *identity.User) bool {
	if actor == nil || target == nil {
		return false
	}
	if actor.RoleScope == "all" {
		return true
	}
	if target.ID == actor.ID {
		return true
	}
	seen := map[int64]bool{target.ID: true}
	parent := target.ParentID.Int64
	if !target.ParentID.Valid {
		return false
	}
	for {
		if parent == actor.ID {
			return true
		}
		if seen[parent] {
			return false
		}
		seen[parent] = true
		p, err := repo.FindByID(context.Background(), parent)
		if err != nil || p == nil {
			return false
		}
		if !p.ParentID.Valid {
			return false
		}
		parent = p.ParentID.Int64
	}
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

// usersSuspend POST /users/{id}/suspend — marks a hosting account as
// suspended (suspended_at + suspend_reason). This blocks further sign-ins
// via the existing canAct/suspended gates. Note that on-the-server
// enforcement (stopping websites, mail, DB users, cron, workers and
// locking the system account) is performed by the scheduler/enforce job
// after the flag is flipped; mirrors SuspensionService::suspend for the
// account-toggle part only (users.suspend, users.manage).
func (s *Server) usersSuspend(w http.ResponseWriter, r *http.Request) {
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
	to := "/users/" + strconv.FormatInt(id, 10) + "/edit"
	if errors.Is(err, sql.ErrNoRows) || target == nil {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if !hosting.IsAll(actor) {
		sess.Flash("error", "Only administrators can suspend accounts.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	if target.RoleScope == "all" {
		sess.Flash("error", "Only hosting accounts (customers and resellers) can be suspended. Turn staff accounts off with \"Active\" instead.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	if target.ID == actor.ID {
		sess.Flash("error", "You cannot suspend your own account.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	// Collapse whitespace and cap at 250 bytes (matches PHP SuspensionService).
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > 250 {
		reason = reason[:250]
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE users SET suspended_at = COALESCE(suspended_at, ?), suspend_reason = ? WHERE id = ?`,
		now, reason, id); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	uid := actor.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "account.suspend", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
		Result:  audit.Success,
		Details: map[string]any{"username": target.Username, "reason": reason},
		UserID:  &uid, Username: actor.Username,
	})
	sess.Flash("success", "The account is suspended: it cannot sign in. The server-side services are turned off by the scheduler within a minute; nothing was deleted.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// usersUnsuspend POST /users/{id}/unsuspend — clears suspended_at /
// suspend_reason so the account can sign in again (users.unsuspend,
// users.manage). Server-side services are re-enabled by the scheduler.
func (s *Server) usersUnsuspend(w http.ResponseWriter, r *http.Request) {
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
	to := "/users/" + strconv.FormatInt(id, 10) + "/edit"
	if errors.Is(err, sql.ErrNoRows) || target == nil {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if !hosting.IsAll(actor) {
		sess.Flash("error", "Only administrators can resume accounts.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	if _, err := s.db.ExecContext(r.Context(),
		`UPDATE users SET suspended_at = NULL, suspend_reason = '' WHERE id = ?`, id); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	uid := actor.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "account.unsuspend", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
		Result: audit.Success, Details: map[string]any{"username": target.Username},
		UserID: &uid, Username: actor.Username,
	})
	sess.Flash("success", "The account is resumed. Server-side services are turned back on by the scheduler within a minute.")
	http.Redirect(w, r, to, http.StatusSeeOther)
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

// planLimitMax mirrors AccountAdminService::MAX_VALUE (about 1 PB in MB).
const planLimitMax = 1_000_000_000

// usersChangePlan POST /users/{id}/plan — assigns a new plan to a customer
// (or reseller customer) account, then triggers resource-limit reconciliation
// so cgroup CPU/memory/etc. limits follow immediately. Mirrors
// AccountAdminController::changePlan / AccountAdminService::changePlan
// (users.plan.change, users.manage).
func (s *Server) usersChangePlan(w http.ResponseWriter, r *http.Request) {
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
	to := "/users/" + strconv.FormatInt(id, 10) + "/plan"
	if errors.Is(err, sql.ErrNoRows) || target == nil {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if !canManageUser(actor, target) || target.RoleScope != "own" {
		sess.Flash("error", "That plan does not exist or you cannot assign it.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	planID, err := strconv.ParseInt(r.FormValue("plan_id"), 10, 64)
	if err != nil || planID <= 0 {
		sess.Flash("error", "That plan does not exist or you cannot assign it.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	// Verify the plan exists and is visible to the actor.
	plans, perr := hosting.NewPlanRepository(s.db).Visible(r.Context(), actor)
	var planName string
	found := false
	for _, p := range plans {
		if p.ID == planID {
			planName = p.Name
			found = true
			break
		}
	}
	if perr != nil || !found {
		sess.Flash("error", "That plan does not exist or you cannot assign it.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	fromName := ""
	if target.PlanID.Valid {
		s.db.QueryRowContext(r.Context(), "SELECT name FROM plans WHERE id = ?", target.PlanID.Int64).Scan(&fromName)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(r.Context(), "UPDATE users SET plan_id = ?, updated_at = ? WHERE id = ?", planID, now, id); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	uid := actor.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "account.plan_change", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
		Result:  audit.Success,
		Details: map[string]any{"username": target.Username, "from": fromName, "to": planName},
		UserID:  &uid, Username: actor.Username,
	})
	edit := "/users/" + strconv.FormatInt(id, 10) + "/edit"
	// Apply new cgroup limits immediately; if that fails, warn but don't roll back
	// the plan change (the scheduler re-applies them within minutes).
	warn := s.reconcileLimitsNow(r)
	if warn != "" {
		sess.Flash("error", "The account now has the \""+planName+"\" plan. "+warn)
		http.Redirect(w, r, edit, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "The account now has the \""+planName+"\" plan. Its limits apply from now on; nothing was removed.")
	http.Redirect(w, r, edit, http.StatusSeeOther)
}

// reconcileLimitsNow applies any pending resource-limit changes by invoking
// the agent (best effort) and returns a user-facing warning string on
// failure, empty on success.
func (s *Server) reconcileLimitsNow(r *http.Request) string {
	// Reuse the public path: we simply invoke limitsApply-like logic via a
	// dedicated reconcile on the current server; for simplicity we call the
	// agent's limits.set for every acct slice that differs. We mirror the
	// post-plan-change reconciliation from AccountAdminController::changePlan
	// via limitsApply's behaviour, but inline a minimal pass here to avoid
	// double-auditing (we don't want to fire per-account limits.apply audits
	// when a plan was just changed).
	sid := s.currentServerID(r)
	raw, err := s.agent.Call(r.Context(), "limits.status", nil, 30*time.Second)
	applied := map[string]any{}
	if err == nil {
		var st map[string]any
		if json.Unmarshal(raw, &st) == nil {
			if a, ok := st["accounts"].(map[string]any); ok {
				applied = a
			}
		}
	}
	want, err := s.limitsDesired(r)
	if err != nil {
		return "The plan was changed, but its resource limits were not applied: " + err.Error() + ". The scheduler applies them again within minutes."
	}
	keys := map[string]bool{}
	for k := range applied {
		keys[k] = true
	}
	for k := range want {
		keys[k] = true
	}
	for acct := range keys {
		w, haveW := want[acct]
		curI, haveC := applied[acct]
		var wantLimits map[string]int
		var wantUsers []string
		if haveW {
			wantLimits = w["limits"].(map[string]int)
			wantUsers, _ = w["users"].([]string)
		} else {
			wantLimits = map[string]int{}
			for _, f := range limitsFields {
				wantLimits[f] = 0
			}
		}
		curLimits := map[string]int{}
		curUsers := []string{}
		if haveC {
			if cm, ok := curI.(map[string]any); ok {
				if cu, ok := cm["users"].([]any); ok {
					for _, u := range cu {
						if us, ok := u.(string); ok {
							curUsers = append(curUsers, us)
						}
					}
				}
				if cl, ok := cm["limits"].(map[string]any); ok {
					for _, f := range limitsFields {
						if n, ok := cl[f].(float64); ok {
							curLimits[f] = int(n)
						}
					}
				}
			}
		}
		callUsers := unionStrings(wantUsers, curUsers)
		if !haveW {
			callUsers = curUsers
		}
		params := map[string]any{"account": acct, "users": callUsers}
		for _, f := range limitsFields {
			params[f] = wantLimits[f]
		}
		s.agent.Call(r.Context(), "limits.set", params, 60*time.Second) // best effort
	}
	_ = sid
	return ""
}

// usersSaveLimits POST /users/{id}/limits — saves per-account plan-field
// overrides on top of the assigned plan (disk/bandwidth/max_*). Empty
// values clear the override (the plan's value is used). Values must be
// integers between 0 and planLimitMax inclusive. Mirrors
// AccountAdminService::saveOverrides / AccountAdminController::saveLimits
// (users.limits, users.manage).
func (s *Server) usersSaveLimits(w http.ResponseWriter, r *http.Request) {
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
	to := "/users/" + strconv.FormatInt(id, 10) + "/edit#account-limits"
	if errors.Is(err, sql.ErrNoRows) || target == nil || !canManageUser(actor, target) || target.RoleScope != "own" {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	if !target.PlanID.Valid || target.PlanID.Int64 <= 0 {
		sess.Flash("error", "Assign a hosting plan first: overrides change the limits of the account's plan.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	// Fetch current overrides (raw JSON) for the audit "before" snapshot.
	var beforeJSON sql.NullString
	s.db.QueryRowContext(r.Context(), "SELECT limit_overrides FROM users WHERE id = ?", id).Scan(&beforeJSON)
	before := map[string]int{}
	if beforeJSON.Valid {
		var bm map[string]any
		_ = json.Unmarshal([]byte(beforeJSON.String), &bm)
		for k, v := range bm {
			if n, ok := v.(float64); ok && n == float64(int(n)) {
				before[k] = int(n)
			}
		}
	}
	newOverrides := map[string]int{}
	errs := []string{}
	for _, col := range hosting.PlanFields {
		raw := strings.TrimSpace(r.FormValue(col))
		if raw == "" {
			continue
		}
		v, perr := strconv.Atoi(raw)
		if perr != nil || v < 0 || v > planLimitMax {
			errs = append(errs, col+": a whole number from 0 (unlimited) to 1,000,000,000, or empty for the plan's value.")
			continue
		}
		newOverrides[col] = v
	}
	if len(errs) > 0 {
		sess.Flash("error", strings.Join(errs, " "))
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	blob, err := json.Marshal(newOverrides)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.ExecContext(r.Context(),
		"UPDATE users SET limit_overrides = ?, updated_at = ? WHERE id = ?", string(blob), now, id); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	uid := actor.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "account.limits", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
		Result:  audit.Success,
		Details: map[string]any{"username": target.Username, "before": before, "after": newOverrides},
		UserID:  &uid, Username: actor.Username,
	})
	sess.Flash("success", "Limits saved. The plan itself is unchanged.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// usersSaveOptions POST /users/{id}/hosting-options — updates hosting
// defaults (DNS and spam) for a customer account via
// hosting.AccountOptionStore.Set. Re-syncs the mail server spam config
// afterwards; on SyncSpam failure flashes a non-fatal warning (matches
// AccountsController::saveOptions / AccountOptions::set).
func (s *Server) usersSaveOptions(w http.ResponseWriter, r *http.Request) {
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
	to := "/users/" + strconv.FormatInt(id, 10) + "/edit"
	if errors.Is(err, sql.ErrNoRows) || target == nil || !canManageUser(actor, target) || target.RoleScope != "own" {
		sess.Flash("error", "The user no longer exists.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	opts := map[string]any{
		"dns_dkim":         r.FormValue("dns_dkim") == "1",
		"dns_spf":          r.FormValue("dns_spf") == "1",
		"dns_dmarc":        r.FormValue("dns_dmarc") == "1",
		"dns_registrar_ns": r.FormValue("dns_registrar_ns") == "1",
		"dns_overwrite":    r.FormValue("dns_overwrite") == "1",
		"spam_filter":      r.FormValue("spam_filter") == "1",
		"spam_box":         r.FormValue("spam_box") == "1",
		"mail_routing":     r.FormValue("mail_routing"),
	}
	if err := hosting.NewAccountOptionStore(s.db).Set(r.Context(), id, opts); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	uid := actor.ID
	store := hosting.NewAccountOptionStore(s.db)
	loaded, _ := store.Get(r.Context(), &id)
	s.audit.Record(r.Context(), audit.Entry{
		Action: "account.options", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
		Result: audit.Success,
		Details: map[string]any{"username": target.Username,
			"dns_dkim": loaded.DNSDKIM, "dns_spf": loaded.DNSSPF, "dns_dmarc": loaded.DNSDMARC,
			"dns_registrar_ns": loaded.DNSRegistrarNS, "dns_overwrite": loaded.DNSOverwrite,
			"mail_routing": loaded.MailRouting, "spam_filter": loaded.SpamFilter, "spam_box": loaded.SpamBox,
		},
		UserID: &uid, Username: actor.Username,
	})
	// Apply the spam settings to the mail server (best effort).
	synced := s.siteMailService(r, actor).SyncSpam(r.Context(), s.currentServerID(r))
	if !synced {
		sess.Flash("error", "Hosting options saved, but the spam filter settings could not be applied to the mail server. Save the spam filter again under Email.")
		http.Redirect(w, r, to, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Hosting options saved. DNS options apply to new websites; spam options to the account's email now.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// usersResetBandwidth POST /users/{id}/bandwidth/reset — resets this
// month's bandwidth counter (bw_bytes = 0) for all websites owned by the
// target account (and their direct customers for a reseller). Mirrors
// AccountAdminController::resetBandwidth / AccountAdminService::resetBandwidth
// (users.bandwidth.reset, users.manage).
func (s *Server) usersResetBandwidth(w http.ResponseWriter, r *http.Request) {
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
	if !canManageUser(actor, target) {
		sess.Flash("error", "You cannot manage that account.")
		http.Redirect(w, r, "/users", http.StatusSeeOther)
		return
	}
	owners := []int64{id}
	// Include direct customers (matches PlanLimits::family).
	rows, err := s.db.QueryContext(r.Context(), "SELECT id FROM users WHERE parent_id = ?", id)
	if err == nil {
		for rows.Next() {
			var cid int64
			if rows.Scan(&cid) == nil {
				owners = append(owners, cid)
			}
		}
		rows.Close()
	}
	month := time.Now().UTC().Format("2006-01")
	// Sum current month bandwidth (MB) before reset.
	var usedMB int
	if len(owners) > 0 {
		placeholders := strings.Repeat("?,", len(owners))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, 0, len(owners)+1)
		args = append(args, month)
		for _, o := range owners {
			args = append(args, o)
		}
		q := `SELECT COALESCE(SUM(bw_bytes),0) FROM website_usage
		      WHERE bw_month = ? AND website_id IN (SELECT id FROM websites WHERE owner_id IN (` + placeholders + `))`
		var usedBytes int64
		if err := s.db.QueryRowContext(r.Context(), q, args...).Scan(&usedBytes); err == nil {
			usedMB = int(usedBytes / (1024 * 1024))
		}
		_, _ = s.db.ExecContext(r.Context(),
			`UPDATE website_usage SET bw_bytes = 0 WHERE bw_month = ? AND website_id IN (SELECT id FROM websites WHERE owner_id IN (`+placeholders+`))`,
			args...)
	}
	back := "/users/" + strconv.FormatInt(id, 10) + "/edit"
	if r.FormValue("return_to") == "bandwidth" {
		back = "/accounts/bandwidth"
	}
	uid := actor.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "account.bandwidth_reset", TargetType: "user", TargetID: strconv.FormatInt(id, 10),
		Result:  audit.Success,
		Details: map[string]any{"username": target.Username, "month": month, "mb_before": usedMB},
		UserID:  &uid, Username: actor.Username,
	})
	sess.Flash("success", "Bandwidth of "+target.Username+" reset for "+month+" ("+strconv.Itoa(usedMB)+" MB were counted).")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// flashStr pulls a flash value without consuming it (for forms rendered
// after redirect that want to surface the flash once; our PullFlash in
// other handlers removes it, here we mirror that by pulling).
func flashStr(sess *session.Session, key string) string {
	v, _ := sess.PullFlash(key)
	return v
}
