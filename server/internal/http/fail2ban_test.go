package http

// This suite drives the fail2ban page (SecurityController's fail2ban area:
// the jails, the banned addresses with unban, the protection-settings form)
// and the overview's fix action (POST /security/fix — the server-wide
// security fixes with their undo, administrators only).

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

// fail2banEnv seeds an administrator, a customer (bob — server.manage but no
// security.manage, for the fix's role gate) and one remote database user
// (the database_network fix's refusal). The fake agent answers the fail2ban
// and fix actions.
func fail2banEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'websites.view','v','web'),(2,'security.view','sv','security'),
		(3,'security.manage','sm','security'),(4,'server.manage','srv','server')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(1,4),(2,1),(2,2),(2,4)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','')`,
		loginHash, loginHash)
	exec(`INSERT INTO database_users (server_id,username,host,created_at) VALUES
		(1,'remote','203.0.113.77',''),(1,'local','localhost','')`)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sa, socket, tokenFile := startSitesAgent(t, false)
	sa.f2bInstalled = true
	sa.f2bManaged = "bantime = 30m\nfindtime = 5m\nmaxretry = 3\n" +
		"ignoreip = 127.0.0.1/8 ::1 203.0.113.100\n\n[sshd]\nenabled = true\n\n[nginx-botsearch]\nenabled = true\n"
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

func TestFail2banPage(t *testing.T) {
	h, sa, _ := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/fail2ban", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		// Status bar.
		"Brute-force protection (fail2ban)", "wcp-state is-ok\">Running", "Active jails</span> 2",
		// The jails.
		">sshd<", ">recidive<", "wcp-bad",
		// The banned addresses.
		"203.0.113.66", "198.51.100.9", "Unban",
		// The settings form (the managed file's values).
		"Protection settings", "SSH login failures", "value=\"30\"", "value=\"5\"", "value=\"3\"", "203.0.113.100",
		`id="j-sshd" checked`, `id="j-nginx-botsearch" checked`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "No active jails.") {
		t.Errorf("the empty-jails row shows although there are jails")
	}
	// recidive is not in the managed file's enabled list.
	if strings.Contains(body, `id="j-recidive" checked`) {
		t.Errorf("recidive checked although not in the managed file")
	}
	if !sa.f2bInstalled {
		t.Fatalf("the fake's f2bInstalled defaulted false")
	}
}

func TestFail2banPageDefaults(t *testing.T) {
	h, sa, _ := fail2banEnv(t)
	sa.f2bManaged = "" // fresh install: no managed file
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/fail2ban", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{`value="60"`, `value="10"`, `value="5"`, `id="j-sshd" checked`, `id="j-recidive" checked`} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, "203.0.113.100") {
		t.Errorf("the ignore list shows the old managed file")
	}
}

func TestFail2banPageNotInstalled(t *testing.T) {
	h, sa, _ := fail2banEnv(t)
	sa.f2bInstalled = false
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/fail2ban", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "fail2ban is not installed") {
		t.Errorf("the not-installed state is missing")
	}
}

func TestFail2banPageAgentDown(t *testing.T) {
	h, sa, _ := fail2banEnv(t)
	sa.failActions = map[string]string{"fail2ban.status": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/fail2ban", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "the socket is closed") || strings.Contains(body, "Brute-force protection (fail2ban)") {
		t.Errorf("agent-down page wrong: %s", body[:min(300, len(body))])
	}
}

func TestFail2banPageCustomer(t *testing.T) {
	h, _, _ := fail2banEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/security/fail2ban", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, not := range []string{"Protection settings", "Unban", "Test and apply"} {
		if strings.Contains(body, not) {
			t.Errorf("a change control shows for a non-manager: %q", not)
		}
	}
	if rr := postForm(h, "/security/fail2ban", url.Values{"ban_minutes": {"60"}}, bob); rr.Code != http.StatusForbidden {
		t.Errorf("customer configure = %d, want 403", rr.Code)
	}
}

