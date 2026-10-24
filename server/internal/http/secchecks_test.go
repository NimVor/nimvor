package http

// This suite drives the on-demand security checks (SecurityChecksController):
// file integrity (run + baseline), the TLS configuration audit (the endpoint
// list, the no-endpoints short-circuit) and the OS vulnerability advisories
// — the instant pages, the synchronous run re-render, the audits and the
// flashes.

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// checksEnv seeds an administrator, a customer (security.view only — the
// baseline's guard) and — when httpsSite — one enabled HTTPS website plus
// the excluded ones (no-https, disabled). The fake agent answers the check
// actions + mail.status.
func checksEnv(t *testing.T, httpsSite bool) (http.Handler, *sitesAgent, *sql.DB) {
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
	https := 0
	if httpsSite {
		https = 1
	}
	exec("INSERT INTO websites (id,server_id,domain,system_user,root,document_root,kind,enabled,https,created_at) VALUES " +
		"(1,1,'a.example.com','ua','/home/ua','/home/ua/public_html','php',1," + strconv.Itoa(https) + ",'')," +
		"(2,1,'b.example.com','ub','/home/ub','/home/ub/public_html','php',1,0,'')," +
		"(3,1,'c.example.com','uc','/home/uc','/home/uc/public_html','php',0,1,'')")

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

func TestIntegrityPageInitial(t *testing.T) {
	h, sa, _ := checksEnv(t, true)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/integrity", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"File integrity", "dpkg/rpm verification + WhaCP baseline",
		">Run check<", ">Set baseline<",
		"Run the check to verify the agent binary, WhaCP configuration and the installed packages.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if len(sa.called()) != 0 {
		t.Errorf("the page made agent calls: %v", sa.called())
	}
}

func TestIntegrityRun(t *testing.T) {
	h, sa, db := checksEnv(t, true)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/integrity")
	rr := postForm(h, "/security/integrity/run", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("run = %d (it re-renders, no redirect): %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<span class="badge text-bg-warning">MEDIUM</span>`,
		"/usr/bin/passwd", "modified", "(package)", "checksum changed",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("result missing %q", want)
		}
	}
	sa.mu.Lock()
	var params map[string]any
	for i := len(sa.actions) - 1; i >= 0; i-- {
		if sa.actions[i] == "security.integrity" {
			params = sa.params[i]
			break
		}
	}
	sa.mu.Unlock()
	if len(params) != 0 {
		t.Errorf("the run sends params: %v", params)
	}
	var target, details string
	if err := db.QueryRow(`SELECT target_id, details FROM audit_logs WHERE action = 'security.integrity'`).Scan(&target, &details); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "integrity" || !strings.Contains(details, `"findings":1`) {
		t.Errorf("audit = %s / %s", target, details)
	}
}

