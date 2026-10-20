package authz

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// fixtureDB builds a fresh migrated DB and seeds the roles, permissions and
// plans that the PHP bootstrap would create, so these tests exercise the real
// schema (foreign keys on) rather than a hand-built mock.
func fixtureDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	// Two roles: staff (scope all) and customer (scope own).
	exec(`INSERT INTO roles (id, name, label, scope, created_at) VALUES
		(1,'administrator','Administrator','all',''),
		(2,'customer','Customer','own','')`)
	// A couple of permissions, including ones that features gate.
	exec(`INSERT INTO permissions (id, name, label, grp) VALUES
		(1,'databases.view','View databases','db'),
		(2,'databases.manage','Manage databases','db'),
		(3,'mail.manage','Manage mail','mail'),
		(4,'websites.manage','Manage websites','web')`)
	// Staff holds everything; customer holds db + websites + mail.
	exec(`INSERT INTO role_permissions (role_id, permission_id) VALUES
		(1,1),(1,2),(1,3),(1,4),
		(2,1),(2,2),(2,3),(2,4)`)
	return db
}

func seedUser(t *testing.T, db *sql.DB, id, roleID int64, scopeNote string, parentID, planID *int64) {
	t.Helper()
	var p, pl any
	if parentID != nil {
		p = *parentID
	}
	if planID != nil {
		pl = *planID
	}
	_, err := db.Exec(`INSERT INTO users (id, username, email, password_hash, role_id, is_active, password_changed_at, created_at, updated_at, parent_id, plan_id)
		VALUES (?, ?, ?, 'x', ?, 1, '', '', '', ?, ?)`,
		id, scopeNote, scopeNote+"@example.test", roleID, p, pl)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

func load(t *testing.T, db *sql.DB, id int64) *identity.User {
	t.Helper()
	u, err := identity.NewRepository(db).Find(context.Background(), id)
	if err != nil {
		t.Fatalf("load user %d: %v", id, err)
	}
	return u
}

func TestStaffBypassesFeatureGating(t *testing.T) {
	db := fixtureDB(t)
	seedUser(t, db, 1, 1, "admin", nil, nil)
	admin := load(t, db, 1)

	az := NewAuthorizer(NewRoleRepository(db), NewFeatureService(db))
	ctx := context.Background()

	// A feature list that disables mysql — but staff is never gated.
	if _, err := db.Exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES (1,'limited','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO feature_list_disabled (list_id,feature) VALUES (1,'mysql')`); err != nil {
		t.Fatal(err)
	}
	ok, err := az.Can(ctx, admin, "databases.view")
	if err != nil || !ok {
		t.Fatalf("staff databases.view = %v,%v want true", ok, err)
	}
}

func TestCustomerFeatureGatingWithholdsPermission(t *testing.T) {
	db := fixtureDB(t)
	// Plan 1 -> feature list that disables mysql.
	if _, err := db.Exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES (1,'nodb','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO feature_list_disabled (list_id,feature) VALUES (1,'mysql')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO plans (id,name,feature_list_id,created_at,updated_at) VALUES (1,'Basic',1,'','')`); err != nil {
		t.Fatal(err)
	}
	planID := int64(1)
	seedUser(t, db, 10, 2, "cust", nil, &planID)
	cust := load(t, db, 10)

	az := NewAuthorizer(NewRoleRepository(db), NewFeatureService(db))
	ctx := context.Background()

	// mysql feature is off -> databases.view/manage withheld even though the
	// role grants them.
	for _, p := range []string{"databases.view", "databases.manage"} {
		ok, err := az.Can(ctx, cust, p)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatalf("gated customer should not hold %s", p)
		}
	}
	// mail is still on -> mail.manage allowed.
	ok, err := az.Can(ctx, cust, "mail.manage")
	if err != nil || !ok {
		t.Fatalf("customer mail.manage = %v,%v want true", ok, err)
	}
}

func TestRouteGatingByName(t *testing.T) {
	db := fixtureDB(t)
	// Disable mongodb (route-owned feature: sites.mongo) for the plan.
	if _, err := db.Exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES (1,'nomongo','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO feature_list_disabled (list_id,feature) VALUES (1,'mongodb')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO plans (id,name,feature_list_id,created_at,updated_at) VALUES (1,'P',1,'','')`); err != nil {
		t.Fatal(err)
	}
	planID := int64(1)
	seedUser(t, db, 11, 2, "c2", nil, &planID)
	cust := load(t, db, 11)
	az := NewAuthorizer(NewRoleRepository(db), NewFeatureService(db))
	ctx := context.Background()

	for route, want := range map[string]bool{
		"sites.mongo":        false, // exact match of the owned prefix
		"sites.mongo.create": false, // prefix.*
		"sites.mongoose":     true,  // not the prefix, must not be gated
		"sites.redis":        true,  // different feature, still on
		"dashboard":          true,
	} {
		got, err := az.RouteAllowed(ctx, cust, route)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("RouteAllowed(%q) = %v, want %v", route, got, want)
		}
	}
}

func TestResellerParentPlanAlsoGates(t *testing.T) {
	db := fixtureDB(t)
	// Reseller's plan disables mail; customer's plan does not. The customer
	// must still lose mail (both slots apply). Mirrors the PHP two-slot rule.
	if _, err := db.Exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES
		(1,'reseller-nomail','',''),(2,'cust-full','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO feature_list_disabled (list_id,feature) VALUES (1,'email')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO plans (id,name,feature_list_id,created_at,updated_at) VALUES
		(1,'ResellerPlan',1,'',''),(2,'CustPlan',2,'','')`); err != nil {
		t.Fatal(err)
	}
	// Reseller (id 20) on plan 1; customer (id 21) child of reseller on plan 2.
	resellerPlan := int64(1)
	seedUser(t, db, 20, 2, "reseller", nil, &resellerPlan)
	parent := int64(20)
	custPlan := int64(2)
	seedUser(t, db, 21, 2, "subcust", &parent, &custPlan)

	cust := load(t, db, 21)
	az := NewAuthorizer(NewRoleRepository(db), NewFeatureService(db))
	ok, err := az.Can(context.Background(), cust, "mail.manage")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("customer under a mail-disabled reseller must not hold mail.manage")
	}
}

func TestSuspendedUserDeniedEverything(t *testing.T) {
	db := fixtureDB(t)
	seedUser(t, db, 30, 1, "susp", nil, nil)
	if _, err := db.Exec(`UPDATE users SET suspended_at = '2026-01-01T00:00:00Z' WHERE id = 30`); err != nil {
		t.Fatal(err)
	}
	u := load(t, db, 30)
	if !u.Suspended {
		t.Fatal("expected Suspended=true")
	}
	if identity.CanAct(u) {
		t.Fatal("CanAct must be false for a suspended user")
	}
	az := NewAuthorizer(NewRoleRepository(db), NewFeatureService(db))
	ok, err := az.Can(context.Background(), u, "databases.view")
	if err != nil || ok {
		t.Fatalf("suspended user Can = %v,%v want false,nil", ok, err)
	}
}

func TestSuspensionInheritedFromParent(t *testing.T) {
	db := fixtureDB(t)
	seedUser(t, db, 40, 2, "parent", nil, nil)
	parent := int64(40)
	seedUser(t, db, 41, 2, "child", &parent, nil)
	if _, err := db.Exec(`UPDATE users SET suspended_at = '2026-01-01T00:00:00Z' WHERE id = 40`); err != nil {
		t.Fatal(err)
	}
	child := load(t, db, 41)
	if !child.Suspended {
		t.Fatal("child of a suspended reseller must read Suspended=true")
	}
}
