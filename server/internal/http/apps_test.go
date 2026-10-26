package http

// Tests for the one-click application installer (AppsController):
// GET /sites/{id}/apps (the catalog page + installations history) and
// POST /sites/{id}/apps/{app} (install onto the website or a subdomain),
// both gated by apps.install.

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// containsAction reports whether the recorded agent actions include a.
func containsAction(acts []string, a string) bool {
	for _, x := range acts {
		if x == a {
			return true
		}
	}
	return false
}

// agentParams returns the last recorded parameter map of an agent action.
func agentParams(t *testing.T, sa *sitesAgent, action string) map[string]any {
	t.Helper()
	sa.mu.Lock()
	defer sa.mu.Unlock()
	for i := len(sa.actions) - 1; i >= 0; i-- {
		if sa.actions[i] == action {
			return sa.params[i]
		}
	}
	t.Fatalf("agent action %s never called; got %v", action, sa.actions)
	return nil
}

// appsEnv seeds a customer (bob) with a PHP site + a PHP subdomain (an
// install target) + a non-PHP site, a customer without apps.install
// (carol), and an admin. The site's root is /home/whacp/bob.example with
// the document root one level down (public_html).
func appsEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
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
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'administrator','Admin','all',''),(2,'customer','Customer','own',''),(3,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','View sites','Hosting'),(2,'apps.install','Install applications','Hosting')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(2,1),(2,2),(3,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,max_databases,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,5,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,3,1,'2026-01-01T00:00:00Z','','',1)`, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,parent_id,created_at,updated_at) VALUES
		(10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active',NULL,'',''),
		(11,1,2,'www.bob.example','u_bob','/home/whacp/www.bob.example','/home/whacp/www.bob.example/public_html','php','8.3.1','active',10,'',''),
		(12,1,2,'node.bob.example','u_bob','/home/whacp/node.bob.example','/home/whacp/node.bob.example','nodejs','','active',NULL,'','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES
		(1,10,'bob.example',1),(1,11,'www.bob.example',1),(1,12,'node.bob.example',1)`)

	sa, socket, tokenFile := startSitesAgent(t, false)
	kf := t.TempDir() + "/panel.key"
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	c, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {loginAs}, "password": {loginPassword}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d", loginAs, rr.Code)
	}
	return h, rr.Result().Cookies()[0], sa, db
}

func TestAppsPageCatalog(t *testing.T) {
	h, cookie, _, _ := appsEnv(t, "bob")
	body := get(t, h, "/sites/10/apps", cookie).Body.String()
	for _, want := range []string{"Applications", "WordPress", "Grav", "Install"} {
		if !pageHas(body, want) {
			t.Fatalf("apps page missing %q:\\n%s", want, body)
		}
	}
	// The subdomain is a PHP site: it is an enabled install target.
	if !pageHas(body, "www.bob.example") || !pageHas(body, "subdomain") {
		t.Fatalf("apps page missing the subdomain target:\\n%s", body)
	}
}

func TestAppsInstallWordpress(t *testing.T) {
	h, cookie, sa, db := appsEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/apps")
	rr := postForm(h, "/sites/10/apps/wordpress", url.Values{
		"_token": {token}, "title": {"Bob's blog"}, "admin_user": {"admin"}, "admin_email": {"bob@x.test"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/apps" {
		t.Fatalf("install wordpress = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	acts := sa.called()
	for _, want := range []string{"db.create", "db.user_create", "db.grant", "apps.install"} {
		if !containsAction(acts, want) {
			t.Fatalf("agent actions %v missing %q", acts, want)
		}
	}
	p := agentParams(t, sa, "apps.install")
	if p["app"] != "wordpress" || p["path"] != "public_html" || p["php_version"] != "8.3.1" ||
		p["url"] != "http://bob.example" || p["title"] != "Bob's blog" || p["admin_user"] != "admin" || p["admin_email"] != "bob@x.test" {
		t.Fatalf("apps.install params = %v", p)
	}
	pw, _ := p["admin_password"].(string)
	if len(pw) != 20 {
		t.Fatalf("admin_password len = %d, want 20", len(pw))
	}
	dbp, _ := p["db"].(map[string]any)
	if dbp == nil || dbp["name"] != "bob_bob_wp" || dbp["user"] != "bob_bob_wp" || dbp["host"] != "localhost" {
		t.Fatalf("apps.install db params = %v", dbp)
	}
	// The database + user exist, granted, and the install row is running.
	if n := rowCount(t, db, `SELECT COUNT(*) FROM databases WHERE name = 'bob_bob_wp'`); n != 1 {
		t.Fatalf("databases rows = %d", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM database_users WHERE username = 'bob_bob_wp'`); n != 1 {
		t.Fatalf("database_users rows = %d", n)
	}
	var appRowID, status string
	if err := db.QueryRow(`SELECT id, status FROM app_installs WHERE website_id = 10 AND app = 'wordpress'`).Scan(&appRowID, &status); err != nil {
		t.Fatalf("app_installs row: %v", err)
	}
	if status != "running" {
		t.Fatalf("install status = %q", status)
	}
	var storedDbID, jobID, adminUser, path string
	if err := db.QueryRow(`SELECT database_id, job_id, admin_user, path FROM app_installs WHERE id = ?`, appRowID).Scan(&storedDbID, &jobID, &adminUser, &path); err != nil {
		t.Fatalf("app_installs details: %v", err)
	}
	if jobID != "feedfacefeed1a01" || adminUser != "admin" || path != "public_html" {
		t.Fatalf("app_installs job=%q admin=%q path=%q", jobID, adminUser, path)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM agent_jobs WHERE job_id = 'feedfacefeed1a01' AND target_type = 'website' AND target_id = '10' AND kind = 'Install WordPress'`); n != 1 {
		t.Fatalf("agent_jobs tracked rows = %d", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'app.install' AND target_type = 'website' AND target_id = '10'`); n != 1 {
		t.Fatalf("audit app.install rows = %d", n)
	}
	// The agent still knows the job while it runs: the next page view
	// keeps the row running ("activating").
	sa.jobs["feedfacefeed1a01"] = `{"success":true,"data":{"status":"running","error":""}}`
	// The page shows the once-only credentials and the activating row.
	body := get(t, h, "/sites/10/apps", refreshCookie(cookie, rr)).Body.String()
	// html/template escapes the generated password (+, &, ' ...) in the page.
	for _, want := range []string{"WordPress is being installed.", "WordPress administrator", htmlTextEscape(pw), "activating"} {
		if !pageHas(body, want) {
			t.Fatalf("post-install page missing %q:\\n%s", want, body)
		}
	}
}

func TestAppsInstallStagedRefreshAndDocroot(t *testing.T) {
	h, cookie, sa, db := appsEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/apps")
	rr := postForm(h, "/sites/10/apps/opencart", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install opencart = %d", rr.Code)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM app_installs WHERE app = 'opencart' AND status = 'running'`); n != 1 {
		t.Fatalf("running opencart installs = %d", n)
	}
	// The agent finishes the job; the next page view settles the row and
	// points the document root at <path>/upload (OpenCart's web root).
	sa.jobs["feedfacefeed1a01"] = `{"success":true,"data":{"status":"succeeded","error":""}}`
	body := get(t, h, "/sites/10/apps", refreshCookie(cookie, rr)).Body.String()
	if n := rowCount(t, db, `SELECT COUNT(*) FROM app_installs WHERE app = 'opencart' AND status = 'succeeded'`); n != 1 {
		t.Fatalf("succeeded opencart installs = %d", n)
	}
	var docroot string
	if err := db.QueryRow(`SELECT document_root FROM websites WHERE id = 10`).Scan(&docroot); err != nil {
		t.Fatal(err)
	}
	if docroot != "/home/whacp/bob.example/public_html/upload" {
		t.Fatalf("document_root = %q", docroot)
	}
	if !pageHas(body, "http://bob.example/install/") || !pageHas(body, "Finish setup") {
		t.Fatalf("page missing the finish-setup link:\\n%s", body)
	}
}

func TestAppsRefreshUnknown(t *testing.T) {
	h, cookie, sa, db := appsEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/apps")
	rr := postForm(h, "/sites/10/apps/wordpress", url.Values{
		"_token": {token}, "title": {"T"}, "admin_user": {"admin"}, "admin_email": {"bob@x.test"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install = %d", rr.Code)
	}
	// No jobs.get reply registered: the agent answers not_found, so the
	// row settles to 'unknown'.
	_ = sa
	get(t, h, "/sites/10/apps", refreshCookie(cookie, rr))
	if n := rowCount(t, db, `SELECT COUNT(*) FROM app_installs WHERE status = 'unknown'`); n != 1 {
		t.Fatalf("unknown installs = %d", n)
	}
}

func TestAppsInstallValidation(t *testing.T) {
	h, cookie, sa, db := appsEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/apps")
	rr := postForm(h, "/sites/10/apps/wordpress", url.Values{
		"_token": {token}, "title": {"T"}, "admin_user": {"admin"}, "admin_email": {""},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install = %d", rr.Code)
	}
	if acts := sa.called(); len(acts) != 0 {
		t.Fatalf("agent actions on validation failure = %v", acts)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM app_installs`); n != 0 {
		t.Fatalf("installs created on validation failure = %d", n)
	}
	body := get(t, h, "/sites/10/apps", refreshCookie(cookie, rr)).Body.String()
	if !pageHas(body, "Enter the administrator&#39;s email address.") {
		t.Fatalf("page missing the field error:\\n%s", body)
	}
}

func TestAppsInstallNotPHP(t *testing.T) {
	h, cookie, sa, db := appsEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/12/apps")
	rr := postForm(h, "/sites/12/apps/wordpress", url.Values{
		"_token": {token}, "title": {"T"}, "admin_user": {"admin"}, "admin_email": {"bob@x.test"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install = %d", rr.Code)
	}
	if acts := sa.called(); len(acts) != 0 {
		t.Fatalf("agent actions on a non-PHP site = %v", acts)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM app_installs`); n != 0 {
		t.Fatalf("installs on a non-PHP site = %d", n)
	}
	body := get(t, h, "/sites/12/apps", refreshCookie(cookie, rr)).Body.String()
	if !pageHas(body, "WordPress needs a PHP website.") {
		t.Fatalf("page missing the PHP warning:\\n%s", body)
	}
	if !pageHas(body, "Applications need a PHP website") {
		t.Fatalf("page missing the non-PHP notice:\\n%s", body)
	}
}

func TestAppsInstallAgentFailureRollsBackDatabase(t *testing.T) {
	h, cookie, sa, db := appsEnv(t, "bob")
	sa.failActions = map[string]string{"apps.install": "no composer on the server"}
	token, cookie := pageToken(t, h, cookie, "/sites/10/apps")
	rr := postForm(h, "/sites/10/apps/wordpress", url.Values{
		"_token": {token}, "title": {"T"}, "admin_user": {"admin"}, "admin_email": {"bob@x.test"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install = %d", rr.Code)
	}
	acts := sa.called()
	if !containsAction(acts, "db.create") || !containsAction(acts, "apps.install") || !containsAction(acts, "db.drop") || !containsAction(acts, "db.user_drop") {
		t.Fatalf("agent actions = %v (want db.create, apps.install, db.drop, db.user_drop)", acts)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM app_installs`); n != 0 {
		t.Fatalf("install rows after failure = %d", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM databases`); n != 0 {
		t.Fatalf("databases left after rollback = %d", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM database_users`); n != 0 {
		t.Fatalf("database users left after rollback = %d", n)
	}
	body := get(t, h, "/sites/10/apps", refreshCookie(cookie, rr)).Body.String()
	if !pageHas(body, "The application could not be installed.") {
		t.Fatalf("page missing the failure message:\\n%s", body)
	}
}

func TestAppsInstallTargetSubdomain(t *testing.T) {
	h, cookie, sa, db := appsEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/apps")
	rr := postForm(h, "/sites/10/apps/wordpress", url.Values{
		"_token": {token}, "target_id": {"11"}, "title": {"T"}, "admin_user": {"admin"}, "admin_email": {"bob@x.test"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/11/apps" {
		t.Fatalf("install on subdomain = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if p := agentParams(t, sa, "apps.install"); p["domain"] != "www.bob.example" {
		t.Fatalf("apps.install domain = %v", p["domain"])
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM app_installs WHERE website_id = 11`); n != 1 {
		t.Fatalf("installs on the subdomain = %d", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM databases WHERE website_id = 11`); n != 1 {
		t.Fatalf("databases attached to the subdomain = %d", n)
	}
}

func TestAppsInstallTargetOutsideTheFamily(t *testing.T) {
	h, cookie, sa, db := appsEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/apps")
	rr := postForm(h, "/sites/10/apps/wordpress", url.Values{
		"_token": {token}, "target_id": {"999"}, "title": {"T"}, "admin_user": {"admin"}, "admin_email": {"bob@x.test"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/apps" {
		t.Fatalf("install on a foreign target = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if acts := sa.called(); len(acts) != 0 {
		t.Fatalf("agent actions on a foreign target = %v", acts)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM app_installs`); n != 0 {
		t.Fatalf("installs on a foreign target = %d", n)
	}
	body := get(t, h, "/sites/10/apps", refreshCookie(cookie, rr)).Body.String()
	if !pageHas(body, "Choose this website or one of its subdomains.") {
		t.Fatalf("page missing the target error:\\n%s", body)
	}
}

func TestAppsInstallerDisabled(t *testing.T) {
	h, cookie, _, db := appsEnv(t, "bob")
	if _, err := db.Exec(`INSERT INTO settings (key,value,updated_at) VALUES ('apps_installer_enabled','0','')`); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, "/sites/10/apps", cookie).Body.String()
	if !pageHas(body, "The application installer is turned off") {
		t.Fatalf("page missing the disabled notice:\\n%s", body)
	}
	// And the install is refused too.
	token, cookie := pageToken(t, h, cookie, "/sites/10/apps")
	rr := postForm(h, "/sites/10/apps/wordpress", url.Values{
		"_token": {token}, "title": {"T"}, "admin_user": {"admin"}, "admin_email": {"bob@x.test"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install = %d", rr.Code)
	}
	body = get(t, h, "/sites/10/apps", refreshCookie(cookie, rr)).Body.String()
	if !pageHas(body, "This application is not available for installation.") {
		t.Fatalf("page missing the availability error:\\n%s", body)
	}
}

func TestAppsForbidden(t *testing.T) {
	h, cookie, _, _ := appsEnv(t, "carol") // role without apps.install
	if rr := get(t, h, "/sites/10/apps", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("carol GET apps = %d, want 403", rr.Code)
	}
	// Unauthenticated: to the login page.
	rr := get(t, h, "/sites/10/apps", nil)
	if rr.Code != http.StatusFound && rr.Code != http.StatusSeeOther {
		t.Fatalf("anonymous GET apps = %d, want redirect", rr.Code)
	}
}

// htmlTextEscape is how html/template renders text: & < > " ' and + become
// entities (generated passwords contain + and others).
func htmlTextEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&#34;", "'", "&#39;", "+", "&#43;").Replace(s)
}
