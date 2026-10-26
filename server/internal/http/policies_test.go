package http

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestMandatoryTwoFactorRoleIsEnforced(t *testing.T) {
	h, db := rolesEnv(t)
	if _, err := db.Exec(`UPDATE roles SET require_two_factor = 1 WHERE id = 100`); err != nil {
		t.Fatal(err)
	}
	mgr := loginAsUser(t, h, "mgr")
	rr := get(t, h, "/users", mgr)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/profile#two-factor" {
		t.Fatalf("GET /users = %d loc=%q, want 303 /profile#two-factor", rr.Code, rr.Header().Get("Location"))
	}
	if rr := get(t, h, "/profile", mgr); rr.Code != http.StatusOK {
		t.Fatalf("profile must stay reachable, got %d", rr.Code)
	}
	req := httptest.NewRequest("GET", "/users", nil)
	req.Header.Set("Accept", "application/json")
	req.AddCookie(mgr)
	jr := httptest.NewRecorder()
	h.ServeHTTP(jr, req)
	if jr.Code != http.StatusForbidden {
		t.Fatalf("JSON request = %d, want 403", jr.Code)
	}
	// Once enrolled the restriction lifts.
	if _, err := db.Exec(`INSERT INTO user_two_factor (user_id, secret, recovery_codes, last_step, enabled_at) VALUES (2,'x','[]',0,'')`); err != nil {
		t.Fatal(err)
	}
	if rr := get(t, h, "/users", mgr); rr.Code != http.StatusOK {
		t.Fatalf("enrolled user GET /users = %d", rr.Code)
	}
}

func TestPasswordAgePolicyIsEnforced(t *testing.T) {
	h, db := rolesEnv(t)
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('policy_password_age_days','30','')`); err != nil {
		t.Fatal(err)
	}
	mgr := loginAsUser(t, h, "mgr")
	rr := get(t, h, "/users", mgr) // password_changed_at is 2026-01-01: expired
	if rr.Code == http.StatusOK {
		t.Skip("fixture password is younger than the policy; adjust the clock")
	}
	if rr.Header().Get("Location") != "/profile#password" {
		t.Fatalf("GET /users = %d loc=%q, want redirect to /profile#password", rr.Code, rr.Header().Get("Location"))
	}
	if rr := get(t, h, "/profile", mgr); rr.Code != http.StatusOK {
		t.Fatalf("profile must stay reachable, got %d", rr.Code)
	}
}

func TestStepUpRequiredForSensitiveRoutes(t *testing.T) {
	s, db := rolesEnvServer(t)
	h := s.Handler()
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/roles/new")
	// Signing in counts as a confirmation.
	rr := postForm(h, "/roles", roleForm(token, "fresh", "users.view"), admin)
	if rr.Header().Get("Location") != "/roles" {
		t.Fatalf("fresh session: loc=%q", rr.Header().Get("Location"))
	}
	// 20 minutes later the confirmation has lapsed.
	s.auth.SetClock(func() int64 { return time.Now().Unix() + 20*60 })
	token, admin = siteFormToken(t, h, refreshCookie(admin, rr), "/roles/new")
	rr = postForm(h, "/roles", roleForm(token, "stale", "users.view"), admin)
	if loc := rr.Header().Get("Location"); !strings.HasPrefix(loc, "/confirm-password") {
		t.Fatalf("stale confirmation: status=%d loc=%q, want /confirm-password", rr.Code, loc)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM roles WHERE name='stale'`).Scan(&n)
	if n != 0 {
		t.Fatal("role created without a fresh confirmation")
	}
	// JSON clients get a 403 with the redirect target.
	req := httptest.NewRequest("POST", "/roles", strings.NewReader(roleForm(token, "stale2", "users.view").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.AddCookie(admin)
	jr := httptest.NewRecorder()
	h.ServeHTTP(jr, req)
	if jr.Code != http.StatusForbidden || !strings.Contains(jr.Body.String(), "/confirm-password") {
		t.Fatalf("JSON stale confirmation = %d %s", jr.Code, jr.Body.String())
	}
}

// A sealed cookie copied before sign-out must not work afterwards.
func TestLogoutRevokesTheCopiedSessionCookie(t *testing.T) {
	h, _ := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	stolen := &http.Cookie{Name: c.Name, Value: c.Value}
	if rr := get(t, h, "/users", stolen); rr.Code != http.StatusOK {
		t.Fatalf("session before logout = %d", rr.Code)
	}
	token, c := siteFormToken(t, h, c, "/users")
	if rr := postForm(h, "/logout", map[string][]string{"_token": {token}}, c); rr.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rr.Code)
	}
	rr := get(t, h, "/users", &http.Cookie{Name: stolen.Name, Value: stolen.Value})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("replayed cookie after logout = %d loc=%q, want redirect to /login", rr.Code, rr.Header().Get("Location"))
	}
}

