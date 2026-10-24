package http

// This suite drives the database server manager (DbServerController):
// the permission gate + the all-scope denial, the overview/settings/
// connections pages with the live agent data, the managed-settings form
// (change diff, no-op, failure audit), the checked restart, flush, kill,
// and the database-server log viewer (the MySQL/MariaDB sources only).

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
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

// dbServerEnv seeds an administrator (all-scope, all server permissions),
// a customer (bob, no server permissions) and a scoped tech (dave,
// server.view + logs.view but own-scope: the controller-level denial).
// The fake agent answers db.server_status / db.processlist / the
// mutating actions and logs.sources.
func dbServerEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'administrator','Admin','all',''),(2,'customer','Customer','own',''),(3,'tech','Tech','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'server.view','sv','srv'),(3,'server.manage','sm','srv'),
		(4,'services.manage','svc','srv'),(5,'logs.view','lv','logs')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(1,4),(1,5),(2,1),(3,1),(3,2),(3,5)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'dave','d@x','Dave',?,3,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at) VALUES
		(10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1)`)

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

func TestDbServerPageDenied(t *testing.T) {
	h, _, _ := dbServerEnv(t)
	// No server.view at all: the route permission refuses.
	bob := loginAsUser(t, h, "bob")
	if rr := get(t, h, "/databases/server", bob); rr.Code != http.StatusForbidden {
		t.Fatalf("GET /databases/server (customer) = %d, want 403", rr.Code)
	}
	// server.view but own-scope: the controller's all-scope denial.
	dave := loginAsUser(t, h, "dave")
	rr := get(t, h, "/databases/server", dave)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("GET /databases/server (own-scope) = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "managed by server administrators") {
		t.Errorf("denial page missing the message: %s", rr.Body.String())
	}
}

func TestDbServerOverviewPage(t *testing.T) {
	h, sa, _ := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/databases/server", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /databases/server = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"mariadb.org binary distribution",
		"10.11.6-MariaDB",
		"Up 1d 0h",
		"12 / 151",
		"Peak 40 · 2 running",
		"11.6 /s",
		"3 slow · 1 failed logins",
		"1 GB",
		"7 databases · 11 users",
		"8%",
		"50%",
		"99.90%",
		"4.0%",
		"100 of 4000",
		"1 MB / 5 MB",
		"900,000 / 90,000 / 9,000 / 1,000",
		"/run/mysqld/mysqld.sock",
		"127.0.0.1",
		"mariadb.service",
		"Reload privileges",
		"Reopen logs",
		"Unblock hosts",
		"Restart",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview missing %q", want)
		}
	}
	if strings.Contains(body, "low</span>") || strings.Contains(body, "high</span>") {
		t.Errorf("unexpected low/high warning badge")
	}
	if len(sa.called()) != 1 || sa.called()[0] != "db.server_status" {
		t.Errorf("overview agent calls = %v", sa.called())
	}
}

func TestDbServerAgentErrorPage(t *testing.T) {
	h, sa, _ := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	sa.failActions = map[string]string{"db.server_status": "the socket is closed"}
	rr := get(t, h, "/databases/server", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /databases/server (agent down) = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "the socket is closed") || !strings.Contains(body, "could not report the database server") {
		t.Errorf("agent error page missing the error: %s", body)
	}
	if strings.Contains(body, "10.11.6-MariaDB") {
		t.Errorf("a failed status must not render the overview")
	}
}

func TestDbServerDownPage(t *testing.T) {
	h, sa, _ := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	sa.dbDown = true
	rr := get(t, h, "/databases/server", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /databases/server (server down) = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "The database server does not answer") || !strings.Contains(body, "Connection refused (111)") {
		t.Errorf("down page missing the status error: %s", body)
	}
	// The flush buttons need a running server; restart stays (for the
	// services) but the page must not show the overview cards.
	if strings.Contains(body, "Reload privileges") || strings.Contains(body, "10.11.6-MariaDB") {
		t.Errorf("down page must not show the overview or flush buttons")
	}
}

func TestDbServerSettingsPage(t *testing.T) {
	h, _, _ := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/databases/server/settings", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /databases/server/settings = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`name="settings[max_connections]" value="200"`,
		`name="managed[max_connections]" value="200"`,
		`placeholder="151"`,
		"Memory and storage",
		"Connections",
		`placeholder="128 MB"`,
		"utf8mb4_general_ci",
		"utf8mb4_unicode_ci",
		"zz-whacp.cnf",
		"dbs-restart",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("settings page missing %q", want)
		}
	}
	// The bool field renders the ON/OFF select.
	if !strings.Contains(body, `name="settings[skip_name_resolve]"`) || !strings.Contains(body, "<option value=\"ON\">") {
		t.Errorf("bool setting select missing")
	}
}

func TestDbServerSaveSettingsNoOp(t *testing.T) {
	h, sa, _ := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases/server/settings")

	rr := postForm(h, "/databases/server/settings", url.Values{
		"_token":                    {token},
		"settings[max_connections]": {"200"}, "managed[max_connections]": {"200"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save (no change) = %d, want 303", rr.Code)
	}
	for _, a := range sa.called() {
		if a == "db.server_settings" {
			t.Fatalf("no-op save called the agent")
		}
	}
	flashAt(t, h, refreshCookie(admin, rr), "/databases/server/settings", "Nothing changed.")
}

func TestDbServerSaveSettingsChanged(t *testing.T) {
	h, sa, db := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases/server/settings")

	rr := postForm(h, "/databases/server/settings", url.Values{
		"_token":                    {token},
		"settings[max_connections]": {"300"}, "managed[max_connections]": {"200"},
		"settings[innodb_buffer_pool_size]": {"1G"}, "managed[innodb_buffer_pool_size]": {""},
		"settings[character_set_server]": {"utf8mb4"}, "managed[character_set_server]": {"utf8mb4"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d, want 303", rr.Code)
	}
	sent, _ := agentParam(t, sa, "db.server_settings", "settings").(map[string]any)
	if len(sent) != 2 || sent["max_connections"] != "300" || sent["innodb_buffer_pool_size"] != "1G" {
		t.Errorf("agent settings = %v (only the changed fields)", sent)
	}
	if agentParam(t, sa, "db.server_settings", "restart") != false {
		t.Errorf("restart flag = %v", agentParam(t, sa, "db.server_settings", "restart"))
	}
	var targetType, targetID, result, details string
	err := db.QueryRow(`SELECT target_type, target_id, result, details FROM audit_logs WHERE action = 'database.server_settings'`).
		Scan(&targetType, &targetID, &result, &details)
	if err != nil || targetType != "server" || targetID != "database" || result != "success" {
		t.Errorf("settings audit = %q %q %q %v", targetType, targetID, result, err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(details), &body); err != nil {
		t.Fatalf("details: %v", err)
	}
	if s, ok := body["settings"].(map[string]any); !ok || s["max_connections"] != "300" {
		t.Errorf("audit details settings = %v", body["settings"])
	}
	flashAt(t, h, refreshCookie(admin, rr), "/databases/server/settings", "Settings saved and applied.")
}

func TestDbServerSaveSettingsFailure(t *testing.T) {
	h, sa, db := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases/server/settings")
	sa.failActions = map[string]string{"db.server_settings": "the server refused innodb_buffer_pool_size"}

	rr := postForm(h, "/databases/server/settings", url.Values{
		"_token":                            {token},
		"settings[innodb_buffer_pool_size]": {"16T"}, "managed[innodb_buffer_pool_size]": {""},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save (failure) = %d, want 303", rr.Code)
	}
	var result, details string
	err := db.QueryRow(`SELECT result, details FROM audit_logs WHERE action = 'database.server_settings'`).Scan(&result, &details)
	if err != nil || result != "failure" || !strings.Contains(details, "the server refused innodb_buffer_pool_size") {
		t.Errorf("failure audit = %q %q %v", result, details, err)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/databases/server/settings", "the server refused innodb_buffer_pool_size")
}

func TestDbServerRestart(t *testing.T) {
	h, sa, db := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases/server")

	rr := postForm(h, "/databases/server/restart", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("restart = %d, want 303", rr.Code)
	}
	if !inList(sa.called(), "db.server_restart") {
		t.Fatalf("db.server_restart not called: %v", sa.called())
	}
	var result string
	err := db.QueryRow(`SELECT result FROM audit_logs WHERE action = 'database.server_restart'`).Scan(&result)
	if err != nil || result != "success" {
		t.Errorf("restart audit = %q %v", result, err)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/databases/server", "The database server restarted in 2 s and answers again.")
}

func TestDbServerFlush(t *testing.T) {
	h, sa, db := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases/server")

	rr := postForm(h, "/databases/server/flush", url.Values{"_token": {token}, "what": {"hosts"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("flush = %d, want 303", rr.Code)
	}
	if agentParam(t, sa, "db.server_flush", "what") != "hosts" {
		t.Errorf("agent what = %v", agentParam(t, sa, "db.server_flush", "what"))
	}
	var result, details string
	err := db.QueryRow(`SELECT result, details FROM audit_logs WHERE action = 'database.server_flush'`).Scan(&result, &details)
	if err != nil || result != "success" || !strings.Contains(details, `"what":"hosts"`) {
		t.Errorf("flush audit = %q %q %v", result, details, err)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/databases/server", "Blocked hosts cleared.")
}

func TestDbServerKill(t *testing.T) {
	h, sa, db := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases/server/connections")

	rr := postForm(h, "/databases/server/kill", url.Values{"_token": {token}, "id": {"7"}, "query_only": {"1"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("kill = %d, want 303", rr.Code)
	}
	if agentParam(t, sa, "db.kill", "id") != float64(7) {
		t.Errorf("agent id = %v", agentParam(t, sa, "db.kill", "id"))
	}
	if agentParam(t, sa, "db.kill", "query_only") != true {
		t.Errorf("agent query_only = %v", agentParam(t, sa, "db.kill", "query_only"))
	}
	var result, details string
	err := db.QueryRow(`SELECT result, details FROM audit_logs WHERE action = 'database.kill'`).Scan(&result, &details)
	if err != nil || result != "success" || !strings.Contains(details, `"id":7`) {
		t.Errorf("kill audit = %q %q %v", result, details, err)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/databases/server/connections", "The statement of connection 7 was stopped.")
}

func TestDbServerConnectionsPage(t *testing.T) {
	h, sa, _ := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/databases/server/connections", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /databases/server/connections = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"u_bob", "SELECT * FROM posts", "server thread",
		`name="id" value="7"`, `name="query_only" value="1"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("connections page missing %q", want)
		}
	}
	if calls := sa.called(); !inList(calls, "db.processlist") {
		t.Errorf("db.processlist not called: %v", calls)
	}
}

