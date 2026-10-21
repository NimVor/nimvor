package hosting

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

// accountHarness bundles the user, account and cron services over writeDB,
// with role permissions seeded (writeDB seeds only the roles). The seeded
// permissions mirror the production seed's shape: administrator holds
// users.manage, the reseller role holds it too (resellers manage customers).
type accountHarness struct {
	db       *sql.DB
	agent    *fakeAgent
	audit    []auditRec
	users    *UserService
	accounts *AccountService
	sites    *WebsiteService
	cron     *CronService
}

func newAccountHarness(t *testing.T) *accountHarness {
	t.Helper()
	db := writeDB(t)
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO permissions (name, label, grp) VALUES
		('users.manage','Manage users','Administration'),
		('sites.manage','Manage websites','Hosting'),
		('plans.manage','Manage plans','Administration')`)
	exec(`INSERT INTO role_permissions (role_id, permission_id)
		SELECT 1, id FROM permissions WHERE name IN ('users.manage','sites.manage','plans.manage')`)
	exec(`INSERT INTO role_permissions (role_id, permission_id)
		SELECT 2, id FROM permissions WHERE name IN ('users.manage','sites.manage')`)

	fa := &fakeAgent{}
	h := &accountHarness{db: db, agent: fa, audit: []auditRec{}}
	audit := func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		h.audit = append(h.audit, auditRec{action: action, target: target, result: result, details: details})
	}
	usersRepo := identity.NewRepository(db)
	limits := NewPlanLimits(db)
	h.users = NewUserService(
		usersRepo, authz.NewRoleRepository(db), security.NewPolicy(settings.NewRepository(db)),
		NewPlanRepository(db), limits, audit,
	)
	h.sites = NewWebsiteService(
		NewWebsiteRepository(db), fa, authz.NewFeatureService(db), limits,
		NewDnsRepository(db), settings.NewRepository(db), audit, 1,
	)
	h.accounts = NewAccountService(h.users, usersRepo, h.sites, NewAccountOptionStore(db), db, audit)
	h.cron = NewCronService(db, fa)
	return h
}

// testPassword passes the base rules and the default strength policy.
const testPassword = "Tr0ub4dor&3xtra-long"

// lastAuditOf finds the most recent record of an action in a log
// (lastAuditAction for the account harness, which keeps its own log).
func lastAuditOf(t *testing.T, log []auditRec, action string) auditRec {
	t.Helper()
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].action == action {
			return log[i]
		}
	}
	t.Fatalf("audit %q not found; log=%v", action, log)
	return auditRec{}
}

func validationField(t *testing.T, err error, field string) string {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error = %v, want *ValidationError", err)
	}
	msg, ok := ve.Fields[field]
	if !ok {
		t.Fatalf("fields = %v, want error on %q", ve.Fields, field)
	}
	return msg
}

func TestUserCreateValidation(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)
	base := map[string]string{
		"username":              "newuser",
		"email":                 "new@example.test",
		"display_name":          "New User",
		"password":              testPassword,
		"password_confirmation": testPassword,
		"role_id":               "3",
		"is_active":             "1",
	}

	cases := []struct {
		mutate func(m map[string]string)
		field  string
	}{
		{func(m map[string]string) { m["username"] = "AB" }, "username"},
		{func(m map[string]string) { m["username"] = "9bad" }, "username"},
		{func(m map[string]string) { m["username"] = "admin" }, "username"},
		{func(m map[string]string) { m["email"] = "nope" }, "email"},
		{func(m map[string]string) { m["email"] = "a@x" }, "email"},
		{func(m map[string]string) { m["display_name"] = "bad\x01" }, "display_name"},
		{func(m map[string]string) { m["role_id"] = "99" }, "role_id"},
		{func(m map[string]string) { m["password"] = "short" }, "password"},
		{func(m map[string]string) { m["password"] = "newuser" + strings.Repeat("x", 20) }, "password"},
		{func(m map[string]string) { m["password_confirmation"] = "different" }, "password_confirmation"},
	}
	for _, c := range cases {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		c.mutate(m)
		if _, err := h.users.Create(ctx, m, admin); err == nil {
			t.Fatalf("%s: create succeeded", c.field)
		} else {
			validationField(t, err, c.field)
		}
	}

	// Errors accumulate: two broken fields in one pass.
	m := map[string]string{}
	for k, v := range base {
		m[k] = v
	}
	m["username"] = "AB"
	m["email"] = "nope"
	_, err := h.users.Create(ctx, m, admin)
	var ve *ValidationError
	if !errors.As(err, &ve) || len(ve.Fields) != 2 {
		t.Fatalf("fields = %v, want username+email", err)
	}
}

func TestUserCreateHappyPath(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	admin := loadUser(t, h.db, 1)
	id, err := h.users.Create(ctx, map[string]string{
		"username":              "newuser",
		"email":                 "new@example.test",
		"display_name":          "New User",
		"password":              testPassword,
		"password_confirmation": testPassword,
		"role_id":               "3",
		"is_active":             "1",
		"plan_id":               "11",
	}, admin)
	if err != nil {
		t.Fatal(err)
	}
	u, err := identity.NewRepository(h.db).Find(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if u.RoleName != "customer" || !u.IsActive || !u.PlanID.Valid || u.PlanID.Int64 != 11 || u.ParentID.Valid {
		t.Fatalf("user = %+v", u)
	}
	hash, err := identity.NewRepository(h.db).PasswordHash(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !identity.VerifyPassword(hash, testPassword) {
		t.Fatal("stored hash does not verify the password")
	}
	a := lastAuditOf(t, h.audit, "user.create")
	if a.result != "success" || a.details["username"] != "newuser" || a.details["plan_id"] != int64(11) {
		t.Fatalf("audit = %+v", a)
	}
}

func TestUserCreateResellerScope(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	reseller := loadUser(t, h.db, 2)

	// A reseller creates its own customers: parent set, plan restricted to
	// its own.
	id, err := h.users.Create(ctx, map[string]string{
		"username":              "resellercust",
		"email":                 "rc@example.test",
		"display_name":          "RC",
		"password":              testPassword,
		"password_confirmation": testPassword,
		"role_id":               "3",
		"is_active":             "1",
	}, reseller)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := identity.NewRepository(h.db).Find(ctx, id)
	if !u.ParentID.Valid || u.ParentID.Int64 != 2 {
		t.Fatalf("customer parent = %+v, want reseller 2", u.ParentID)
	}

	// The reseller role carries users.manage: resellers cannot create
	// resellers (or anyone with user management).
	_, err = h.users.Create(ctx, map[string]string{
		"username":              "anotherreseller",
		"email":                 "ar@example.test",
		"display_name":          "AR",
		"password":              testPassword,
		"password_confirmation": testPassword,
		"role_id":               "2",
		"is_active":             "1",
	}, reseller)
	if msg := validationField(t, err, "role_id"); msg != "You can only create hosting customers." {
		t.Fatalf("message = %q", msg)
	}

	// The administrator role has permissions the reseller lacks.
	_, err = h.users.Create(ctx, map[string]string{
		"username":              "anotheradmin",
		"email":                 "aa@example.test",
		"display_name":          "AA",
		"password":              testPassword,
		"password_confirmation": testPassword,
		"role_id":               "1",
		"is_active":             "1",
	}, reseller)
	if msg := validationField(t, err, "role_id"); msg != "You cannot assign a role with permissions you do not have yourself." {
		t.Fatalf("message = %q", msg)
	}
}

func TestUserCreateCustomersLimit(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	// The reseller's own plan caps its customers at 1.
	if _, err := h.db.Exec(`INSERT INTO plans (id, name, owner_id, max_customers, created_at, updated_at)
		VALUES (13, 'Reseller1', 2, 1, '', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`UPDATE users SET plan_id = 13 WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	reseller := loadUser(t, h.db, 2)

	form := func(n string) map[string]string {
		return map[string]string{
			"username": n, "email": n + "@example.test", "display_name": n,
			"password": testPassword, "password_confirmation": testPassword,
			"role_id": "3", "is_active": "1",
		}
	}
	if _, err := h.users.Create(ctx, form("firstcust"), reseller); err != nil {
		t.Fatal(err)
	}
	_, err := h.users.Create(ctx, form("secondcust"), reseller)
	var ue *UserError
	if !errors.As(err, &ue) || ue.What != "Your plan allows at most 1 customers." {
		t.Fatalf("error = %v, want the customers limit", err)
	}
}

func TestUserCreatePlanAssignability(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	// A reseller-owned plan the reseller may assign.
	if _, err := h.db.Exec(`INSERT INTO plans (id, name, owner_id, max_websites, created_at, updated_at)
		VALUES (13, 'Reseller1', 2, 10, '', '')`); err != nil {
		t.Fatal(err)
	}
	reseller := loadUser(t, h.db, 2)
	admin := loadUser(t, h.db, 1)
	form := map[string]string{
		"username":              "plancust",
		"email":                 "pc@example.test",
		"display_name":          "PC",
		"password":              testPassword,
		"password_confirmation": testPassword,
		"role_id":               "3",
		"is_active":             "1",
		"plan_id":               "11", // a global plan: invisible to the reseller
	}
	_, err := h.users.Create(ctx, form, reseller)
	if msg := validationField(t, err, "plan_id"); msg != "Choose one of your plans." {
		t.Fatalf("message = %q", msg)
	}
	// The same global plan is fine for staff.
	if _, err := h.users.Create(ctx, form, admin); err != nil {
		t.Fatalf("staff assigning a global plan: %v", err)
	}
	// And the reseller's own plan is fine for the reseller.
	own := map[string]string{}
	for k, v := range form {
		own[k] = v
	}
	own["username"] = "plancust2"
	own["email"] = "pc2@example.test"
	own["plan_id"] = "13"
	if _, err := h.users.Create(ctx, own, reseller); err != nil {
		t.Fatalf("reseller assigning its own plan: %v", err)
	}
}

func TestCanManage(t *testing.T) {
	admin := &identity.User{ID: 1, RoleScope: "all"}
	reseller := &identity.User{ID: 2, RoleScope: "own"}
	other := &identity.User{ID: 3, RoleScope: "own"}
	customer := &identity.User{ID: 4, RoleScope: "own"}
	customer.ParentID = sql.NullInt64{Int64: 2, Valid: true}
	other.ParentID = sql.NullInt64{Int64: 9, Valid: true}

	if !CanManage(admin, other) || !CanManage(nil, other) {
		t.Fatal("staff (and system callers) manage everyone")
	}
	if !CanManage(reseller, customer) {
		t.Fatal("a reseller manages its own customers")
	}
	if CanManage(reseller, other) {
		t.Fatal("a reseller does not manage other resellers' customers")
	}
}
