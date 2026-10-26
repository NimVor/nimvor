package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/twofactor"
)

// usersIndex lists the panel users the actor may manage: all users for staff,
// only their own customers for a reseller (UserRepository::manageableBy).
// Mirrors UsersController::index (route "users", users.view).
type userListRow struct {
	ID                                                  int64
	Username, Name, Email, Role, Plan, StatusLabel, Cls string
	LastLogin, LastIP, DeleteConfirm                    string
	Self, CanTerminate                                  bool
}

func (s *Server) usersIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := userFrom(ctx)
	sess := sessionFrom(ctx)
	users, err := identity.NewRepository(s.db).ManageableBy(ctx, user)
	if err != nil {
		s.log.Error("list users", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	manage := s.can(r, "users.manage")
	bulk := manage && hosting.IsAll(user)
	rows := make([]userListRow, 0, len(users))
	for _, u := range users {
		row := userListRow{ID: u.ID, Username: u.Username, Email: u.Email, Role: u.RoleLabel, Plan: "—", Self: u.ID == user.ID,
			CanTerminate: bulk && u.RoleScope == "own", LastLogin: "never"}
		if u.DisplayName != "" {
			row.Name = u.DisplayName + " · "
		}
		if u.PlanName.Valid {
			row.Plan = u.PlanName.String
		}
		switch {
		case u.Suspended:
			row.StatusLabel, row.Cls = "suspended", "text-bg-danger"
		case u.IsActive:
			row.StatusLabel, row.Cls = "active", "text-bg-success"
		default:
			row.StatusLabel, row.Cls = "disabled", "text-bg-secondary"
		}
		if u.LastLoginAt.Valid {
			row.LastLogin = timeAgo(u.LastLoginAt.String)
		}
		if u.LastLoginIP.Valid {
			row.LastIP = u.LastLoginIP.String
		}
		row.DeleteConfirm = "Delete the panel user \"" + u.Username + "\"? They will no longer be able to sign in. This cannot be undone."
		rows = append(rows, row)
	}
	s.render(w, "users", map[string]any{
		"User": user, "CSRF": session.CSRFToken(sess), "Title": "Panel users", "Rows": rows, "Manage": manage, "Bulk": bulk,
		"Success": pullFlashKey(sess, "success"), "Error": pullFlashKey(sess, "error"),
	})
}

// profileShow renders the signed-in user's own account, API tokens and the
// two-factor self-service (status, staged setup secret, one-time recovery
// codes, flash messages). Mirrors ProfileController::show (route "profile",
// auth only); the password/token-management forms are a later batch.
func (s *Server) profileShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	tokens, err := identity.NewRepository(s.db).APITokens(r.Context(), user.ID)
	if err != nil {
		s.log.Error("list api tokens", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	twoFactor, err := s.twoFactor.Status(r.Context(), user.ID)
	if err != nil {
		s.log.Error("2fa status", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setupSecret := sess.Get(sessionTwoFactorSetup)
	setupURI := ""
	if setupSecret != "" {
		setupURI = twofactor.URI(setupSecret, user.Username, "WhaCP")
	}
	keys, err := s.wa.Credentials(user.ID)
	if err != nil {
		s.log.Error("list webauthn keys", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	success, _ := sess.PullFlash("success")
	errMsg, _ := sess.PullFlash("error")
	recovery, _ := sess.PullFlash("recovery_codes")
	newToken, _ := sess.PullFlash("api_token")
	rules, _ := s.policy.Describe(r.Context())
	s.render(w, "profile", map[string]any{
		"User":          user,
		"Title":         "Profile",
		"PasswordRules": rules,
		"CSRF":          session.CSRFToken(sess),
		"Tokens":        tokens,
		"TwoFactor":     twoFactor,
		"Keys":          keys,
		"SetupSecret":   setupSecret,
		"SetupURI":      setupURI,
		"RecoveryCodes": recovery,
		"NewToken":      newToken,
		"Success":       success,
		"Error":         errMsg,
	})
}

// apiTokenStore creates a new personal API token for the signed-in user and
// flashes the plaintext token back to /profile. Mirrors
// ApiTokensController::store (POST /profile/tokens).
func (s *Server) apiTokenStore(w http.ResponseWriter, r *http.Request) {
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
	days := 90
	if v := strings.TrimSpace(r.FormValue("expires_days")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			n = -1
		}
		days = n
	}
	plain, _, err := identity.NewRepository(s.db).CreateAPIToken(r.Context(), user.ID, identity.NewAPIToken{
		Name: r.FormValue("name"), Ability: r.FormValue("ability"), ExpiresDays: days,
	})
	if err != nil {
		var te identity.APITokenError
		if errors.As(err, &te) {
			sess.Flash("error", te.Error())
		} else {
			s.log.Error("create api token", "error", err)
			sess.Flash("error", "Could not create the token.")
		}
		http.Redirect(w, r, "/profile#api-tokens", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{UserID: &user.ID, Username: user.Username, Action: "api.token_create",
		TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success, IP: clientIP(r),
		Details: map[string]any{"name": strings.TrimSpace(r.FormValue("name")), "ability": r.FormValue("ability"), "expires_days": days}})
	sess.Flash("api_token", plain)
	sess.Flash("success", "Token created. Copy it now: it is shown only once.")
	http.Redirect(w, r, "/profile#api-tokens", http.StatusSeeOther)
}

// apiTokenRevoke deletes one of the user's own API tokens. Mirrors
// ApiTokensController::revoke (POST /profile/tokens/{id}/revoke).
func (s *Server) apiTokenRevoke(w http.ResponseWriter, r *http.Request) {
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
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	n, err := identity.NewRepository(s.db).RevokeAPIToken(r.Context(), user.ID, id)
	if err != nil {
		s.log.Error("revoke api token", "error", err)
		sess.Flash("error", "Could not revoke the token.")
		http.Redirect(w, r, "/profile#api-tokens", http.StatusSeeOther)
		return
	}
	if n == 0 {
		sess.Flash("error", "Token not found.")
	} else {
		s.audit.Record(r.Context(), audit.Entry{UserID: &user.ID, Username: user.Username, Action: "api.token_revoke",
			TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success, IP: clientIP(r),
			Details: map[string]any{"token": id}})
		sess.Flash("success", "Token revoked.")
	}
	http.Redirect(w, r, "/profile#api-tokens", http.StatusSeeOther)
}

// profileRevokeSessions ends all of the signed-in user's other sessions by
// bumping their session_epoch (mirrors ProfileController::revokeSessions —
// POST /profile/sessions/revoke). The current session keeps working until
// the epoch in its sealed cookie is refreshed on the next request; every
// other session fails validation and is signed out.
func (s *Server) profileRevokeSessions(w http.ResponseWriter, r *http.Request) {
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
	if err := s.auth.RefreshOwnSession(r.Context(), sess, user); err != nil {
		s.log.Error("bump session epoch", "error", err)
		sess.Flash("error", "Could not sign out other sessions.")
		http.Redirect(w, r, "/profile#sessions", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{UserID: &user.ID, Username: user.Username, Action: "auth.sessions_revoked",
		TargetType: "user", TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success, IP: clientIP(r)})
	sess.Flash("success", "You were signed out everywhere else.")
	http.Redirect(w, r, "/profile#sessions", http.StatusSeeOther)
}

// profilePassword POST /profile/password — changes the signed-in user's
// password (ProfileController::password). Audits a wrong-current-password
// failure, validates the new password against PasswordWeakness (length,
// repetition, doesn't contain username, confirmation matches), then
// refreshes the current session's fingerprint and bumps the epoch so
// every other session is signed out.
func (s *Server) profilePassword(w http.ResponseWriter, r *http.Request) {
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
	current := r.FormValue("current_password")
	newPW := r.FormValue("password")
	confirm := r.FormValue("password_confirmation")
	repo := identity.NewRepository(s.db)
	err := repo.ChangeOwnPassword(r.Context(), user.ID, current, newPW, confirm)
	if err != nil {
		if pe, ok := err.(*identity.PasswordChangeError); ok {
			if pe.BadCurrent {
				uid := user.ID
				s.audit.Record(r.Context(), audit.Entry{
					Action: "user.password_change", TargetType: "user",
					TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Failure,
					Details: map[string]any{"reason": "bad_current_password"},
					UserID:  &uid, Username: user.Username,
				})
			}
			sess.Flash("error", pe.Message)
			http.Redirect(w, r, "/profile", http.StatusSeeOther)
			return
		}
		s.log.Error("change own password", "error", err)
		sess.Flash("error", "Could not change password.")
		http.Redirect(w, r, "/profile", http.StatusSeeOther)
		return
	}
	// Refresh current session so we stay signed in; bump epoch signs
	// every other session out.
	if err := s.auth.RefreshOwnSession(r.Context(), sess, user); err != nil {
		s.log.Error("refresh session after password change", "error", err)
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "user.password_change", TargetType: "user",
		TargetID: strconv.FormatInt(user.ID, 10), Result: audit.Success,
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Password changed. Other sessions have been signed out.")
	http.Redirect(w, r, "/profile", http.StatusSeeOther)
}
