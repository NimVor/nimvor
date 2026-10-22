package http

// This file ports the per-website security and health overview pages
// (SitesController::security and ::health): a small list of checks, each
// with a status, an explanation and how to fix it (and whether the
// customer or the hosting provider has to act).
//
// Routes:
//   GET /sites/{id}/security  (websites.view)
//   GET /sites/{id}/health    (websites.view)

import (
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// healthSiteOf resolves the website for a /sites/{id} route (nil = not
// found or no access for the signed-in user).
func (s *Server) healthSiteOf(r *http.Request, id string) *hosting.Website {
	user := userFrom(r.Context())
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), n, user)
	if err != nil || site == nil {
		return nil
	}
	return site
}

// siteSecurity renders the website's security checks (route
// "sites.security").
func (s *Server) siteSecurity(w http.ResponseWriter, r *http.Request) {
	site := s.healthSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.siteHealthNotFound(w)
		return
	}
	user := userFrom(r.Context())
	svc := hosting.NewSiteHealthService(s.db, s.sslService(r, user))
	checks, err := svc.Security(r.Context(), site)
	if err != nil {
		s.log.Error("site security checks", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.renderSiteHealthPage(w, r, site, "site_security", checks)
}

// siteHealth renders the website's health checks (route "sites.health").
func (s *Server) siteHealth(w http.ResponseWriter, r *http.Request) {
	site := s.healthSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.siteHealthNotFound(w)
		return
	}
	user := userFrom(r.Context())
	svc := hosting.NewSiteHealthService(s.db, s.sslService(r, user))
	checks, err := svc.Health(r.Context(), site)
	if err != nil {
		s.log.Error("site health checks", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.renderSiteHealthPage(w, r, site, "site_health", checks)
}

// renderSiteHealthPage renders the shared security/health layout: the
// check list plus an infobar counting the items that need attention.
func (s *Server) renderSiteHealthPage(w http.ResponseWriter, r *http.Request, site *hosting.Website, name string, checks []hosting.SiteCheck) {
	problems := 0
	for _, c := range checks {
		if c.Level != "ok" {
			problems++
		}
	}
	data := map[string]any{
		"User":     userFrom(r.Context()),
		"CSRF":     session.CSRFToken(sessionFrom(r.Context())),
		"Site":     site,
		"Checks":   checks,
		"Problems": problems,
	}
	s.render(w, name, data)
}

// siteHealthNotFound renders the 404 page for a website the user cannot
// see (mirrors SitesController::notFound).
func (s *Server) siteHealthNotFound(w http.ResponseWriter) {
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Website not found",
		"Message": "This website does not exist or you do not have access to it.",
	}, http.StatusNotFound)
}
