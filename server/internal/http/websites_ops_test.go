package http

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// This suite drives the advanced website operations end to end: PHP reload,
// re-apply, directory listing, file permissions (JSON check + repair), the
// shared Node.js/Python runtime page and its update, and the app Worker that
// a new Node.js/Python site provisions. Flashes live in the sealed session
// cookie, so every POST response's cookie is refreshed before the page that
// asserts the flash is fetched.

// refreshCookie takes the freshly sealed session cookie off a response.
func refreshCookie(cookie *http.Cookie, rr *httptest.ResponseRecorder) *http.Cookie {
	if cookie == nil {
		if cs := rr.Result().Cookies(); len(cs) > 0 {
			return cs[0]
		}
		return nil
	}
	// Update the caller's cookie in place (a browser jar) and hand the same
	// pointer back, so every holder of it sees the new session.
	adoptCookie(cookie, rr)
	return cookie
}

// flashAt fetches a page and asserts the flashed message is on it.
func flashAt(t *testing.T, h http.Handler, cookie *http.Cookie, path, want string) string {
	t.Helper()
	rr := get(t, h, path, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
	}
	// The page is HTML: html/template escapes quotes, apostrophes, & < > and + in
	// text, so the flash reads the same once decoded.
	if body := rr.Body.String(); !pageHas(body, want) && !pageHas(body, htmlTextEscape(want)) {
		t.Errorf("flash %q not on %s", want, path)
	}
	return rr.Body.String()
}

// seedAppSite inserts a Node.js app site (kind proxy + app_runtime) for the
// given owner (1 = bob).
func seedAppSite(t *testing.T, db *sql.DB, id, owner int64) {
	t.Helper()
	dom := "app" + strconv.FormatInt(id, 10) + ".example"
	if _, err := db.Exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,app_runtime,proxy_pass,status,created_at,updated_at)
		VALUES (?,1,?,?,'u_app','/home/whacp/app','/home/whacp/app/public_html','proxy','','node:20.1.0','http://127.0.0.1:3000','active','','')`,
		id, owner, dom); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,?,?,1)`, id, dom); err != nil {
		t.Fatal(err)
	}
}

// seedAppWorker inserts the site's "app" Worker.
func seedAppWorker(t *testing.T, db *sql.DB, websiteID int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO workers (server_id,website_id,slug,name,directory,command,env,restart,memory_mb,procs,runtime,enabled,created_at,updated_at)
		VALUES (1,?,'app-worker','app','','node app.js','{"PORT":"3000"}','always',0,1,'node:20.1.0',1,'','')`, websiteID); err != nil {
		t.Fatal(err)
	}
}

// seedNodejsPlan enables the Node.js site type (a default-off feature) for
// bob's plan so app sites can be created.
func seedNodejsPlan(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES (7,'nodejs-ok','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO feature_list_enabled (list_id,feature) VALUES (7,'type_nodejs')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE plans SET feature_list_id = 7 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
}

func auditResult(t *testing.T, db *sql.DB, action string) string {
	t.Helper()
	var result string
	err := db.QueryRow(`SELECT result FROM audit_logs WHERE action = ? ORDER BY id DESC LIMIT 1`, action).Scan(&result)
	if err != nil {
		t.Fatalf("no %s audit row: %v", action, err)
	}
	return result
}

func TestSiteRestartPhp(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10")
	rr := postForm(h, "/sites/10/restart-php", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10" {
		t.Fatalf("restart = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	i := lastActionIndex(sa.called(), "services.control")
	if i < 0 {
		t.Fatalf("services.control not called: %v", sa.called())
	}
	// The fake discovery reports fpm_binary only, so the default unit name
	// (php<version>-fpm.service) is used.
	if sa.param(i, "unit") != "php8.3.1-fpm.service" || sa.param(i, "op") != "reload" {
		t.Errorf("services.control = %v %v", sa.param(i, "unit"), sa.param(i, "op"))
	}
	if got := auditResult(t, db, "website.restart_php"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10", "PHP 8.3.1 was reloaded.")
}

func TestSiteRestartPhpNoPhp(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	seedAppSite(t, db, 30, 1) // proxy, no PHP
	token, cookie := siteFormToken(t, h, cookie, "/sites/30")
	rr := postForm(h, "/sites/30/restart-php", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("restart = %d", rr.Code)
	}
	if i := lastActionIndex(sa.called(), "services.control"); i >= 0 {
		t.Errorf("services.control must not be called for a non-PHP site: %v", sa.called())
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/30", "This website does not use PHP.")
}

func TestSiteReapply(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10")
	before := lastActionIndex(sa.called(), "sites.apply")
	rr := postForm(h, "/sites/10/reapply", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("reapply = %d", rr.Code)
	}
	if i := lastActionIndex(sa.called(), "sites.apply"); i <= before {
		t.Errorf("sites.apply not re-issued: %v", sa.called())
	}
	if got := auditResult(t, db, "website.reapply"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10", "Configuration re-applied from the panel.")
}

func TestSiteDirectoryListing(t *testing.T) {
	h, cookie, _, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10")
	rr := postForm(h, "/sites/10/directory-listing", url.Values{"_token": {token}, "enabled": {"1"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("listing on = %d", rr.Code)
	}
	var on int
	if err := db.QueryRow(`SELECT directory_listing FROM websites WHERE id = 10`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 1 {
		t.Errorf("directory_listing = %d, want 1", on)
	}
	if got := auditResult(t, db, "website.listing_on"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10", "Directory listing is on")

	// Toggle back off.
	token2, cookie2 := siteFormToken(t, h, cookie, "/sites/10")
	rr = postForm(h, "/sites/10/directory-listing", url.Values{"_token": {token2}, "enabled": {"0"}}, cookie2)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("listing off = %d", rr.Code)
	}
	if got := auditResult(t, db, "website.listing_off"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie2, rr), "/sites/10", "Directory listing is off.")

	// A proxy site refuses the toggle.
	seedAppSite(t, db, 30, 1)
	token3, cookie3 := siteFormToken(t, h, cookie, "/sites/30")
	rr = postForm(h, "/sites/30/directory-listing", url.Values{"_token": {token3}, "enabled": {"1"}}, cookie3)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("proxy listing = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(cookie3, rr), "/sites/30", "directory listing does not apply")
}

func TestSiteCheckPermissions(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	// JSON report for an owned site.
	rr := get(t, h, "/sites/10/permissions", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("check = %d: %s", rr.Code, rr.Body.String())
	}
	var rep struct {
		Checked      int `json:"checked"`
		Fixed        int `json:"fixed"`
		SkippedCount int `json:"skipped_count"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&rep); err != nil {
		t.Fatal(err)
	}
	if rep.Checked != 120 || rep.Fixed != 3 || rep.SkippedCount != 2 {
		t.Errorf("report = %+v", rep)
	}
	// A missing site is a 404 JSON answer.
	rr = get(t, h, "/sites/999/permissions", cookie)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("missing = %d: %s", rr.Code, rr.Body.String())
	}
	var missing map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&missing); err != nil {
		t.Fatal(err)
	}
	if missing["error"] != "Website not found." {
		t.Errorf("missing error = %v", missing["error"])
	}
}

