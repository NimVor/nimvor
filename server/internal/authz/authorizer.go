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
