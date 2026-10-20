package hosting

import (
	"context"
	"database/sql"
	"sort"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// This suite proves tenant isolation against the real schema: a query built
// from Condition returns exactly the rows a principal may see, derived only
// from the authenticated user — never a client-supplied id.
func scopeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'administrator','Admin','all',''),(2,'reseller','Reseller','own',''),(3,'customer','Customer','own','')`)
	// 1 admin; 2 reseller; 3 customer-of-reseller; 4 standalone customer.
	exec(`INSERT INTO users (id,username,email,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,parent_id) VALUES
		(1,'admin','a@x','h',1,1,'','','',NULL),
		(2,'reseller','r@x','h',2,1,'','','',NULL),
		(3,'subcust','s@x','h',3,1,'','','',2),
		(4,'solo','o@x','h',3,1,'','','',NULL)`)
	// websites: owned across the users, all on server 1.
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,status,system_user,root,document_root,kind,created_at,updated_at) VALUES
		(10,1,1,'admin.example','active','u_admin','/srv/a','/srv/a/public','php','',''),
		(11,1,2,'reseller.example','active','u_res','/srv/r','/srv/r/public','php','',''),
		(12,1,3,'subcust.example','active','u_sub','/srv/s','/srv/s/public','php','',''),
		(13,1,4,'solo.example','active','u_solo','/srv/o','/srv/o/public','php','','')`)
	return db
}

func load(t *testing.T, db *sql.DB, id int64) *identity.User {
	t.Helper()
	u, err := identity.NewRepository(db).Find(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func visibleDomains(t *testing.T, db *sql.DB, u *identity.User) []string {
	t.Helper()
	cond, args := Condition(u, "w.owner_id")
	q := "SELECT w.domain FROM websites w WHERE w.server_id = ? AND " + cond + " ORDER BY w.domain"
	full := append([]any{int64(1)}, args...)
	rows, err := db.Query(q, full...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAdminSeesEveryWebsite(t *testing.T) {
	db := scopeDB(t)
	got := visibleDomains(t, db, load(t, db, 1))
	want := []string{"admin.example", "reseller.example", "solo.example", "subcust.example"}
	if !eq(got, want) {
		t.Fatalf("admin sees %v, want all %v", got, want)
	}
}

func TestResellerSeesOwnAndCustomers(t *testing.T) {
	db := scopeDB(t)
	got := visibleDomains(t, db, load(t, db, 2))
	want := []string{"reseller.example", "subcust.example"}
	if !eq(got, want) {
		t.Fatalf("reseller sees %v, want own+customer %v", got, want)
	}
}

func TestCustomerSeesOnlyOwn(t *testing.T) {
	db := scopeDB(t)
	got := visibleDomains(t, db, load(t, db, 3))
	if !eq(got, []string{"subcust.example"}) {
		t.Fatalf("subcust sees %v, want only its own", got)
	}
	// The standalone customer must never see the reseller's tenant.
	got4 := visibleDomains(t, db, load(t, db, 4))
	if !eq(got4, []string{"solo.example"}) {
		t.Fatalf("solo sees %v, want only its own", got4)
	}
}

func TestWithWebsiteExtendsToAttachedSiteOwner(t *testing.T) {
	db := scopeDB(t)
	// A DNS-zone-like table: owner_id + website_id. Give the customer (3) a
	// zone owned by the admin but attached to the customer's website 12.
	if _, err := db.Exec(`CREATE TABLE t_obj (id INTEGER PRIMARY KEY, owner_id INTEGER, website_id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t_obj (id,owner_id,website_id) VALUES
		(1, 1, 12),  -- admin-owned, attached to customer 3's site -> visible to 3
		(2, 1, 10),  -- admin-owned, admin's site -> NOT visible to 3
		(3, 3, NULL) -- customer-owned, no site -> visible to 3`); err != nil {
		t.Fatal(err)
	}
	u := load(t, db, 3)
	cond, args := WithWebsite(u, "owner_id", "website_id")
	rows, err := db.Query("SELECT id FROM t_obj WHERE "+cond+" ORDER BY id", args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int
	for rows.Next() {
		var id int
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	// Expect 1 (via attached website) and 3 (owned); never 2.
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatalf("withWebsite visibility = %v, want [1 3]", ids)
	}
}

func TestNilUserFailsClosed(t *testing.T) {
	db := scopeDB(t)
	got := visibleDomains(t, db, nil)
	if len(got) != 0 {
		t.Fatalf("nil user saw %v, want nothing", got)
	}
}
