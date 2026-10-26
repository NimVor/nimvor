package http

// This suite drives the firewall page (SecurityController's firewall area):
// the page (status bar, the ports WhaCP keeps open, the rules with their
// listening state, the enable form, the CSF-style protection panel, agent
// down), the guarded changes (toggle on/off with allow-me, add/delete rule,
// the protections with their validation) and the confirm route's
// return_to to this page.

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

// firewallEnv seeds an administrator, a customer with security.view only
// (bob — the page, but not the change forms) and a viewer. The fake agent
// answers the firewall actions.
func firewallEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own',''),(3,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'security.view','sv','security'),(3,'security.manage','sm','security')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(2,1),(2,2),(3,1),(3,2)`)
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
	sa.guardChanged = true // guarded changes apply (changed:true + pending)
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

func TestFirewallPage(t *testing.T) {
	h, sa, _ := firewallEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/firewall", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		// Status bar (off, UFW, the listening SSH).
		"Firewall", "UFW", "wcp-state is-error\">Off", "Incoming</span> deny", "SSH port</span> 22",
		// The ports WhaCP keeps open.
		"Ports WhaCP keeps open", "8443", "Panel (HTTPS)", "access", "Opened when turned on",
		// The rules.
		"All rules: 2", "Port rules: 1", "IP rules: 1", "Listening", "SSH", "Bad guy",
		`title="deny from 203.0.113.99"`, "Inbound",
		// The enable form (off + canManage; the test client's public IP).
		"Turn on the firewall", "allow_services", "Allow my address", "192.0.2.1",
		// The protection panel (fresh configuration → the defaults).
		"Protection", "Turn on the protections below", "100", "Exempt addresses and networks", "Apply",
		"Add firewall rule", "data-wcp-confirm=",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if sa.fwActive {
		t.Fatalf("the fake's fwActive defaulted true")
	}
}

func TestFirewallPageOn(t *testing.T) {
	h, sa, _ := firewallEnv(t)
	sa.fwActive = true
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/firewall", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"wcp-state is-ok\">On", "data-wcp-switch-submit",
		`data-wcp-confirm="Turn off the firewall? Every listening service becomes reachable."`,
		"Not allowed", "data-wcp-busy", "Delete the rule",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if pageHas(body, "Turn on the firewall") {
		t.Errorf("the enable form shows while the firewall is on")
	}
}

func TestFirewallPageCustomer(t *testing.T) {
	h, _, _ := firewallEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/security/firewall", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Firewall", "All rules: 2", "disabled",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, not := range []string{
		"Turn on the firewall", "Add firewall rule", "Delete the rule", "data-wcp-switch-submit",
	} {
		if pageHas(body, not) {
			t.Errorf("a change control shows for a viewer-only account: %q", not)
		}
	}
	// …and the guard refuses the change route.
	if rr := postForm(h, "/security/firewall/toggle", url.Values{"enabled": {"0"}}, bob); rr.Code != http.StatusForbidden {
		t.Errorf("customer toggle = %d, want 403", rr.Code)
	}
}

func TestFirewallPageAgentDown(t *testing.T) {
	h, sa, _ := firewallEnv(t)
	sa.failActions = map[string]string{"firewall.status": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/firewall", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !pageHas(body, "the socket is closed") || pageHas(body, "SSH port") {
		t.Errorf("agent-down page wrong: %s", body[:min(300, len(body))])
	}
}

