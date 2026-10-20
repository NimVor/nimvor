package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// currentServerID resolves the active server for the request. Multi-server
// switching (session _server_id) is a later concern; for now the local server
// (id 1) is used, matching ServerContext's default.
func (s *Server) currentServerID(r *http.Request) int64 { return 1 }

// websitesIndex lists the websites the signed-in user may see on the active
// server, tenant-scoped. Mirrors WebsitesController::index (route "websites",
// permission websites.view).
func (s *Server) websitesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sites, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list websites", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "websites", map[string]any{
		"User":  user,
		"CSRF":  session.CSRFToken(sessionFrom(r.Context())),
		"Sites": sites,
	})
}
