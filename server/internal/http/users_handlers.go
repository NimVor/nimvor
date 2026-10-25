package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshahzad/whacp/server/internal/twofactor"
)

// usersIndex lists the panel users the actor may manage: all users for staff,
// only their own customers for a reseller (UserRepository::manageableBy).
// Mirrors UsersController::index (route "users", users.view).
func (s *Server) usersIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	users, err := identity.NewRepository(s.db).ManageableBy(r.Context(), user)
	if err != nil {
		s.log.Error("list users", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "users", map[string]any{
		"User":  user,
		"CSRF":  session.CSRFToken(sessionFrom(r.Context())),
		"Users": users,
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
	success, _ := sess.PullFlash("success")
	errMsg, _ := sess.PullFlash("error")
	recovery, _ := sess.PullFlash("recovery_codes")
	newToken, _ := sess.PullFlash("api_token")
	s.render(w, "profile", map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sess),
		"Tokens":        tokens,
		"TwoFactor":     twoFactor,
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
	name := strings.TrimSpace(r.FormValue("name"))
	ability := r.FormValue("ability")
	if ability != "write" {
		ability = "read"
	}
	if name == "" {
		sess.Flash("error", "Token name is required.")
		http.Redirect(w, r, "/profile#api-tokens", http.StatusSeeOther)
		return
	}
	plain, _, err := identity.NewRepository(s.db).CreateAPIToken(r.Context(), user.ID, identity.NewAPIToken{
		Name: name, Ability: ability,
	})
	if err != nil {
		s.log.Error("create api token", "error", err)
		sess.Flash("error", "Could not create the token.")
		http.Redirect(w, r, "/profile#api-tokens", http.StatusSeeOther)
		return
	}
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
		sess.Flash("success", "Token revoked.")
	}
	http.Redirect(w, r, "/profile#api-tokens", http.StatusSeeOther)
}