func TestFirewallToggleOff(t *testing.T) {
	h, sa, db := firewallEnv(t)
	sa.fwActive = true
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/firewall")
	rr := postForm(h, "/security/firewall/toggle", url.Values{
		"_token": {token}, "enabled": {"0"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/firewall",
		"Applied. Check that you can still connect, then confirm within 2 minutes — otherwise the change is undone automatically.")
	if got := agentParam(t, sa, "firewall.set_enabled", "enabled"); got != false {
		t.Errorf("enabled = %v", got)
	}
	var target, targetID, result string
	if err := db.QueryRow(`SELECT target_type, target_id, result FROM audit_logs WHERE action = 'firewall.disable'`).Scan(&target, &targetID, &result); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "server" || targetID != "security" || result != "success" {
		t.Errorf("audit = %s/%s/%s", target, targetID, result)
	}
}

func TestFirewallToggleOnAllowMe(t *testing.T) {
	h, sa, _ := firewallEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/firewall")
	// postForm's client address is 203.0.113.50.
	rr := postForm(h, "/security/firewall/toggle", url.Values{
		"_token": {token}, "enabled": {"1"}, "allow_services": {"1"}, "allow_me": {"1"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle on = %d", rr.Code)
	}
	if got := agentParam(t, sa, "firewall.set_enabled", "enabled"); got != true {
		t.Errorf("enabled = %v", got)
	}
	if got := agentParam(t, sa, "firewall.set_enabled", "allow_services"); got != true {
		t.Errorf("allow_services = %v", got)
	}
	if got := agentParam(t, sa, "firewall.set_enabled", "allow_from"); got != "203.0.113.50" {
		t.Errorf("allow_from = %v", got)
	}
}

func TestFirewallToggleNothingChanged(t *testing.T) {
	h, sa, _ := firewallEnv(t)
	sa.guardChanged = false
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/firewall")
	rr := postForm(h, "/security/firewall/toggle", url.Values{
		"_token": {token}, "enabled": {"1"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle on = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/firewall", "Nothing to change.")
}

func TestFirewallAddRule(t *testing.T) {
	h, sa, db := firewallEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/firewall")
	rr := postForm(h, "/security/firewall/rules", url.Values{
		"_token": {token}, "action": {"deny"}, "port": {"3306"}, "proto": {"tcp"},
		"from": {"203.0.113.4"}, "comment": {"MySQL"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("add rule = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/firewall", "confirm within 2 minutes")
	for key, want := range map[string]any{"action": "deny", "port": "3306", "proto": "tcp", "from": "203.0.113.4", "comment": "MySQL"} {
		if got := agentParam(t, sa, "firewall.rule_add", key); got != want {
			t.Errorf("%s = %v", key, got)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'firewall.rule_add'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("firewall.rule_add audits = %d, %v", n, err)
	}
}

func TestFirewallDeleteRule(t *testing.T) {
	h, sa, _ := firewallEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/firewall")
	rr := postForm(h, "/security/firewall/rules/delete", url.Values{
		"_token": {token}, "spec": {"deny from 203.0.113.99"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete rule = %d", rr.Code)
	}
	if got := agentParam(t, sa, "firewall.rule_delete", "spec"); got != "deny from 203.0.113.99" {
		t.Errorf("spec = %v", got)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/firewall", "confirm within 2 minutes")
}

func TestFirewallProtection(t *testing.T) {
	h, sa, db := firewallEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/firewall")
	rr := postForm(h, "/security/firewall/protection", url.Values{
		"_token": {token}, "enabled": {"1"}, "invalid": {"1"},
		"syn_rate": {"100"}, "icmp_rate": {"5"},
		"conn_limits": {"22:10, 80:150"}, "port_flood": {"tcp/22:20"},
		"exempt": {"10.0.0.0/8"}, "exempt_me": {"1"},
	}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/security/firewall#fw-protect" {
		t.Fatalf("protection = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/firewall", "confirm within 2 minutes")
	if got := agentParam(t, sa, "firewall.protection_set", "enabled"); got != true {
		t.Errorf("enabled = %v", got)
	}
	if got := agentParam(t, sa, "firewall.protection_set", "invalid"); got != true {
		t.Errorf("invalid = %v", got)
	}
	if got := agentParam(t, sa, "firewall.protection_set", "syn_rate"); got != float64(100) {
		t.Errorf("syn_rate = %v", got)
	}
	conn, _ := agentParam(t, sa, "firewall.protection_set", "conn_limits").([]any)
	if len(conn) != 2 {
		t.Fatalf("conn_limits = %v", conn)
	}
	first := conn[0].(map[string]any)
	if first["port"] != float64(22) || first["proto"] != "tcp" || first["max"] != float64(10) {
		t.Errorf("conn_limits[0] = %v", first)
	}
	flood, _ := agentParam(t, sa, "firewall.protection_set", "port_flood").([]any)
	if len(flood) != 1 {
		t.Fatalf("port_flood = %v", flood)
	}
	f0 := flood[0].(map[string]any)
	if f0["proto"] != "tcp" || f0["port"] != float64(22) || f0["max"] != float64(20) {
		t.Errorf("port_flood[0] = %v", f0)
	}
	// exempt_me adds the (public) client address to the list.
	exempt, _ := agentParam(t, sa, "firewall.protection_set", "exempt").([]any)
	if strings.Join(strOfs(exempt), ",") != "10.0.0.0/8,203.0.113.50" {
		t.Errorf("exempt = %v", exempt)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'firewall.protection'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("firewall.protection audits = %d, %v", n, err)
	}
}

func TestFirewallProtectionValidation(t *testing.T) {
	h, sa, _ := firewallEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/firewall")
	cases := []struct {
		extra url.Values
		want  string
	}{
		{url.Values{"syn_rate": {"100001"}}, "SYN flood: a number from 0 to 100000."},
		{url.Values{"icmp_rate": {"10001"}}, "Ping: a number from 0 to 10000."},
		{url.Values{"conn_limits": {"22:0"}}, `Connection limits: "22:0" is not like 22:10.`},
		{url.Values{"conn_limits": {"70000:10"}}, `Connection limits: "70000:10" is not like 22:10.`},
		{url.Values{"port_flood": {"80:300"}}, `Port flood: "80:300" is not like tcp/80:300.`},
		{url.Values{"exempt": {"not-an-ip"}}, `Exempt: "not-an-ip" is not an IP address or network.`},
	}
	for _, c := range cases {
		form := url.Values{"_token": {token}, "enabled": {"1"}}
		for k, v := range c.extra {
			form[k] = v
		}
		rr := postForm(h, "/security/firewall/protection", form, admin)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("protection (%s) = %d", c.want, rr.Code)
		}
		flashAt(t, h, refreshCookie(admin, rr), "/security/firewall", c.want)
	}
	for _, a := range sa.called() {
		if strings.HasPrefix(a, "firewall.") && a != "firewall.status" {
			t.Errorf("a refused change reached the agent: %v", sa.called())
		}
	}
}

func TestFirewallAgentFailure(t *testing.T) {
	h, sa, _ := firewallEnv(t)
	sa.failActions = map[string]string{"firewall.set_enabled": "UFW is not running"}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/firewall")
	rr := postForm(h, "/security/firewall/toggle", url.Values{
		"_token": {token}, "enabled": {"1"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/firewall", "UFW is not running")
}

func TestFirewallBadCSRF(t *testing.T) {
	h, _, _ := firewallEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := postForm(h, "/security/firewall/rules", url.Values{"port": {"443"}}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("add rule without token = %d, want 400", rr.Code)
	}
}

func TestSecurityConfirmReturnToFirewall(t *testing.T) {
	h, _, _ := firewallEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/firewall")
	rr := postForm(h, "/security/confirm", url.Values{
		"_token": {token}, "id": {"fwpend-fw"}, "return_to": {"/security/firewall"},
	}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/security/firewall" {
		t.Fatalf("confirm = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/firewall", "Change confirmed and kept.")
}

// strOfs renders the fake's recorded string list (exempt and friends).
func strOfs(vs []any) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, strOf(v))
	}
	return out
}