func TestSiteCheckPermissionsAgentFailure(t *testing.T) {
	h, cookie, sa, _ := sitesEnv(t, false, false)
	sa.failActions = map[string]string{"sites.permissions": "the agent refused"}
	rr := get(t, h, "/sites/10/permissions", cookie)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("agent failure = %d, want 502: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stringOf(body["error"]), "the agent refused") {
		t.Errorf("error = %v", body["error"])
	}
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func TestSiteRepairPermissions(t *testing.T) {
	h, cookie, _, db := sitesEnv(t, false, false)
	token, cookie := siteFormToken(t, h, cookie, "/sites/10")
	rr := postForm(h, "/sites/10/permissions", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("repair = %d", rr.Code)
	}
	if got := auditResult(t, db, "website.permissions_repair"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10", "Checked 120 file(s) and folder(s); repaired 3.")
}

func TestSiteRuntimePage(t *testing.T) {
	h, cookie, _, db := sitesEnv(t, false, false)
	seedAppSite(t, db, 30, 1)
	seedAppWorker(t, db, 30)
	rr := get(t, h, "/sites/30/nodejs", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("runtime page = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{`name="app_runtime"`, "Node.js 20.1.0", "Node.js 22.0.0", "node app.js", "Edit command, env &amp; restart"} {
		if !pageHas(body, want) {
			t.Errorf("runtime page missing %q", want)
		}
	}
	// The stored runtime (node:20.1.0) is the selected option.
	if !pageHas(body, `value="node:20.1.0" selected`) {
		t.Errorf("current runtime not selected: %s", body)
	}
}

func TestSiteRuntimeUpdate(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	seedAppSite(t, db, 30, 1)
	seedAppWorker(t, db, 30)
	token, cookie := siteFormToken(t, h, cookie, "/sites/30/nodejs")
	rr := postForm(h, "/sites/30/nodejs", url.Values{"_token": {token}, "app_runtime": {"node:22.0.0"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/30/nodejs" {
		t.Fatalf("update = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	var rt string
	if err := db.QueryRow(`SELECT app_runtime FROM websites WHERE id = 30`).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	if rt != "node:22.0.0" {
		t.Errorf("app_runtime = %q", rt)
	}
	// The app Worker is re-applied on the new runtime.
	i := lastActionIndex(sa.called(), "workers.apply")
	if i < 0 {
		t.Fatalf("workers.apply not called: %v", sa.called())
	}
	if sa.param(i, "runtime") != "node:22.0.0" {
		t.Errorf("worker runtime = %v", sa.param(i, "runtime"))
	}
	var wrt string
	if err := db.QueryRow(`SELECT runtime FROM workers WHERE website_id = 30`).Scan(&wrt); err != nil {
		t.Fatal(err)
	}
	if wrt != "node:22.0.0" {
		t.Errorf("worker row runtime = %q", wrt)
	}
	if got := auditResult(t, db, "website.runtime"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/30/nodejs", "Runtime set to node:22.0.0.")
}

func TestSiteRuntimeUpdateValidation(t *testing.T) {
	h, cookie, _, db := sitesEnv(t, false, false)
	seedAppSite(t, db, 30, 1)
	token, cookie := siteFormToken(t, h, cookie, "/sites/30/nodejs")
	// A Python runtime on a Node.js site is rejected and re-rendered with
	// the submitted value selected and the DB untouched.
	rr := postForm(h, "/sites/30/nodejs", url.Values{"_token": {token}, "app_runtime": {"python:3.12"}}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("validation re-render = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Choose a Node.js") {
		t.Errorf("field error not rendered")
	}
	if !pageHas(rr.Body.String(), `value="python:3.12" selected`) {
		t.Errorf("old input not preserved as selected")
	}
	var rt string
	if err := db.QueryRow(`SELECT app_runtime FROM websites WHERE id = 30`).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	if rt != "node:20.1.0" {
		t.Errorf("app_runtime changed on a rejected update: %q", rt)
	}
}

func TestSiteRuntimeFeatureGate(t *testing.T) {
	h, cookie, _, db := sitesEnv(t, false, false)
	seedAppSite(t, db, 30, 1)
	// Disable the nodejs feature for bob's plan.
	if _, err := db.Exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES (8,'no-nodejs','','')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO feature_list_disabled (list_id,feature) VALUES (8,'nodejs')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE plans SET feature_list_id = 8 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	ck := refreshCookie(cookie, get(t, h, "/websites", cookie))
	if rr := get(t, h, "/sites/30/nodejs", ck); rr.Code != http.StatusForbidden {
		t.Errorf("nodejs = %d, want 403", rr.Code)
	}
	if rr := get(t, h, "/sites/30/python", ck); rr.Code != http.StatusOK {
		t.Errorf("python = %d, want 200", rr.Code)
	}
}

func TestSiteStoreProvisionsApp(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	seedNodejsPlan(t, db)
	token, cookie := siteFormToken(t, h, cookie, "/websites/new")
	rr := postForm(h, "/websites/new", url.Values{
		"_token": {token}, "domain": {"app.example"}, "kind": {"nodejs"},
		"app_runtime": {"node:20.1.0"}, "app_port": {"3000"}, "app_command": {"node app.js"},
		"system_user": {"u_app"}, "new_system_user": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("store = %d: %s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, "/sites/") || !strings.HasSuffix(loc, "/nodejs") {
		t.Fatalf("app store must land on the runtime page, got %q", loc)
	}
	// The app Worker was provisioned.
	i := lastActionIndex(sa.called(), "workers.apply")
	if i < 0 {
		t.Fatalf("workers.apply not called: %v", sa.called())
	}
	if sa.param(i, "command") != "node app.js" || sa.param(i, "runtime") != "node:20.1.0" {
		t.Errorf("worker apply = %v %v", sa.param(i, "command"), sa.param(i, "runtime"))
	}
	flashAt(t, h, refreshCookie(cookie, rr), loc, "Website created. Set up your app below.")
}

func TestSiteRuntimeMissingSite(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	ck := refreshCookie(cookie, get(t, h, "/websites", cookie))
	// GET 404s.
	if rr := get(t, h, "/sites/999/nodejs", ck); rr.Code != http.StatusNotFound {
		t.Errorf("GET missing = %d, want 404", rr.Code)
	}
	// The update redirects to the list with a flash (PHP act() behavior).
	rr := postForm(h, "/sites/999/nodejs", url.Values{"_token": {"x"}, "app_runtime": {"node:22.0.0"}}, ck)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("POST missing = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(ck, rr), "/websites", "Website not found.")
}

func TestSiteOpsRequireCSRF(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	for _, path := range []string{"/sites/10/restart-php", "/sites/10/reapply", "/sites/10/directory-listing", "/sites/10/permissions"} {
		if rr := postForm(h, path, url.Values{"enabled": {"1"}}, cookie); rr.Code != http.StatusBadRequest {
			t.Errorf("POST %s without CSRF = %d, want 400", path, rr.Code)
		}
	}
}

// lastActionIndex finds the most recent agent action (or -1).
func lastActionIndex(actions []string, name string) int {
	for i := len(actions) - 1; i >= 0; i-- {
		if actions[i] == name {
			return i
		}
	}
	return -1
}
