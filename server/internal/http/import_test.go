package http

// This suite drives the website import end to end (ImportController +
// SitesController::undoImport + WebsitesController::show/index): the
// discovery detail page, the import preview and plan, the run (job +
// "importing" status), the hub's refresh (success -> active + database,
// failure -> cleanup), the undo, and the index's "other websites" section.
// The agent is the shared recording fake; its discovery.inventory/adopt.*
// replies mirror the PHP ImportTest fixtures.

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// importEnv seeds an administrator (import.run) and a customer (bob,
// websites.view only) who owns website 10 (bob.example). Returns the
// handler, the login cookie for loginAs, the recording agent and the DB.
func importEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
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
		(1,'administrator','Admin','all',''),(2,'customer','Customer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'import.run','i','web'),(3,'websites.manage','m','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(2,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1)`,
		loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1)`)

	sa, socket, tokenFile := startSitesAgent(t, false)
	h, cookie := importServer(t, db, socket, tokenFile, loginAs)
	return h, cookie, sa, db
}

// importServer builds the handler and signs in loginAs.
func importServer(t *testing.T, db *sql.DB, socket, tokenFile, loginAs string) (http.Handler, *http.Cookie) {
	t.Helper()
	kf := filepath.Join(t.TempDir(), "panel.key")
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
	return h, rr.Result().Cookies()[0]
}

func pageToken(t *testing.T, h http.Handler, cookie *http.Cookie, path string) (string, *http.Cookie) {
	t.Helper()
	rr := get(t, h, path, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
	}
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token on %s", path)
	}
	cookie = refreshCookie(cookie, rr)
	return m[1], cookie
}

func siteIDFromLocation(t *testing.T, rr *httptest.ResponseRecorder) int64 {
	t.Helper()
	loc := rr.Header().Get("Location")
	id, err := strconv.ParseInt(strings.TrimPrefix(loc, "/sites/"), 10, 64)
	if err != nil {
		t.Fatalf("unexpected Location %q", loc)
	}
	return id
}

