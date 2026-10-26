package http

import (
	"net/http"
	"path/filepath"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// pluginsDir is the directory the panel scans for plugins. In Go there is no
// PluginManager yet; the page renders an empty list so admins see the page
// without a 404, and POST /admin/plugins/{name}/toggle returns a friendly
// "plugin not found" error rather than crashing.
const pluginsDir = "plugins"

// pluginsIndex GET /admin/plugins — administrator plugin list.
// Mirrors PluginsController::index (plugins, plugins.manage).
func (s *Server) pluginsIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.renderError(w, http.StatusForbidden, "Access denied", "Plugins are managed by server administrators.")
		return
	}
	sess := sessionFrom(r.Context())
	// Discover any Go-land plugins directory if present, otherwise empty list.
	dir := pluginsDir
	if !filepath.IsAbs(dir) {
		// Relative to working directory — the panel stores plugins in its
		// own ./plugins folder; we don't assume it exists.
	}
	s.render(w, "plugins", map[string]any{
		"CSRF":    session.CSRFToken(sess),
		"User":    user,
		"Title":   "Plugins",
		"Plugins": []any{},
		"Dir":     dir,
		"Success": "",
		"Error":   "",
	})
}

// pluginsToggle POST /admin/plugins/{name}/toggle — enable/disable a plugin.
// Mirrors PluginsController::toggle; with no plugin runtime in Go yet this
// returns a friendly message.
func (s *Server) pluginsToggle(w http.ResponseWriter, r *http.Request) {
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
	if !hosting.IsAll(user) {
		http.Redirect(w, r, "/admin/plugins", http.StatusSeeOther)
		return
	}
	name := r.PathValue("name")
	enable := r.FormValue("enabled") == "1"
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action:     "plugin." + map[bool]string{true: "enable", false: "disable"}[enable],
		TargetType: "plugin", TargetID: name, Result: audit.Failure,
		Details: map[string]any{"reason": "plugin system not yet ported to Go"},
		UserID:  &uid, Username: user.Username,
	})
	sess.Flash("error", "Plugin '"+name+"' not found (plugins runtime is not yet ported to Go).")
	http.Redirect(w, r, "/admin/plugins", http.StatusSeeOther)
}