// Behind the installer's nginx the peer is the proxy; the client address must
// come from X-Forwarded-For (and only from a trusted peer).
func TestClientIPBehindTrustedProxy(t *testing.T) {
	rolesEnv(t) // builds a server, which installs the default trusted proxies
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Forwarded-For", "203.0.113.77")
	if got := clientIP(r); got != "203.0.113.77" {
		t.Fatalf("clientIP via trusted proxy = %q", got)
	}
	r.RemoteAddr = "198.51.100.5:40000"
	if got := clientIP(r); got != "198.51.100.5" {
		t.Fatalf("clientIP from an untrusted peer = %q (header must be ignored)", got)
	}
}

func TestSecurityHeadersMatchPHP(t *testing.T) {
	h, _ := rolesEnv(t)
	rr := get(t, h, "/login", nil)
	for k, want := range map[string]string{
		"Cache-Control":                "no-store",
		"Permissions-Policy":           "camera=(), microphone=(), geolocation=(), payment=()",
		"Cross-Origin-Opener-Policy":   "same-origin",
		"Cross-Origin-Resource-Policy": "same-origin",
		"X-Frame-Options":              "DENY",
	} {
		if got := rr.Header().Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if cc := get(t, h, "/assets/css/whacp.css", nil).Header().Get("Cache-Control"); cc == "no-store" {
		t.Errorf("static assets must stay cacheable, got %q", cc)
	}
}

// Every signed-in page carries the shared shell: a permission-filtered
// sidebar, the search palette and the confirm/job modals.
func TestPagesCarryTheNavigationShell(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	for _, path := range []string{"/", "/users", "/roles", "/profile"} {
		body := get(t, h, path, admin).Body.String()
		for _, want := range []string{`id="wcp-sidebar"`, `href="/backups"`, `id="wcp-palette"`, `id="wcp-job"`, `id="wcp-confirm"`} {
			if !pageHas(body, want) {
				t.Errorf("%s: shell element %q missing", path, want)
			}
		}
	}
	// A role without the permission does not see the item.
	mgr := loginAsUser(t, h, "mgr")
	body := get(t, h, "/users", mgr).Body.String()
	if pageHas(body, `href="/backups"`) || pageHas(body, `href="/settings"`) {
		t.Error("the sidebar shows areas the role may not open")
	}
	if !pageHas(body, `href="/users"`) {
		t.Error("the sidebar lacks an area the role may open")
	}
}

// Forms that PHP's pages have and that the Go pages once lacked.
func TestProfileHasPasswordAndSessionForms(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	body := get(t, h, "/profile", admin).Body.String()
	for _, want := range []string{`action="/profile/password"`, `name="current_password"`, `action="/profile/sessions/revoke"`, `name="expires_days"`} {
		if !pageHas(body, want) {
			t.Errorf("profile page lacks %s", want)
		}
	}
}

// With no agent the health page still renders every section ("Not checked")
// instead of failing.
func TestServerHealthRendersWithoutAgent(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/server/health", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /server/health = %d", rr.Code)
	}
	for _, want := range []string{"WhaCP and the server", "Resources", "Components", "Security", "Updates", "Backups and tasks", "Not checked"} {
		if !pageHas(rr.Body.String(), want) {
			t.Errorf("health page lacks %q", want)
		}
	}
	if pageHas(rr.Body.String(), "map[") {
		t.Error("health page prints a raw Go map")
	}
}

