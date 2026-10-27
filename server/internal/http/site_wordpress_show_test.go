package http

// This suite drives the WordPress installation's overview and its
// show / check / login / logs actions (WordPressController::show, check,
// login, logs): the overview (the agent's info with the computed checks,
// the toolkit's extras — protection, security measures, restore points,
// copy / sync targets — and the database browser link), the forced
// re-check (the flash), the one-time sign-in (the HTTPS guard, the agent's
// file), and the debug log view. The agent is the recording fake
// (the wordpress.info / protect.status / wordpress.security /
// wordpress.restore_points / wordpress.login fixtures in startSitesAgent).

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestWordPressShow(t *testing.T) {
	h, _, db := mongoWEnv(t)
	// The installation lives in the document root (web rules can reach it)
	// and its database is one of the panel's.
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	if _, err := db.Exec(`INSERT INTO databases (server_id, website_id, owner_id, name, created_at) VALUES (1, 10, 2, 'u_bob_wp', '')`); err != nil {
		t.Fatal(err)
	}
	// The link follows the installation's stored database name.
	if _, err := db.Exec(`UPDATE wordpress_installs SET db_name = 'u_bob_wp' WHERE website_id = 10`); err != nil {
		t.Fatal(err)
	}
	bob := loginAsUser(t, h, "bob")

	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/wordpress/1 = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"Bob's Blog",                   // the overview's title
		"WordPress 6.7.1",              // the hero's version
		"Update to 6.8",                // outdated
		"Security 1/9",                 // one of the nine measures applied
		"Vulnerabilities not checked",  // never checked
		"1 update(s)",                  // the plugin's update
		"6 to review",                  // the non-info checks
		"Hello Dolly", "1.8 available", // the plugin's table
		"Twenty Twenty-Five",         // the theme
		"WordPress 6.8 is available", // the danger check
		"Debugging is on",            // WP_DEBUG
		"The theme and plugin file editor is on",
		"The website has no HTTPS",
		"8.3.1 (latest WordPress needs 7.2.24+)",
		"2025-10-20 22:40 UTC", // the core's checked time
		"u_bob_wp", "42 tables · 5.0 MB · 10.11.6-MariaDB",
		"/databases/",                            // the browser link (the database is seeded)
		"Visitors sign in as <code>u_bob</code>", // the protection match
		"(allowed: partner.com)",                 // the hotlink's allowed list
		"2026-01-01 01:01 UTC",                   // the restore point (formatted)
		"Before the sync · files and database",
		"Log in to WordPress", // the manage's sign-in
	} {
		if !pageHas(page, want) {
			t.Errorf("show page missing %q", want)
		}
	}
	// The other restore point belongs to another installation: absent.
	if pageHas(page, "Before the clone") {
		t.Error("a foreign restore point is listed")
	}
	// A viewer cannot see bob's website: flashed, redirected to the list.
	carol := loginAsUser(t, h, "carol")
	rr = get(t, h, "/sites/10/wordpress/1", carol)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Errorf("carol's show = %d %q, want 303 /websites", rr.Code, rr.Header().Get("Location"))
	}
}

func TestWordPressShowAgentError(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	sa.failActions["wordpress.info"] = "WordPress could not be read."
	bob := loginAsUser(t, h, "bob")

	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("show = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	if !pageHas(page, "The website could not be read") ||
		!pageHas(page, "WordPress could not be read.") {
		t.Error("the agent's error is not shown")
	}
	if pageHas(page, "Health checks") {
		t.Error("the overview is rendered although the agent failed")
	}
}

func TestWordPressCheck(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// The check posts without a token round trip: the page carries the form.
	token, bob := siteFormToken(t, h, bob, "/sites/10/wordpress/1")
	rr := postForm(h, "/sites/10/wordpress/1/check", url.Values{"_token": {token}}, bob)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("check = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	// The next overview is forced: the agent gets force = true.
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("forced show = %d", rr.Code)
	}
	last := -1 // sa.param indexes the calls without db.overview
	for i, a := range sa.writes() {
		if a == "wordpress.info" {
			last = i
		}
	}
	if force, _ := sa.param(last, "force").(bool); !force {
		t.Errorf("the forced check sent force = %v", sa.param(last, "force"))
	}
}

func TestWordPressLogin(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// No HTTPS: the sign-in is refused with an explanation.
	token, bob := siteFormToken(t, h, bob, "/sites/10/wordpress/1")
	rr := postForm(h, "/sites/10/wordpress/1/login", url.Values{"_token": {token}, "admin_id": {"1"}}, bob)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("login = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, bob, "/sites/10/wordpress/1", "One-click sign-in needs HTTPS")

	// With HTTPS: a one-time link to the agent's file.
	if _, err := db.Exec(`UPDATE websites SET https = 1 WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	bob = loginAsUser(t, h, "bob")
	token, bob = siteFormToken(t, h, bob, "/sites/10/wordpress/1")
	rr = postForm(h, "/sites/10/wordpress/1/login", url.Values{"_token": {token}, "admin_id": {"1"}}, bob)
	if rr.Code != http.StatusFound ||
		rr.Header().Get("Location") != "https://bob.example/whacp-login-a1b2c3d4e5f60718293a4b5c6d7e8f90.php" {
		t.Fatalf("login = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	// The agent got the chosen administrator.
	last := len(sa.called()) - 1
	if id, _ := sa.param(last, "admin_id").(float64); id != 1 {
		t.Errorf("agent got admin_id %v", sa.param(last, "admin_id"))
	}
	if n := wpLoginAuditCount(t, db); n != 1 {
		t.Errorf("login audit rows = %d", n)
	}
}

func TestWordPressLogs(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// The log's end (newest first).
	rr := get(t, h, "/sites/10/wordpress/1/logs", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("logs = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"debug.log",
		"[2026-10-20 09:05:00] PHP Warning:  Something",
		"[2026-10-20 09:00:00] PHP Notice:  Undefined index foo",
		"Newest first; the last 2 lines.",
		"2.0 KB",
	} {
		if !pageHas(page, want) {
			t.Errorf("logs page missing %q", want)
		}
	}
	// The newest line comes first in the list.
	if i := strings.Index(page, "09:05:00"); i == 0 || strings.Index(page, "09:00:00") < i {
		t.Errorf("log lines are not newest first")
	}

	// An empty log.
	sa.wpDebugLog = "empty"
	rr = get(t, h, "/sites/10/wordpress/1/logs", bob)
	if !pageHas(rr.Body.String(), "The debug log is empty.") {
		t.Error("the empty log state is missing")
	}
	// No log file at all.
	sa.wpDebugLog = "missing"
	rr = get(t, h, "/sites/10/wordpress/1/logs", bob)
	if !pageHas(rr.Body.String(), "No debug log") {
		t.Error("the missing log state is missing")
	}
}

// wpLoginAuditCount is the number of wordpress.login audit rows for website 10.
func wpLoginAuditCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'wordpress.login' AND target_id = '10'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
