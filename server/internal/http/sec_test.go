package http

// This suite drives the Security overview (SecurityController::index): the
// checks (the agent's security.audit + the panel's own checks, the sorting,
// the safety rating, the fix/undo actions and the open links), the
// SSH/panel login cards, the fail2ban note, and the anonymous MySQL users
// removal (security.mysql_anonymous).

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

// secEnv seeds an administrator and a customer (security.view only). The
// fake agent answers the overview's actions (security.audit, ssh.logins,
// hardening.status, db.drop_anonymous_users) on top of the shared ones.
func secEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'websites.view','v','web'),(2,'security.view','sv','security'),(3,'security.manage','sm','security'),
		(4,'server.manage','srv','server'),(5,'audit.view','av','audit')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(1,4),(1,5),(2,1),(2,2)`)
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

func TestSecurityOverview(t *testing.T) {
	h, _, _ := secEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		// The page + the safety rating (the fixture's: 4 fail, 6 warn, 3
		// pass, 1 info; 12.5 of 28 weighted points).
		"Security overview of the server", "Safety rating", "45 / 100", "Insecure",
		"4 to fix", "6 to review", "3 passed",
		// The checks (pass details, the fail remediation first + its detail).
		"UFW is active with 3 rules (default incoming: deny).",
		"Set root login to keys only or no under Security → SSH.",
		"root can log in with a password.",
		// The fix/undo actions (server.manage).
		"Remove anonymous users", `action="/security/mysql-anonymous"`,
		"Apply recommended settings", `name="check" value="kernel_settings"`, `name="op" value="apply"`,
		"Turn on automatic security updates", `name="check" value="auto_updates"`,
		"Forget the files marked as known", "btn-outline-secondary",
		`name="check" value="host_persistence"`, `name="op" value="undo"`,
		// The open links (the non-pass checks with a ported page).
		`href="/security/ssh"`, `href="/security/fail2ban"`,
		// The SSH logins (the agent's journal read).
		"SSH logins today", "Failed today", "wcp-w-95", "(publickey)",
		"Last failure: root from 198.51.100.77", "2026-10-22 08:12 UTC",
		"SSH brute-force protection (fail2ban) is on.",
		// The panel login (the audit log).
		"Panel login", "Audit log",
		// The panel's own checks (the settings defaults + hardening).
		"Password strength requirements are strong (minimum strength 65).",
		"cPHulk Brute Force Protection is disabled.",
		"Fork bomb protection is disabled.",
		"Traceroute is enabled for every user.",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The passing firewall check shows no "Open" link (non-pass only).
	if pageHas(body, `href="/security/firewall"`) {
		t.Error("a passing check must not offer an Open link")
	}
	// The sorting: status (fail, warn, pass, info) then title.
	titles := []string{
		"MySQL anonymous users", "SSH root login", "Security updates",
		"Automatic security updates", "Brute-force protection", "Fork bomb protection",
		"Recommended kernel settings", "SSH password logins", "Traceroute status",
		"Firewall", "Host persistence", "Password strength requirements", "SSH server",
	}
	for i := 0; i+1 < len(titles); i++ {
		if strings.Index(body, titles[i+1]) < strings.Index(body, titles[i]) {
			t.Errorf("checks out of order: %q before %q", titles[i+1], titles[i])
		}
	}
}

func TestSecurityOverviewCustomer(t *testing.T) {
	h, _, _ := secEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/security", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Safety rating", "45 / 100", `href="/security/ssh"`} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, not := range []string{
		"Remove anonymous users", "Apply recommended settings",
		"Forget the files marked as known", "Audit log",
	} {
		if pageHas(body, not) {
			t.Errorf("a view-only customer must not see %q", not)
		}
	}
}

func TestSecurityOverviewAgentDown(t *testing.T) {
	h, sa, _ := secEnv(t)
	sa.failActions = map[string]string{"security.audit": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !pageHas(body, "the socket is closed") {
		t.Error("the agent error is not shown")
	}
	for _, not := range []string{"Safety rating", "Last login information"} {
		if pageHas(body, not) {
			t.Errorf("the agent-down page must not show %q", not)
		}
	}
}

func TestSecurityOverviewProtectionOff(t *testing.T) {
	h, sa, _ := secEnv(t)
	sa.f2bRunning = false
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !pageHas(body, "SSH brute-force protection is off") || !pageHas(body, "Set up protection") {
		t.Error("the protection-off note is missing")
	}
	if pageHas(body, "protection (fail2ban) is on") {
		t.Error("the protection-on note must not show")
	}
}

func TestSecurityOverviewPending(t *testing.T) {
	h, sa, _ := secEnv(t)
	sa.secPending = `{"id":"pend1","kind":"ssh","summary":"SSH settings changed","deadline":"2026-01-01T12:03:00Z"}`
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"Unconfirmed change", "SSH settings changed", `name="return_to" value="/security"`} {
		if !pageHas(body, want) {
			t.Errorf("pending banner missing %q", want)
		}
	}
}

func TestSecurityDropAnonymous(t *testing.T) {
	h, sa, db := secEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security")
	rr := postForm(h, "/security/mysql-anonymous", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("drop = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security", "Removed the anonymous MySQL users (@localhost, @).")
	called := false
	for _, a := range sa.called() {
		if a == "db.drop_anonymous_users" {
			called = true
		}
	}
	if !called {
		t.Errorf("agent actions: %v", sa.called())
	}
	var target, result string
	if err := db.QueryRow(`SELECT target_id, result FROM audit_logs WHERE action = 'db.drop_anonymous_users'`).Scan(&target, &result); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "mysql" || result != "success" {
		t.Errorf("audit = %s/%s", target, result)
	}
}

func TestSecurityDropAnonymousNone(t *testing.T) {
	h, sa, _ := secEnv(t)
	sa.anonRemoved = "none"
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security")
	rr := postForm(h, "/security/mysql-anonymous", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("drop = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security", "There were no anonymous users.")
}

func TestSecurityDropAnonymousDenied(t *testing.T) {
	h, sa, _ := secEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/security")
	rr := postForm(h, "/security/mysql-anonymous", url.Values{"_token": {token}}, bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("drop (bob) = %d, want 403", rr.Code)
	}
	for _, a := range sa.called() {
		if a == "db.drop_anonymous_users" {
			t.Error("a refused removal reached the agent")
		}
	}
}

func TestSecurityDropAnonymousAgentFailure(t *testing.T) {
	h, sa, _ := secEnv(t)
	sa.failActions = map[string]string{"db.drop_anonymous_users": "the agent refused"}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security")
	rr := postForm(h, "/security/mysql-anonymous", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("drop = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security", "the agent refused")
}

func TestSecurityDropAnonymousBadCSRF(t *testing.T) {
	h, _, _ := secEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := postForm(h, "/security/mysql-anonymous", url.Values{}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("drop without token = %d, want 400", rr.Code)
	}
}
