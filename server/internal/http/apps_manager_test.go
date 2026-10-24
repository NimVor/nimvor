package http

// This suite drives Administration → Application Manager (batch 64): the
// page (the seeded catalog), the installer switch, category enablement and
// the per-app enabled / customer_visible flags, each audited.

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// appsEnv is importEnv plus the settings permissions for the manager routes.
func appsEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sql.DB) {
	t.Helper()
	h, cookie, _, db := importEnv(t, loginAs)
	if _, err := db.Exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(11,'settings.view','sv','server'),(12,'settings.manage','sm','server')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,11),(1,12)`); err != nil {
		t.Fatal(err)
	}
	return h, cookie, db
}

// appsPost follows a manager action and returns the page's flash text.
func appsPost(t *testing.T, h http.Handler, cookie *http.Cookie, path string, values url.Values) (string, *http.Cookie) {
	t.Helper()
	token, cookie := pageToken(t, h, cookie, "/apps-manager")
	values.Set("_token", token)
	rr := postForm(h, path, values, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("post %s = %d, want 303", path, rr.Code)
	}
	body := ipFlash(t, h, cookie, "/apps-manager")
	return body, cookie
}

func TestAppsManagerPage(t *testing.T) {
	h, cookie, _ := appsEnv(t, "admin")
	body := ipFlash(t, h, cookie, "/apps-manager")
	for _, want := range []string{
		"Application Manager",
		"The application installer is <strong>on</strong>",
		"Blogs & CMS", "WordPress", "Joomla", // the seeded catalog
		"Forums", "phpBB",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("manager page missing %q", want)
		}
	}

	// The customer has no settings.view.
	h2, c2, _ := appsEnv(t, "bob")
	if rr := get(t, h2, "/apps-manager", c2); rr.Code != http.StatusForbidden {
		t.Errorf("customer page = %d, want 403", rr.Code)
	}
}

func TestAppsManagerInstaller(t *testing.T) {
	h, cookie, db := appsEnv(t, "admin")

	body, cookie := appsPost(t, h, cookie, "/apps-manager/installer", url.Values{"on": {"0"}})
	if !strings.Contains(body, "Application installer disabled.") {
		t.Errorf("disable flash missing:\n%s", body)
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'apps_installer_enabled'`).Scan(&value); err != nil || value != "false" {
		t.Fatalf("setting after disable = %q (%v)", value, err)
	}
	body, cookie = appsPost(t, h, cookie, "/apps-manager/installer", url.Values{"on": {"1"}})
	if !strings.Contains(body, "Application installer enabled.") {
		t.Errorf("enable flash missing:\n%s", body)
	}
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'apps_installer_enabled'`).Scan(&value); err != nil || value != "true" {
		t.Fatalf("setting after enable = %q (%v)", value, err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'apps.installer'`) != 2 {
		t.Error("apps.installer audits missing")
	}
}

func TestAppsManagerCategory(t *testing.T) {
	h, cookie, db := appsEnv(t, "admin")

	body, cookie := appsPost(t, h, cookie, "/apps-manager/category", url.Values{"slug": {"forums"}, "on": {"0"}})
	if !strings.Contains(body, "Category disabled.") {
		t.Errorf("disable flash missing:\n%s", body)
	}
	var enabled int
	if err := db.QueryRow(`SELECT enabled FROM app_categories WHERE slug = 'forums'`).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("category after disable = %d (%v)", enabled, err)
	}
	// The badge shows on the page.
	body = ipFlash(t, h, cookie, "/apps-manager")
	if !strings.Contains(body, "category disabled") {
		t.Error("the disabled badge must show")
	}
	body, cookie = appsPost(t, h, cookie, "/apps-manager/category", url.Values{"slug": {"forums"}, "on": {"1"}})
	if !strings.Contains(body, "Category enabled.") {
		t.Errorf("enable flash missing:\n%s", body)
	}
	if err := db.QueryRow(`SELECT enabled FROM app_categories WHERE slug = 'forums'`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("category after enable = %d (%v)", enabled, err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'apps.category'`) != 2 {
		t.Error("apps.category audits missing")
	}
}

func TestAppsManagerApp(t *testing.T) {
	h, cookie, db := appsEnv(t, "admin")

	body, cookie := appsPost(t, h, cookie, "/apps-manager/app",
		url.Values{"slug": {"wordpress"}, "field": {"customer_visible"}, "on": {"0"}})
	if !strings.Contains(body, "Application updated.") {
		t.Errorf("visibility flash missing:\n%s", body)
	}
	var visible int
	if err := db.QueryRow(`SELECT customer_visible FROM app_catalog WHERE slug = 'wordpress'`).Scan(&visible); err != nil || visible != 0 {
		t.Fatalf("visibility after toggle = %d (%v)", visible, err)
	}
	body, cookie = appsPost(t, h, cookie, "/apps-manager/app",
		url.Values{"slug": {"wordpress"}, "field": {"enabled"}, "on": {"0"}})
	if !strings.Contains(body, "Application updated.") {
		t.Errorf("enabled flash missing:\n%s", body)
	}
	var enabled int
	if err := db.QueryRow(`SELECT enabled FROM app_catalog WHERE slug = 'wordpress'`).Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("enabled after toggle = %d (%v)", enabled, err)
	}
	// An unknown slug is a flash, no change, no audit.
	body, cookie = appsPost(t, h, cookie, "/apps-manager/app",
		url.Values{"slug": {"nope"}, "field": {"enabled"}, "on": {"1"}})
	if !strings.Contains(body, "Unknown application.") {
		t.Errorf("unknown app flash missing:\n%s", body)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'apps.app'`) != 2 {
		t.Error("apps.app audits must cover the two real toggles only")
	}
}

func TestAppsManagerForbiddenForCustomer(t *testing.T) {
	h, cookie, _ := appsEnv(t, "bob")
	rr := postForm(h, "/apps-manager/installer", url.Values{"on": {"1"}}, cookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("customer installer = %d, want 403", rr.Code)
	}
}
