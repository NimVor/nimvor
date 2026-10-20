package http

import (
	"errors"
	"net/http"
	"strconv"

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

// websiteShow renders one website (scoped to the user) with its databases.
// A website outside the user's tenant is a 404 — the id never grants access.
// Mirrors WebsitesController::show (route "websites.show", websites.view).
func (s *Server) websiteShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	serverID := s.currentServerID(r)
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), serverID, id, user)
	if errors.Is(err, hosting.ErrWebsiteNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.log.Error("find website", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	dbs, err := hosting.NewDatabaseRepository(s.db).All(r.Context(), serverID, user, &id)
	if err != nil {
		s.log.Error("website databases", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "website_show", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sessionFrom(r.Context())),
		"Site":      site,
		"Databases": dbs,
	})
}

// databasesIndex lists the databases the user may see, tenant-scoped. The
// mysql feature withholds databases.view when a plan disables it, so the
// permission guard already enforces the feature. Mirrors
// DatabasesController::index (route "databases", databases.view).
func (s *Server) databasesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	dbs, err := hosting.NewDatabaseRepository(s.db).All(r.Context(), s.currentServerID(r), user, nil)
	if err != nil {
		s.log.Error("list databases", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "databases", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sessionFrom(r.Context())),
		"Databases": dbs,
	})
}

// notFound renders a 404 page (JSON or HTML).
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Not found."},
		})
		return
	}
	renderStatus(w, s, "error", map[string]any{"Title": "Not found", "Message": "That page does not exist."}, http.StatusNotFound)
}
