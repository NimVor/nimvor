package http

// RolesController port (panel/app/Http/Controllers/RolesController.php, 118
// LOC, 8 routes): role CRUD plus permission reset and per-role 2FA policy.
// Roles are a staff-only area; permission rows live in role_permissions and
// are synced against the seed default set when resetting a role.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

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

// rolesStore POST /roles
func (s *Server) rolesStore(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	name := strings.TrimSpace(r.PostFormValue("name"))
	label := strings.TrimSpace(r.PostFormValue("label"))
	desc := r.PostFormValue("description")
	scope := r.PostFormValue("scope")
	if scope != "own" && scope != "all" {
		scope = "own"
	}
	if name == "" || label == "" {
		rolesFlashErr(w, r, "Name and label are required.", "/roles/new")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(r.Context(),
		`INSERT INTO roles (name, label, description, is_system, scope, require_two_factor, created_at)
		 VALUES (?, ?, ?, 0, ?, 0, ?)`, name, label, desc, scope, now)
	if err != nil {
		rolesFlashErr(w, r, "A role with that name already exists.", "/roles/new")
		return
	}
	id, _ := res.LastInsertId()
	s.syncRolePerms(r, id, r.PostForm["permissions"])
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Role created.")
	http.Redirect(w, r, "/roles", http.StatusSeeOther)
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

// rolesUpdate POST /roles/{id}
func (s *Server) rolesUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/roles", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	label := strings.TrimSpace(r.PostFormValue("label"))
	desc := r.PostFormValue("description")
	scope := r.PostFormValue("scope")
	if scope != "own" && scope != "all" {
		scope = "own"
	}
	if label == "" {
		rolesFlashErr(w, r, "Label is required.", "/roles/"+strconv.FormatInt(id, 10)+"/edit")
		return
	}
	role, err := s.findRole(r, id)
	if err != nil || role == nil {
		rolesFlashErr(w, r, "The role no longer exists.", "/roles")
		return
	}
	if role.IsSystem {
		// System roles keep their name; only label/description/perms may be changed.
		_, err = s.db.ExecContext(r.Context(),
			`UPDATE roles SET label=?, description=?, scope=? WHERE id=?`, label, desc, scope, id)
	} else {
		name := strings.TrimSpace(r.PostFormValue("name"))
		if name == "" {
			rolesFlashErr(w, r, "Name is required.", "/roles/"+strconv.FormatInt(id, 10)+"/edit")
			return
		}
		_, err = s.db.ExecContext(r.Context(),
			`UPDATE roles SET name=?, label=?, description=?, scope=? WHERE id=?`, name, label, desc, scope, id)
	}
	if err != nil {
		rolesFlashErr(w, r, "A role with that name already exists.", "/roles/"+strconv.FormatInt(id, 10)+"/edit")
		return
	}
	s.syncRolePerms(r, id, r.PostForm["permissions"])
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Role updated.")
	http.Redirect(w, r, "/roles", http.StatusSeeOther)
}

// rolesReset POST /roles/{id}/reset — restore default permission set.
func (s *Server) rolesReset(w http.ResponseWriter, r *http.Request) {
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
	if !role.IsSystem {
		rolesFlashErr(w, r, "Only built-in roles can be reset to defaults.", "/roles/"+strconv.FormatInt(id, 10)+"/edit")
		return
	}
	if err := authz.SyncDefaults(r.Context(), s.db); err != nil {
		rolesFlashErr(w, r, "Reset failed.", "/roles/"+strconv.FormatInt(id, 10)+"/edit")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "The role has WhaCP's default permissions again.")
	http.Redirect(w, r, "/roles/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
}

// rolesDestroy POST /roles/{id}/delete
func (s *Server) rolesDestroy(w http.ResponseWriter, r *http.Request) {
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
	if role.IsSystem {
		rolesFlashErr(w, r, "Built-in roles cannot be deleted.", "/roles")
		return
	}
	// Refuse to delete a role that still has users.
	var n int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE role_id=?`, id).Scan(&n); err == nil && n > 0 {
		rolesFlashErr(w, r, "Move or delete its members first.", "/roles")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM roles WHERE id=? AND is_system=0`, id); err != nil {
		rolesFlashErr(w, r, "Delete failed.", "/roles")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Role deleted.")
	http.Redirect(w, r, "/roles", http.StatusSeeOther)
}

// rolesTwoFactor POST /roles/{id}/two-factor
func (s *Server) rolesTwoFactor(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/roles", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	on := r.PostFormValue("require_two_factor") == "1"
	if err := s.roles.SetRequireTwoFactor(r.Context(), id, on); err != nil {
		rolesFlashErr(w, r, "Policy update failed.", "/roles/"+strconv.FormatInt(id, 10)+"/edit")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Sign-in policy saved. Members without two-factor sign-in must set it up at their next page load.")
	http.Redirect(w, r, "/roles/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
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

func (s *Server) syncRolePerms(r *http.Request, roleID int64, granted []string) {
	// Clear then re-insert granted permissions.
	ctx := r.Context()
	s.db.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id=?`, roleID)
	if len(granted) == 0 {
		return
	}
	// Map names to ids.
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM permissions`)
	if err != nil {
		return
	}
	defer rows.Close()
	nameToID := map[string]int64{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return
		}
		nameToID[name] = id
	}
	tx, _ := s.db.BeginTx(ctx, nil)
	defer tx.Rollback()
	stmt, _ := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO role_permissions (role_id, permission_id) VALUES (?, ?)`)
	defer stmt.Close()
	seen := map[string]bool{}
	for _, p := range granted {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		pid, ok := nameToID[p]
		if !ok {
			continue
		}
		if _, err := stmt.ExecContext(ctx, roleID, pid); err != nil {
			return
		}
		seen[p] = true
	}
	_ = tx.Commit()
}

func rolesFlashErr(w http.ResponseWriter, r *http.Request, msg, to string) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}
