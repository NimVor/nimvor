package http

// Role administration (RoleService port): validation, the "never beyond your
// own permissions" rule, built-in role protection, transactional permission
// writes and auditing.

import (
	"context"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

var roleNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)

// roleInput is the validated role form.
type roleInput struct {
	Name, Label, Description string
	Perms                    []string
}

// roleError is a message shown to the user.
type roleError string

func (e roleError) Error() string { return string(e) }

func (s *Server) roleActorPerms(ctx context.Context, actor *identity.User) (map[string]bool, error) {
	list, err := s.roles.PermissionsFor(ctx, actor.RoleID)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, p := range list {
		set[p] = true
	}
	return set, nil
}

// validateRole mirrors RoleService::validate.
func (s *Server) validateRole(ctx context.Context, name, label, desc string, perms []string, isNew bool) (*roleInput, error) {
	name, label, desc = strings.TrimSpace(name), strings.TrimSpace(label), strings.TrimSpace(desc)
	if isNew {
		if !roleNameRe.MatchString(name) {
			return nil, roleError("Use 3–32 lowercase letters, digits or underscores, starting with a letter.")
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM roles WHERE name = ?`, name).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, roleError("A role with this name already exists.")
		}
	}
	if label == "" || utf8.RuneCountInString(label) > 60 {
		return nil, roleError("Enter a label of at most 60 characters.")
	}
	if utf8.RuneCountInString(desc) > 255 {
		return nil, roleError("Use at most 255 characters for the description.")
	}
	known, err := s.allPermissionNames(ctx)
	if err != nil {
		return nil, err
	}
	knownSet := map[string]bool{}
	for _, k := range known {
		knownSet[k] = true
	}
	seen := map[string]bool{}
	var clean, unknown []string
	for _, p := range perms {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		if !knownSet[p] {
			unknown = append(unknown, p)
			continue
		}
		clean = append(clean, p)
	}
	if len(unknown) > 0 {
		return nil, roleError("Unknown permission(s): " + strings.Join(unknown, ", "))
	}
	sort.Strings(clean)
	return &roleInput{Name: name, Label: label, Description: desc, Perms: clean}, nil
}

// checkGrantable mirrors RoleService::checkGrantable.
func (s *Server) checkGrantable(ctx context.Context, perms []string, actor *identity.User, scope string) error {
	if scope == "own" {
		allowed := map[string]bool{}
		for _, p := range authz.OwnScopeAllowed() {
			allowed[p] = true
		}
		var outside []string
		for _, p := range perms {
			if !allowed[p] {
				outside = append(outside, p)
			}
		}
		if len(outside) > 0 {
			return roleError("Resellers and hosting customers only see their own objects; server-wide permissions cannot be given to them: " + strings.Join(outside, ", "))
		}
	}
	mine, err := s.roleActorPerms(ctx, actor)
	if err != nil {
		return err
	}
	var beyond []string
	for _, p := range perms {
		if !mine[p] {
			beyond = append(beyond, p)
		}
	}
	if len(beyond) > 0 {
		return roleError("You cannot grant permissions you do not have yourself: " + strings.Join(beyond, ", "))
	}
	return nil
}

// setRolePerms replaces a role's permissions in one transaction.
func (s *Server) setRolePerms(ctx context.Context, roleID int64, perms []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, roleID); err != nil {
		return err
	}
	for _, p := range perms {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO role_permissions (role_id, permission_id) SELECT ?, id FROM permissions WHERE name = ?`,
			roleID, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Server) auditRole(r *http.Request, actor *identity.User, action string, id int64, result string, details map[string]any) {
	uid := actor.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &uid, Username: actor.Username, Action: action, TargetType: "role",
		TargetID: strconv.FormatInt(id, 10), Result: result, IP: clientIP(r), Details: details,
	})
}

func (s *Server) roleFail(w http.ResponseWriter, r *http.Request, err error, back string) {
	if re, ok := err.(roleError); ok {
		rolesFlashErr(w, r, string(re), back)
		return
	}
	s.log.Error("role administration", "error", err)
	rolesFlashErr(w, r, "The role could not be saved.", back)
}

func (s *Server) isAdminRole(role *authz.RoleRow) bool { return role.IsSystem && role.Name == "admin" }

