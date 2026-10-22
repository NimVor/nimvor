package http

// This suite drives the website's visitor statistics (StatsController):
// the month's report (the parsed AWStats data file), the month select +
// fallback, the on/off switch (the first update, the audits), the
// on-demand update (the job) and the negative cases.

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// statsEnv seeds an administrator (websites.view/manage + security.manage +
// logs.view) and a customer (bob, websites.view + logs.view only) who owns
// website 10 (bob.example).
func statsEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'security.manage','sm','security'),(4,'logs.view','lv','logs')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(1,4),(2,1),(2,4)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','')`,
		loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','','')`)

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

// statsFixture is the fake's stats.data content (a September 2026 month).
const statsFixture = `# file awstats092026.bob.example.txt
BEGIN_GENERAL
LastUpdate 20260927153000
TotalUnique 42
TotalVisits 250
END_GENERAL
BEGIN_DAY
20260901 150 320 45000 12
20260902 140 300 42000 11
END_DAY
BEGIN_TIME
0 10 20 1000
1 12 25 1500
END_TIME
BEGIN_SIDER
/index.php 250
/admin/ 120
END_SIDER
BEGIN_BROWSER
chrome_120 150
firefox_121 30
END_BROWSER
BEGIN_DOMAIN
us 100
ip 20
END_DOMAIN
`

// statsEnvWithData gives the fake two months and the September report.
func statsEnvWithData(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
	t.Helper()
	h, sa, db := statsEnv(t)
	sa.mu.Lock()
	sa.statsEnabled = true
	sa.statsMonthsJSON = `["202609","202608"]`
	sa.statsUpdatedAt = "2026-09-27T15:30:00Z"
	sa.statsDataFound = true
	sa.statsDataContent = statsFixture
	sa.mu.Unlock()
	return h, sa, db
}

