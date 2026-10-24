package http

// This suite drives the cPHulk brute force protection page (HulkController:
// the settings form, the countries management, the failed-logins report, and
// the save/countries/geo/unblock/clear actions).

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// hulkEnv seeds an administrator and a customer (own scope, for the
// all-scope gate) plus the cPHulk store rows.
func hulkEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'security.view','sv','security'),(3,'security.manage','sm','security')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(2,1),(2,2)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','')`,
		loginHash, loginHash)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sa, socket, tokenFile := startSitesAgent(t, false)
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

// hulkForm is the full settings form at its defaults (the page's fields).
func hulkForm() url.Values {
	return url.Values{
		"hulk_enabled": {"1"}, "hulk_user_enabled": {"1"},
		"hulk_user_period": {"5"}, "hulk_user_max": {"5"},
		"hulk_user_scope": {"all"}, "hulk_user_lock_root": {"0"},
		"hulk_ip_enabled": {"1"}, "hulk_ip_period": {"15"}, "hulk_ip_max": {"15"},
		"hulk_ip_command": {""}, "hulk_ip_firewall": {"0"},
		"hulk_oneday_max": {"30"}, "hulk_oneday_command": {""}, "hulk_oneday_firewall": {"0"},
		"hulk_retention":   {"360"},
		"hulk_notify_root": {"1"}, "hulk_notify_root_netblock": {"0"}, "hulk_notify_brute": {"1"},
		"hulk_whitelist": {"203.0.113.10, 198.51.100.0/24"},
	}
}

func TestHulkPageDisabled(t *testing.T) {
	h, _, _ := hulkEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/hulk", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"cPHulk Brute Force Protection",
		"wcp-state is-off\">Disabled",
		"0 blocks in force",
		// The subtabs.
		"cPHulk Settings", "Countries Management", `Failed Logins Report <span class="badge rounded-pill text-bg-light border fw-normal">0</span>`,
		// The form's defaults.
		`value="5"`, `value="15"`, `value="30"`, `value="360"`,
		`id="h-scope-all" checked`,
		`id="h-cc-login" checked`,
		// The countries table (the catalogue order, first and last).
		">Afghanistan<", ">Zimbabwe<", `name="country[US]"`,
		// The settings and the report empty states.
		"Whitelisted IP addresses", "No failed logins in this period.",
		// The variable list.
		"<code>%ip</code> - the blocked IP address",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, `id="h-scope-local" checked`) {
		t.Errorf("the local scope radio is checked although the default is all")
	}
}

func TestHulkPageAgentErrorAndNftables(t *testing.T) {
	h, sa, db := hulkEnv(t)
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('hulk_enabled','true','')`); err != nil {
		t.Fatal(err)
	}
	admin := loginAsUser(t, h, "admin")

	// The agent is unreachable: the page still renders, with the warning.
	sa.failActions = map[string]string{"hulk.status": "the nftables service is not running"}
	rr := get(t, h, "/security/hulk", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"the nftables service is not running",
		"wcp-state is-ok\">Enabled", // status unknown: the enabled state
	} {
		if !strings.Contains(body, want) {
			t.Errorf("agent-error page missing %q", want)
		}
	}
	delete(sa.failActions, "hulk.status")

	// nftables missing: the warned enabled state.
	sa.hulkAvailable = false
	rr = get(t, h, "/security/hulk", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "wcp-state is-warn\">Enabled (WhaCP sign-ins only: nftables is not installed)") {
		t.Errorf("missing the nftables-missing state")
	}
	sa.hulkAvailable = true
}

func TestHulkPageDeniedForNonAllScope(t *testing.T) {
	h, _, _ := hulkEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/security/hulk", bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("GET = %d (want 403)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Only administrators manage brute force protection.") {
		t.Errorf("missing the deny message")
	}
}

func TestHulkSave(t *testing.T) {
	h, sa, db := hulkEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/hulk")
	form := hulkForm()
	form.Set("hulk_user_period", "10")
	form.Set("hulk_user_lock_root", "1")
	form.Set("hulk_ip_command", "echo %ip")
	form.Set("_token", token)
	rr := postForm(h, "/security/hulk", form, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "cPHulk settings saved.")
	var enabled string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='hulk_enabled'`).Scan(&enabled); err != nil {
		t.Fatalf("hulk_enabled: %v", err)
	}
	if enabled != "true" {
		t.Errorf("hulk_enabled = %s", enabled)
	}
	var period string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='hulk_user_period'`).Scan(&period); err != nil {
		t.Fatalf("hulk_user_period: %v", err)
	}
	if period != "10" {
		t.Errorf("hulk_user_period = %s", period)
	}
	var wl string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='hulk_whitelist'`).Scan(&wl); err != nil {
		t.Fatalf("hulk_whitelist: %v", err)
	}
	if wl != `"203.0.113.10, 198.51.100.0/24"` {
		t.Errorf("hulk_whitelist = %s", wl)
	}
	// The sync went to the agent with the enabled payload.
	synced, _ := agentParam(t, sa, "hulk.sync", "enabled").(bool)
	if !synced {
		t.Errorf("hulk.sync enabled = %v", synced)
	}
	if wlParam, _ := agentParam(t, sa, "hulk.sync", "whitelist").([]any); len(wlParam) != 2 {
		t.Errorf("hulk.sync whitelist = %v", wlParam)
	}
}

