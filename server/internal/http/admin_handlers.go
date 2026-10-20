package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// rolesIndex lists the roles with user counts (staff only). Mirrors
// RolesController::index (route "roles", users.view).
func (s *Server) rolesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	roles, err := s.roles.All(r.Context())
	if err != nil {
		s.log.Error("list roles", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "roles", map[string]any{
		"User":  user,
		"CSRF":  session.CSRFToken(sessionFrom(r.Context())),
		"Roles": roles,
	})
}

// plansIndex lists hosting plans (staff only). Mirrors PlansController::index
// (route "plans", plans.manage).
func (s *Server) plansIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	plans, err := hosting.NewPlanRepository(s.db).Visible(r.Context(), user)
	if err != nil {
		s.log.Error("list plans", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "plans", map[string]any{
		"User":  user,
		"CSRF":  session.CSRFToken(sessionFrom(r.Context())),
		"Plans": plans,
	})
}

// serversIndex lists the managed servers (staff only). Mirrors
// ServersController::index (route "servers", servers.manage).
func (s *Server) serversIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	servers, err := hosting.NewServerRepository(s.db).All(r.Context())
	if err != nil {
		s.log.Error("list servers", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "servers", map[string]any{
		"User":    user,
		"CSRF":    session.CSRFToken(sessionFrom(r.Context())),
		"Servers": servers,
	})
}