func TestSiteStatsPage(t *testing.T) {
	h, sa, _ := statsEnvWithData(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/stats", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		// The month select + the toolbar.
		`<option value="202609" selected>September 2026</option>`,
		`<option value="202608">August 2026</option>`,
		"Updated every 6 hours", "last update",
		"Update now", "Turn off",
		// The summary.
		"42", "250", "290", "620", "85.0 KB",
		// The days + hours.
		"Days", "Tue 1", "Wed 2", "43.9 KB",
		"Hours (server time)", ">00<", ">01<",
		// The lists.
		"Most viewed pages", "/index.php", "250", "/admin/", "120",
		"Browsers", "Chrome", "150", "Firefox", "30",
		"Countries", ">US<", "100", "Unknown",
		"No data.", // the empty lists (referrers, search, systems, errors)
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The agent got the site's target + the (fallback-corrected) month.
	if got := agentParam(t, sa, "stats.status", "domain"); got != "bob.example" {
		t.Errorf("stats.status domain = %v", got)
	}
	if got := agentParam(t, sa, "stats.status", "user"); got != "u_bob" {
		t.Errorf("stats.status user = %v", got)
	}
	if got := agentParam(t, sa, "stats.data", "month"); got != "202609" {
		t.Errorf("stats.data month = %v", got)
	}
	// The owner (logs.view) can view the page, without the buttons.
	bob := loginAsUser(t, h, "bob")
	if rr = get(t, h, "/sites/10/stats", bob); rr.Code != http.StatusOK {
		t.Fatalf("bob GET = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "Update now") || strings.Contains(rr.Body.String(), "Turn off") {
		t.Error("bob may not update or switch the statistics")
	}
}

func TestSiteStatsMonthFallback(t *testing.T) {
	h, sa, _ := statsEnvWithData(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/stats?month=202001", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	if got := agentParam(t, sa, "stats.data", "month"); got != "202609" {
		t.Errorf("month fallback = %v, want 202609", got)
	}
	if !strings.Contains(rr.Body.String(), `<option value="202609" selected>September 2026</option>`) {
		t.Error("the fallback month is not selected")
	}
}

func TestSiteStatsOffNoData(t *testing.T) {
	h, _, _ := statsEnv(t) // installed, disabled, no months
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/stats", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Visitor statistics are off") {
		t.Error("the off state is missing")
	}
	if !strings.Contains(body, "Turn on statistics") {
		t.Error("the administrator's turn-on button is missing")
	}
	if strings.Contains(body, "Most viewed pages") || strings.Contains(body, "No data yet") {
		t.Error("the off state must not show the report")
	}
	bob := loginAsUser(t, h, "bob")
	if rr = get(t, h, "/sites/10/stats", bob); rr.Code != http.StatusOK {
		t.Fatalf("bob GET = %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "Turn on statistics") {
		t.Error("bob may not turn the statistics on")
	}
}

func TestSiteStatsNotInstalled(t *testing.T) {
	h, sa, _ := statsEnv(t)
	sa.statsInstalled = false
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/stats", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Statistics are not available on this server yet") {
		t.Error("the not-installed state is missing")
	}
	if !strings.Contains(rr.Body.String(), "Open Packages") {
		t.Error("the administrator's Packages link is missing")
	}
	bob := loginAsUser(t, h, "bob")
	if rr = get(t, h, "/sites/10/stats", bob); rr.Code != http.StatusOK {
		t.Fatalf("bob GET = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Please ask your hosting provider to install AWStats.") {
		t.Error("bob's not-installed hint is missing")
	}
}

func TestSiteStatsAgentDown(t *testing.T) {
	h, sa, _ := statsEnv(t)
	sa.failActions = map[string]string{"stats.status": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/stats", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "the socket is closed") {
		t.Error("the agent error is missing")
	}
}

func TestSiteStatsNoDataYet(t *testing.T) {
	h, sa, _ := statsEnv(t)
	sa.mu.Lock()
	sa.statsEnabled = true
	sa.statsMonthsJSON = `["202609"]`
	sa.statsDataFound = false // the month's file is not there yet
	sa.mu.Unlock()
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/stats", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "No data yet") {
		t.Error("the no-data state is missing")
	}
	if !strings.Contains(rr.Body.String(), "Statistics are off; showing the data collected before") {
		t.Error("the disabled-with-data toolbar text is missing")
	}
}

func TestSiteStatsEnable(t *testing.T) {
	h, sa, db := statsEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/stats")
	rr := postForm(h, "/sites/10/stats/enable", url.Values{"_token": {token}, "enabled": {"1"}}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/stats" {
		t.Fatalf("POST = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	// The agent: enabled=true, then the first update.
	if got := agentParam(t, sa, "stats.enable", "enabled"); got != true {
		t.Errorf("stats.enable enabled = %v", got)
	}
	if got := agentParam(t, sa, "stats.update", "domain"); got != "bob.example" {
		t.Errorf("stats.update domain = %v", got)
	}
	// The first update is tracked for the website.
	var kind, targetType, targetID string
	if err := db.QueryRow(`SELECT kind, target_type, target_id FROM agent_jobs`).Scan(&kind, &targetType, &targetID); err != nil {
		t.Fatal(err)
	}
	if kind != "Statistics of bob.example" || targetType != "website" || targetID != "10" {
		t.Errorf("agent_jobs = %s %s/%s", kind, targetType, targetID)
	}
	// The audit.
	var target, details string
	if err := db.QueryRow(`SELECT target_id, details FROM audit_logs WHERE action = 'stats.enable'`).Scan(&target, &details); err != nil {
		t.Fatal(err)
	}
	if target != "10" || !strings.Contains(details, "bob.example") {
		t.Errorf("stats.enable audit = %s %s", target, details)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/stats",
		"Statistics are on. They are updated every 6 hours; the first update is running now.")
}

func TestSiteStatsDisable(t *testing.T) {
	h, sa, db := statsEnvWithData(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/stats")
	rr := postForm(h, "/sites/10/stats/enable", url.Values{"_token": {token}, "enabled": {"0"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST = %d", rr.Code)
	}
	if got := agentParam(t, sa, "stats.enable", "enabled"); got != false {
		t.Errorf("stats.enable enabled = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'stats.disable'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("stats.disable audits = %d: %v", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_jobs`).Scan(&n); err != nil || n != 0 {
		t.Errorf("disabling must not start an update (jobs = %d)", n)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/stats",
		"Statistics are off. The collected data is kept.")
}

func TestSiteStatsEnableAgentRefused(t *testing.T) {
	h, sa, db := statsEnv(t)
	sa.failActions = map[string]string{"stats.enable": "AWStats is not installed on this server."}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/stats")
	rr := postForm(h, "/sites/10/stats/enable", url.Values{"_token": {token}, "enabled": {"1"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST = %d", rr.Code)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a refused switch must not audit (rows = %d)", n)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/stats",
		"AWStats is not installed on this server.")
}

func TestSiteStatsUpdate(t *testing.T) {
	h, sa, db := statsEnvWithData(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/stats")
	rr := postForm(h, "/sites/10/stats/update", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/stats" {
		t.Fatalf("POST = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	var kind, targetID string
	if err := db.QueryRow(`SELECT kind, target_id FROM agent_jobs`).Scan(&kind, &targetID); err != nil {
		t.Fatal(err)
	}
	if kind != "Statistics of bob.example" || targetID != "10" {
		t.Errorf("agent_jobs = %s/%s", kind, targetID)
	}
	body := flashAt(t, h, refreshCookie(admin, rr), "/sites/10/stats", "Updating the statistics.")
	if !strings.Contains(body, `Job <code>fedcba9876543210</code> started in the background.`) {
		t.Error("the job infobar is missing")
	}
}

func TestSiteStatsUpdateRefused(t *testing.T) {
	h, sa, _ := statsEnvWithData(t)
	sa.failActions = map[string]string{"stats.update": "Statistics are not turned on for bob.example."}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/stats")
	rr := postForm(h, "/sites/10/stats/update", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/stats",
		"Statistics are not turned on for bob.example.")
}

func TestSiteStatsForbidden(t *testing.T) {
	h, _, _ := statsEnvWithData(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/stats")
	if rr := postForm(h, "/sites/10/stats/enable", url.Values{"_token": {token}, "enabled": {"1"}}, bob); rr.Code != http.StatusForbidden {
		t.Fatalf("bob enable = %d, want 403", rr.Code)
	}
	token, bob = siteFormToken(t, h, bob, "/sites/10/stats")
	if rr := postForm(h, "/sites/10/stats/update", url.Values{"_token": {token}}, bob); rr.Code != http.StatusForbidden {
		t.Fatalf("bob update = %d, want 403", rr.Code)
	}
}

func TestSiteStatsNotFound(t *testing.T) {
	h, _, _ := statsEnvWithData(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/999/stats", admin)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET = %d, want 404", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "This website does not exist or you do not have access to it.") {
		t.Error("the 404 message is missing")
	}
	token, admin := siteFormToken(t, h, admin, "/sites/10/stats")
	rr = postForm(h, "/sites/999/stats/enable", url.Values{"_token": {token}, "enabled": {"1"}}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("POST = %d %q, want /websites", rr.Code, rr.Header().Get("Location"))
	}
}
