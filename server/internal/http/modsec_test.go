package http

// This suite drives the ModSecurity page (ModSecurityController: the
// server-wide settings, the custom rules, the per-website settings, the
// background install/update/rollback jobs) and a website's Web firewall
// page (the mode switch, turning a rule off from an event).

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
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

// modsecEnv seeds an administrator, a customer (bob) who owns website 10
// (bob.example), and the recording agent.
func modsecEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'security.view','sv','security'),(4,'security.manage','sm','security')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(1,4),(2,1),(2,2)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','')`,
		loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','','')`)
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

// modsecRuleTexts are the fake agent's custom rules (custom_get), the texts
// the rule forms' sha fields must match.
var modsecRuleTexts = [2]string{
	`SecRule REQUEST_URI "@beginsWith /xmlrpc.php" "id:10001,phase:1,deny,status:403,log,msg:'xmlrpc blocked'"`,
	`SecAction "id:10002,phase:1,nolog,pass,secmark='EXECCMD'"`,
}

func sha256Of(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestModsecPageRenders(t *testing.T) {
	h, _, _ := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/modsec", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		// The status bar.
		"Nginx connector working", "package libnginx-mod-http-modsecurity",
		"OWASP CRS</span> 1.1", `wcp-state is-ok">Active`, "120.6 KB",
		// The subtabs.
		`Websites <span class="badge rounded-pill text-bg-light border fw-normal">1</span>`,
		`Recent events <span class="badge rounded-pill text-bg-light border fw-normal">2</span>`,
		`Custom rules <span class="badge rounded-pill text-bg-light border fw-normal">2</span>`,
		// The websites tab (bob.example's setting from the agent's status).
		`href="/sites/10/firewall">bob.example`, ">bob<", `wcp-state is-ok">On`,
		">OWASP Core Rule Set<", ">942100<", `data-bs-target="#ms-edit-10"`,
		// The events tab.
		`<a href="/sites/10/firewall">bob.example</a>`, "unknown.example",
		`wcp-state is-error">Blocked (403)`, `wcp-state is-warn">Detected`,
		">942100<", "SQL Injection Attack", "203.0.113.9",
		// The custom rules tab.
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if pageHas(body, "949110") {
		t.Errorf("the anomaly-score summary rule 949110 must not be shown")
	}
	for _, want := range []string{
		// The vendors tab.
		">OWASP Core Rule Set<", "900000–999999", "Generic protection",
		">Custom rules<", "10000–999999",
		// The custom rules table.
		">10001<", "xmlrpc blocked", ">deny<", "10001", // the id cell
		`data-bs-target="#ms-rule-0"`, `name="sha" value="` + sha256Of(modsecRuleTexts[0]) + `"`,
		// The installation tab.
		"2026-10-01 08:00 UTC", "package", `wcp-state is-ok">Done`, "in effect",
		"Nginx 1.24.0", "Debian 12", "libnginx-mod-http-modsecurity 3.0.10-1",
		`/security/modsec/rollback/2026-10-01T08:00:00Z-a1b2c3d4`,
		"records are kept 30 days (Settings)",
		// The settings modal (the fake's config).
		`id="ms-on" name="enabled" value="1" checked`, `id="ms-crs" name="crs_enabled" value="1" checked`,
		`<option value="1" selected>1 — few false positives (recommended)</option>`,
		`name="inbound_threshold" value="5"`, `name="outbound_threshold" value="4"`,
		// The rule modals.
		`id="ms-rule-new"`, `data-bs-target="#ms-rule-0"`, `data-bs-target="#ms-rule-1"`,
		"New custom rule", "Custom rule 10001", "Custom rule 10002",
		`id:10003,phase:1,deny,status:403,log,msg:'example'`, // the suggested id
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The custom rules are disabled in the fake's config.
	if !pageHas(body, "not in use (Server-wide settings)") {
		t.Errorf("missing the custom-rules-disabled state")
	}
}

func TestModsecPageDeniedForNonAllScope(t *testing.T) {
	h, _, _ := modsecEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/security/modsec", bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("GET = %d (want 403)", rr.Code)
	}
	if !pageHas(rr.Body.String(), "ModSecurity rules are managed by server administrators.") {
		t.Errorf("missing the deny message")
	}
}

func TestModsecPageAgentError(t *testing.T) {
	h, sa, _ := modsecEnv(t)
	sa.failActions = map[string]string{"modsec.status": "the agent socket is not running"}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/modsec", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !pageHas(body, "the agent socket is not running") {
		t.Errorf("missing the agent warning")
	}
	if pageHas(body, "Web application firewall (ModSecurity)") {
		t.Errorf("the page content must not render without a status")
	}
}

