package http

// RolesController port (panel/app/Http/Controllers/RolesController.php, 118
// LOC, 8 routes): role CRUD plus permission reset and per-role 2FA policy.
// Roles are a staff-only area; permission rows live in role_permissions and
// are synced against the seed default set when resetting a role.

import (
	"context"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// rolesIndex GET /roles
func (s *Server) rolesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	roles, err := s.roles.All(r.Context())
	if err != nil {
		http.Error(w, "failed to load roles", http.StatusInternalServerError)
		return
	}
	s.render(w, "roles_index", map[string]any{
		"User":  user,
		"CSRF":  session.CSRFToken(sessionFrom(r.Context())),
		"Title": "Roles",
		"Roles": roles,
	})
}

// rolesCreate GET /roles/new
func (s *Server) rolesCreate(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	perms, err := s.allPermissionCheckboxes(r.Context(), map[string]bool{})
	if err != nil {
		http.Error(w, "failed to load permissions", http.StatusInternalServerError)
		return
	}
	s.render(w, "roles_form", map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sessionFrom(r.Context())),
		"Title":       "New role",
		"Role":        map[string]any{"id": 0, "name": "", "label": "", "description": "", "scope": "own", "require_two_factor": false},
		"Permissions": perms,
	})
}

// rolesEdit GET /roles/{id}/edit
func (s *Server) rolesEdit(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/roles", http.StatusSeeOther)
		return
	}
	role, err := s.findRole(r, id)
	if err != nil || role == nil {
		rolesFlashErr(w, r, "The role no longer exists.", "/roles")
		return
	}
	granted, err := s.roles.PermissionsFor(r.Context(), id)
	if err != nil {
		http.Error(w, "failed to load permissions", http.StatusInternalServerError)
		return
	}
	grantSet := map[string]bool{}
	for _, p := range granted {
		grantSet[p] = true
	}
	perms, err := s.allPermissionCheckboxes(r.Context(), grantSet)
	if err != nil {
		http.Error(w, "failed to load permissions", http.StatusInternalServerError)
		return
	}
	s.render(w, "roles_form", map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sessionFrom(r.Context())),
		"Title":       "Edit role",
		"Role":        role,
		"Permissions": perms,
	})
}

// ---- helpers ----

func (s *Server) findRole(r *http.Request, id int64) (*authz.RoleRow, error) {
	roles, err := s.roles.All(r.Context())
	if err != nil {
		return nil, err
	}
	for i := range roles {
		if roles[i].ID == id {
			return &roles[i], nil
		}
	}
	return nil, nil
}

func (s *Server) allPermissionNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM permissions ORDER BY grp, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

type permCheck struct {
	Name    string
	Granted bool
}

func (s *Server) allPermissionCheckboxes(ctx context.Context, granted map[string]bool) ([]permCheck, error) {
	names, err := s.allPermissionNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]permCheck, 0, len(names))
	for _, n := range names {
		out = append(out, permCheck{Name: n, Granted: granted[n]})
	}
	return out, nil
}

func rolesFlashErr(w http.ResponseWriter, r *http.Request, msg, to string) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}
