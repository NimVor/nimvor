package http

// This suite drives the WordPress management operations
// (WordPressController::manage + WordPressService::manage): the 19
// operations (the updates as tracked agent jobs, the core file
// verification with its flashed file list, the instant operations), the
// item filtering, the suspended account guard, the unknown operation, the
// automatic updates' value guard, and the permission gate. The agent is
// the recording fake (the wordpress.manage fixture in startSitesAgent).

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// wpManagePost fetches the overview for a fresh CSRF token, then POSTs the
// operation form to the manage route.
func wpManagePost(t *testing.T, h http.Handler, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/wordpress/1")
	form.Set("_token", token)
	return postForm(h, "/sites/10/wordpress/1/manage", form, cookie)
}

func TestWordPressManageDone(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	res := wpManagePost(t, h, bob, url.Values{"op": {"maintenance_off"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("manage = %d %q", res.Code, res.Header().Get("Location"))
	}
	// The flash + the operation the agent got.
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Turn maintenance mode off: done for bob.example (public_html).") {
		t.Errorf("the done flash is missing: %s", rr.Body.String())
	}
	if op, _ := agentParam(t, sa, "wordpress.manage", "op").(string); op != "maintenance_off" {
		t.Errorf("agent got op %q", op)
	}
	if items, _ := agentParam(t, sa, "wordpress.manage", "items").([]any); len(items) != 0 {
		t.Errorf("agent got items %v", items)
	}
	if n := wpAuditCount(t, db, "wordpress.maintenance_off"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
}

func TestWordPressManageJob(t *testing.T) {
	h, _, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	res := wpManagePost(t, h, bob, url.Values{"op": {"core_update"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("manage = %d %q", res.Code, res.Header().Get("Location"))
	}
	// The job is tracked and its flash is on the overview.
	var kind, target string
	if err := db.QueryRow(`SELECT kind, target_id FROM agent_jobs WHERE job_id = '0123456789ab0001'`).Scan(&kind, &target); err != nil {
		t.Fatalf("the job is not tracked: %v", err)
	}
	if kind != "Update WordPress: bob.example (public_html)" || target != "10" {
		t.Errorf("job tracked as %q / %q", kind, target)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	page := rr.Body.String()
	for _, want := range []string{
		"Update WordPress for bob.example (public_html) started; you will be notified when it finishes.",
		"Job <code>0123456789ab0001</code> started in the background.",
	} {
		if !pageHas(page, want) {
			t.Errorf("the overview misses %q", want)
		}
	}
}

func TestWordPressManageVerifyCore(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// Differing files: the list is flashed and rendered on the overview.
	res := wpManagePost(t, h, bob, url.Values{"op": {"verify_core"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("manage = %d", res.Code)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	page := rr.Body.String()
	for _, want := range []string{
		"2 core file(s) differ from the official release.",
		"Core file verification",
		"These files differ from the official WordPress 6.7.1 release",
		"wp-includes/theme-compat/class-wp-theme-compat.php",
		"wp-content/uploads/evil.php",
	} {
		if !pageHas(page, want) {
			t.Errorf("the verify state misses %q", want)
		}
	}

	// A clean verification: the all-matches message.
	sa.wpVerifyReply = "ok"
	bob = loginAsUser(t, h, "bob")
	res = wpManagePost(t, h, bob, url.Values{"op": {"verify_core"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("verify = %d", res.Code)
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "All WordPress core files match the official release.") {
		t.Error("the clean verify flash is missing")
	}
	if !pageHas(rr.Body.String(), "Every WordPress core file matches the official release.") {
		t.Error("the clean verify panel is missing")
	}
}

func TestWordPressManageErrors(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")

	// The unknown operation.
	bob := loginAsUser(t, h, "bob")
	res := wpManagePost(t, h, bob, url.Values{"op": {"nope"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("manage = %d", res.Code)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Unknown operation.") {
		t.Error("the unknown-operation flash is missing")
	}

	// The automatic updates' value guard (no agent call).
	bob = loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/wordpress/1")
	before := len(sa.called())
	res = postForm(h, "/sites/10/wordpress/1/manage", url.Values{"_token": {token}, "op": {"core_auto_update"}, "value": {"sometimes"}}, bob)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("manage = %d", res.Code)
	}
	if len(sa.called()) != before {
		t.Error("the invalid value still reached the agent")
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Choose minor, all or no automatic updates.") {
		t.Error("the value-guard flash is missing")
	}

	// A suspended hosting account cannot be changed (no agent call): an
	// administrator acting on the customer's website is refused too. (The
	// suspended customer cannot sign in at all.)
	admin := loginAsUser(t, h, "admin")
	if _, err := db.Exec(`UPDATE users SET suspended_at = '2026-01-01T00:00:00Z' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	token, admin = siteFormToken(t, h, admin, "/sites/10/wordpress/1")
	before = len(sa.called())
	res = postForm(h, "/sites/10/wordpress/1/manage", url.Values{"_token": {token}, "op": {"maintenance_off"}}, admin)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("manage = %d", res.Code)
	}
	if len(sa.called()) != before {
		t.Error("the suspended account still reached the agent")
	}
	rr = get(t, h, "/sites/10/wordpress/1", admin)
	if !pageHas(rr.Body.String(), "The hosting account is suspended; WordPress cannot be changed.") {
		t.Error("the suspended flash is missing")
	}

	// The items are filtered to the valid, unique names (deduped).
	if _, err := db.Exec(`UPDATE users SET suspended_at = NULL WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	bob = loginAsUser(t, h, "bob")
	res = wpManagePost(t, h, bob, url.Values{"op": {"plugin_update"}, "items[]": {"hello", "bad/name", "jetpack", "jetpack", "has space"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("manage = %d", res.Code)
	}
	items, _ := agentParam(t, sa, "wordpress.manage", "items").([]any)
	if len(items) != 2 || items[0] != "hello" || items[1] != "jetpack" {
		t.Errorf("agent got items %v", items)
	}
}

func TestWordPressManagePermission(t *testing.T) {
	h, _, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	// The viewer has websites.view only: a 403 before the handler.
	carol := loginAsUser(t, h, "carol")
	token, carol := siteFormToken(t, h, carol, "/sites/20/wordpress?list=1")
	rr := postForm(h, "/sites/10/wordpress/1/manage", url.Values{"_token": {token}, "op": {"maintenance_off"}}, carol)
	if rr.Code != http.StatusForbidden {
		t.Errorf("carol's manage = %d, want 403", rr.Code)
	}
}

// wpAuditCount is the number of audit_logs rows with action for website 10.
func wpAuditCount(t *testing.T, db *sql.DB, action string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = ? AND target_id = '10'`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