func modsecConfigForm(t *testing.T, h http.Handler, admin *http.Cookie) (string, *http.Cookie) {
	t.Helper()
	return siteFormToken(t, h, admin, "/security/modsec")
}

func TestModsecConfigure(t *testing.T) {
	h, sa, db := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := modsecConfigForm(t, h, admin)
	form := url.Values{
		"_token":             {token},
		"enabled":            {"1"},
		"crs_enabled":        {"1"},
		"paranoia":           {"2"},
		"inbound_threshold":  {"6"},
		"outbound_threshold": {"4"},
		"detection_only":     {"1"},
		"custom_enabled":     {"1"},
	}
	rr := postForm(h, "/security/modsec", form, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "ModSecurity settings saved and loaded.")
	// The agent got the server-wide config.
	crs, _ := agentParam(t, sa, "modsec.configure", "crs").(map[string]any)
	if crs == nil || crs["enabled"] != true || crs["paranoia"] != float64(2) || crs["inbound_threshold"] != float64(6) {
		t.Errorf("modsec.configure crs = %v", crs)
	}
	if enabled, _ := agentParam(t, sa, "modsec.configure", "enabled").(bool); !enabled {
		t.Errorf("modsec.configure enabled = %v", enabled)
	}
	if det, _ := agentParam(t, sa, "modsec.configure", "detection_only").(bool); !det {
		t.Errorf("modsec.configure detection_only = %v", det)
	}
	var action string
	if err := db.QueryRow(`SELECT action FROM audit_logs WHERE action='modsec.configure' ORDER BY id DESC LIMIT 1`).Scan(&action); err != nil {
		t.Fatal(err)
	}
}

func TestModsecConfigureOff(t *testing.T) {
	h, _, _ := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := modsecConfigForm(t, h, admin)
	form := url.Values{"_token": {token}, "enabled": {"0"}, "paranoia": {"1"}, "inbound_threshold": {"5"}, "outbound_threshold": {"4"}}
	rr := postForm(h, "/security/modsec", form, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "ModSecurity is off: no website is filtered.")
}

func TestModsecConfigureValidation(t *testing.T) {
	h, _, _ := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := modsecConfigForm(t, h, admin)
	base := func() url.Values {
		return url.Values{"_token": {token}, "paranoia": {"1"}, "inbound_threshold": {"5"}, "outbound_threshold": {"4"}}
	}
	cases := []struct{ field, value, want string }{
		{"paranoia", "9", "Paranoia level: 1 to 4."},
		{"paranoia", "0", "Paranoia level: 1 to 4."},
		{"paranoia", "abc", "Paranoia level: 1 to 4."},
		{"inbound_threshold", "1", "Inbound threshold: 2 to 1000."},
		{"inbound_threshold", "", "Inbound threshold: 2 to 1000."},
		{"outbound_threshold", "1001", "Outbound threshold: 2 to 1000."},
	}
	for _, c := range cases {
		form := base()
		form.Set(c.field, c.value)
		rr := postForm(h, "/security/modsec", form, admin)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("%s=%s: %d", c.field, c.value, rr.Code)
		}
		flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", c.want)
	}
}