func TestDbServerLogsPage(t *testing.T) {
	h, _, _ := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/databases/server/logs", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /databases/server/logs = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`value="system:mysql" selected`,
		`value="journal:mariadb.service"`,
		"Database logs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("db logs page missing %q", want)
		}
	}
	for _, not := range []string{"nginx:access", "system:auth", "Authentication log"} {
		if strings.Contains(body, not) {
			t.Errorf("db logs page must not list %q", not)
		}
	}
	// A non-all-scope user with logs.view still gets the 403 page.
	dave := loginAsUser(t, h, "dave")
	if rr := get(t, h, "/databases/server/logs", dave); rr.Code != http.StatusForbidden {
		t.Fatalf("db logs (own-scope) = %d, want 403", rr.Code)
	}
}

func TestDbServerMutatingRequiresCSRF(t *testing.T) {
	h, sa, _ := dbServerEnv(t)
	admin := loginAsUser(t, h, "admin")

	rr := postForm(h, "/databases/server/restart", url.Values{}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("restart without token = %d, want 400", rr.Code)
	}
	for _, a := range sa.called() {
		if a == "db.server_restart" {
			t.Fatalf("agent called without a CSRF token: %v", sa.called())
		}
	}
}

// contains is a list membership helper (agent action names).
func inList(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