func TestIntegrityRunAgentDown(t *testing.T) {
	h, sa, db := checksEnv(t, true)
	sa.failActions = map[string]string{"security.integrity": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/integrity")
	rr := postForm(h, "/security/integrity/run", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("run = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "The integrity check could not run: the socket is closed") {
		t.Error("the error is not shown")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'security.integrity'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a failed run must not audit (n=%d, %v)", n, err)
	}
}

func TestIntegrityBaseline(t *testing.T) {
	h, _, db := checksEnv(t, true)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/integrity")
	rr := postForm(h, "/security/integrity/baseline", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/security/integrity" {
		t.Fatalf("baseline = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/integrity",
		"Integrity baseline recorded for 7 WhaCP-owned file(s). Run the check after an update to refresh it.")
	var target, details string
	if err := db.QueryRow(`SELECT target_id, details FROM audit_logs WHERE action = 'security.integrity_baseline'`).Scan(&target, &details); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "integrity" || !strings.Contains(details, `"files":7`) {
		t.Errorf("audit = %s / %s", target, details)
	}
}

func TestIntegrityBaselineAgentDown(t *testing.T) {
	h, sa, _ := checksEnv(t, true)
	sa.failActions = map[string]string{"security.integrity_baseline": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/integrity")
	rr := postForm(h, "/security/integrity/baseline", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("baseline = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/integrity",
		"The baseline could not be recorded: the socket is closed.")
}

func TestIntegrityBaselineDenied(t *testing.T) {
	h, sa, _ := checksEnv(t, true)
	bob := loginAsUser(t, h, "bob")
	// The baseline guard is security.manage, which bob lacks.
	rr := postForm(h, "/security/integrity/baseline", url.Values{"_token": {"x"}}, bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("baseline (bob) = %d, want 403", rr.Code)
	}
	for _, a := range sa.called() {
		if a == "security.integrity_baseline" {
			t.Error("a refused save reached the agent")
		}
	}
}

func TestTlsPageInitial(t *testing.T) {
	h, _, _ := checksEnv(t, true)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/tls", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"TLS configuration audit",
		"4 endpoint(s): HTTPS websites and mail",
		"Run the audit to probe the server's HTTPS and mail endpoints.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(body, `btn-primary" disabled`) {
		t.Error("the run button must be enabled with endpoints")
	}
}

func TestTlsRun(t *testing.T) {
	h, sa, db := checksEnv(t, true)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/tls")
	rr := postForm(h, "/security/tls/run", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("run = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"a.example.com:443", "best: TLS 1.3, lowest: TLS 1.0",
		"TLS 1.0 accepted", `<span class="badge text-bg-danger">HIGH</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("result missing %q", want)
		}
	}
	// The clean endpoint (no findings) is not rendered.
	if strings.Contains(body, "b.example.com:443") {
		t.Error("an endpoint without findings must not render a panel")
	}
	// The endpoint list: the one website + the three mail ports.
	eps, ok := agentParam(t, sa, "tls.audit", "endpoints").([]any)
	if !ok || len(eps) != 4 {
		t.Fatalf("endpoints = %#v", agentParam(t, sa, "tls.audit", "endpoints"))
	}
	first, _ := eps[0].(map[string]any)
	if first["port"] != float64(443) || first["server_name"] != "a.example.com" || first["kind"] != "website" {
		t.Errorf("first endpoint = %#v", first)
	}
	sub, _ := eps[2].(map[string]any)
	if sub["port"] != float64(587) || sub["server_name"] != "mail.example.com" ||
		sub["kind"] != "mail-submission" || sub["starttls"] != "smtp" {
		t.Errorf("submission endpoint = %#v", sub)
	}
	var target, details string
	if err := db.QueryRow(`SELECT target_id, details FROM audit_logs WHERE action = 'tls.audit'`).Scan(&target, &details); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "tls" || !strings.Contains(details, `"endpoints":4`) {
		t.Errorf("audit = %s / %s", target, details)
	}
}

func TestTlsRunAgentDown(t *testing.T) {
	h, sa, db := checksEnv(t, true)
	sa.failActions = map[string]string{"tls.audit": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/tls")
	rr := postForm(h, "/security/tls/run", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("run = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "The TLS audit could not run: the socket is closed") {
		t.Error("the error is not shown")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'tls.audit'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("a failed audit must not record (n=%d, %v)", n, err)
	}
}

func TestTlsNoEndpoints(t *testing.T) {
	h, sa, _ := checksEnv(t, false)
	sa.mailData = `{"installed":true,"configured":false,"hostname":""}`
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/tls", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"0 endpoint(s): HTTPS websites and mail",
		"No HTTPS websites or mail endpoints to audit yet.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if !strings.Contains(body, `btn-primary" disabled`) {
		t.Error("the run button must be disabled without endpoints")
	}
	token, admin := siteFormToken(t, h, admin, "/security/tls")
	rr = postForm(h, "/security/tls/run", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("run = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "No HTTPS websites or mail endpoints to audit yet.") {
		t.Error("the no-endpoints run re-renders the same note")
	}
	for _, a := range sa.called() {
		if a == "tls.audit" {
			t.Error("no endpoints: the agent must not be called")
		}
	}
}

func TestAdvisoriesPageInitial(t *testing.T) {
	h, sa, _ := checksEnv(t, true)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/advisories", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Vulnerability advisories", "from the operating system's own advisories",
		">Check now<", "Check now to list pending updates and their advisories.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if len(sa.called()) != 0 {
		t.Errorf("the page made agent calls: %v", sa.called())
	}
}

func TestAdvisoriesRun(t *testing.T) {
	h, _, db := checksEnv(t, true)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/advisories")
	rr := postForm(h, "/security/advisories/run", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("run = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<span class="badge text-bg-danger">1</span> security`,
		`<span class="badge text-bg-secondary">1</span> normal`,
		"openssl", "3.0.2", "3.0.13",
		`<span class="badge text-bg-danger">security</span>`,
		`<span class="badge text-bg-danger">important</span>`,
		"USN-1234-1", "CVE-2026-0001",
		"vim", "9.0", "9.1",
		`<span class="badge text-bg-secondary">normal</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("result missing %q", want)
		}
	}
	if strings.Contains(body, "unknown") {
		t.Error("the unknown badge only renders when its count is above zero")
	}
	// The description's em dash + the vim row's empty advisory/cve cells.
	if n := strings.Count(body, "—"); n != 3 {
		t.Errorf("the empty advisory/cve cells show an em dash (n=%d)", n)
	}
	// Like PHP, the advisories run is not audited.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'packages.advisories'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("the run must not audit (n=%d, %v)", n, err)
	}
}

func TestAdvisoriesRunAgentDown(t *testing.T) {
	h, sa, _ := checksEnv(t, true)
	sa.failActions = map[string]string{"packages.advisories": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/advisories")
	rr := postForm(h, "/security/advisories/run", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("run = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "The advisory check could not run: the socket is closed") {
		t.Error("the error is not shown")
	}
}

func TestChecksBadCSRF(t *testing.T) {
	h, _, _ := checksEnv(t, true)
	admin := loginAsUser(t, h, "admin")
	rr := postForm(h, "/security/integrity/run", url.Values{}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("run without token = %d, want 400", rr.Code)
	}
}