func TestModsecConfigureAgentError(t *testing.T) {
	h, sa, _ := modsecEnv(t)
	sa.failActions = map[string]string{"modsec.configure": "nginx -t failed: unknown directive"}
	admin := loginAsUser(t, h, "admin")
	token, admin := modsecConfigForm(t, h, admin)
	rr := postForm(h, "/security/modsec", url.Values{"_token": {token}, "enabled": {"1"}, "paranoia": {"1"}, "inbound_threshold": {"5"}, "outbound_threshold": {"4"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "nginx -t failed: unknown directive")
}

func TestModsecSaveCustom(t *testing.T) {
	h, sa, db := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := modsecConfigForm(t, h, admin)
	rules := "# the whole file\nSecRule REQUEST_URI \"@rx evil\" \"id:10001,phase:1,deny,msg:'evil'\"\n"
	rr := postForm(h, "/security/modsec/custom", url.Values{"_token": {token}, "rules": {rules}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("custom = %d", rr.Code)
	}
	// The fake's custom_set answers "2 rules"; the custom vendor is off in
	// the fake's config, so the hint is appended.
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec",
		`2 custom rule(s) saved; Nginx accepted them. Turn "Custom rules" on in the server-wide settings to use them.`)
	sent, _ := agentParam(t, sa, "modsec.custom_set", "rules").(string)
	if sent != rules {
		t.Errorf("custom_set rules = %q", sent)
	}
	var action, details string
	if err := db.QueryRow(`SELECT action, details FROM audit_logs WHERE action='modsec.custom_rules' ORDER BY id DESC LIMIT 1`).Scan(&action, &details); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(details, sha256Of(rules)) {
		t.Errorf("audit details missing the sha: %s", details)
	}
	// Above the 64 KiB cap.
	big := strings.Repeat("x", 65537)
	rr = postForm(h, "/security/modsec/custom", url.Values{"_token": {token}, "rules": {big}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("custom big = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "The rules are larger than 64 KiB.")
}

func TestModsecSaveRuleAdd(t *testing.T) {
	h, sa, _ := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := modsecConfigForm(t, h, admin)
	rule := `SecRule REQUEST_URI "@beginsWith /evil" "id:10003,phase:1,deny,status:403,log,msg:'evil'"`
	rr := postForm(h, "/security/modsec/rule", url.Values{
		"_token": {token}, "rule": {rule}, "comment": {"block evil"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("rule = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec",
		`Rule added; Nginx accepted it. Turn "Custom rules" on in the server-wide settings to use them.`)
	sent, _ := agentParam(t, sa, "modsec.custom_set", "rules").(string)
	for _, want := range []string{"# block evil", "id:10003", modsecRuleTexts[0], modsecRuleTexts[1]} {
		if !strings.Contains(sent, want) {
			t.Errorf("custom_set rules missing %q:\n%s", want, sent)
		}
	}
}

func TestModsecSaveRuleEditAndStale(t *testing.T) {
	h, sa, _ := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := modsecConfigForm(t, h, admin)
	// Edit rule 0 with the matching sha.
	rr := postForm(h, "/security/modsec/rule", url.Values{
		"_token": {token}, "rule": {modsecRuleTexts[0] + " # tweaked"}, "comment": {"block xmlrpc"},
		"index": {"0"}, "sha": {sha256Of(modsecRuleTexts[0])},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("edit = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "Rule saved; Nginx accepted it.")
	sent, _ := agentParam(t, sa, "modsec.custom_set", "rules").(string)
	if !strings.Contains(sent, "tweaked") {
		t.Errorf("custom_set rules missing the edit:\n%s", sent)
	}
	// A stale sha is refused (the optimistic lock).
	rr = postForm(h, "/security/modsec/rule", url.Values{
		"_token": {token}, "rule": {"anything"}, "index": {"0"}, "sha": {"nope"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("stale = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec",
		"The custom rules changed since the page was loaded. Reload the page and try again.")
}

func TestModsecSaveRuleDelete(t *testing.T) {
	h, sa, _ := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := modsecConfigForm(t, h, admin)
	// Delete rule 1 (the SecAction).
	rr := postForm(h, "/security/modsec/rule", url.Values{
		"_token": {token}, "op": {"delete"}, "index": {"1"}, "sha": {sha256Of(modsecRuleTexts[1])},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "Rule deleted; Nginx accepted the remaining rules.")
	sent, _ := agentParam(t, sa, "modsec.custom_set", "rules").(string)
	if strings.Contains(sent, "10002") {
		t.Errorf("custom_set rules still has the deleted rule:\n%s", sent)
	}
	if !strings.Contains(sent, "10001") {
		t.Errorf("custom_set rules lost the kept rule:\n%s", sent)
	}
	// Deleting without an index.
	rr = postForm(h, "/security/modsec/rule", url.Values{"_token": {token}, "op": {"delete"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete no index = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "Choose a rule to delete.")
	// Saving with an empty rule.
	rr = postForm(h, "/security/modsec/rule", url.Values{"_token": {token}, "rule": {"  "}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("empty rule = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "Enter the rule.")
}

func TestModsecSiteSave(t *testing.T) {
	h, sa, db := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/modsec")
	// Mode detect, the CRS vendor kept (custom unchecked → excluded), two
	// rules turned off.
	rr := postForm(h, "/security/modsec/sites/10", url.Values{
		"_token": {token}, "mode": {"detect"}, "vendors[]": {"crs"}, "disabled_rules": {"942100 920350"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("site = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "ModSecurity for bob.example saved.")
	if domain, _ := agentParam(t, sa, "modsec.site", "domain").(string); domain != "bob.example" {
		t.Errorf("modsec.site domain = %q", domain)
	}
	if mode, _ := agentParam(t, sa, "modsec.site", "mode").(string); mode != "detect" {
		t.Errorf("modsec.site mode = %q", mode)
	}
	if exclude, _ := agentParam(t, sa, "modsec.site", "exclude").([]any); len(exclude) != 1 || exclude[0] != "custom" {
		t.Errorf("modsec.site exclude = %v", exclude)
	}
	if dis, _ := agentParam(t, sa, "modsec.site", "disabled_rules").([]any); len(dis) != 2 || dis[0] != float64(942100) {
		t.Errorf("modsec.site disabled_rules = %v", dis)
	}
	var action, targetID string
	if err := db.QueryRow(`SELECT action, target_id FROM audit_logs WHERE action='modsec.site' ORDER BY id DESC LIMIT 1`).Scan(&action, &targetID); err != nil {
		t.Fatal(err)
	}
	if targetID != "10" {
		t.Errorf("audit target = %s", targetID)
	}
}

func TestModsecSiteSaveErrors(t *testing.T) {
	h, _, _ := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/modsec")

	rr := postForm(h, "/security/modsec/sites/10", url.Values{"_token": {token}, "mode": {"bogus"}, "vendors[]": {"crs"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("bad mode = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "Choose off, detection only or on.")

	rr = postForm(h, "/security/modsec/sites/10", url.Values{"_token": {token}, "mode": {"off"}, "disabled_rules": {"abc"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("bad ids = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", `"abc" is not a rule ID.`)

	rr = postForm(h, "/security/modsec/sites/999", url.Values{"_token": {token}, "mode": {"off"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("no site = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "That website does not exist.")

	// A non-administrator is refused by the route's permission.
	bob := loginAsUser(t, h, "bob")
	rr = postForm(h, "/security/modsec/sites/10", url.Values{"_token": {"x"}, "mode": {"off"}}, bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bob site = %d (want 403)", rr.Code)
	}
}

func TestModsecUpdate(t *testing.T) {
	h, sa, db := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/modsec")
	rr := postForm(h, "/security/modsec/update", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec",
		"Updating the rule packages in the background; you will be notified when it finishes.")
	var jobID, kind, target string
	if err := db.QueryRow(`SELECT job_id, kind, target_type FROM agent_jobs ORDER BY id DESC LIMIT 1`).Scan(&jobID, &kind, &target); err != nil {
		t.Fatal(err)
	}
	if jobID != "1111111111111111" || kind != "Update ModSecurity rules" || target != "server" {
		t.Errorf("agent_jobs = %s %s %s", jobID, kind, target)
	}
	_ = sa
}

func TestModsecInstall(t *testing.T) {
	h, sa, db := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/modsec")
	// A bad SHA is refused before the agent is called.
	rr := postForm(h, "/security/modsec/install", url.Values{
		"_token": {token}, "method": {"source"}, "nginx_source_sha256": {"zzz"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install bad sha = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "The SHA-256 is 64 hexadecimal characters.")
	// An upgrade from source.
	sha := strings.Repeat("a", 64)
	rr = postForm(h, "/security/modsec/install", url.Values{
		"_token": {token}, "method": {"source"}, "upgrade": {"1"}, "nginx_source_sha256": {sha},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec",
		"Installing the ModSecurity connector in the background. Everything is backed up first and rolled back if any step fails; you will be notified when it finishes.")
	if method, _ := agentParam(t, sa, "modsec.install", "method").(string); method != "source" {
		t.Errorf("install method = %q", method)
	}
	if up, _ := agentParam(t, sa, "modsec.install", "upgrade").(bool); !up {
		t.Errorf("install upgrade = %v", up)
	}
	if ret, _ := agentParam(t, sa, "modsec.install", "retention_days").(float64); ret != 30 {
		t.Errorf("install retention_days = %v", ret)
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM agent_jobs WHERE job_id='2222222222222222'`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "Upgrade the ModSecurity connector" {
		t.Errorf("job kind = %q", kind)
	}
}

func TestModsecInstallCrs(t *testing.T) {
	h, sa, db := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/modsec")
	rr := postForm(h, "/security/modsec/crs", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("crs = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec",
		"Installing the OWASP Core Rule Set in the background. It starts in detection only: websites set to block only log until you allow blocking.")
	if ret, _ := agentParam(t, sa, "modsec.crs_install", "retention_days").(float64); ret != 30 {
		t.Errorf("crs retention_days = %v", ret)
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM agent_jobs WHERE job_id='3333333333333333'`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "Install the OWASP Core Rule Set" {
		t.Errorf("job kind = %q", kind)
	}
}

func TestModsecRollback(t *testing.T) {
	h, sa, db := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/modsec")
	rr := postForm(h, "/security/modsec/rollback/2026-10-01T08:00:00Z-a1b2c3d4", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("rollback = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec",
		"Rolling back installation 2026-10-01T08:00:00Z-a1b2c3d4 in the background. If Nginx would not accept the result, nothing changes.")
	if id, _ := agentParam(t, sa, "modsec.rollback", "id").(string); id != "2026-10-01T08:00:00Z-a1b2c3d4" {
		t.Errorf("rollback id = %q", id)
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM agent_jobs WHERE job_id='4444444444444444'`).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "Roll back ModSecurity installation 2026-10-01T08:00:00Z-a1b2c3d4" {
		t.Errorf("job kind = %q", kind)
	}
	// An unknown installation id.
	rr = postForm(h, "/security/modsec/rollback/not-an-id", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("rollback bad id = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/modsec", "Unknown installation.")
	// A non-administrator is refused by the route's permission.
	bob := loginAsUser(t, h, "bob")
	rr = postForm(h, "/security/modsec/rollback/2026-10-01T08:00:00Z-a1b2c3d4", url.Values{"_token": {"x"}}, bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("bob rollback = %d (want 403)", rr.Code)
	}
}

func TestSiteFirewallPage(t *testing.T) {
	h, _, _ := modsecEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/firewall", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Web firewall (ModSecurity)", `id="fw-on" value="on" checked`,
		"OWASP Core Rule Set", ">942100<", "203.0.113.9",
		`wcp-state is-error">Blocked (403)`, `wcp-state is-warn">Detected`,
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if pageHas(body, "Turn off here") {
		t.Errorf("an owner may not turn rules off from an event")
	}
	// An administrator sees the "turn off here" actions.
	admin := loginAsUser(t, h, "admin")
	rr = get(t, h, "/sites/10/firewall", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin GET = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Turn off here") {
		t.Errorf("admin page missing the turn-off actions")
	}
}

func TestSiteFirewallInactive(t *testing.T) {
	h, sa, _ := modsecEnv(t)
	sa.modsecActive = false
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/firewall", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !pageHas(body, "The web application firewall is not active on this server") {
		t.Errorf("missing the inactive state")
	}
	if !pageHas(body, "Please ask your hosting provider to enable it.") {
		t.Errorf("missing the owner's hint")
	}
	// An administrator gets the link to the ModSecurity page.
	admin := loginAsUser(t, h, "admin")
	rr = get(t, h, "/sites/10/firewall", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin GET = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), `<a class="btn btn-primary" href="/security/modsec">Open ModSecurity</a>`) {
		t.Errorf("admin page missing the ModSecurity link")
	}
}

func TestSiteFirewallNotFound(t *testing.T) {
	h, _, _ := modsecEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/999/firewall", bob)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("GET = %d (want 404)", rr.Code)
	}
	if !pageHas(rr.Body.String(), "This website does not exist or you do not have access to it.") {
		t.Errorf("missing the 404 message")
	}
}

func TestSiteFirewallMode(t *testing.T) {
	h, sa, _ := modsecEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/firewall")
	rr := postForm(h, "/sites/10/firewall", url.Values{"_token": {token}, "mode": {"detect"}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("mode = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, refreshCookie(bob, rr), "/sites/10/firewall",
		"Web firewall for bob.example: detection only (log, do not block).")
	if mode, _ := agentParam(t, sa, "modsec.site", "mode").(string); mode != "detect" {
		t.Errorf("modsec.site mode = %q", mode)
	}
	// A non-administrator keeps the current vendors and rules.
	if exclude, _ := agentParam(t, sa, "modsec.site", "exclude").([]any); len(exclude) != 1 || exclude[0] != "custom" {
		t.Errorf("modsec.site exclude = %v (kept from the status)", exclude)
	}
	if dis, _ := agentParam(t, sa, "modsec.site", "disabled_rules").([]any); len(dis) != 1 || dis[0] != float64(942100) {
		t.Errorf("modsec.site disabled_rules = %v (kept from the status)", dis)
	}
	// An invalid mode.
	rr = postForm(h, "/sites/10/firewall", url.Values{"_token": {token}, "mode": {"bogus"}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("bad mode = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(bob, rr), "/sites/10/firewall", "Choose off, detection only or on.")
	// A disable_rule from an owner is ignored (mode only).
	rr = postForm(h, "/sites/10/firewall", url.Values{"_token": {token}, "mode": {"off"}, "disable_rule": {"941100"}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("owner disable = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(bob, rr), "/sites/10/firewall", "Web firewall for bob.example: off.")
	if dis, _ := agentParam(t, sa, "modsec.site", "disabled_rules").([]any); len(dis) != 1 || dis[0] != float64(942100) {
		t.Errorf("owner's disable_rule must be ignored, got %v", dis)
	}
}

func TestSiteFirewallDisableRule(t *testing.T) {
	h, sa, _ := modsecEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/sites/10/firewall")
	rr := postForm(h, "/sites/10/firewall", url.Values{"_token": {token}, "disable_rule": {"941100"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("disable = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/firewall", "Rule 941100 is now off for bob.example.")
	if mode, _ := agentParam(t, sa, "modsec.site", "mode").(string); mode != "on" {
		t.Errorf("the mode must stay on, got %q", mode)
	}
	dis, _ := agentParam(t, sa, "modsec.site", "disabled_rules").([]any)
	// The current rule plus the new one.
	if len(dis) != 2 || dis[0] != float64(942100) || dis[1] != float64(941100) {
		t.Errorf("modsec.site disabled_rules = %v", dis)
	}
	// A bad rule id from an event.
	rr = postForm(h, "/sites/10/firewall", url.Values{"_token": {token}, "disable_rule": {"abc"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("disable bad id = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/firewall", `"abc" is not a rule ID.`)
}