func TestFail2banConfigure(t *testing.T) {
	h, sa, db := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	rr := postForm(h, "/security/fail2ban", url.Values{
		"_token": {token},
		"jails[]": {"sshd", "nginx-botsearch"},
		"ban_minutes": {"30"}, "find_minutes": {"5"}, "max_retry": {"3"},
		"ignore": {"10.0.0.0/8  203.0.113.4"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban", "fail2ban settings tested and applied.")
	jails, _ := agentParam(t, sa, "fail2ban.configure", "jails").([]any)
	if len(jails) != 2 || strOf(jails[0]) != "sshd" || strOf(jails[1]) != "nginx-botsearch" {
		t.Errorf("jails = %v", jails)
	}
	if got := agentParam(t, sa, "fail2ban.configure", "ban_minutes"); got != float64(30) {
		t.Errorf("ban_minutes = %v", got)
	}
	ignore, _ := agentParam(t, sa, "fail2ban.configure", "ignore").([]any)
	if strings.Join(strOfs(ignore), ",") != "10.0.0.0/8,203.0.113.4" {
		t.Errorf("ignore = %v", ignore)
	}
	var target, result string
	if err := db.QueryRow(`SELECT target_type, result FROM audit_logs WHERE action = 'fail2ban.configure'`).Scan(&target, &result); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "server" || result != "success" {
		t.Errorf("audit = %s/%s", target, result)
	}
}

func TestFail2banConfigureDefaults(t *testing.T) {
	h, sa, _ := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	rr := postForm(h, "/security/fail2ban", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d", rr.Code)
	}
	jails, _ := agentParam(t, sa, "fail2ban.configure", "jails").([]any)
	if len(jails) != 0 {
		t.Errorf("jails = %v", jails)
	}
	if got := agentParam(t, sa, "fail2ban.configure", "ban_minutes"); got != float64(60) {
		t.Errorf("ban_minutes = %v", got)
	}
	if got := agentParam(t, sa, "fail2ban.configure", "find_minutes"); got != float64(10) {
		t.Errorf("find_minutes = %v", got)
	}
	if got := agentParam(t, sa, "fail2ban.configure", "max_retry"); got != float64(5) {
		t.Errorf("max_retry = %v", got)
	}
}

func TestFail2banUnban(t *testing.T) {
	h, sa, db := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	rr := postForm(h, "/security/fail2ban/unban", url.Values{
		"_token": {token}, "jail": {"sshd"}, "ip": {"203.0.113.66"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unban = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban", "Address unbanned.")
	if got := agentParam(t, sa, "fail2ban.unban", "jail"); got != "sshd" {
		t.Errorf("jail = %v", got)
	}
	if got := agentParam(t, sa, "fail2ban.unban", "ip"); got != "203.0.113.66" {
		t.Errorf("ip = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'fail2ban.unban'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("fail2ban.unban audits = %d, %v", n, err)
	}
}

func TestFail2banUnbanBadIP(t *testing.T) {
	h, sa, _ := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	rr := postForm(h, "/security/fail2ban/unban", url.Values{
		"_token": {token}, "jail": {"sshd"}, "ip": {"not-an-ip"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unban = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban", "That is not an IP address.")
	for _, a := range sa.called() {
		if a == "fail2ban.unban" {
			t.Errorf("a refused unban reached the agent: %v", sa.called())
		}
	}
}

func TestFail2banAgentFailure(t *testing.T) {
	h, sa, _ := fail2banEnv(t)
	sa.failActions = map[string]string{"fail2ban.configure": "fail2ban-client said no"}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	rr := postForm(h, "/security/fail2ban", url.Values{"_token": {token}, "ban_minutes": {"60"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban", "fail2ban-client said no")
}

func TestFail2banBadCSRF(t *testing.T) {
	h, _, _ := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := postForm(h, "/security/fail2ban", url.Values{"ban_minutes": {"60"}}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("configure without token = %d, want 400", rr.Code)
	}
}

func TestSecurityFixAdminGate(t *testing.T) {
	h, _, _ := fail2banEnv(t)
	// bob has server.manage (the guard) but is not role admin.
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/security/fail2ban")
	rr := postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"kernel_settings"}, "op": {"apply"},
	}, bob)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/security" {
		t.Fatalf("fix = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(bob, rr), "/security/fail2ban", "Only administrators change server-wide security settings.")
}

func TestSecurityFixUnknown(t *testing.T) {
	h, _, _ := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	for check, op := range map[string]string{"bogus": "apply", "database_network": "undo"} {
		rr := postForm(h, "/security/fix", url.Values{
			"_token": {token}, "check": {check}, "op": {op},
		}, admin)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("fix (%s/%s) = %d", check, op, rr.Code)
		}
		flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban", "Unknown fix.")
	}
}

func TestSecurityFixKernel(t *testing.T) {
	h, sa, db := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	rr := postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"kernel_settings"}, "op": {"apply"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("fix = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban",
		"Recommended kernel settings applied: net.ipv4.conf.all.rp_filter.")
	if got := agentParam(t, sa, "security.fix_kernel", "undo"); got != false {
		t.Errorf("undo = %v", got)
	}
	var target string
	if err := db.QueryRow(`SELECT target_id FROM audit_logs WHERE action = 'security.fix'`).Scan(&target); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "kernel_settings" {
		t.Errorf("audit target = %s", target)
	}
	// …and the undo.
	rr = postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"kernel_settings"}, "op": {"undo"},
	}, admin)
	if got := agentParam(t, sa, "security.fix_kernel", "undo"); got != true {
		t.Errorf("undo = %v", got)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban", "The earlier kernel settings are back.")
}

func TestSecurityFixHideProcesses(t *testing.T) {
	h, sa, _ := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	rr := postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"proc_hidepid"}, "op": {"apply"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("fix = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban", "Each account now sees only its own processes.")
	if got := agentParam(t, sa, "security.hide_processes", "enabled"); got != true {
		t.Errorf("enabled = %v", got)
	}
	rr = postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"proc_hidepid"}, "op": {"undo"},
	}, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban", "Every account sees all processes again.")
	if got := agentParam(t, sa, "security.hide_processes", "enabled"); got != false {
		t.Errorf("enabled = %v", got)
	}
}

func TestSecurityFixAutoUpdates(t *testing.T) {
	h, sa, db := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	rr := postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"auto_updates"}, "op": {"apply"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("fix = %d", rr.Code)
	}
	if got := agentParam(t, sa, "security.auto_updates", "enabled"); got != true {
		t.Errorf("enabled = %v", got)
	}
	// The job is tracked for the /jobs viewer…
	var kind, targetType, targetID string
	if err := db.QueryRow(`SELECT kind, target_type, target_id FROM agent_jobs WHERE job_id = 'ab12cd34ef56ab78'`).Scan(&kind, &targetType, &targetID); err != nil {
		t.Fatalf("tracked job: %v", err)
	}
	if kind != "Turn on automatic security updates" || targetType != "server" || targetID != "1" {
		t.Errorf("tracked job = %s %s/%s", kind, targetType, targetID)
	}
	// …and the job + success flashes ride the next page.
	rr = get(t, h, "/security/fail2ban", refreshCookie(admin, rr))
	if rr.Code != http.StatusOK {
		t.Fatalf("page = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Job <code>ab12cd34ef56ab78</code> started in the background.",
		"Turning on automatic security updates…",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestSecurityFixHostKnown(t *testing.T) {
	h, sa, _ := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	rr := postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"host_persistence"}, "op": {"apply"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("fix = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban",
		"Marked as known: cron/daily-backup, systemd/whacp-agent.service.")
	if got := agentParam(t, sa, "security.host_known", "forget"); got != false {
		t.Errorf("forget = %v", got)
	}
	rr = postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"host_persistence"}, "op": {"undo"},
	}, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban", "The files marked as known are reported again.")
	if got := agentParam(t, sa, "security.host_known", "forget"); got != true {
		t.Errorf("forget = %v", got)
	}
}

func TestSecurityFixDatabaseNetwork(t *testing.T) {
	h, sa, db := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/fail2ban")
	// A remote database user refuses the fix…
	rr := postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"database_network"}, "op": {"apply"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("fix = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban",
		"These database users connect from other hosts: remote@203.0.113.77. Remove them (or change their host to localhost) first, or keep remote access.")
	for _, a := range sa.called() {
		if a == "db.server_settings" {
			t.Fatalf("the refused fix reached the agent: %v", sa.called())
		}
	}
	// …and without one it applies (restarting the database server).
	if _, err := db.Exec(`DELETE FROM database_users WHERE username = 'remote'`); err != nil {
		t.Fatal(err)
	}
	rr = postForm(h, "/security/fix", url.Values{
		"_token": {token}, "check": {"database_network"}, "op": {"apply"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("fix = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/fail2ban",
		"The database server now accepts connections from this server only.")
	if got := agentParam(t, sa, "db.server_settings", "restart"); got != true {
		t.Errorf("restart = %v", got)
	}
}

func TestSecurityFixBadCSRF(t *testing.T) {
	h, _, _ := fail2banEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := postForm(h, "/security/fix", url.Values{"check": {"kernel_settings"}, "op": {"apply"}}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("fix without token = %d, want 400", rr.Code)
	}
}
