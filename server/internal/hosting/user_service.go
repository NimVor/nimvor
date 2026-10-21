package hosting

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
)

// UserService creates panel users with validation and the safety rules
// (UserService::create): an actor may only assign roles with permissions it
// has itself, and a reseller (scope "own") creates its own customers, not
// more staff. The /users management UI is a later batch; account creation
// (AccountService) builds on this.
type UserService struct {
	users  *identity.Repository
	roles  *authz.RoleRepository
	policy *security.Policy
	plans  *PlanRepository
	limits *PlanLimits
	audit  Auditor
}

// NewUserService builds the service.
func NewUserService(users *identity.Repository, roles *authz.RoleRepository, policy *security.Policy, plans *PlanRepository, limits *PlanLimits, audit Auditor) *UserService {
	return &UserService{users: users, roles: roles, policy: policy, plans: plans, limits: limits, audit: audit}
}

// CanManage reports whether the actor may manage the target user
// (UserService::canManage): staff manage everyone, a reseller only its own
// customers.
func CanManage(actor, target *identity.User) bool {
	if actor == nil || actor.RoleScope == "all" {
		return true
	}
	return target.ParentID.Valid && target.ParentID.Int64 == actor.ID
}

// userForm is the validated user form (UserService::validate's result).
type userForm struct {
	Username    string
	Email       string
	DisplayName string
	Password    string
	RoleID      int64
	IsActive    bool
}

// usernameRe mirrors UserService::validate's username pattern.
var usernameRe = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,31}$`)

// controlChars matches the unprintable characters display names reject
// (the /[\x00-\x1F\x7F]/ pattern).
var controlChars = regexp.MustCompile(`[\x00-\x1F\x7F]`)

// emailRe is the practical stand-in for FILTER_VALIDATE_EMAIL used for panel
// logins: a non-empty local part and a dotted domain.
var emailRe = regexp.MustCompile(`^[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)

// Create makes a panel user (UserService::create) and returns its id. A nil
// actor (system callers) skips the assignment checks.
func (s *UserService) Create(ctx context.Context, in map[string]string, actor *identity.User) (int64, error) {
	form, err := s.validate(ctx, in)
	if err != nil {
		return 0, err
	}
	if err := s.assertCanAssign(ctx, actor, form.RoleID); err != nil {
		return 0, err
	}
	var parentID *int64
	if actor != nil && actor.RoleScope == "own" {
		// Resellers create customers: no user management of their own.
		perms, err := s.roles.PermissionsFor(ctx, form.RoleID)
		if err != nil {
			return 0, err
		}
		for _, p := range perms {
			if p == "users.manage" {
				return 0, &ValidationError{Fields: map[string]string{"role_id": "You can only create hosting customers."}}
			}
		}
		if s.limits != nil {
			if err := s.limits.AssertCanCreate(ctx, "customers", &actor.ID, 1); err != nil {
				return 0, err
			}
		}
		parentID = &actor.ID
	}
	planID, err := s.planInput(ctx, actor, in)
	if err != nil {
		return 0, err
	}
	hash, err := identity.HashPassword(form.Password)
	if err != nil {
		return 0, err
	}
	id, err := s.users.Create(ctx, &identity.NewUser{
		Username:     form.Username,
		Email:        form.Email,
		DisplayName:  form.DisplayName,
		PasswordHash: hash,
		RoleID:       form.RoleID,
		IsActive:     form.IsActive,
	})
	if err != nil {
		return 0, err
	}
	if parentID != nil || planID != nil {
		if err := s.users.SetOwnership(ctx, id, parentID, planID); err != nil {
			return 0, err
		}
	}
	s.recordAudit(ctx, "user.create", "user", strconv.FormatInt(id, 10), "success", map[string]any{
		"username":  form.Username,
		"role_id":   form.RoleID,
		"parent_id": int64OrNil(parentID),
		"plan_id":   int64OrNil(planID),
	})
	return id, nil
}

