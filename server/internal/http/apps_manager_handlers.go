// This file ports Administration → Application Manager (AppManagerController):
// the global installer switch, whole-category enablement and the per-app
// enabled / customer-visible flags. The route permissions do the gating
// (settings.view / settings.manage, administrators in practice).
package http

import (
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// appManagerAudit records a manager change (the PHP AuditLogger,
// server-scoped).
func (s *Server) appManagerAudit(r *http.Request, action string, details map[string]any) {
	user := userFrom(r.Context())
	s.websiteAuditHook(r, user)(r.Context(), action, "server",
		strconv.FormatInt(s.currentServerID(r), 10), "success", details)
}

// appsManagerIndex renders the manager page (AppManagerController::index).
func (s *Server) appsManagerIndex(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	cats, err := s.appCatalog().ForAdmin(r.Context())
	if err != nil {
		s.log.Error("app catalog", "error", err)
		sess.Flash("error", "The application catalog could not be read.")
		http.Redirect(w, r, "/apps-manager", http.StatusSeeOther)
		return
	}
	s.render(w, "server_app_manager", map[string]any{
		"User":             user,
		"CSRF":             session.CSRFToken(sess),
		"Categories":       cats,
		"InstallerEnabled": s.appCatalog().InstallerEnabled(r.Context()),
		"CanManage":        s.can(r, "settings.manage"),
		"Success":          pullFlashKey(sess, "success"),
		"Error":            pullFlashKey(sess, "error"),
	})
}

// appManagerWrite runs one manager change: audit + success flash, or the
// fallback error flash (PHP: no catch — a DB error is a hard failure).
func (s *Server) appManagerWrite(w http.ResponseWriter, r *http.Request, action string, details map[string]any, change func() error, msg func() string) {
	sess := sessionFrom(r.Context())
	if err := change(); err != nil {
		s.log.Error("app manager", "action", action, "error", err)
		sess.Flash("error", "The change could not be made.")
		http.Redirect(w, r, "/apps-manager", http.StatusSeeOther)
		return
	}
	s.appManagerAudit(r, action, details)
	sess.Flash("success", msg())
	http.Redirect(w, r, "/apps-manager", http.StatusSeeOther)
}

// appsManagerInstaller flips the global installer switch
// (AppManagerController::installer).
func (s *Server) appsManagerInstaller(w http.ResponseWriter, r *http.Request) {
	if !postCSRF(w, r) {
		return
	}
	user := userFrom(r.Context())
	on := r.PostFormValue("on") == "1"
	s.appManagerWrite(w, r, "apps.installer", map[string]any{"enabled": on}, func() error {
		return s.appCatalog().SetInstallerEnabled(r.Context(), on, user.ID)
	}, func() string {
		if on {
			return "Application installer enabled."
		}
		return "Application installer disabled."
	})
}

// appsManagerCategory flips one category's flag
// (AppManagerController::category).
func (s *Server) appsManagerCategory(w http.ResponseWriter, r *http.Request) {
	if !postCSRF(w, r) {
		return
	}
	slug := r.PostFormValue("slug")
	on := r.PostFormValue("on") == "1"
	s.appManagerWrite(w, r, "apps.category",
		map[string]any{"category": slug, "enabled": on}, func() error {
			return s.appCatalog().SetCategoryEnabled(r.Context(), slug, on)
		}, func() string {
			if on {
				return "Category enabled."
			}
			return "Category disabled."
		})
}

// appsManagerApp flips one application's enabled / customer_visible flag
// (AppManagerController::app); an unknown slug is a flash, no change.
func (s *Server) appsManagerApp(w http.ResponseWriter, r *http.Request) {
	if !postCSRF(w, r) {
		return
	}
	sess := sessionFrom(r.Context())
	slug := r.PostFormValue("slug")
	field := r.PostFormValue("field")
	if field != "customer_visible" {
		field = "enabled"
	}
	on := r.PostFormValue("on") == "1"
	app, err := s.appCatalog().Find(r.Context(), slug)
	if err != nil || app == nil {
		sess.Flash("error", "Unknown application.")
		http.Redirect(w, r, "/apps-manager", http.StatusSeeOther)
		return
	}
	s.appManagerWrite(w, r, "apps.app",
		map[string]any{"app": slug, "field": field, "on": on}, func() error {
			return s.appCatalog().SetAppFlag(r.Context(), slug, field, on)
		}, func() string { return "Application updated." })
}
