package identity

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// User is a panel account joined to its role. Field set and the derived
// Suspended flag mirror UserRepository::COLUMNS in the PHP panel.
type User struct {
	ID                int64
	Username          string
	Email             string
	DisplayName       string
	RoleID            int64
	IsActive          bool
	RoleName          string
	RoleLabel         string
	RoleScope         string // "all" (staff) or "own" (hosting account / reseller)
	RoleRequires2FA   bool
	ParentID          sql.NullInt64
	PlanID            sql.NullInt64
	Suspended         bool
	PasswordChangedAt sql.NullString
	LastLoginAt       sql.NullString
	LastLoginIP       sql.NullString
}

// suspended is the correlated sub-query from UserRepository::SUSPENDED: an
// account is suspended itself or through its (suspended) reseller parent.
const suspendedExpr = `(u.suspended_at IS NOT NULL OR EXISTS (SELECT 1 FROM users sp WHERE sp.id = u.parent_id AND sp.suspended_at IS NOT NULL))`

// userColumns matches UserRepository::COLUMNS (minus the plan_name join, added
// only where needed).
const userColumns = `u.id, u.username, u.email, u.display_name, u.role_id, u.is_active,
	u.last_login_at, u.last_login_ip, u.password_changed_at,
	r.name AS role_name, r.label AS role_label, r.scope AS role_scope,
	r.require_two_factor AS role_requires_2fa, u.parent_id, u.plan_id, ` + suspendedExpr + ` AS suspended`

// CanAct reports whether an account may sign in and act: it exists, is active
// and is not suspended. Mirrors UserRepository::canAct — the single gate every
// authentication and authorization path funnels through.
func CanAct(u *User) bool {
	return u != nil && u.IsActive && !u.Suspended
}

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("identity: user not found")

// Repository reads and updates accounts. It owns no business logic; callers
// (AuthService, authorizer) apply policy.
type Repository struct{ db *sql.DB }

// NewRepository wraps a database handle.
func NewRepository(db *sql.DB) *Repository { return &Repository{db: db} }

func (r *Repository) scanUser(row interface{ Scan(...any) error }) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.RoleID, &u.IsActive,
		&u.LastLoginAt, &u.LastLoginIP, &u.PasswordChangedAt,
		&u.RoleName, &u.RoleLabel, &u.RoleScope, &u.RoleRequires2FA, &u.ParentID, &u.PlanID, &u.Suspended)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// Find loads a user by id (with role).
func (r *Repository) Find(ctx context.Context, id int64) (*User, error) {
	q := `SELECT ` + userColumns + ` FROM users u JOIN roles r ON r.id = u.role_id WHERE u.id = ?`
	return r.scanUser(r.db.QueryRowContext(ctx, q, id))
}

// ManagedUser is a user row for the admin list, with the plan name.
type ManagedUser struct {
	User
	PlanName sql.NullString
}

