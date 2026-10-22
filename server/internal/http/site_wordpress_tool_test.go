package http

// This suite drives the WordPress toolkit's tools (WordPressController::tool):
// the debug switches, the password and hotlink protection, the administrator
// and the database passwords, the security measures' one-click apply, the
// unknown and the not-yet-migrated tools, and the permission gate. The agent
// is the recording fake (the wordpress.manage, protect.* fixtures).

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// wpToolPost fetches the overview for a fresh CSRF token, then POSTs the tool
// form to the tools route.
func wpToolPost(t *testing.T, h http.Handler, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/wordpress/1")
	form.Set("_token", token)
	return postForm(h, "/sites/10/wordpress/1/tools", form, cookie)
}

func TestWordPressToolDebug(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// The switches on: the agent gets the filtered, deduped flags.
	res := wpToolPost(t, h, bob, url.Values{
		"tool": {"debug"}, "enabled": {"1"},
		"debug[]": {"WP_DEBUG_LOG", "WP_DEBUG", "bogus", "WP_DEBUG_LOG"},
	})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	last := len(sa.called()) - 1
	if op, _ := sa.param(last, "op").(string); op != "debug" {
		t.Errorf("agent got op %q", op)
	}
	if items, _ := sa.param(last, "items").([]any); len(items) != 2 || items[0] != "WP_DEBUG" || items[1] != "WP_DEBUG_LOG" {
		t.Errorf("agent got items %v", items)
	}
	if n := wpAuditCount(t, db, "wordpress.debug"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	var debug int
	if err := db.QueryRow(`SELECT debug FROM wordpress_installs WHERE website_id = 10`).Scan(&debug); err != nil {
		t.Fatal(err)
	}
	if debug != 1 {
		t.Errorf("the debug column = %d", debug)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "Debugging settings saved: WP_DEBUG, WP_DEBUG_LOG.") {
		t.Errorf("the debug flash is missing: %s", rr.Body.String())
	}

	// The switch off: no flags, the column back to 0.
	res = wpToolPost(t, h, bob, url.Values{"tool": {"debug"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool off = %d", res.Code)
	}
	if err := db.QueryRow(`SELECT debug FROM wordpress_installs WHERE website_id = 10`).Scan(&debug); err != nil {
		t.Fatal(err)
	}
	if debug != 0 {
		t.Errorf("the debug column = %d after off", debug)
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "Debugging is off.") {
		t.Error("the off flash is missing")
	}
}

func TestWordPressToolPasswordProtection(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// On: the agent gets the path, the user and the password.
	res := wpToolPost(t, h, bob, url.Values{
		"tool": {"password_protection"}, "enabled": {"1"},
		"protect_user": {"wpuser"}, "protect_password": {"secret123"},
	})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	last := len(sa.called()) - 1
	if action := sa.called()[last]; action != "protect.password" {
		t.Fatalf("agent action = %q", action)
	}
	for key, want := range map[string]string{"domain": "bob.example", "path": "/", "user": "wpuser", "password": "secret123", "realm": "Restricted area"} {
		if got, _ := sa.param(last, key).(string); got != want {
			t.Errorf("agent %s = %q, want %q", key, got, want)
		}
	}
	if n := wpAuditCount(t, db, "wordpress.password_protection"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "Password protection is on: visitors to bob.example/ must sign in as wpuser.") {
		t.Errorf("the on flash is missing: %s", rr.Body.String())
	}

	// A short password is refused before the agent.
	token, bob := siteFormToken(t, h, bob, "/sites/10/wordpress/1")
	before := len(sa.called())
	res = postForm(h, "/sites/10/wordpress/1/tools", url.Values{
		"_token": {token}, "tool": {"password_protection"}, "enabled": {"1"},
		"protect_user": {"wpuser"}, "protect_password": {"short"},
	}, bob)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool = %d", res.Code)
	}
	if len(sa.called()) != before {
		t.Error("the invalid password still reached the agent")
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "Use 8 to 128 characters.") {
		t.Error("the password flash is missing")
	}

	// Off: the flag only.
	res = wpToolPost(t, h, bob, url.Values{"tool": {"password_protection"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool off = %d", res.Code)
	}
	last = len(sa.called()) - 1
	if action := sa.called()[last]; action != "protect.password" {
		t.Fatalf("agent action = %q", action)
	}
	if got, _ := sa.param(last, "enabled").(bool); got {
		t.Error("the agent got enabled=true")
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "Password protection is off.") {
		t.Error("the off flash is missing")
	}
}

func TestWordPressToolHotlink(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// On with the allowed list (lowercased, split).
	res := wpToolPost(t, h, bob, url.Values{
		"tool": {"hotlink"}, "enabled": {"1"}, "hotlink_allowed": {"Partner.com, *.cdn.example.com"},
	})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	last := len(sa.called()) - 1
	if action := sa.called()[last]; action != "protect.hotlink" {
		t.Fatalf("agent action = %q", action)
	}
	if allowed, _ := sa.param(last, "allowed").([]any); len(allowed) != 2 || allowed[0] != "partner.com" || allowed[1] != "*.cdn.example.com" {
		t.Errorf("agent got allowed %v", allowed)
	}
	if n := wpAuditCount(t, db, "wordpress.hotlink"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "Hotlink protection is on: images and media are only served to pages of this website and partner.com, *.cdn.example.com.") {
		t.Errorf("the on flash is missing: %s", rr.Body.String())
	}

	// A bad host is refused before the agent.
	token, bob := siteFormToken(t, h, bob, "/sites/10/wordpress/1")
	before := len(sa.called())
	res = postForm(h, "/sites/10/wordpress/1/tools", url.Values{
		"_token": {token}, "tool": {"hotlink"}, "enabled": {"1"}, "hotlink_allowed": {"bad_host"},
	}, bob)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool = %d", res.Code)
	}
	if len(sa.called()) != before {
		t.Error("the bad host still reached the agent")
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), `"bad_host" is not a host name (a leading *. is allowed).`) {
		t.Error("the host flash is missing")
	}
}

func TestWordPressToolAdminPassword(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// The matched pair: the agent gets the admin and the new password.
	res := wpToolPost(t, h, bob, url.Values{
		"tool": {"admin_password"}, "admin_id": {"1"},
		"admin_password": {"newpass123"}, "admin_password_confirm": {"newpass123"},
	})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	last := len(sa.called()) - 1
	if op, _ := sa.param(last, "op").(string); op != "admin_password" {
		t.Errorf("agent got op %q", op)
	}
	if items, _ := sa.param(last, "items").([]any); len(items) != 1 || items[0] != "1" {
		t.Errorf("agent got items %v", items)
	}
	if pw, _ := sa.param(last, "password").(string); pw != "newpass123" {
		t.Errorf("agent got password %q", pw)
	}
	if n := wpAuditCount(t, db, "wordpress.admin_password"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "The password of the WordPress administrator bob was changed and their other sessions were signed out.") {
		t.Errorf("the flash is missing: %s", rr.Body.String())
	}

	// The mismatch is refused before the agent.
	token, bob := siteFormToken(t, h, bob, "/sites/10/wordpress/1")
	before := len(sa.called())
	res = postForm(h, "/sites/10/wordpress/1/tools", url.Values{
		"_token": {token}, "tool": {"admin_password"}, "admin_id": {"1"},
		"admin_password": {"newpass123"}, "admin_password_confirm": {"newpass124"},
	}, bob)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool = %d", res.Code)
	}
	if len(sa.called()) != before {
		t.Error("the mismatch still reached the agent")
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "The passwords do not match.") {
		t.Error("the mismatch flash is missing")
	}
}

func TestWordPressToolDbPassword(t *testing.T) {
	h, _, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	// The owner kept a stored copy of the database password.
	if _, err := db.Exec(`INSERT INTO database_users (server_id, owner_id, username, host, password_enc, created_at)
		VALUES (1, 2, 'u_bob_wp', 'localhost', 'oldenc', '')`); err != nil {
		t.Fatal(err)
	}
	bob := loginAsUser(t, h, "bob")

	res := wpToolPost(t, h, bob, url.Values{"tool": {"db_password"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1#database" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	if n := wpAuditCount(t, db, "wordpress.db_password"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	// The new password is shown once on the overview...
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	const marker = "New database password (shown once; wp-config.php already uses it): <code class=\"user-select-all\">"
	body := rr.Body.String()
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("the password alert is missing: %s", body)
	}
	rest := body[start+len(marker):]
	password := rest[:strings.Index(rest, "</code>")]
	if len(password) != 24 {
		t.Errorf("the password length = %d (%q)", len(password), password)
	}
	// ...and the stored copy follows it.
	var enc string
	if err := db.QueryRow(`SELECT password_enc FROM database_users WHERE username = 'u_bob_wp' AND host = 'localhost'`).Scan(&enc); err != nil {
		t.Fatal(err)
	}
	if enc == "" || enc == "oldenc" {
		t.Errorf("the stored password_enc = %q", enc)
	}
	// A second look: the flash is consumed.
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if strings.Contains(rr.Body.String(), marker) {
		t.Error("the password is shown twice")
	}
}

func TestWordPressToolSecurity(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// The one-click apply sends the nine recommended measures.
	res := wpToolPost(t, h, bob, url.Values{"tool": {"security"}, "op": {"apply"}, "recommended": {"1"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1#security" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	last := len(sa.called()) - 1
	if action := sa.called()[last]; action != "wordpress.security" {
		t.Fatalf("agent action = %q", action)
	}
	if op, _ := sa.param(last, "op").(string); op != "apply" {
		t.Errorf("agent got op %q", op)
	}
	if measures, _ := sa.param(last, "measures").([]any); len(measures) != 9 || measures[0] != "block_sensitive_files" || measures[8] != "config_permissions" {
		t.Errorf("agent got measures %v", measures)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "Security measures applied: 9 measure(s).") {
		t.Errorf("the flash is missing: %s", rr.Body.String())
	}

	// No measures: the guard, no agent call.
	token, bob := siteFormToken(t, h, bob, "/sites/10/wordpress/1")
	before := len(sa.called())
	res = postForm(h, "/sites/10/wordpress/1/tools", url.Values{
		"_token": {token}, "tool": {"security"}, "op": {"apply"},
	}, bob)
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1#security" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	if len(sa.called()) != before {
		t.Error("the empty measures still reached the agent")
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "Choose at least one measure.") {
		t.Error("the guard flash is missing")
	}
}

func TestWordPressToolCron(t *testing.T) {
	h, _, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// On: the cron job is saved with the agent's command and linked.
	res := wpToolPost(t, h, bob, url.Values{"tool": {"cron"}, "enabled": {"1"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	var jobID int64
	var schedule, command, description string
	if err := db.QueryRow(`SELECT id, schedule, command, description FROM cron_jobs WHERE website_id = 10 ORDER BY id DESC LIMIT 1`).Scan(&jobID, &schedule, &command, &description); err != nil {
		t.Fatalf("no cron job: %v", err)
	}
	if schedule != "*/5 * * * *" {
		t.Errorf("schedule = %q", schedule)
	}
	if command != "/usr/bin/php8.3.1 /usr/local/lib/whacp/wp-cli.phar --path=/home/whacp/bob.example/public_html cron event run --due-now --quiet" {
		t.Errorf("command = %q", command)
	}
	if description != "WordPress scheduled tasks (public_html) — taken over from wp-cron.php" {
		t.Errorf("description = %q", description)
	}
	var linked sql.NullInt64
	if err := db.QueryRow(`SELECT cron_job_id FROM wordpress_installs WHERE website_id = 10`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if !linked.Valid || linked.Int64 != jobID {
		t.Errorf("cron_job_id = %v, want %d", linked, jobID)
	}
	if n := wpAuditCount(t, db, "wordpress.cron_takeover"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "wp-cron.php is taken over: WordPress's scheduled tasks now run every 5 minutes from a cron job (listed under Cron jobs).") {
		t.Errorf("the flash is missing: %s", rr.Body.String())
	}

	// On again: no duplicate job.
	res = wpToolPost(t, h, bob, url.Values{"tool": {"cron"}, "enabled": {"1"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool = %d", res.Code)
	}
	var jobs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cron_jobs WHERE website_id = 10`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Errorf("cron jobs = %d", jobs)
	}

	// Off: the job is deleted and the link cleared.
	res = wpToolPost(t, h, bob, url.Values{"tool": {"cron"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool off = %d", res.Code)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM cron_jobs WHERE website_id = 10`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 0 {
		t.Errorf("cron jobs after off = %d", jobs)
	}
	if err := db.QueryRow(`SELECT cron_job_id FROM wordpress_installs WHERE website_id = 10`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if linked.Valid {
		t.Errorf("cron_job_id = %d after off", linked.Int64)
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !strings.Contains(rr.Body.String(), "WordPress runs its scheduled tasks on visits again (wp-cron.php).") {
		t.Error("the off flash is missing")
	}
}

func TestWordPressToolUnknownAndSoon(t *testing.T) {
	h, _, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	for _, tool := range []string{"nope", "vulnerability", "vuln_check", "clone", "sync", "restore"} {
		want := "Unknown tool."
		if tool != "nope" {
			want = "This tool is coming soon."
		}
		res := wpToolPost(t, h, bob, url.Values{"tool": {tool}})
		if res.Code != http.StatusSeeOther {
			t.Fatalf("tool %s = %d", tool, res.Code)
		}
		rr := get(t, h, "/sites/10/wordpress/1", bob)
		if !strings.Contains(rr.Body.String(), want) {
			t.Errorf("tool %s: the flash %q is missing", tool, want)
		}
	}
}

func TestWordPressToolPermission(t *testing.T) {
	h, _, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	// The viewer has websites.view only: a 403 before the handler.
	carol := loginAsUser(t, h, "carol")
	token, carol := siteFormToken(t, h, carol, "/sites/20/wordpress?list=1")
	rr := postForm(h, "/sites/10/wordpress/1/tools", url.Values{"_token": {token}, "tool": {"db_password"}}, carol)
	if rr.Code != http.StatusForbidden {
		t.Errorf("carol's tool = %d, want 403", rr.Code)
	}
}
