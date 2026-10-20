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
