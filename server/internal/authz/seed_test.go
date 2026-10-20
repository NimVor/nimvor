package authz

import (
	"context"
	"database/sql"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func count(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSyncDefaultsSeedsRolesAndPermissions(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	if err := SyncDefaults(ctx, db); err != nil {
		t.Fatal(err)
	}

	if got := count(t, db, `SELECT COUNT(*) FROM permissions`); got != len(Catalogue) {
		t.Fatalf("permissions = %d, want %d", got, len(Catalogue))
	}
	if got := count(t, db, `SELECT COUNT(*) FROM roles`); got != len(DefaultRoles) {
		t.Fatalf("roles = %d, want %d", got, len(DefaultRoles))
	}

	// admin holds every catalogue permission.
	adminPerms := count(t, db, `SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.name = 'admin'`)
	if adminPerms != len(Catalogue) {
		t.Fatalf("admin perms = %d, want %d", adminPerms, len(Catalogue))
	}

	// customer holds exactly HOSTING_SELF.
	custPerms := count(t, db, `SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id = rp.role_id WHERE r.name = 'customer'`)
	if custPerms != len(hostingSelf) {
		t.Fatalf("customer perms = %d, want %d", custPerms, len(hostingSelf))
	}
	// ...and must NOT include a server-wide permission.
	if count(t, db, `SELECT COUNT(*) FROM role_permissions rp
		JOIN roles r ON r.id = rp.role_id JOIN permissions p ON p.id = rp.permission_id
		WHERE r.name = 'customer' AND p.name = 'server.manage'`) != 0 {
		t.Fatal("customer must not hold server.manage")
	}

	// All default roles are system roles with the right scope.
	if count(t, db, `SELECT COUNT(*) FROM roles WHERE is_system = 1`) != len(DefaultRoles) {
		t.Fatal("default roles must be is_system=1")
	}
	var scope string
	if err := db.QueryRow(`SELECT scope FROM roles WHERE name='customer'`).Scan(&scope); err != nil {
		t.Fatal(err)
	}
	if scope != "own" {
		t.Fatalf("customer scope = %q, want own", scope)
	}
}

func TestSyncDefaultsIsIdempotent(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	if err := SyncDefaults(ctx, db); err != nil {
		t.Fatal(err)
	}
	p1 := count(t, db, `SELECT COUNT(*) FROM permissions`)
	r1 := count(t, db, `SELECT COUNT(*) FROM role_permissions`)
	if err := SyncDefaults(ctx, db); err != nil {
		t.Fatal(err)
	}
	if count(t, db, `SELECT COUNT(*) FROM permissions`) != p1 || count(t, db, `SELECT COUNT(*) FROM role_permissions`) != r1 {
		t.Fatal("second sync changed row counts")
	}
}

func TestSyncDefaultsKeepsPluginPermsAndCustomizedRoles(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	if err := SyncDefaults(ctx, db); err != nil {
		t.Fatal(err)
	}
	// A plugin permission the plugin manager owns must survive a re-sync and be
	// granted to the wildcard admin role.
	if _, err := db.Exec(`INSERT INTO permissions (name,label,grp) VALUES ('plugin.demo.use','Use demo','Plugins')`); err != nil {
		t.Fatal(err)
	}
	// The operator role is customised and loses a grant the administrator removed.
	if _, err := db.Exec(`UPDATE roles SET customized = 1 WHERE name = 'operator'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM role_permissions WHERE role_id = (SELECT id FROM roles WHERE name='operator')`); err != nil {
		t.Fatal(err)
	}

	if err := SyncDefaults(ctx, db); err != nil {
		t.Fatal(err)
	}

	// plugin perm survived.
	if count(t, db, `SELECT COUNT(*) FROM permissions WHERE name='plugin.demo.use'`) != 1 {
		t.Fatal("plugin permission was deleted by the sync")
	}
	// admin got the plugin perm (wildcard includes plugin perms).
	if count(t, db, `SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id JOIN permissions p ON p.id=rp.permission_id
		WHERE r.name='admin' AND p.name='plugin.demo.use'`) != 1 {
		t.Fatal("wildcard admin role should include plugin permissions")
	}
	// operator stayed empty (customised, non-wildcard role keeps its grants).
	if count(t, db, `SELECT COUNT(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id WHERE r.name='operator'`) != 0 {
		t.Fatal("customised operator role should not have been re-granted")
	}
}
