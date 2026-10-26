package authz

import (
	"context"
	"database/sql"
	"sync"

	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// RoleRepository reads a role's permission set. Mirrors
// RoleRepository::permissionsFor.
type RoleRepository struct{ db *sql.DB }

// NewRoleRepository wraps a database handle.
func NewRoleRepository(db *sql.DB) *RoleRepository { return &RoleRepository{db: db} }

// RoleRow is a role for the admin list, with how many users hold it.
type RoleRow struct {
	ID               int64
	Name             string
	Label            string
	Description      string
	IsSystem         bool
	Scope            string
	RequireTwoFactor bool
	UserCount        int
	Customized       bool // a built-in role whose permissions were changed from the defaults
}

// All lists every role with its user count. Mirrors RoleRepository::all.
// Roles are server-wide objects; viewing them is a staff permission, so there
// is no tenant scoping.
func (r *RoleRepository) All(ctx context.Context) ([]RoleRow, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.id, r.name, r.label, r.description, r.is_system, r.scope, r.require_two_factor,
		       (SELECT COUNT(*) FROM users u WHERE u.role_id = r.id) AS user_count, r.customized
		FROM roles r ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RoleRow
	for rows.Next() {
		var rr RoleRow
		if err := rows.Scan(&rr.ID, &rr.Name, &rr.Label, &rr.Description, &rr.IsSystem, &rr.Scope, &rr.RequireTwoFactor, &rr.UserCount, &rr.Customized); err != nil {
			return nil, err
		}
		out = append(out, rr)
	}
	return out, rows.Err()
}

// Find loads a single role by id. Returns sql.ErrNoRows when missing.
// Mirrors RoleRepository::find.
func (r *RoleRepository) Find(ctx context.Context, id int64) (*RoleRow, error) {
	var rr RoleRow
	err := r.db.QueryRowContext(ctx,
		`SELECT id, name, label, description, is_system, scope, require_two_factor, customized FROM roles WHERE id = ?`,
		id,
	).Scan(&rr.ID, &rr.Name, &rr.Label, &rr.Description, &rr.IsSystem, &rr.Scope, &rr.RequireTwoFactor, &rr.Customized)
	if err != nil {
		return nil, err
	}
	return &rr, nil
}

// SetRequireTwoFactor is RoleRepository::setRequireTwoFactor.
func (r *RoleRepository) SetRequireTwoFactor(ctx context.Context, id int64, required bool) error {
	_, err := r.db.ExecContext(ctx, `UPDATE roles SET require_two_factor = ? WHERE id = ?`, required, id)
	return err
}

// PermissionsFor returns the permission names granted to a role.
func (r *RoleRepository) PermissionsFor(ctx context.Context, roleID int64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT p.name FROM permissions p
		 JOIN role_permissions rp ON rp.permission_id = p.id
		 WHERE rp.role_id = ? ORDER BY p.name`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// Authorizer answers permission and route questions for a user. It mirrors
// Security\Authorizer: a role's permission must be granted AND not withheld by
// a plan-disabled feature. A per-role permission cache matches the PHP
// request-scoped cache; build one Authorizer per request.
type Authorizer struct {
	roles    *RoleRepository
	features *FeatureService

	mu    sync.Mutex
	cache map[int64][]string
}

// NewAuthorizer builds an Authorizer. features may be nil (no plan gating).
func NewAuthorizer(roles *RoleRepository, features *FeatureService) *Authorizer {
	return &Authorizer{roles: roles, features: features, cache: map[int64][]string{}}
}

// Can reports whether the user holds the permission. A nil, inactive or
// suspended user is always denied (fail-closed). Mirrors Authorizer::can.
func (a *Authorizer) Can(ctx context.Context, u *identity.User, permission string) (bool, error) {
	if !identity.CanAct(u) {
		return false, nil
	}
	perms, err := a.permissions(ctx, u.RoleID)
	if err != nil {
		return false, err
	}
	held := false
	for _, p := range perms {
		if p == permission {
			held = true
			break
		}
	}
	if !held {
		return false, nil
	}
	if a.features == nil {
		return true, nil
	}
	return a.features.PermissionAllowed(ctx, u, permission)
}

// RouteAllowed reports whether a feature-gated route is allowed for the user.
// A nil user or absent feature service allows everything (the auth middleware
// handles the unauthenticated case separately). Mirrors Authorizer::routeAllowed.
func (a *Authorizer) RouteAllowed(ctx context.Context, u *identity.User, route string) (bool, error) {
	if u == nil || a.features == nil {
		return true, nil
	}
	return a.features.RouteAllowed(ctx, u, route)
}

func (a *Authorizer) permissions(ctx context.Context, roleID int64) ([]string, error) {
	a.mu.Lock()
	if p, ok := a.cache[roleID]; ok {
		a.mu.Unlock()
		return p, nil
	}
	a.mu.Unlock()
	p, err := a.roles.PermissionsFor(ctx, roleID)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.cache[roleID] = p
	a.mu.Unlock()
	return p, nil
}