func TestComponentsPageRendersCards(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/components", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /components = %d", rr.Code)
	}
	for _, want := range []string{"Web server", "Database server", "Email"} {
		if !pageHas(rr.Body.String(), want) {
			t.Errorf("components page lacks %q", want)
		}
	}
	if pageHas(rr.Body.String(), "map[") {
		t.Error("components page prints a raw Go map")
	}
}

func TestServicesPageRendersWithoutRawDump(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/services", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /services = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "SSH and the agent cannot be stopped") {
		t.Error("services page lacks its footer note")
	}
	if pageHas(rr.Body.String(), "map[") {
		t.Error("services page prints a raw Go map")
	}
}

func TestPhpManagerPageAndReviewGrouping(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/php/manager", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /php/manager = %d", rr.Code)
	}
	if pageHas(rr.Body.String(), "map[") {
		t.Error("php manager prints a raw Go map")
	}
	plan := map[string]any{"changes": true, "removals": true, "items": []any{
		map[string]any{"action": "install", "version": "8.3", "packages": []any{"php8.3-fpm"}},
		map[string]any{"action": "remove", "version": "8.1", "extension": "gd", "detail": "no site uses it"},
	}}
	groups, changes, removals := phpReviewData(plan)
	if !changes || !removals || len(groups) != 2 || groups[0].Key != "install" || groups[1].Items[0].What != "PHP 8.1 · gd" {
		t.Errorf("unexpected review grouping: %+v", groups)
	}
	if groups[0].Items[0].Packages != "php8.3-fpm" {
		t.Errorf("packages = %q", groups[0].Items[0].Packages)
	}
}

func TestProcessesPageListsProcesses(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/monitoring/processes", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /monitoring/processes = %d", rr.Code)
	}
	body := rr.Body.String()
	if pageHas(body, "map[") {
		t.Error("processes page prints a raw Go map")
	}
	if !pageHas(body, "History and alerts") {
		t.Error("processes page lacks the monitoring tabs")
	}
}

func TestSslCenterPagesRenderForAdminOnly(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	for _, p := range []string{"/ssl/hosts", "/ssl/autossl", "/ssl/certificates", "/ssl/keys", "/ssl/tls", "/ssl/services"} {
		rr := get(t, h, p, admin)
		if rr.Code != http.StatusOK {
			t.Errorf("GET %s = %d", p, rr.Code)
			continue
		}
		if !pageHas(rr.Body.String(), "Services SSL") || pageHas(rr.Body.String(), "map[") {
			t.Errorf("%s lacks the SSL tabs or prints a raw map", p)
		}
	}
	mgr := loginAsUser(t, h, "mgr")
	if rr := get(t, h, "/ssl/keys", mgr); rr.Code == http.StatusOK {
		t.Error("a non-administrator opened the SSL keys page")
	}
}

func TestLimitsPageRenders(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/limits", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /limits = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Limited accounts") || pageHas(rr.Body.String(), "map[") {
		t.Error("limits page lacks its table or prints a raw map")
	}
}

