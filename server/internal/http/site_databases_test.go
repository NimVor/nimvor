package http

// This suite drives GET /sites/{id}/databases (DatabasesController::site,
// route sites.databases, databases.view): the same page as /databases with
// the databases scoped to the website, the operations' return_to pointing
// back to the site, foreign site ids as 404s, and the mysql-feature
// withholding.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSiteDatabasesScoped(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	body := get(t, h, "/sites/10/databases", bob).Body.String()
	// Own site's database and user, the site create form (website_id pinned,
	// return_to to the site page), and no "Website" column on a site page.
	for _, want := range []string{
		"a_main", "bob_db",
		`name="website_id" value="10"`,
		`name="return_to" value="/sites/10/databases"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("site page missing %q:\\n%s", want, body)
		}
	}
	if strings.Contains(body, "<th>Website</th>") {
		t.Fatalf("site page shows the Website column:\\n%s", body)
	}
	// Nothing from the other tenants.
	for _, leak := range []string{"b_main", "c_main", "alice_db"} {
		if strings.Contains(body, leak) {
			t.Fatalf("TENANT LEAK: bob's site page shows %q:\\n%s", leak, body)
		}
	}
}

func TestSiteDatabasesForeignIs404(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	// alice (site 20) must get a 404 for bob's site 10 — and no leak.
	alice := loginAsUser(t, h, "alice")
	rr := get(t, h, "/sites/10/databases", alice)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("foreign site = %d, want 404", rr.Code)
	}
	if body := rr.Body.String(); strings.Contains(body, "a_main") || strings.Contains(body, "bob_db") {
		t.Fatalf("TENANT LEAK in 404 body:\\n%s", body)
	}
	// A nonexistent id is also 404.
	if rr := get(t, h, "/sites/999/databases", alice); rr.Code != http.StatusNotFound {
		t.Fatalf("nonexistent site = %d, want 404", rr.Code)
	}
}

func TestSiteDatabasesAdminSeesAll(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	admin := loginAsUser(t, h, "admin")
	body := get(t, h, "/sites/10/databases", admin).Body.String()
	if !strings.Contains(body, "a_main") || !strings.Contains(body, "bob_db") {
		t.Fatalf("admin missing the site's data:\\n%s", body)
	}
	if !strings.Contains(body, `name="return_to" value="/sites/10/databases"`) {
		t.Fatalf("admin site page missing the return_to:\\n%s", body)
	}
}

func TestSiteDatabasesCreateAttachesSite(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	// The site page's create form pins website_id — the new database belongs
	// to the site, and the flash lands on the site page (return_to).
	token, bob := siteFormToken(t, h, bob, "/sites/10/databases")
	rr := postForm(h, "/databases", url.Values{
		"_token": {token}, "name": {"site_db"},
		"website_id": {"10"}, "return_to": {"/sites/10/databases"},
	}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	body := get(t, h, "/sites/10/databases", bob).Body.String()
	if !strings.Contains(body, "Database created.") || !strings.Contains(body, "bob_site_db") {
		t.Fatalf("site page missing the created database:\\n%s", body)
	}
	var siteID, ownerID *int64
	var name string
	err := db.QueryRow(`SELECT website_id, owner_id, name FROM databases WHERE owner_id = 2 ORDER BY id DESC`).Scan(&siteID, &ownerID, &name)
	if err != nil || name != "bob_site_db" || siteID == nil || *siteID != 10 {
		t.Fatalf("new row = %v %v %q %v", siteID, ownerID, name, err)
	}
	// The agent saw the create.
	if acts := sa.called(); len(acts) == 0 || acts[len(acts)-1] != "db.create" {
		t.Fatalf("agent actions = %v", acts)
	}
	// The global page still shows it too (same row, scoped list).
	if body := get(t, h, "/databases", bob).Body.String(); !strings.Contains(body, "bob_site_db") {
		t.Fatalf("global page missing the new database:\\n%s", body)
	}
}

func TestSiteDatabasesWithheldByDisabledMysqlFeature(t *testing.T) {
	// dbEnv: custA owns site 10 and his plan disables the mysql feature
	// (databases.view withheld); custB owns site 11 with an unrestricted plan.
	h, cookie, _ := dbEnv(t, "custA")
	if rr := get(t, h, "/sites/10/databases", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("mysql-disabled plan on the site page = %d, want 403", rr.Code)
	}
	h2, cookie2, _ := dbEnv(t, "custB")
	body := get(t, h2, "/sites/11/databases", cookie2).Body.String()
	if !strings.Contains(body, "b_main") {
		t.Fatalf("custB site page missing own database:\\n%s", body)
	}
}