func TestHulkSaveSyncFailure(t *testing.T) {
	h, sa, _ := hulkEnv(t)
	sa.failActions = map[string]string{"hulk.sync": "nft: command not found"}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/hulk")
	form := hulkForm()
	form.Set("_token", token)
	rr := postForm(h, "/security/hulk", form, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "nft: command not found")
}

func TestHulkSaveWhitelistErrors(t *testing.T) {
	h, _, _ := hulkEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/hulk")

	form := hulkForm()
	form.Set("hulk_whitelist", "not-an-ip")
	form.Set("_token", token)
	rr := postForm(h, "/security/hulk", form, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", `"not-an-ip" is not an IP address or network.`)

	form = hulkForm()
	ips := []string{}
	for i := 0; i < 201; i++ {
		ips = append(ips, "10.0.0.1")
	}
	form.Set("hulk_whitelist", strings.Join(ips, " "))
	form.Set("_token", token)
	rr = postForm(h, "/security/hulk", form, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "At most 200 whitelisted addresses.")
}

func TestHulkSaveAddsOwnAddressWhenEnabling(t *testing.T) {
	h, sa, db := hulkEnv(t)
	if _, err := db.Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('hulk_enabled','false','')`); err != nil {
		t.Fatal(err)
	}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/hulk")
	form := hulkForm()
	form.Set("hulk_whitelist", "")
	form.Set("_token", token)
	rr := postForm(h, "/security/hulk", form, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	// postForm's peer is 203.0.113.50.
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk",
		"cPHulk settings saved. Your address (203.0.113.50) was added to the whitelist.")
	var wl string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='hulk_whitelist'`).Scan(&wl); err != nil {
		t.Fatalf("hulk_whitelist: %v", err)
	}
	if wl != `"203.0.113.50"` {
		t.Errorf("hulk_whitelist = %s", wl)
	}
	_ = sa
}

func TestHulkSaveScopeAndRangeErrors(t *testing.T) {
	h, _, _ := hulkEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/hulk")

	form := hulkForm()
	form.Set("hulk_user_scope", "everywhere")
	form.Set("_token", token)
	rr := postForm(h, "/security/hulk", form, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "Choose a protection type.")

	form = hulkForm()
	form.Set("hulk_user_period", "99999")
	form.Set("hulk_oneday_max", "0")
	form.Set("_token", token)
	rr = postForm(h, "/security/hulk", form, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "Enter a whole number between 1 and 1440.")
}

func TestHulkDeniedForNonAdmin(t *testing.T) {
	h, _, _ := hulkEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := postForm(h, "/security/hulk", url.Values{"_token": {"x"}}, bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST = %d (want 403)", rr.Code)
	}
}

