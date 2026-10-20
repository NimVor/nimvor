package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
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

// profileShow renders the signed-in user's own account and API tokens
// (read-only; the 2FA, password and token-management forms are a later batch).
// Mirrors ProfileController::show (route "profile", auth only).
func (s *Server) profileShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	tokens, err := identity.NewRepository(s.db).APITokens(r.Context(), user.ID)
	if err != nil {
		s.log.Error("list api tokens", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "profile", map[string]any{
		"User":   user,
		"CSRF":   session.CSRFToken(sessionFrom(r.Context())),
		"Tokens": tokens,
	})
}