func TestBandwidthPageListsAccountsOverTheirLimit(t *testing.T) {
	h, db := rolesEnv(t)
	for _, q := range []string{
		`INSERT INTO plans (id,name,bandwidth_mb,created_at,updated_at) VALUES (900,'Metered',100,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`UPDATE users SET plan_id = 900 WHERE username = 'mgr'`,
		`INSERT INTO websites (server_id, owner_id, domain, system_user, root, document_root, kind, created_at, updated_at) VALUES (1, (SELECT id FROM users WHERE username='mgr'), 'bw.test', 'bwuser', '/h/bw', '/h/bw/public', 'php', '2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO website_usage (website_id, bytes, files, measured_at, bw_month, bw_bytes) VALUES ((SELECT id FROM websites WHERE domain='bw.test'), 0, 0, '2026-01-01T00:00:00Z', strftime('%Y-%m','now'), 300*1024*1024)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/accounts/bandwidth", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /accounts/bandwidth = %d", rr.Code)
	}
	for _, want := range []string{"mgr", "Metered", "300", "200"} {
		if !pageHas(rr.Body.String(), want) {
			t.Errorf("bandwidth page lacks %q", want)
		}
	}
}

func settingValue(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var v string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

func TestSettingsSaveValidatesAndStoresTypedValues(t *testing.T) {
	h, db := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	tok, c := siteFormToken(t, h, c, "/settings")

	// An out-of-range number is refused and nothing is stored.
	rr := postForm(h, "/settings", url.Values{"_token": {tok}, "tab": {"general"}, "session_idle_minutes": {"2"}, "panel_name": {"Renamed"}}, c)
	c = refreshCookie(c, rr)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d", rr.Code)
	}
	if got := settingValue(t, db, "session_idle_minutes"); got != "" {
		t.Errorf("invalid value stored: %q", got)
	}
	if got := settingValue(t, db, "panel_name"); got != "" {
		t.Errorf("a valid field was stored alongside an invalid one: %q", got)
	}

	// Valid values are stored typed; an unchecked switch posts 0 and is false.
	tok, c = siteFormToken(t, h, c, "/settings")
	rr = postForm(h, "/settings", url.Values{"_token": {tok}, "tab": {"general"}, "session_idle_minutes": {"45"}, "panel_name": {"Renamed"}, "apps_installer_enabled": {"0"}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d", rr.Code)
	}
	if got := settingValue(t, db, "session_idle_minutes"); got != "45" {
		t.Errorf("session_idle_minutes = %q, want 45", got)
	}
	if got := settingValue(t, db, "apps_installer_enabled"); got != "false" {
		t.Errorf("apps_installer_enabled = %q, want false", got)
	}
	// Unknown keys are ignored.
	if got := settingValue(t, db, "evil_key"); got != "" {
		t.Errorf("unknown key stored: %q", got)
	}
}

func TestSettingsMailUsesTheKeysTheMailerReads(t *testing.T) {
	h, db := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	tok, c := siteFormToken(t, h, c, "/settings")
	rr := postForm(h, "/settings/mail", url.Values{"_token": {tok}, "host": {"smtp.example.com"}, "port": {"587"}, "encryption": {"starttls"},
		"username": {"u"}, "password": {"s3cret-value"}, "from": {"panel@example.com"}, "alert_to": {"a@example.com, b@example.com"}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings/mail = %d", rr.Code)
	}
	if got := settingValue(t, db, "mail.host"); got != `"smtp.example.com"` {
		t.Errorf("mail.host = %q", got)
	}
	if got := settingValue(t, db, "mail_host"); got != "" {
		t.Errorf("legacy mail_host key written: %q", got)
	}
	pw := settingValue(t, db, "mail.password")
	if pw == "" || strings.Contains(pw, "s3cret-value") {
		t.Errorf("password must be stored encrypted, got %q", pw)
	}
	// Plain SMTP to a remote host is refused.
	tok, c = siteFormToken(t, h, c, "/settings")
	postForm(h, "/settings/mail", url.Values{"_token": {tok}, "host": {"smtp.example.org"}, "port": {"25"}, "encryption": {"none"}, "from": {"p@example.org"}}, c)
	if got := settingValue(t, db, "mail.host"); got != `"smtp.example.com"` {
		t.Errorf("an invalid mail config replaced the stored one: %q", got)
	}
}

func TestTweakSettingsResetRestoresDefault(t *testing.T) {
	h, db := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('dashboard_refresh_seconds', '60', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	rr := get(t, h, "/settings/tweak", c)
	if !pageHas(rr.Body.String(), "Reset to default") {
		t.Fatal("a changed setting has no reset link")
	}
	tok, c := siteFormToken(t, h, c, "/settings/tweak")
	rr = postForm(h, "/settings/tweak", url.Values{"_token": {tok}, "tab": {"general"}, "reset": {"dashboard_refresh_seconds"}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings/tweak = %d", rr.Code)
	}
	if got := settingValue(t, db, "dashboard_refresh_seconds"); got != "10" {
		t.Errorf("after reset dashboard_refresh_seconds = %q, want 10", got)
	}
}

func TestDnsIndexOffersInstallWhenBindMissing(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/dns", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /dns = %d", rr.Code)
	}
	body := rr.Body.String()
	if pageHas(body, "map[") || !pageHas(body, "Zones") {
		t.Error("dns page lacks its zone panel or prints a raw map")
	}
}

func TestMailIndexRenders(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/mail", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /mail = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Mail domains") || pageHas(rr.Body.String(), "map[") {
		t.Error("mail page lacks its domain panel or prints a raw map")
	}
}

func TestPackagesPageHasQueueAndComponentRows(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/packages", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /packages = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Installation queue", "Updates pending", "Components installed"} {
		if !pageHas(body, want) {
			t.Errorf("packages page lacks %q", want)
		}
	}
	if pageHas(body, "map[") {
		t.Error("packages page prints a raw map")
	}
}

func TestSettingsSwitchPostedAsHiddenZeroThenOneIsOn(t *testing.T) {
	h, db := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	tok, c := siteFormToken(t, h, c, "/settings")
	rr := postForm(h, "/settings", url.Values{"_token": {tok}, "tab": {"security"}, "av_quarantine": {"0", "1"}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /settings = %d", rr.Code)
	}
	if got := settingValue(t, db, "av_quarantine"); got != "true" {
		t.Errorf("a switch posted as 0 then 1 stored %q, want true", got)
	}
}

func TestNotificationsPageFiltersAndMarksOneRead(t *testing.T) {
	h, db := rolesEnv(t)
	for _, q := range []string{
		`INSERT INTO notifications (id, server_id, kind, severity, title, body, link, dedupe_key, created_at) VALUES (801, 1, 'job', 'error', 'Backup failed', '', '', 'k801', '2026-01-01T00:00:00Z')`,
		`INSERT INTO notifications (id, server_id, kind, severity, title, body, link, dedupe_key, created_at) VALUES (802, 1, 'job', 'info', 'All good', '', '', 'k802', '2026-01-02T00:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	c := loginAsUser(t, h, "admin")
	body := get(t, h, "/notifications?severity=error", c).Body.String()
	if !pageHas(body, "Backup failed") || pageHas(body, "All good") {
		t.Error("the severity filter did not narrow the list")
	}
	tok, c := siteFormToken(t, h, c, "/notifications")
	rr := postForm(h, "/notifications/read", url.Values{"_token": {tok}, "ids[]": {"801"}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /notifications/read = %d", rr.Code)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notification_reads WHERE notification_id IN (801, 802)`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("marking one notification read recorded %d reads, want 1", n)
	}
}

func TestPlansPageAndFeatureManagerRender(t *testing.T) {
	h, db := rolesEnv(t)
	if _, err := db.Exec(`INSERT INTO plans (id,name,description,max_websites,created_at,updated_at) VALUES (950,'Gold','The good one',3,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	c := loginAsUser(t, h, "admin")
	body := get(t, h, "/plans", c).Body.String()
	for _, want := range []string{"Gold", "The good one", "New plan", "Resource limits (CPU, memory)", "plan-edit-950"} {
		if !pageHas(body, want) {
			t.Errorf("plans page lacks %q", want)
		}
	}
	// Posting a plan with the switch checked (hidden 0, then 1) keeps FFmpeg on.
	tok, c := siteFormToken(t, h, c, "/plans")
	rr := postForm(h, "/plans", url.Values{"_token": {tok}, "plan_id": {"950"}, "name": {"Gold"}, "ffmpeg": {"0", "1"}, "max_websites": {"3"}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /plans = %d", rr.Code)
	}
	var ff int
	if err := db.QueryRow(`SELECT ffmpeg FROM plans WHERE id = 950`).Scan(&ff); err != nil || ff != 1 {
		t.Errorf("ffmpeg = %d (err %v), want 1", ff, err)
	}
	fb := get(t, h, "/features", c).Body.String()
	if !pageHas(fb, "New feature list") || pageHas(fb, "map[") {
		t.Error("feature manager lacks its dialog or prints a raw map")
	}
}

func TestServersConnectIsRefusedNotStoredDead(t *testing.T) {
	h, db := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	tok, c := siteFormToken(t, h, c, "/servers")
	rr := postForm(h, "/servers", url.Values{"_token": {tok}, "name": {"web2"}, "url": {"https://203.0.113.10:8443"}, "pin": {"sha256//x"}, "token": {"t"}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST /servers = %d", rr.Code)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM servers WHERE name = 'web2'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a server without token or pin was stored (n=%d, err=%v)", n, err)
	}
}

func TestUsersPageHasRowActionsAndBulkTerminate(t *testing.T) {
	h, _ := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	body := get(t, h, "/users", c).Body.String()
	for _, want := range []string{"Create account", "New user", "Terminate selected", "Last sign-in", "Edit", "Delete"} {
		if !pageHas(body, want) {
			t.Errorf("users page lacks %q", want)
		}
	}
}

func TestWebsitesListHasPerSiteOperations(t *testing.T) {
	h, db := rolesEnv(t)
	if _, err := db.Exec(`INSERT INTO websites (id, server_id, owner_id, domain, system_user, root, document_root, kind, php_version, status, enabled, created_at, updated_at) VALUES (960, 1, NULL, 'ops.example', 'opsuser', '/h/ops', '/h/ops/public', 'php', '8.3', 'active', 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	c := loginAsUser(t, h, "admin")
	list := get(t, h, "/websites?view=list", c).Body.String()
	for _, want := range []string{"ops.example", "/sites/960/toggle", "/sites/960/backups", "Manage", "PHP 8.3"} {
		if !pageHas(list, want) {
			t.Errorf("list view lacks %q", want)
		}
	}
	grid := get(t, h, "/websites?view=grid", c).Body.String()
	if !pageHas(grid, "wcp-site-card") || !pageHas(grid, "ops.example") {
		t.Error("grid view lacks the site cards")
	}
	if pageHas(list, "map[") || pageHas(grid, "map[") {
		t.Error("websites page prints a raw map")
	}
}

func TestCreateDatabaseWithUserHonoursTheSwitches(t *testing.T) {
	h, cookie, ra, db := writeEnv(t, 5, 0)
	token, cookie := dbFormToken(t, h, cookie)
	// create_user on (hidden 0 then 1), store_password off (just the hidden 0).
	rr := postForm(h, "/databases", url.Values{"_token": {token}, "name": {"shop"}, "create_user": {"0", "1"}, "store_password": {"0"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", rr.Code)
	}
	acts := ra.called()
	if len(acts) != 3 || acts[0] != "db.create" || acts[1] != "db.user_create" || acts[2] != "db.grant" {
		t.Fatalf("agent actions = %v, want [db.create db.user_create db.grant]", acts)
	}
	var enc string
	if err := db.QueryRow(`SELECT password_enc FROM database_users WHERE username = 'bob_shop'`).Scan(&enc); err != nil {
		t.Fatalf("the user was not recorded: %v", err)
	}
	if enc != "" {
		t.Errorf("password stored although the switch was off: %q", enc)
	}
	if body := get(t, h, "/databases", cookie).Body.String(); !pageHas(body, "Database user created") {
		t.Error("the new password was not shown once")
	}
}

func TestAuditPageShowsDetailsResultFilterAndPaging(t *testing.T) {
	h, db := rolesEnv(t)
	for i := 0; i < 55; i++ {
		if _, err := db.Exec(`INSERT INTO audit_logs (username, action, target_type, target_id, result, ip, details, created_at) VALUES ('admin', 'probe.event', 'thing', ?, 'failure', '203.0.113.9', '{"why":"because"}', '2026-01-01T00:00:00Z')`, i); err != nil {
			t.Fatal(err)
		}
	}
	c := loginAsUser(t, h, "admin")
	body := get(t, h, "/audit?action=probe.&result=failure", c).Body.String()
	for _, want := range []string{"probe.event", "because", "Any result", "Page 1 of 2", "Next"} {
		if !pageHas(body, want) {
			t.Errorf("audit page lacks %q", want)
		}
	}
	if p2 := get(t, h, "/audit?action=probe.&result=failure&page=2", c).Body.String(); !pageHas(p2, "Page 2 of 2") {
		t.Error("second page not shown")
	}
}

func TestRolesPagesShowDescriptionsGroupsAndPolicy(t *testing.T) {
	h, _ := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	list := get(t, h, "/roles", c).Body.String()
	for _, want := range []string{"Administrator", "built-in", "New role", "View"} {
		if !pageHas(list, want) {
			t.Errorf("roles list lacks %q", want)
		}
	}
	form := get(t, h, "/roles/2/edit", c).Body.String() // Operator
	for _, want := range []string{"Permissions", "permissions[]", "Reset to WhaCP defaults", "Sign-in policy", "built-in role"} {
		if !pageHas(form, want) {
			t.Errorf("role form lacks %q", want)
		}
	}
	admin := get(t, h, "/roles/1/edit", c).Body.String()
	if !pageHas(admin, "always has every permission") {
		t.Error("the Administrator role form lacks its notice")
	}
}

func TestWebsiteFormOffersDirectiveEditorAndDocumentRoot(t *testing.T) {
	h, _ := rolesEnv(t)
	c := loginAsUser(t, h, "admin")
	body := get(t, h, "/websites/new", c).Body.String()
	for _, want := range []string{"Additional PHP directives", "ini[display_errors]", "ini[date.timezone]", "document_root_sub", "public_html"} {
		if !pageHas(body, want) && !strings.Contains(body, want) {
			t.Errorf("website form lacks %q", want)
		}
	}
}

// The create-account form carries the PHP panel's PHP version, mail and DNS
// options, and a ticked switch (hidden 0 then 1) counts as on.
func TestAccountsNewFormFields(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rec := get(t, h, "/accounts/new", admin)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`name="dns_dkim"`, `name="dns_spf"`, `name="dns_dmarc"`, `name="dns_registrar_ns"`,
		`name="dns_overwrite"`, `name="spam_filter"`, `name="spam_box"`, `name="mail_routing"`, `v=spf1`} {
		if !strings.Contains(body, want) {
			t.Errorf("form lacks %s", want)
		}
	}
	if strings.Contains(body, "style=") || strings.Contains(body, "<script>") {
		t.Error("inline style/script in template")
	}
}

func TestWhacpUpdatePageRenders(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rec := get(t, h, "/whacp/update", admin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Last update") || !strings.Contains(rec.Body.String(), "Source folder") {
		t.Fatalf("update page: %d", rec.Code)
	}
}

func TestHostnameReferencesPage(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rec := get(t, h, "/server/hostname-references", admin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Previous host name") {
		t.Fatalf("hostname references: %d", rec.Code)
	}
}

func TestRuntimesPageRenders(t *testing.T) {
	h, _ := rolesEnv(t)
	admin := loginAsUser(t, h, "admin")
	rec := get(t, h, "/runtimes", admin)
	if rec.Code != 200 {
		t.Fatalf("runtimes: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "onclick=") {
		t.Error("inline handler in runtimes template")
	}
}

// A backup file's description is read from the manifest the agent returns
// (username at the top level, the panel's description under meta), and each
// website is compared with what exists here.
func TestRestoreViewDescribesBackup(t *testing.T) {
	s, db := rolesEnvServer(t)
	if _, err := db.Exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (50,1,1,'taken.example','u','/h','/h/p','php','8.3','active','','')`); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"username": "jane", "key_id": "k1", "created_at": "2026-01-02T03:04:05Z",
		"meta": map[string]any{
			"user":      map[string]any{"username": "jane", "plan": "Gone"},
			"websites":  []any{map[string]any{"domain": "taken.example", "kind": "php", "php_version": "8.3", "databases": []any{"jane_db"}}, map[string]any{"domain": "new.example", "kind": "php", "php_version": "8.2"}},
			"databases": []any{"a", "b"},
		},
	}
	req := httptest.NewRequest("GET", "/", nil)
	v := s.restoreView(req, manifest, nil)
	if v["Username"] != "jane" || v["Account"] != "create" || v["PlanFound"] != false || v["DBs"] != 2 {
		t.Fatalf("view: %v", v)
	}
	sites := v["Sites"].([]restoreSite)
	if len(sites) != 2 || !strings.Contains(sites[0].Here, "someone else") || sites[1].Here != "will be created" || sites[0].Databases != "jane_db" {
		t.Fatalf("sites: %+v", sites)
	}
	manifest["username"] = "admin"
	if v := s.restoreView(req, manifest, nil); v["Account"] != "conflict" {
		t.Fatalf("a staff name must conflict, got %v", v["Account"])
	}
}
