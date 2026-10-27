package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
)

// authorizer builds a per-request Authorizer. The Authorizer caches role
// permissions for the life of the request, matching the PHP request-scoped
// cache, so a fresh one per request is correct and cheap.
func (s *Server) authorizer() *authz.Authorizer {
	return authz.NewAuthorizer(s.roles, s.features)
}

// requirePermission guards a handler with an RBAC permission. It must be
// composed inside requireAuth (it assumes a user is present). Mirrors the
// permission half of the Authenticate middleware: a denied check is a 403 and
// is answered as JSON or an HTML error page per the client. Plan feature
// gating is applied by Authorizer.Can, so a permission withheld by a disabled
// feature is also denied here.
func (s *Server) requirePermission(permission string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r.Context())
		ok, err := s.authorizer().Can(r.Context(), user, permission)
		if err != nil {
			s.log.Error("permission check", "permission", permission, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok {
			s.deny(w, r, "You do not have permission to access this page.")
			return
		}
		next(w, r)
	}
}

// requireFeatureRoute refuses a route owned by a plan-disabled feature,
// matching the feature half of the Authenticate middleware (routes are matched
// by name, so features that share a permission with others are still gated).
func (s *Server) requireFeatureRoute(routeName string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r.Context())
		ok, err := s.authorizer().RouteAllowed(r.Context(), user, routeName)
		if err != nil {
			s.log.Error("feature check", "route", routeName, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok {
			s.deny(w, r, "This feature is not included in your hosting plan.")
			return
		}
		next(w, r)
	}
}

// guard is the common composition for a feature-gated, permission-protected,
// authenticated page: requireAuth → requireFeatureRoute → requirePermission.
// routeName may be "" to skip feature gating; permission may be "" to skip the
// RBAC check (rare — most pages carry one).
func (s *Server) guard(routeName, permission string, next http.HandlerFunc) http.HandlerFunc {
	h := next
	if permission != "" {
		h = s.requirePermission(permission, h)
	}
	if routeName != "" {
		h = s.requireFeatureRoute(routeName, h)
	}
	return s.sessionCtx(s.requireAuth(s.requirePolicies(routeName, h)))
}

// deny writes a 403 as JSON or an HTML error page.
func (s *Server) deny(w http.ResponseWriter, r *http.Request, message string) {
	if wantsJSON(r) {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"success": false,
			"error":   map[string]string{"message": message},
		})
		return
	}
	var data = map[string]any{"Title": "Access denied", "Message": message}
	renderStatus(w, s, "error", data, http.StatusForbidden)
}

// renderStatus renders a template with an explicit status code.
func renderStatus(w http.ResponseWriter, s *Server, name string, data any, code int) {
	if m, ok := data.(map[string]any); ok {
		if _, has := m["Status"]; !has {
			m["Status"] = code
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if err := s.tpl.Render(w, name, data); err != nil {
		s.log.Error("render error page", "error", err)
	}
}
