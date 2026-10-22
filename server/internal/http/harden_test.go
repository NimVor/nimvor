package http

// This suite drives the hardening pages (HardeningController): Compiler
// Access (the page + the save with the account validation), SMTP
// Restrictions, Outgoing Connections (the port-list validation), Shell
// Fork Bomb Protection and Traceroute — the all-scope gate, the role-admin
// save gate, the agent calls, the audits and the flashes.

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

// hardenEnv seeds an administrator, a customer (security.manage but not the
// admin role — the role gate; own scope — the all-scope gate) and two
// website accounts (the compilers' allowed users). The fake agent answers
// hardening.status + the five hardening.* write actions.
func hardenEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
	// bob (customer) holds security.manage (the POST guard) but is not the
	// admin role, so his save reaches the role gate — mirroring
	// fail2banEnv's server.manage seed.
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(2,1),(2,2),(2,3)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','')`,
		loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,domain,system_user,root,document_root,kind,created_at) VALUES
		(1,1,'a.example.com','u1-site','/home/u1-site','/home/u1-site/public_html','php',''),
		(2,1,'b.example.com','u2-site','/home/u2-site','/home/u2-site/public_html','php','')`)

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

func TestHardeningCompilersPage(t *testing.T) {
	h, _, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/compilers", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Compiler access for unprivileged users:", "Disabled",
		"the <code>whacp-compiler</code> group",
		"Deny compiler access to unprivileged users",
		`name="users[]" value="u1-site"`, `name="users[]" value="u2-site"`,
		"/usr/bin/gcc", "0750",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// u1-site is a member (checked), u2-site is not.
	i1 := strings.Index(body, `value="u1-site"`)
	i2 := strings.Index(body, `value="u2-site"`)
	if i1 < 0 || i2 < 0 {
		t.Fatalf("account checkboxes missing: %s", body)
	}
	if !strings.Contains(body[i1:i1+120], "checked") || strings.Contains(body[i2:i2+120], "checked") {
		t.Error("the member checkboxes are wrong")
	}
}

func TestHardeningCompilersSave(t *testing.T) {
	h, sa, db := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/compilers")
	rr := postForm(h, "/security/compilers", url.Values{
		"_token": {token}, "restricted": {"1"}, "users[]": {"u2-site", "u1-site"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/compilers", "Compilers are restricted. Allowed: u2-site, u1-site.")
	if got := agentParam(t, sa, "hardening.compilers", "restricted"); got != true {
		t.Errorf("restricted = %v", got)
	}
	if got := agentParam(t, sa, "hardening.compilers", "users"); got != []any{"u2-site", "u1-site"} {
		t.Errorf("users = %v", got)
	}
	var target, result string
	if err := db.QueryRow(`SELECT target_id, result FROM audit_logs WHERE action = 'hardening.compilers'`).Scan(&target, &result); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "1" || result != "success" {
		t.Errorf("audit = %s/%s", target, result)
	}
}

func TestHardeningCompilersSaveRoot(t *testing.T) {
	h, sa, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/compilers")
	rr := postForm(h, "/security/compilers", url.Values{"_token": {token}, "restricted": {"1"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/compilers", "Compilers are restricted to root.")
	if got := agentParam(t, sa, "hardening.compilers", "users"); got != []any{} {
		t.Errorf("users = %v (root-only sends none)", got)
	}
}

func TestHardeningCompilersSaveUnknownUser(t *testing.T) {
	h, sa, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/compilers")
	rr := postForm(h, "/security/compilers", url.Values{
		"_token": {token}, "restricted": {"1"}, "users[]": {"ghost"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/compilers", "Not a website account on this server: ghost.")
	for _, a := range sa.called() {
		if a == "hardening.compilers" {
			t.Error("a refused save reached the agent")
		}
	}
}

func TestHardeningSmtpPage(t *testing.T) {
	h, _, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/smtp", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<span class="wcp-state is-ok">Enabled</span>`,
		"ports 25, 465, 587 are allowed for:", "root, postfix, mailman, whacp",
		`name="enabled" value="0"`, ">Disable<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestHardeningSmtpSave(t *testing.T) {
	h, sa, db := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/smtp")
	rr := postForm(h, "/security/smtp", url.Values{"_token": {token}, "enabled": {"0"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/smtp", "SMTP restrictions are off.")
	if got := agentParam(t, sa, "hardening.smtp", "enabled"); got != false {
		t.Errorf("enabled = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'hardening.smtp'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("hardening.smtp audits = %d, %v", n, err)
	}
}

func TestHardeningEgressPage(t *testing.T) {
	h, _, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/egress", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<span class="wcp-state is-ok">Active</span>`,
		"3 website account(s)", "169.254.169.254", "fd00:ec2::254",
		`name="tcp_ports" value="53, 80, 443"`, `name="udp_ports" value="53"`,
		`name="metadata_blocked" value="1" id="eg-meta" checked`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// block_private is not set in the fixture → no checked.
	if strings.Contains(body, `name="block_private" value="1" id="eg-private" checked`) {
		t.Error("block_private must not be checked")
	}
}

func TestHardeningEgressSave(t *testing.T) {
	h, sa, db := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/egress")
	rr := postForm(h, "/security/egress", url.Values{
		"_token": {token}, "metadata_blocked": {"1"},
		"tcp_ports": {" 80 , 443 80"}, "udp_ports": {""},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/egress", "Outgoing connection limits for website accounts saved.")
	if got := agentParam(t, sa, "hardening.egress", "tcp_ports"); got != []any{float64(80), float64(443)} {
		t.Errorf("tcp_ports = %v (deduplicated + sorted)", got)
	}
	if got := agentParam(t, sa, "hardening.egress", "udp_ports"); got != []any{} {
		t.Errorf("udp_ports = %v", got)
	}
	if got := agentParam(t, sa, "hardening.egress", "block_private"); got != false {
		t.Errorf("block_private = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'hardening.egress'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("hardening.egress audits = %d, %v", n, err)
	}
}

func TestHardeningEgressSaveBadPorts(t *testing.T) {
	h, sa, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/egress")
	cases := []struct {
		tcp, udp, want string
	}{
		{"abc", "", `TCP ports: "abc" is not a port number (1–65535).`},
		{"70000", "", `TCP ports: "70000" is not a port number (1–65535).`},
		{"", "0", `UDP ports: "0" is not a port number (1–65535).`},
	}
	for _, c := range cases {
		rr := postForm(h, "/security/egress", url.Values{
			"_token": {token}, "tcp_ports": {c.tcp}, "udp_ports": {c.udp},
		}, admin)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("save (%s) = %d", c.want, rr.Code)
		}
		flashAt(t, h, refreshCookie(admin, rr), "/security/egress", c.want)
	}
	for _, a := range sa.called() {
		if a == "hardening.egress" {
			t.Errorf("a refused save reached the agent: %v", sa.called())
		}
	}
}

func TestHardeningForkbombPage(t *testing.T) {
	h, _, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/forkbomb", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<span class="wcp-state is-off">Disabled</span>`,
		"limited to 200 processes (hard limit 300)",
		`name="enabled" value="1"`, ">Enable Protection<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestHardeningForkbombSave(t *testing.T) {
	h, sa, db := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/forkbomb")
	rr := postForm(h, "/security/forkbomb", url.Values{"_token": {token}, "enabled": {"1"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/forkbomb", "Fork bomb protection is on. It applies to new logins.")
	if got := agentParam(t, sa, "hardening.forkbomb", "enabled"); got != true {
		t.Errorf("enabled = %v", got)
	}
	var target string
	if err := db.QueryRow(`SELECT target_id FROM audit_logs WHERE action = 'hardening.forkbomb'`).Scan(&target); err != nil || target != "1" {
		t.Errorf("audit target = %q, %v", target, err)
	}
}

func TestHardeningTraceroutePage(t *testing.T) {
	h, _, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/traceroute", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<span class="wcp-state is-warn">Enabled</span>`,
		"/usr/bin/traceroute", "0755",
		`name="enabled" value="0"`, ">Disable<",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestHardeningTracerouteSave(t *testing.T) {
	h, sa, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/traceroute")
	rr := postForm(h, "/security/traceroute", url.Values{"_token": {token}, "enabled": {"0"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/traceroute", "Traceroute is disabled for users other than root.")
	if got := agentParam(t, sa, "hardening.traceroute", "enabled"); got != false {
		t.Errorf("enabled = %v", got)
	}
}

func TestHardeningAgentDown(t *testing.T) {
	h, sa, _ := hardenEnv(t)
	sa.failActions = map[string]string{"hardening.status": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/compilers", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "the socket is closed") {
		t.Error("the agent error is not shown")
	}
	for _, not := range []string{"Compiler access for unprivileged users", "Compilers on this server"} {
		if strings.Contains(body, not) {
			t.Errorf("the agent-down page must not show %q", not)
		}
	}
}

func TestHardeningDeniedScope(t *testing.T) {
	h, _, _ := hardenEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/security/compilers", bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("GET (bob) = %d, want 403", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Only administrators manage server hardening.") {
		t.Errorf("the 403 message is wrong: %s", rr.Body.String())
	}
}

func TestHardeningSaveDeniedRole(t *testing.T) {
	h, sa, _ := hardenEnv(t)
	// bob holds security.manage (the guard) but is not the admin role — the
	// save must stop at the role gate. The CSRF token is session-scoped, so
	// it comes from a page bob may view (the hardening pages 403 for him).
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/security/fail2ban")
	rr := postForm(h, "/security/forkbomb", url.Values{"_token": {token}, "enabled": {"1"}}, bob)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/security/forkbomb" {
		t.Fatalf("save (bob) = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(bob, rr), "/security/fail2ban", "Only administrators change server hardening.")
	for _, a := range sa.called() {
		if a == "hardening.forkbomb" {
			t.Error("a refused save reached the agent")
		}
	}
}

func TestHardeningBadCSRF(t *testing.T) {
	h, _, _ := hardenEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := postForm(h, "/security/smtp", url.Values{"enabled": {"0"}}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("save without token = %d, want 400", rr.Code)
	}
}