func rowCount(t *testing.T, db *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func TestImportPreviewAndDetail(t *testing.T) {
	h, cookie, sa, _ := importEnv(t, "admin")

	// The discovery detail page: the read-only website with the import action.
	rr := get(t, h, "/websites/abc123", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Discovered website", "Import into WhaCP", "/websites/abc123/import",
		"www.old.test", "Let's Encrypt", "160 days",
		"php /var/www/old/cron/daily.php",
	} {
		if !pageHas(body, want) {
			t.Errorf("detail missing %q", want)
		}
	}

	// The import preview: the plan and the two configuration blocks.
	rr = get(t, h, "/websites/abc123/import", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", rr.Code, rr.Body.String())
	}
	body = rr.Body.String()
	for _, want := range []string{
		"Import old.test", "PHP 8.3.1", "/var/www/old", // 8.1 is not installed: the newest installed one is planned
		"/etc/nginx/sites-enabled/old", "oldddb", "wp-config.php",
		"server { # whacp }", "server { root /var/www/old; }",
		"No owner (administrators)", "bob", // the owner select (admin)
	} {
		if !pageHas(body, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	if pageHas(body, "cannot run yet") {
		t.Error("a clean preview must not be blocked")
	}

	// The agent got the preview with the source and the website's spec.
	calls := sa.called()
	i := -1
	for j, a := range calls {
		if a == "adopt.preview" {
			i = j
		}
	}
	if i < 0 {
		t.Fatal("adopt.preview was never called")
	}
	if src, _ := sa.param(i, "source").(string); src != "/var/www/old" {
		t.Errorf("adopt.preview source = %q", src)
	}
	if spec, ok := sa.param(i, "spec").(map[string]any); !ok || spec["domain"] != "old.test" {
		t.Errorf("adopt.preview spec = %v", sa.param(i, "spec"))
	}

	// An id that is not in the snapshot is a 404.
	if rr = get(t, h, "/websites/zzz999", cookie); rr.Code != http.StatusNotFound {
		t.Errorf("unknown discovery id = %d, want 404", rr.Code)
	}
}

func TestImportBlockedPreviewCannotRun(t *testing.T) {
	h, cookie, sa, db := importEnv(t, "admin")
	sa.adoptPreviewJSON = `{
	  "domain":"old.test","source":"/var/www/old","target":"/home/whacp/old.test/public_html","user":"u_old",
	  "files":0,"bytes":0,"skipped_links":[],"database":"","database_source":"",
	  "new_config":"","old_config":"","blockers":["The source directory /var/www/old is not readable."],"warnings":[],"steps":[]}`
	rr := get(t, h, "/websites/abc123/import", cookie)
	body := rr.Body.String()
	for _, want := range []string{"cannot run yet", "The source directory /var/www/old is not readable."} {
		if !pageHas(body, want) {
			t.Errorf("blocked preview missing %q:\\n%s", want, body)
		}
	}
	if !pageHas(body, "disabled") {
		t.Error("the import button must be disabled for a blocked preview")
	}

	// Confirming a blocked import flashes the blocker and never runs.
	token, cookie := pageToken(t, h, cookie, "/websites/abc123/import")
	rr = postForm(h, "/websites/abc123/import", url.Values{
		"_token": {token}, "confirm": {"1"}, "system_user": {""}, "php_version": {""},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("blocked run = %d, want 303", rr.Code)
	}
	if rr.Header().Get("Location") != "/websites/abc123/import" {
		t.Errorf("blocked run back = %q", rr.Header().Get("Location"))
	}
	for _, a := range sa.called() {
		if a == "adopt.run" {
			t.Error("a blocked preview must not call adopt.run")
		}
	}
	rr = get(t, h, "/websites/abc123/import", cookie)
	if !pageHas(rr.Body.String(), "The import is blocked.") {
		t.Error("blocked flash missing")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM websites`) != 1 {
		t.Error("a blocked import must not create a website")
	}
}

func TestImportWithoutConfirmDoesNotRun(t *testing.T) {
	h, cookie, sa, _ := importEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/websites/abc123/import")
	rr := postForm(h, "/websites/abc123/import", url.Values{
		"_token": {token}, "system_user": {""}, "php_version": {""},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unconfirmed run = %d, want 303", rr.Code)
	}
	for _, a := range sa.called() {
		if a == "adopt.run" {
			t.Error("an unconfirmed run must not call adopt.run")
		}
	}
}

func TestImportRunRefreshSuccessAndUndo(t *testing.T) {
	h, cookie, sa, db := importEnv(t, "admin")
	// The database overview must list the imported site's database.
	sa.dbOverviewJSON = `{"version":"10.11.6-MariaDB","databases":[{"name":"a_main","size_bytes":10485760,"tables":12},{"name":"oldddb","size_bytes":2097152,"tables":9}]}`
	token, cookie := pageToken(t, h, cookie, "/websites/abc123/import")
	rr := postForm(h, "/websites/abc123/import", url.Values{
		"_token": {token}, "confirm": {"1"}, "system_user": {""}, "php_version": {""},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("run = %d: %s", rr.Code, rr.Body.String())
	}
	id := siteIDFromLocation(t, rr)

	// The new website is "importing" with a running import record.
	var status string
	if err := db.QueryRow(`SELECT status FROM websites WHERE id = ?`, id).Scan(&status); err != nil || status != "importing" {
		t.Fatalf("new website status = %q (%v)", status, err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM websites`) != 2 {
		t.Fatal("the import must create exactly one website")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM website_imports WHERE status = 'running'`) != 1 {
		t.Fatal("running import records = 1")
	}
	run := -1
	for j, a := range sa.called() {
		if a == "adopt.run" {
			run = j
		}
	}
	if run < 0 {
		t.Fatal("adopt.run was never called")
	}
	if src, _ := sa.param(run, "source").(string); src != "/var/www/old" {
		t.Errorf("adopt.run source = %q", src)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'website.import'`) != 1 {
		t.Error("website.import audit missing")
	}

	// The hub shows the running import; the job is still unknown to the agent.
	rr = get(t, h, "/sites/"+strconv.FormatInt(id, 10), cookie)
	body := rr.Body.String()
	for _, want := range []string{"Importing from /var/www/old", "Import started."} {
		if !pageHas(body, want) {
			t.Errorf("hub (running) missing %q:\\n%s", want, body)
		}
	}

	// The job finishes: the hub activates the website and tracks its database.
	var jobID string
	if err := db.QueryRow(`SELECT job_id FROM website_imports`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	sa.setJob(jobID, `{"success":true,"data":{"status":"succeeded","error":""}}`)
	rr = get(t, h, "/sites/"+strconv.FormatInt(id, 10), cookie)
	body = rr.Body.String()
	for _, want := range []string{"Imported from /var/www/old", "Undo import"} {
		if !pageHas(body, want) {
			t.Errorf("hub (done) missing %q:\\n%s", want, body)
		}
	}
	if err := db.QueryRow(`SELECT status FROM websites WHERE id = ?`, id).Scan(&status); err != nil || status != "active" {
		t.Fatalf("status after refresh = %q (%v)", status, err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM databases WHERE name = 'oldddb' AND website_id = ?`, id) != 1 {
		t.Error("the imported database must be tracked")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM website_imports WHERE status = 'succeeded'`) != 1 {
		t.Error("succeeded import records = 1")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'website.import_done'`) != 1 {
		t.Error("website.import_done audit missing")
	}

	// The undo: the original configuration serves again, the copy is removed.
	token, cookie = pageToken(t, h, cookie, "/sites/"+strconv.FormatInt(id, 10))
	rr = postForm(h, "/sites/"+strconv.FormatInt(id, 10)+"/import/undo", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("undo = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	undo := -1
	// sa.param indexes the calls without the databases page's live reads.
	for j, a := range sa.writes() {
		if a == "adopt.undo" {
			undo = j
		}
	}
	if undo < 0 {
		t.Fatal("adopt.undo was never called")
	}
	if src, _ := sa.param(undo, "source").(string); src != "/var/www/old" {
		t.Errorf("adopt.undo source = %q", src)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM websites`) != 1 {
		t.Error("the WhaCP website must be gone after the undo")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM databases WHERE name = 'oldddb'`) != 0 {
		t.Error("the imported database row must be gone after the undo")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM website_imports WHERE status = 'undone'`) != 1 {
		t.Error("undone import records = 1")
	}
	sawDelete := false
	for _, a := range sa.called() {
		if a == "sites.delete" {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Error("the undo must delete the WhaCP website from the server")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'website.import_undo'`) != 1 {
		t.Error("website.import_undo audit missing")
	}
}

func TestImportRunFailedCleansUp(t *testing.T) {
	h, cookie, sa, db := importEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/websites/abc123/import")
	rr := postForm(h, "/websites/abc123/import", url.Values{
		"_token": {token}, "confirm": {"1"}, "system_user": {""}, "php_version": {""},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("run = %d", rr.Code)
	}
	id := siteIDFromLocation(t, rr)
	var jobID string
	if err := db.QueryRow(`SELECT job_id FROM website_imports`).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	sa.setJob(jobID, `{"success":true,"data":{"status":"failed","error":"nginx -t failed: directory /var/www/old does not exist"}}`)

	// The hub's refresh finds the failed job: it redirects with an error and
	// the new website is gone from the panel and the server.
	rr = get(t, h, "/sites/"+strconv.FormatInt(id, 10), cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("hub after failed import = %d loc %q, want 303 /websites", rr.Code, rr.Header().Get("Location"))
	}
	rr = get(t, h, "/websites", cookie)
	if !pageHas(rr.Body.String(), "The import of old.test failed") {
		t.Errorf("failed-import flash missing:\\n%s", rr.Body.String())
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM websites`) != 1 {
		t.Error("the WhaCP website must be gone after a failed import")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM website_imports WHERE status = 'failed'`) != 1 {
		t.Error("failed import records = 1")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM agent_jobs WHERE job_id = ?`, jobID) != 1 {
		t.Error("the import job must be tracked (agent_jobs)")
	}
	sawDelete := false
	for _, a := range sa.called() {
		if a == "sites.delete" {
			sawDelete = true
		}
	}
	if !sawDelete {
		t.Error("the failed import must delete the new website from the server")
	}
}

func TestImportForbiddenForCustomer(t *testing.T) {
	h, cookie, _, db := importEnv(t, "bob")

	// The detail page is viewable (websites.view) but has no import action.
	rr := get(t, h, "/websites/abc123", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("customer detail = %d", rr.Code)
	}
	if pageHas(rr.Body.String(), "Import into WhaCP") {
		t.Error("a customer without import.run must not see the import action")
	}
	// The import routes are 403.
	if rr = get(t, h, "/websites/abc123/import", cookie); rr.Code != http.StatusForbidden {
		t.Errorf("customer preview = %d, want 403", rr.Code)
	}
	token, cookie := pageToken(t, h, cookie, "/websites")
	rr = postForm(h, "/websites/abc123/import", url.Values{"_token": {token}, "confirm": {"1"}}, cookie)
	if rr.Code != http.StatusForbidden {
		t.Errorf("customer run = %d, want 403", rr.Code)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM websites`) != 1 {
		t.Error("the customer must not create websites")
	}
}

func TestWebsitesIndexOtherWebsites(t *testing.T) {
	h, cookie, _, _ := importEnv(t, "admin")
	rr := get(t, h, "/websites", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("index = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Other websites on this server (read-only)", "old.test", "www.old.test",
		"Import", "/websites/abc123/import", "1 catch-all",
	} {
		if !pageHas(body, want) {
			t.Errorf("index missing %q", want)
		}
	}
	if !pageHas(body, "bob.example") {
		t.Error("the managed website must still be listed")
	}
	if pageHas(body, "managed.test") {
		t.Error("a WhaCP-managed website must not appear under the other websites")
	}

	// A customer without import.run sees the section but no Import links.
	h2, c2, _, _ := importEnv(t, "bob")
	body = get(t, h2, "/websites", c2).Body.String()
	if !pageHas(body, "Other websites on this server (read-only)") || !pageHas(body, "old.test") {
		t.Error("the customer must still see the other websites")
	}
	if pageHas(body, `/websites/abc123/import`) {
		t.Error("the customer without import.run must not see Import links")
	}
}

func TestWebsiteShowUnknownDoesNotLeak(t *testing.T) {
	h, cookie, _, _ := importEnv(t, "bob")
	rr := get(t, h, "/websites/10", cookie)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("numeric (managed) id on the discovery page = %d, want 404", rr.Code)
	}
	if pageHas(rr.Body.String(), "bob.example") {
		t.Fatal("TENANT LEAK on the discovery detail page")
	}
}

func TestWebsiteShowAgentDown(t *testing.T) {
	h, cookie, sa, _ := importEnv(t, "admin")
	sa.failActions["discovery.inventory"] = "The agent is not running."
	rr := get(t, h, "/websites/abc123", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("agent-down detail = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "The agent is not running.") {
		t.Error("the agent error must be shown")
	}
	rr = get(t, h, "/websites", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("agent-down index = %d", rr.Code)
	}
	if pageHas(rr.Body.String(), "Other websites on this server") {
		t.Error("without an agent the other-websites section must be absent")
	}
}

// seedDiscoverySnapshot stores a fresh inventory snapshot directly (no agent
// call), so pages can read the discovery data deterministically.
func seedDiscoverySnapshot(t *testing.T, db *sql.DB, data string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO discovery_snapshots (server_id, created_at, created_by, website_count, checksum, data)
		VALUES (1, ?, NULL, 1, 'test', ?)`, time.Now().UTC().Format(time.RFC3339), data); err != nil {
		t.Fatal(err)
	}
}
