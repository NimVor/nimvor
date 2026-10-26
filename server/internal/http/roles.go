package http

// RolesController port (panel/app/Http/Controllers/RolesController.php, 118
// LOC, 8 routes): role CRUD plus permission reset and per-role 2FA policy.
// Roles are a staff-only area; permission rows live in role_permissions and
// are synced against the seed default set when resetting a role.

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

type roleListRow struct {
	ID                       int64
	Label, Description, Name string
	System, Customized       bool
	Users                    int
	ActionLabel              string
	CanDelete                bool
	DeleteConfirm            string
}

// rolesIndex GET /roles (RolesController::index).
func (s *Server) rolesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	roles, err := s.roles.All(r.Context())
	if err != nil {
		http.Error(w, "failed to load roles", http.StatusInternalServerError)
		return
	}
	manage := s.can(r, "roles.manage")
	rows := make([]roleListRow, 0, len(roles))
	for _, ro := range roles {
		row := roleListRow{ID: ro.ID, Label: ro.Label, Description: ro.Description, Name: ro.Name, System: ro.IsSystem, Customized: ro.Customized,
			Users: ro.UserCount, ActionLabel: "Edit", CanDelete: !ro.IsSystem && manage, DeleteConfirm: "Delete the role \"" + ro.Label + "\"?"}
		if ro.Name == "admin" || !manage {
			row.ActionLabel = "View"
		}
		rows = append(rows, row)
	}
	s.render(w, "roles_index", map[string]any{
		"User": user, "CSRF": session.CSRFToken(sess), "Title": "Roles", "Rows": rows, "Manage": manage,
		"Success": pullFlashKey(sess, "success"), "Error": pullFlashKey(sess, "error"),
	})
}

type rolePerm struct {
	Name, Label, Why string
	On, Lock         bool
}

type rolePermGroup struct {
	Name, ID string
	Items    []rolePerm
}

type roleFormView struct {
	New, System, AdminRole, Readonly, OwnScope, Customized, CanReset, TwoFactor bool
	ID                                                                          int64
	Name, Label, Description, Title, Subtitle, ResetConfirm                     string
	Groups                                                                      []rolePermGroup
	Missing                                                                     string
	CanManage                                                                   bool
}

// roleForm builds the form of a new role (role == nil) or an existing one.
func (s *Server) roleForm(r *http.Request, role *authz.RoleRow, granted map[string]bool) (*roleFormView, error) {
	ctx := r.Context()
	user := userFrom(ctx)
	manage := s.can(r, "roles.manage")
	v := &roleFormView{New: role == nil, CanManage: manage}
	if role != nil {
		v.ID, v.Name, v.Label, v.Description = role.ID, role.Name, role.Label, role.Description
		v.System, v.Customized, v.TwoFactor = role.IsSystem, role.Customized, role.RequireTwoFactor
		v.AdminRole = role.IsSystem && role.Name == "admin"
		v.OwnScope = role.Scope == "own"
		v.CanReset = v.System && !v.AdminRole && manage
		v.ResetConfirm = "Give \"" + role.Label + "\" WhaCP's default permissions again?"
		v.Title, v.Subtitle = role.Label, role.Description
	} else {
		v.Title, v.Subtitle = "New role", "Choose what members of the role may do"
	}
	v.Readonly = v.AdminRole || !manage
	own := map[string]bool{}
	if names, err := s.roles.PermissionsFor(ctx, user.RoleID); err == nil {
		for _, n := range names {
			own[n] = true
		}
	}
	ownScope := map[string]bool{}
	for _, n := range authz.OwnScopeAllowed() {
		ownScope[n] = true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, label, grp FROM permissions ORDER BY grp, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	idx := map[string]int{}
	for rows.Next() {
		var name, label, grp string
		if err := rows.Scan(&name, &label, &grp); err != nil {
			return nil, err
		}
		on := granted[name]
		why := ""
		if v.OwnScope && !ownScope[name] && !strings.HasPrefix(name, "plugin.") {
			why = "Server-wide: not for resellers or hosting customers"
		} else if !own[name] {
			why = "You do not have this permission yourself"
		}
		g, ok := idx[grp]
		if !ok {
			g = len(v.Groups)
			idx[grp] = g
			v.Groups = append(v.Groups, rolePermGroup{Name: grp, ID: "perm-grp-" + strings.Trim(nonAlnum.ReplaceAllString(strings.ToLower(grp), "-"), "-")})
		}
		v.Groups[g].Items = append(v.Groups[g].Items, rolePerm{Name: name, Label: label, On: on, Why: why, Lock: !on && why != ""})
	}
	if role != nil && role.IsSystem && !v.AdminRole {
		for _, d := range authz.DefaultRoles {
			if d.Name != role.Name {
				continue
			}
			var missing []string
			for _, p := range d.Permissions {
				if p != "*" && !granted[p] {
					missing = append(missing, p)
				}
			}
			v.Missing = strings.Join(missing, ", ")
		}
	}
	return v, rows.Err()
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Server) renderRoleForm(w http.ResponseWriter, r *http.Request, role *authz.RoleRow, granted map[string]bool) {
	v, err := s.roleForm(r, role, granted)
	if err != nil {
		http.Error(w, "failed to load permissions", http.StatusInternalServerError)
		return
	}
	sess := sessionFrom(r.Context())
	if role == nil {
		old := flashJSON(sess, "old")
		if n, ok := old["name"]; ok {
			v.Name = n
		}
		if l, ok := old["label"]; ok {
			v.Label = l
		}
		if d, ok := old["description"]; ok {
			v.Description = d
		}
	}
	s.render(w, "roles_form", map[string]any{
		"User": userFrom(r.Context()), "CSRF": session.CSRFToken(sess), "Title": v.Title, "V": v,
		"Success": pullFlashKey(sess, "success"), "Error": pullFlashKey(sess, "error"),
	})
}

// rolesCreate GET /roles/new
func (s *Server) rolesCreate(w http.ResponseWriter, r *http.Request) {
	s.renderRoleForm(w, r, nil, map[string]bool{})
}

// rolesEdit GET /roles/{id}/edit
func (s *Server) rolesEdit(w http.ResponseWriter, r *http.Request) {
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
	s.renderRoleForm(w, r, role, grantSet)
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