// ManageableBy returns the users an actor may manage: everyone for server-wide
// (scope "all") roles, only their own customers (parent_id = actor) for
// own-scope roles (resellers). Mirrors UserRepository::manageableBy — the
// actor can never widen this with a request parameter.
func (r *Repository) ManageableBy(ctx context.Context, actor *User) ([]ManagedUser, error) {
	q := `SELECT ` + userColumns + `, p.name AS plan_name
	      FROM users u JOIN roles r ON r.id = u.role_id LEFT JOIN plans p ON p.id = u.plan_id`
	var args []any
	if actor == nil || actor.RoleScope != "all" {
		q += ` WHERE u.parent_id = ?`
		var id int64
		if actor != nil {
			id = actor.ID
		}
		args = append(args, id)
	}
	q += ` ORDER BY u.username`
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ManagedUser
	for rows.Next() {
		var m ManagedUser
		u := &m.User
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.DisplayName, &u.RoleID, &u.IsActive,
			&u.LastLoginAt, &u.LastLoginIP, &u.PasswordChangedAt,
			&u.RoleName, &u.RoleLabel, &u.RoleScope, &u.RoleRequires2FA, &u.ParentID, &u.PlanID, &u.Suspended,
			&m.PlanName); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AuthRow is the minimal row used while verifying a password. It deliberately
// carries the password hash, which the full User never does. Mirrors
// UserRepository::findForAuth.
type AuthRow struct {
	ID           int64
	Username     string
	PasswordHash string
	IsActive     bool
	RoleID       int64
	Suspended    bool
}

// FindForAuth looks up a login by username or email (case-insensitive via the
// column collation). Returns ErrNotFound when there is no match; callers must
// still verify against a dummy hash to keep timing uniform.
func (r *Repository) FindForAuth(ctx context.Context, login string) (*AuthRow, error) {
	q := `SELECT u.id, u.username, u.password_hash, u.is_active, u.role_id, ` + suspendedExpr + ` AS suspended
	      FROM users u WHERE u.username = ? OR u.email = ?`
	var a AuthRow
	err := r.db.QueryRowContext(ctx, q, login, login).Scan(&a.ID, &a.Username, &a.PasswordHash, &a.IsActive, &a.RoleID, &a.Suspended)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// PasswordHash returns the stored hash for a user, or "" if the user is gone.
func (r *Repository) PasswordHash(ctx context.Context, id int64) (string, error) {
	var h string
	err := r.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE id = ?`, id).Scan(&h)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return h, err
}

// SessionEpoch returns the user's session epoch (raised to revoke sessions).
func (r *Repository) SessionEpoch(ctx context.Context, id int64) (int64, error) {
	var e int64
	err := r.db.QueryRowContext(ctx, `SELECT session_epoch FROM users WHERE id = ?`, id).Scan(&e)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return e, err
}

// BumpSessionEpoch invalidates the user's other sessions.
func (r *Repository) BumpSessionEpoch(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE users SET session_epoch = session_epoch + 1 WHERE id = ?`, id)
	return err
}

// RecordLogin stamps the successful sign-in time and address.
func (r *Repository) RecordLogin(ctx context.Context, id int64, ip string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE users SET last_login_at = ?, last_login_ip = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), ip, id)
	return err
}

// APIToken is a personal API token for display (never the secret itself — only
// its prefix). Mirrors ApiTokenService::forUser.
type APIToken struct {
	ID         int64
	Name       string
	Prefix     string
	Ability    string
	ExpiresAt  sql.NullString
	LastUsedAt sql.NullString
	CreatedAt  string
}

// APITokens lists a user's own personal API tokens (no secret material).
func (r *Repository) APITokens(ctx context.Context, userID int64) ([]APIToken, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, name, prefix, ability, expires_at, last_used_at, created_at
		 FROM api_tokens WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var t APIToken
		if err := rows.Scan(&t.ID, &t.Name, &t.Prefix, &t.Ability, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdatePassword stores a new hash and stamps password_changed_at.
func (r *Repository) UpdatePassword(ctx context.Context, id int64, hash string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.db.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ? WHERE id = ?`,
		hash, now, now, id)
	return err
}

// NewUser is the data for Repository.Create (UserRepository::create).
type NewUser struct {
	Username     string
	Email        string
	DisplayName  string
	PasswordHash string
	RoleID       int64
	IsActive     bool
}

// Create inserts a panel user and returns the new id
// (UserRepository::create).
func (r *Repository) Create(ctx context.Context, u *NewUser) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	active := 0
	if u.IsActive {
		active = 1
	}
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO users (username, email, display_name, password_hash, role_id, is_active, password_changed_at, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		u.Username, u.Email, u.DisplayName, u.PasswordHash, u.RoleID, active, now, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SetOwnership sets the parent reseller and plan (UserRepository::setOwnership).
func (r *Repository) SetOwnership(ctx context.Context, id int64, parentID, planID *int64) error {
	parent := any(nil)
	if parentID != nil {
		parent = *parentID
	}
	plan := any(nil)
	if planID != nil {
		plan = *planID
	}
	_, err := r.db.ExecContext(ctx, "UPDATE users SET parent_id = ?, plan_id = ? WHERE id = ?", parent, plan, id)
	return err
}

// Delete removes a panel user (UserRepository::delete).
func (r *Repository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, "DELETE FROM users WHERE id = ?", id)
	return err
}

// UsernameExists reports whether the login is taken
// (UserRepository::usernameExists). exceptID skips one row (0 = none).
func (r *Repository) UsernameExists(ctx context.Context, username string, exceptID int64) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, "SELECT 1 FROM users WHERE username = ? AND id != ?", username, exceptID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// EmailExists reports whether the email is taken (UserRepository::emailExists).
// exceptID skips one row (0 = none).
func (r *Repository) EmailExists(ctx context.Context, email string, exceptID int64) (bool, error) {
	var one int
	err := r.db.QueryRowContext(ctx, "SELECT 1 FROM users WHERE email = ? AND id != ?", email, exceptID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
