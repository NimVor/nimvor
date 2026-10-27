package http

// This suite drives the WordPress toolkit's tools (WordPressController::tool):
// the debug switches, the password and hotlink protection, the administrator
// and the database passwords, the security measures' one-click apply, the
// unknown and the not-yet-migrated tools, and the permission gate. The agent
// is the recording fake (the wordpress.manage, protect.* fixtures).

import (
	"database/sql"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
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
	if !pageHas(rr.Body.String(), "Debugging settings saved: WP_DEBUG, WP_DEBUG_LOG.") {
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
	if !pageHas(rr.Body.String(), "Debugging is off.") {
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
	if !pageHas(rr.Body.String(), "Password protection is on: visitors to bob.example/ must sign in as wpuser.") {
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
	if !pageHas(rr.Body.String(), "Use 8 to 128 characters.") {
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
	if !pageHas(rr.Body.String(), "Password protection is off.") {
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
	if !pageHas(rr.Body.String(), "Hotlink protection is on: images and media are only served to pages of this website and partner.com, *.cdn.example.com.") {
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
	if !pageHas(rr.Body.String(), `"bad_host" is not a host name (a leading *. is allowed).`) {
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
	if !pageHas(rr.Body.String(), "The password of the WordPress administrator bob was changed and their other sessions were signed out.") {
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
	if !pageHas(rr.Body.String(), "The passwords do not match.") {
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
	password := html.UnescapeString(rest[:strings.Index(rest, "</code>")])
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
	if pageHas(rr.Body.String(), marker) {
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
	if !pageHas(rr.Body.String(), "Security measures applied: 9 measure(s).") {
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
	if !pageHas(rr.Body.String(), "Choose at least one measure.") {
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
	if !pageHas(rr.Body.String(), "wp-cron.php is taken over: WordPress's scheduled tasks now run every 5 minutes from a cron job (listed under Cron jobs).") {
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
	if !pageHas(rr.Body.String(), "WordPress runs its scheduled tasks on visits again (wp-cron.php).") {
		t.Error("the off flash is missing")
	}
}

// wpLastModsecSiteMode is the mode of the last modsec.site agent call ("").
func wpLastModsecSiteMode(sa *sitesAgent) string {
	for i := len(sa.called()) - 1; i >= 0; i-- {
		if sa.called()[i] == "modsec.site" {
			m, _ := sa.param(i, "mode").(string)
			return m
		}
	}
	return ""
}

func TestWordPressToolVulnerability(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	// On: the site is set to blocking mode and the column is linked.
	res := wpToolPost(t, h, bob, url.Values{"tool": {"vulnerability"}, "enabled": {"1"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	if mode := wpLastModsecSiteMode(sa); mode != "on" {
		t.Errorf("modsec.site mode = %q", mode)
	}
	var on int
	if err := db.QueryRow(`SELECT vuln_protection FROM wordpress_installs WHERE website_id = 10`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 1 {
		t.Errorf("vuln_protection = %d", on)
	}
	if n := wpAuditCount(t, db, "wordpress.vulnerability_protection"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Vulnerability protection is on: requests to the website are filtered by ModSecurity with the OWASP Core Rule Set (blocking), and the installation is checked for known vulnerabilities every day.") {
		t.Errorf("the on flash is missing: %s", rr.Body.String())
	}

	// Off: no other installation wants it, so the site goes back to
	// detection only.
	res = wpToolPost(t, h, bob, url.Values{"tool": {"vulnerability"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool off = %d", res.Code)
	}
	if mode := wpLastModsecSiteMode(sa); mode != "detect" {
		t.Errorf("modsec.site mode = %q, want detect", mode)
	}
	if err := db.QueryRow(`SELECT vuln_protection FROM wordpress_installs WHERE website_id = 10`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 0 {
		t.Errorf("vuln_protection after off = %d", on)
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Vulnerability protection is off (ModSecurity only logs for this website now).") {
		t.Error("the off flash is missing")
	}

	// The guard: ModSecurity not turned on (no site call, no column change).
	sa.modsecActive = false
	bob = loginAsUser(t, h, "bob")
	res = wpToolPost(t, h, bob, url.Values{"tool": {"vulnerability"}, "enabled": {"1"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool = %d", res.Code)
	}
	if mode := wpLastModsecSiteMode(sa); mode != "detect" {
		t.Errorf("modsec.site mode = %q, want the earlier detect", mode)
	}
	if err := db.QueryRow(`SELECT vuln_protection FROM wordpress_installs WHERE website_id = 10`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 0 {
		t.Errorf("vuln_protection after the guard = %d", on)
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Vulnerability protection needs ModSecurity, which is not installed or not turned on on this server. Ask your hosting provider to turn on ModSecurity.") {
		t.Errorf("the guard flash is missing: %s", rr.Body.String())
	}
}

func TestWordPressToolVulnCheck(t *testing.T) {
	h, _, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	// The database answers: one high in the core (affects 6.7.1), one
	// medium in Hello Dolly (affects 1.7), nothing in the rest.
	rest := `{"data":{"vulnerability":[]}}`
	old := hosting.WpVulnFetch
	hosting.WpVulnFetch = func(rawurl string) (string, bool) {
		switch {
		case strings.HasSuffix(rawurl, "core/6.7.1/"):
			return `{"data":{"vulnerability":[{"name":"SQL Injection in <b>core</b>","operator":{"min_operator":"ge","min_version":"6.0","max_operator":"lt","max_version":"6.7.2"},"impact":{"cvss":{"severity":"h","score":"8.1"}},"source":[{"id":"CVE-2026-1234","link":"https://www.wpvulnerability.net/vulnerabilities/1234"}]}]}}`, true
		case strings.HasSuffix(rawurl, "plugin/hello/"):
			return `{"data":{"vulnerability":[{"name":"XSS in Hello","operator":{"max_operator":"lt","max_version":"1.8"},"impact":{"cvss":{"severity":"m","score":"5.4"}},"source":[{"id":"WPVMA-2026-001","link":"https://www.wpvulnerability.net/vulnerabilities/9999"}]}]}}`, true
		case strings.HasSuffix(rawurl, "plugin/jetpack/"), strings.HasSuffix(rawurl, "theme/twentytwentyfive/"):
			return rest, true
		default:
			return "", false
		}
	}
	t.Cleanup(func() { hosting.WpVulnFetch = old })

	bob := loginAsUser(t, h, "bob")
	res := wpToolPost(t, h, bob, url.Values{"tool": {"vuln_check"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1#security" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "2 known vulnerabilities found.") {
		t.Errorf("the flash is missing: %s", rr.Body.String())
	}
	var vulns, checkedAt string
	if err := db.QueryRow(`SELECT vulns, vulns_checked_at FROM wordpress_installs WHERE website_id = 10`).Scan(&vulns, &checkedAt); err != nil {
		t.Fatal(err)
	}
	if checkedAt == "" {
		t.Error("vulns_checked_at is empty")
	}
	// The stored list: the high one first (severity order), the tags
	// stripped, the fixed-in versions kept.
	for _, want := range []string{
		`"kind":"core"`, `"severity":"high"`, `"score":"8.1"`, `"name":"SQL Injection in core"`,
		`"cve":"CVE-2026-1234"`, `"fixed_in":"6.7.2"`, `"title":"Hello Dolly"`, `"fixed_in":"1.8"`,
	} {
		if !strings.Contains(vulns, want) {
			t.Errorf("the stored vulns miss %q: %s", want, vulns)
		}
	}
	if iHigh := strings.Index(vulns, `"severity":"high"`); iHigh < 0 || iHigh > strings.Index(vulns, `"severity":"medium"`) {
		t.Errorf("the stored vulns are not severity-ordered: %s", vulns)
	}

	// The unreachable database: the error, nothing stored.
	hosting.WpVulnFetch = func(string) (string, bool) { return "", false }
	bob = loginAsUser(t, h, "bob")
	res = wpToolPost(t, h, bob, url.Values{"tool": {"vuln_check"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool = %d", res.Code)
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "The vulnerability database (wpvulnerability.net) could not be reached from this server; try again later.") {
		t.Errorf("the unreachable flash is missing: %s", rr.Body.String())
	}
}

// wpSeedDatabases gives the seeded installation its database (one of the
// owner's, in the panel's records — what the copy tools require).
func wpSeedDatabases(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`UPDATE wordpress_installs SET db_name = 'u_bob_wp' WHERE website_id = 10`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO databases (server_id, website_id, owner_id, name, created_at)
		VALUES (1, 10, 2, 'u_bob_wp', '')`); err != nil {
		t.Fatal(err)
	}
}

// wpLastCopyCall is the last index of the wordpress.copy agent call (-1).
func wpLastCopyCall(sa *sitesAgent) int {
	for i := len(sa.called()) - 1; i >= 0; i-- {
		if sa.called()[i] == "wordpress.copy" {
			return i
		}
	}
	return -1
}

func TestWordPressToolClone(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	wpSeedDatabases(t, db)
	bob := loginAsUser(t, h, "bob")

	// To a folder of the same website.
	res := wpToolPost(t, h, bob, url.Values{
		"tool": {"clone"}, "target_site": {"10"}, "target_path": {"staging"}, "staging": {"1"},
	})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1#staging" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	var kind, targetID string
	if err := db.QueryRow(`SELECT kind, target_id FROM agent_jobs WHERE job_id = '0123456789c0a001'`).Scan(&kind, &targetID); err != nil {
		t.Fatalf("the job is not tracked: %v", err)
	}
	if kind != "Clone WordPress: bob.example → http://bob.example/staging" || targetID != "10" {
		t.Errorf("job tracked as %q / %q", kind, targetID)
	}
	// The copy is recorded with its own fresh database (the owner's prefix).
	var newDB string
	var clonedFrom int64
	if err := db.QueryRow(`SELECT db_name, cloned_from FROM wordpress_installs WHERE website_id = 10 AND path = 'public_html/staging'`).Scan(&newDB, &clonedFrom); err != nil {
		t.Fatalf("the copy is not recorded: %v", err)
	}
	if !strings.HasPrefix(newDB, "bob_wp") || len(newDB) != len("bob_wp")+6 {
		t.Errorf("the clone database = %q", newDB)
	}
	if clonedFrom == 0 {
		t.Error("cloned_from is empty")
	}
	var users int
	if err := db.QueryRow(`SELECT COUNT(*) FROM database_users WHERE username = ? AND host = 'localhost'`, newDB).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if users != 1 {
		t.Errorf("the clone database user rows = %d", users)
	}
	if n := wpAuditCount(t, db, "wordpress.clone"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	last := wpLastCopyCall(sa)
	if last < 0 {
		t.Fatal("the agent never got wordpress.copy")
	}
	if mode, _ := sa.param(last, "mode").(string); mode != "clone" {
		t.Errorf("agent got mode %q", mode)
	}
	if dbp, _ := sa.param(last, "db").(map[string]any); dbp == nil || dbp["name"] != newDB || dbp["host"] != "localhost" {
		t.Errorf("agent got db %v", dbp)
	}
	if staging, _ := sa.param(last, "staging").(bool); !staging {
		t.Error("the agent got staging=false")
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	page := rr.Body.String()
	if !pageHas(page, "Cloning started; the copy appears in the WordPress list when it is done.") {
		t.Errorf("the flash is missing: %s", page)
	}
	if !pageHas(page, "Job <code>0123456789c0a001</code> started in the background.") {
		t.Error("the job notice is missing")
	}
}

func TestWordPressToolSync(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	wpSeedDatabases(t, db)
	// The staging copy: another installation of the same owner.
	if _, err := db.Exec(`INSERT INTO wordpress_installs
		(server_id, website_id, path, version, latest, plugin_updates, theme_updates, issues, scanned_at, db_name, cloned_from)
		VALUES (1, 10, 'public_html/staging', '6.7.1', '6.8', 0, 0, 0, '2026-10-20T09:30:00Z', 'u_bob_wp2', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO databases (server_id, website_id, owner_id, name, created_at)
		VALUES (1, 10, 2, 'u_bob_wp2', '')`); err != nil {
		t.Fatal(err)
	}
	var destID int64
	if err := db.QueryRow(`SELECT id FROM wordpress_installs WHERE path = 'public_html/staging'`).Scan(&destID); err != nil {
		t.Fatal(err)
	}
	bob := loginAsUser(t, h, "bob")

	// Neither files nor the database: the guard.
	res := wpToolPost(t, h, bob, url.Values{"tool": {"sync"}, "target": {fmt.Sprintf("%d", destID)}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool = %d", res.Code)
	}
	if wpLastCopyCall(sa) != -1 {
		t.Error("the guard still reached the agent")
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Choose files, the database or both.") {
		t.Error("the guard flash is missing")
	}

	// The files and the database (both owners' databases in the panel): the
	// job with its kind and the agent's parameters.
	res = wpToolPost(t, h, bob, url.Values{"tool": {"sync"}, "target": {fmt.Sprintf("%d", destID)}, "files": {"1"}, "database": {"1"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1#staging" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM agent_jobs WHERE job_id = '0123456789c0a001'`).Scan(&kind); err != nil {
		t.Fatalf("the job is not tracked: %v", err)
	}
	if kind != "Sync WordPress files and database: bob.example → http://bob.example/staging" {
		t.Errorf("job kind = %q", kind)
	}
	last := wpLastCopyCall(sa)
	if last < 0 {
		t.Fatal("the agent never got wordpress.copy")
	}
	if mode, _ := sa.param(last, "mode").(string); mode != "sync" {
		t.Errorf("agent got mode %q", mode)
	}
	if files, _ := sa.param(last, "files").(bool); !files {
		t.Error("the agent got files=false")
	}
	if database, _ := sa.param(last, "database").(bool); !database {
		t.Error("the agent got database=false")
	}
	if targetDB, _ := sa.param(last, "target_db").(string); targetDB != "u_bob_wp2" {
		t.Errorf("the agent got target_db %q", targetDB)
	}
	if n := wpAuditCount(t, db, "wordpress.sync"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Sync started. A restore point of the target is saved first.") {
		t.Error("the flash is missing")
	}
}

func TestWordPressToolRestore(t *testing.T) {
	h, sa, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	wpSeedDatabases(t, db)
	bob := loginAsUser(t, h, "bob")

	// A bad point: the guard.
	res := wpToolPost(t, h, bob, url.Values{"tool": {"restore"}, "restore_point": {"nope"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool = %d", res.Code)
	}
	if wpLastCopyCall(sa) != -1 {
		t.Error("the guard still reached the agent")
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Choose a restore point.") {
		t.Error("the guard flash is missing")
	}

	// The point: the job with its kind.
	res = wpToolPost(t, h, bob, url.Values{"tool": {"restore"}, "restore_point": {"20261020T090000Z"}})
	if res.Code != http.StatusSeeOther || res.Header().Get("Location") != "/sites/10/wordpress/1#staging" {
		t.Fatalf("tool = %d %q", res.Code, res.Header().Get("Location"))
	}
	var kind, targetID string
	if err := db.QueryRow(`SELECT kind, target_id FROM agent_jobs WHERE job_id = '0123456789c0a001'`).Scan(&kind, &targetID); err != nil {
		t.Fatalf("the job is not tracked: %v", err)
	}
	if kind != "Restore WordPress: bob.example (20261020T090000Z)" || targetID != "10" {
		t.Errorf("job tracked as %q / %q", kind, targetID)
	}
	last := wpLastCopyCall(sa)
	if last < 0 {
		t.Fatal("the agent never got wordpress.copy")
	}
	if mode, _ := sa.param(last, "mode").(string); mode != "restore" {
		t.Errorf("agent got mode %q", mode)
	}
	if point, _ := sa.param(last, "restore_point").(string); point != "20261020T090000Z" {
		t.Errorf("agent got restore_point %q", point)
	}
	if n := wpAuditCount(t, db, "wordpress.restore"); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	rr = get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Restoring started.") {
		t.Error("the flash is missing")
	}
}

func TestWordPressToolUnknown(t *testing.T) {
	h, _, db := mongoWEnv(t)
	wpSeedInstall(t, db, 10, "public_html", "6.7.1", "6.8")
	bob := loginAsUser(t, h, "bob")

	res := wpToolPost(t, h, bob, url.Values{"tool": {"nope"}})
	if res.Code != http.StatusSeeOther {
		t.Fatalf("tool = %d", res.Code)
	}
	rr := get(t, h, "/sites/10/wordpress/1", bob)
	if !pageHas(rr.Body.String(), "Unknown tool.") {
		t.Error("the unknown-tool flash is missing")
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
