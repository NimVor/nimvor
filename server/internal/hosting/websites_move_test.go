package hosting

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestMoveToAccount(t *testing.T) {
	h := newHarness(t, writeDB(t))
	ctx := context.Background()
	site := findSite(t, h, 10) // cust.example, owner 3, user u_cust
	owner := int64(9)
	seedAccount(t, h.db, owner)

	if err := h.svc.MoveToAccount(ctx, site, "u_new", &owner, true, false); err != nil {
		t.Fatal(err)
	}
	got := findSite(t, h, 10)
	if got.SystemUser != "u_new" || !got.OwnerID.Valid || got.OwnerID.Int64 != 9 || got.ParentID.Valid {
		t.Fatalf("moved site = %+v", got)
	}
	// The agent re-applied the site for the new system account.
	spec := h.agent.lastParams("sites.apply")
	if spec["user"] != "u_new" || spec["new_user"] != true {
		t.Fatalf("sites.apply spec = %v", spec)
	}
}

func TestMoveToAccountDetaches(t *testing.T) {
	h := newHarness(t, writeDB(t))
	ctx := context.Background()
	// An addon domain under site 10.
	if _, err := h.db.Exec(`INSERT INTO websites (id, server_id, owner_id, parent_id, domain, system_user, root, document_root, kind, php_version, status, created_at, updated_at)
		VALUES (30, 1, 3, 10, 'addon.example', 'u_cust', '/home/whacp/addon.example', '/home/whacp/addon.example/public_html', 'php', '8.3.1', 'active', '', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (1, 30, 'addon.example', 1)`); err != nil {
		t.Fatal(err)
	}
	site := findSite(t, h, 30)
	if !site.ParentID.Valid || site.ParentID.Int64 != 10 {
		t.Fatalf("seed parent = %+v", site.ParentID)
	}
	owner := int64(9)
	seedAccount(t, h.db, owner)
	if err := h.svc.MoveToAccount(ctx, site, "u_new", &owner, false, true); err != nil {
		t.Fatal(err)
	}
	got := findSite(t, h, 30)
	if got.ParentID.Valid {
		t.Fatalf("detach did not clear the parent: %+v", got.ParentID)
	}
	spec := h.agent.lastParams("sites.apply")
	if spec["new_user"] == true {
		t.Fatalf("existing system account must not be recreated: %v", spec)
	}
}

func TestMoveToAccountFailureReverts(t *testing.T) {
	h := newHarness(t, writeDB(t))
	ctx := context.Background()
	h.agent.failActions = map[string]error{"sites.apply": errors.New("agent down")}
	site := findSite(t, h, 10)
	owner := int64(9)
	seedAccount(t, h.db, owner)

	err := h.svc.MoveToAccount(ctx, site, "u_new", &owner, true, false)
	if err == nil {
		t.Fatal("MoveToAccount succeeded despite the agent failure")
	}
	got := findSite(t, h, 10)
	// Owner, parent and system user are back as they were.
	if got.SystemUser != "u_cust" || !got.OwnerID.Valid || got.OwnerID.Int64 != 3 || got.ParentID.Valid {
		t.Fatalf("reverted site = %+v", got)
	}
	// The restore re-applied the previous row state (domains intact).
	var n int
	if err := h.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM domains WHERE website_id = 10 AND name = 'cust.example'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("primary domain rows = %d", n)
	}
}

func TestMoveToAccountKeepsOwnerNil(t *testing.T) {
	h := newHarness(t, writeDB(t))
	ctx := context.Background()
	// A staff-owned website (no owner) moves to an account.
	if _, err := h.db.Exec(`INSERT INTO websites (id, server_id, owner_id, domain, system_user, root, document_root, kind, php_version, status, created_at, updated_at)
		VALUES (40, 1, NULL, 'staff.example', 'u_staff', '/home/whacp/staff.example', '/home/whacp/staff.example/public_html', 'php', '8.3.1', 'active', '', '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (1, 40, 'staff.example', 1)`); err != nil {
		t.Fatal(err)
	}
	site := findSite(t, h, 40)
	owner := int64(3)
	if err := h.svc.MoveToAccount(ctx, site, "u_cust", &owner, false, false); err != nil {
		t.Fatal(err)
	}
	got := findSite(t, h, 40)
	if !got.OwnerID.Valid || got.OwnerID.Int64 != 3 || got.SystemUser != "u_cust" {
		t.Fatalf("moved site = %+v", got)
	}
	// And back: owner goes to NULL again.
	if err := h.svc.MoveToAccount(ctx, got, "u_staff", nil, false, false); err != nil {
		t.Fatal(err)
	}
	got = findSite(t, h, 40)
	if got.OwnerID.Valid || got.SystemUser != "u_staff" {
		t.Fatalf("re-moved site = %+v", got)
	}
	var ownerCol sql.NullInt64
	if err := h.db.QueryRowContext(ctx, "SELECT owner_id FROM websites WHERE id = 40").Scan(&ownerCol); err != nil {
		t.Fatal(err)
	}
	if ownerCol.Valid {
		t.Fatalf("owner_id should be NULL, got %+v", ownerCol)
	}
}

// UpdateFields must bind its placeholders in SQL order (values, updated_at, id):
// swapping the last two made the UPDATE match no row and every website change a
// silent no-op.
func TestUpdateFieldsPersistsAndStampsUpdatedAt(t *testing.T) {
	h := newHarness(t, writeDB(t))
	ctx := context.Background()
	if err := h.svc.sites.UpdateFields(ctx, 10, map[string]any{"system_user": "u_changed", "enabled": false}); err != nil {
		t.Fatal(err)
	}
	var user string
	var enabled int
	var updated string
	if err := h.db.QueryRowContext(ctx, "SELECT system_user, enabled, updated_at FROM websites WHERE id = 10").Scan(&user, &enabled, &updated); err != nil {
		t.Fatal(err)
	}
	if user != "u_changed" || enabled != 0 {
		t.Fatalf("row after UpdateFields = %q enabled=%d", user, enabled)
	}
	if len(updated) < 10 || updated[:2] != "20" {
		t.Fatalf("updated_at = %q, want an RFC 3339 timestamp (not the row id)", updated)
	}
}