// recordAudit is the nil-safe audit hook (same contract as WebsiteService's).
func (s *UserService) recordAudit(ctx context.Context, action, targetType, target, result string, details map[string]any) {
	if s.audit != nil {
		s.audit(ctx, action, targetType, target, result, details)
	}
}

// assertCanAssign checks the role's permissions are a subset of the actor's
// (UserService::assertCanAssign).
func (s *UserService) assertCanAssign(ctx context.Context, actor *identity.User, roleID int64) error {
	if actor == nil {
		return nil
	}
	mine, err := s.roles.PermissionsFor(ctx, actor.RoleID)
	if err != nil {
		return err
	}
	need, err := s.roles.PermissionsFor(ctx, roleID)
	if err != nil {
		return err
	}
	for _, p := range need {
		found := false
		for _, m := range mine {
			if m == p {
				found = true
				break
			}
		}
		if !found {
			return &ValidationError{Fields: map[string]string{"role_id": "You cannot assign a role with permissions you do not have yourself."}}
		}
	}
	return nil
}

// planInput resolves the plan to store, validated against what the actor may
// assign (UserService::planInput).
func (s *UserService) planInput(ctx context.Context, actor *identity.User, in map[string]string) (*int64, error) {
	var planID *int64
	if raw := strings.TrimSpace(in["plan_id"]); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			planID = &n
		}
	}
	if actor != nil && s.plans != nil {
		if err := s.plans.AssertAssignable(ctx, actor, planID); err != nil {
			return nil, err
		}
	}
	return planID, nil
}

// roleExists checks the role id is known (RoleRepository::find).
func (s *UserService) roleExists(ctx context.Context, roleID int64) (bool, error) {
	all, err := s.roles.All(ctx)
	if err != nil {
		return false, err
	}
	for _, r := range all {
		if r.ID == roleID {
			return true, nil
		}
	}
	return false, nil
}

// validate checks the user form (UserService::validate, creation branch).
func (s *UserService) validate(ctx context.Context, in map[string]string) (*userForm, error) {
	errs := &ValidationError{}
	username := strings.TrimSpace(in["username"])
	email := strings.TrimSpace(in["email"])
	display := strings.TrimSpace(in["display_name"])
	password := in["password"]
	confirm := in["password_confirmation"]
	roleID, roleErr := strconv.ParseInt(strings.TrimSpace(in["role_id"]), 10, 64)

	if !usernameRe.MatchString(username) {
		errs.Add("username", "Use 3–32 lowercase letters, digits, dots, dashes or underscores, starting with a letter.")
	} else if taken, err := s.users.UsernameExists(ctx, username, 0); err != nil {
		return nil, err
	} else if taken {
		errs.Add("username", "This username is already taken.")
	}
	if !emailRe.MatchString(email) || len([]rune(email)) > 190 {
		errs.Add("email", "Enter a valid email address.")
	} else if taken, err := s.users.EmailExists(ctx, email, 0); err != nil {
		return nil, err
	} else if taken {
		errs.Add("email", "This email address is already in use.")
	}
	if len([]rune(display)) > 100 || controlChars.MatchString(display) {
		errs.Add("display_name", "Use at most 100 printable characters.")
	}
	if roleErr != nil || roleID <= 0 {
		errs.Add("role_id", "Choose a valid role.")
	} else if ok, err := s.roleExists(ctx, roleID); err != nil {
		return nil, err
	} else if !ok {
		errs.Add("role_id", "Choose a valid role.")
	}
	if weak, err := s.policy.Check(ctx, password, username); err != nil {
		return nil, err
	} else if weak != "" {
		errs.Add("password", weak)
	} else if password != confirm {
		errs.Add("password_confirmation", "The passwords do not match.")
	}
	if errs.Fields != nil {
		return nil, errs
	}
	return &userForm{
		Username:    username,
		Email:       email,
		DisplayName: display,
		Password:    password,
		RoleID:      roleID,
		IsActive:    formBool(in["is_active"]),
	}, nil
}