func TestHulkCountriesBulk(t *testing.T) {
	h, sa, db := hulkEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/hulk")
	rr := postForm(h, "/security/hulk/countries", url.Values{
		"_token":     {token},
		"bulk":       {"blacklist"},
		"selected[]": {"US", "CN"},
		"scope":      {"all"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("countries = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "2 countries updated.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM hulk_countries WHERE status='blacklist'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("blacklisted = %d", n)
	}
	var all string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='hulk_countries_all'`).Scan(&all); err != nil {
		t.Fatalf("hulk_countries_all: %v", err)
	}
	if all != "true" {
		t.Errorf("hulk_countries_all = %s", all)
	}
	// The sync carries the blacklisted countries' ranges (none loaded yet).
	if allParam, _ := agentParam(t, sa, "hulk.sync", "countries_all").(bool); !allParam {
		t.Errorf("hulk.sync countries_all = %v", allParam)
	}
	var action string
	if err := db.QueryRow(`SELECT action FROM audit_logs WHERE target_type='server' AND target_id='hulk' ORDER BY id DESC LIMIT 1`).Scan(&action); err != nil {
		t.Fatal(err)
	}
	if action != "hulk.countries" {
		t.Errorf("audit = %s", action)
	}
}

func TestHulkCountriesIndividualAndInvalid(t *testing.T) {
	h, _, db := hulkEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/hulk")

	rr := postForm(h, "/security/hulk/countries", url.Values{
		"_token": {token}, "country[JP]": {"whitelist"}, "country[RU]": {"none"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("countries = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "1 country updated.")
	var mark string
	if err := db.QueryRow(`SELECT status FROM hulk_countries WHERE code='JP'`).Scan(&mark); err != nil {
		t.Fatal(err)
	}
	if mark != "whitelist" {
		t.Errorf("JP = %s", mark)
	}

	rr = postForm(h, "/security/hulk/countries", url.Values{
		"_token": {token}, "bulk": {"everywhere"}, "selected[]": {"US"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("countries = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "Choose how to mark the countries.")
}

// hulkMultipart posts a form file (the geo upload).
func hulkMultipart(t *testing.T, h http.Handler, path string, fileField, fileName, fileContent, token string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var b strings.Builder
	w := multipart.NewWriter(&b)
	if fileField != "" {
		fw, err := w.CreateFormFile(fileField, fileName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(fileContent)); err != nil {
			t.Fatal(err)
		}
	}
	w.WriteField("_token", token)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", path, strings.NewReader(b.String()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.RemoteAddr = "203.0.113.50:40000"
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHulkGeoUpload(t *testing.T) {
	h, _, db := hulkEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/hulk")
	csv := "1,255,US\n256,511,US\n512,767,CA\nbad-line\n999,1000,US\n"
	rr := hulkMultipart(t, h, "/security/hulk/geo", "database", "dbip.csv", csv, token, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("geo = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "Country database loaded (3 address ranges).")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM geo_ranges`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("geo_ranges = %d", n)
	}
	var stamp string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key='hulk_geo_updated'`).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	if stamp != `"`+time.Now().Format("2006-01-02")+`"` {
		t.Errorf("hulk_geo_updated = %s", stamp)
	}
}

func TestHulkGeoNoFile(t *testing.T) {
	h, _, _ := hulkEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/hulk")
	rr := hulkMultipart(t, h, "/security/hulk/geo", "", "", "", token, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("geo = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk",
		`Choose the DB-IP "IP to Country Lite" CSV file (.csv or .csv.gz).`)
}

func TestHulkUnblockAndClear(t *testing.T) {
	h, _, db := hulkEnv(t)
	now := time.Now().Unix()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO hulk_blocks (kind,ip,user,reason,service,authservice,failures,firewall,created_at,expires_at) VALUES
		('oneday','203.0.113.9',NULL,'30 failures in 60 minutes','sshd','sshd',30,1,?,?),
		('user',NULL,'root','3 failures','web','whacp',3,0,?,?)`,
		now-60, now+600, now-30, now+300)
	exec(`INSERT INTO hulk_events (id,server_id,`+"`time`"+`,service,authservice,user,ip,country,ok) VALUES
		('e1',1,?, 'sshd','sshd','root','203.0.113.9','US',0),('e2',1,?, 'sshd','sshd','admin','198.51.100.7','CA',0)`,
		now-120, now-60)

	// The page shows the report and the blocks.
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/hulk", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Blocks in force", "203.0.113.9", "IP address (one day)", "IP address", "Account", "root",
		"Failed Logins Report", "198.51.100.7", "United States", "Canada",
		"2 blocks in force",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("report page missing %q", want)
		}
	}

	// Unblock the first block (id 1).
	token, admin := siteFormToken(t, h, admin, "/security/hulk")
	rr = postForm(h, "/security/hulk/unblock", url.Values{"_token": {token}, "id": {"1"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unblock = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "The block was removed.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM hulk_blocks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("blocks = %d", n)
	}

	// Clear everything.
	rr = postForm(h, "/security/hulk/clear", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("clear = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/hulk", "All blocks were removed and the report was cleared.")
	if err := db.QueryRow(`SELECT COUNT(*) FROM hulk_blocks`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("blocks after clear = %d", n)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM hulk_events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("events after clear = %d", n)
	}
}
