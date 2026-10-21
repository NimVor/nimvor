package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"github.com/mchkhawarshad/whacp/server/internal/twofactor"
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
	s.render(w, "profile", map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sess),
		"Tokens":        tokens,
		"TwoFactor":     twoFactor,
		"SetupSecret":   setupSecret,
		"SetupURI":      setupURI,
		"RecoveryCodes": recovery,
		"Success":       success,
		"Error":         errMsg,
	})
}