// rolesStore POST /roles
func (s *Server) rolesStore(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	actor := userFrom(r.Context())
	back := "/roles/new"
	in, err := s.validateRole(r.Context(), r.PostFormValue("name"), r.PostFormValue("label"), r.PostFormValue("description"), r.PostForm["permissions"], true)
	if err == nil {
		err = s.checkGrantable(r.Context(), in.Perms, actor, "all")
	}
	if err != nil {
		s.roleFail(w, r, err, back)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.ExecContext(r.Context(),
		`INSERT INTO roles (name, label, description, is_system, scope, require_two_factor, created_at)
		 VALUES (?, ?, ?, 0, 'all', 0, ?)`, in.Name, in.Label, in.Description, now)
	if err != nil {
		s.roleFail(w, r, err, back)
		return
	}
	id, _ := res.LastInsertId()
	if err := s.setRolePerms(r.Context(), id, in.Perms); err != nil {
		s.roleFail(w, r, err, back)
		return
	}
	s.auditRole(r, actor, "role.create", id, audit.Success, map[string]any{"name": in.Name, "permissions": in.Perms})
	sessionFrom(r.Context()).Flash("success", "Role created.")
	http.Redirect(w, r, "/roles", http.StatusSeeOther)
}

// rolesUpdate POST /roles/{id}
func (s *Server) rolesUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/roles", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	actor := userFrom(r.Context())
	back := "/roles/" + strconv.FormatInt(id, 10) + "/edit"
	role, err := s.findRole(r, id)
	if err != nil || role == nil {
		rolesFlashErr(w, r, "The role no longer exists.", "/roles")
		return
	}
	if s.isAdminRole(role) {
		rolesFlashErr(w, r, "The Administrator role cannot be changed. It always has every permission, so the server can never be left without a full administrator. Create a custom role, or change Operator, for narrower access.", back)
		return
	}
	label, desc := r.PostFormValue("label"), r.PostFormValue("description")
	if role.IsSystem { // built-in roles keep their label and description
		label, desc = role.Label, role.Description
	}
	in, err := s.validateRole(r.Context(), role.Name, label, desc, r.PostForm["permissions"], false)
	if err != nil {
		s.roleFail(w, r, err, back)
		return
	}
	before, err := s.roles.PermissionsFor(r.Context(), id)
	if err != nil {
		s.roleFail(w, r, err, back)
		return
	}
	beforeSet := map[string]bool{}
	for _, p := range before {
		beforeSet[p] = true
	}
	var granted, revoked []string
	inSet := map[string]bool{}
	for _, p := range in.Perms {
		inSet[p] = true
		if !beforeSet[p] {
			granted = append(granted, p)
		}
	}
	for _, p := range before {
		if !inSet[p] {
			revoked = append(revoked, p)
		}
	}
	// Only what is newly granted is checked: grants the role has may stay.
	if err := s.checkGrantable(r.Context(), granted, actor, role.Scope); err != nil {
		s.roleFail(w, r, err, back)
		return
	}
	if role.IsSystem {
		_, err = s.db.ExecContext(r.Context(), `UPDATE roles SET customized = 1 WHERE id = ?`, id)
	} else {
		_, err = s.db.ExecContext(r.Context(), `UPDATE roles SET label = ?, description = ? WHERE id = ?`, in.Label, in.Description, id)
	}
	if err == nil {
		err = s.setRolePerms(r.Context(), id, in.Perms)
	}
	if err != nil {
		s.roleFail(w, r, err, back)
		return
	}
	s.auditRole(r, actor, "role.update", id, audit.Success, map[string]any{"name": role.Name, "granted": granted, "revoked": revoked})
	sessionFrom(r.Context()).Flash("success", "Role updated.")
	http.Redirect(w, r, "/roles", http.StatusSeeOther)
}

// rolesReset POST /roles/{id}/reset — a built-in role back to its defaults.
func (s *Server) rolesReset(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/roles", http.StatusSeeOther)
		return
	}
	actor := userFrom(r.Context())
	back := "/roles/" + strconv.FormatInt(id, 10) + "/edit"
	role, err := s.findRole(r, id)
	if err != nil || role == nil {
		rolesFlashErr(w, r, "The role no longer exists.", "/roles")
		return
	}
	if !role.IsSystem {
		rolesFlashErr(w, r, "Only built-in roles have WhaCP defaults.", back)
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `UPDATE roles SET customized = 0 WHERE id = ?`, id); err != nil {
		s.roleFail(w, r, err, back)
		return
	}
	if err := authz.SyncDefaults(r.Context(), s.db); err != nil {
		s.roleFail(w, r, err, back)
		return
	}
	s.auditRole(r, actor, "role.reset", id, audit.Success, map[string]any{"name": role.Name})
	sessionFrom(r.Context()).Flash("success", "The role has WhaCP's default permissions again.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// rolesDestroy POST /roles/{id}/delete
func (s *Server) rolesDestroy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Redirect(w, r, "/roles", http.StatusSeeOther)
		return
	}
	actor := userFrom(r.Context())
	role, err := s.findRole(r, id)
	if err != nil || role == nil {
		rolesFlashErr(w, r, "The role no longer exists.", "/roles")
		return
	}
	if role.IsSystem {
		rolesFlashErr(w, r, "Built-in roles cannot be changed. They are defined by WhaCP and updated with it. Create a custom role instead.", "/roles")
		return
	}
	var n int
	if err := s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE role_id = ?`, id).Scan(&n); err != nil {
		s.roleFail(w, r, err, "/roles")
		return
	}
	if n > 0 {
		rolesFlashErr(w, r, "The role is still assigned to users. Deleting it would leave those users without a role. Move the users to another role first.", "/roles")
		return
	}
	if _, err := s.db.ExecContext(r.Context(), `DELETE FROM roles WHERE id = ? AND is_system = 0`, id); err != nil {
		s.roleFail(w, r, err, "/roles")
		return
	}
	s.auditRole(r, actor, "role.delete", id, audit.Success, map[string]any{"name": role.Name})
	sessionFrom(r.Context()).Flash("success", "Role deleted.")
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
	actor := userFrom(r.Context())
	back := "/roles/" + strconv.FormatInt(id, 10) + "/edit"
	role, err := s.findRole(r, id)
	if err != nil || role == nil {
		rolesFlashErr(w, r, "The role no longer exists.", "/roles")
		return
	}
	on := r.PostFormValue("require_two_factor") == "1"
	if err := s.roles.SetRequireTwoFactor(r.Context(), id, on); err != nil {
		rolesFlashErr(w, r, "Policy update failed.", back)
		return
	}
	s.auditRole(r, actor, "role.two_factor_policy", id, audit.Success, map[string]any{"role": role.Name, "required": on})
	sessionFrom(r.Context()).Flash("success", "Sign-in policy saved. Members without two-factor sign-in must set it up at their next page load.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}
