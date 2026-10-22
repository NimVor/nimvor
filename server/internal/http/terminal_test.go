package http

// This suite drives the web terminal area (TerminalController): the site
// terminal page (states: agent down, shell off, the console with the
// per-response CSP nonce), the ticket endpoints (site account and root:
// the agent parameters, the no-account/suspended refusals, the admin
// gate, the 428 password confirmation), the unlock endpoint (wrong
// password, the 5-failure lock, the 2FA code branch), the administrator
// index (scope, sessions, close) and the sign-out closing the sign-in's
// terminals. The fake agent answers the terminal and shell actions.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
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
	"github.com/mchkhawarshahzad/whacp/server/internal/twofactor"
)

// terminalEnv seeds an administrator, two customers (bob owns site 10,
// alice owns site 20, both files.manage), a viewer (carol, no
// files.manage), a no-account site 30 (bob) and site 40 owned by a
// suspended account (dave). Returns the handler, the fake agent, the DB
// and the server (for the two-factor service in tests).
func terminalEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB, *Server) {
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
	// Role 1's name is "admin" (authz.SyncDefaults), the name terminal.go's
	// root-shell gate compares against.
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own',''),(3,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'files.manage','fm','files'),
		(4,'server.manage','sm','server'),(5,'security.manage','sec','security')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(1,4),(1,5),(2,1),(2,2),(2,3),(3,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id,suspended_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL,NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1,NULL),
		(3,'alice','c@x','Alice',?,2,1,'2026-01-01T00:00:00Z','','',1,NULL),
		(4,'carol','d@x','Carol',?,3,1,'2026-01-01T00:00:00Z','','',1,NULL),
		(5,'dave','e@x','Dave',?,2,1,'2026-01-01T00:00:00Z','','',1,'2026-01-01T00:00:00Z')`,
		loginHash, loginHash, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at) VALUES
		(10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(20,1,3,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active','',''),
		(30,1,2,'noacct.example','','/home/whacp/noacct.example','/home/whacp/noacct.example/public_html','php','8.3.1','active','',''),
		(40,1,5,'suspended.example','u_dave','/home/whacp/suspended.example','/home/whacp/suspended.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES
		(1,10,'bob.example',1),(1,20,'alice.example',1),(1,30,'noacct.example',1),(1,40,'suspended.example',1)`)

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
	return s.Handler(), sa, db, s
}

// jsonBody decodes the {"success":…} body of a JSON response.
func jsonBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad JSON body %d: %s", rr.Code, rr.Body.String())
	}
	return body
}

func TestTerminalSitePage(t *testing.T) {
	h, _, _, _ := terminalEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/terminal", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`data-auth="linux"`, `data-open-url="/sites/10/terminal/open"`, "wcp-term-login",
		"Sign in as the Linux account", "xterm.js", "terminal.js",
		"in a sandbox that shows only this account's website folder",
		"The session closes after 15 minutes without input",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The per-response CSP nonce (style elements for xterm.js); scripts
	// stay 'self' only.
	csp := rr.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "style-src 'self' 'nonce-") || strings.Contains(csp, "script-src 'self' 'nonce-") {
		t.Errorf("CSP = %q", csp)
	}
}

func TestTerminalSitePageShellOff(t *testing.T) {
	h, _, _, _ := terminalEnv(t)
	alice := loginAsUser(t, h, "alice")
	rr := get(t, h, "/sites/20/terminal", alice)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Shell access is off for this website") || strings.Contains(body, "wcp-term") {
		t.Errorf("shell-off page wrong: %s", body[:min(200, len(body))])
	}
	// A customer without security.manage sees the plain hint, not the link.
	if !strings.Contains(body, "Please ask your hosting provider to enable shell access") {
		t.Errorf("missing the provider hint")
	}
}

func TestTerminalSitePageAgentDown(t *testing.T) {
	h, sa, _, _ := terminalEnv(t)
	sa.failActions = map[string]string{"shell.status": "the socket is closed"}
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/terminal", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "the socket is closed") || strings.Contains(body, "wcp-term") {
		t.Errorf("agent-down page wrong: %s", body[:min(300, len(body))])
	}
}

func TestTerminalOpenSite(t *testing.T) {
	h, sa, db, _ := terminalEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/terminal")
	rr := postJSON(t, h, "/sites/10/terminal/open", map[string]any{"cols": 120, "rows": 40}, bob, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("open = %d: %s", rr.Code, rr.Body.String())
	}
	body := jsonBody(t, rr)
	data, _ := body["data"].(map[string]any)
	if data["ticket"] != "feedfacefeedfacefeedfacefeedface" || data["session_id"] != "abcdef0123456789" ||
		data["mode"] != "jailed" || data["user"] != "u_bob" || data["password_required"] != true {
		t.Errorf("data = %v", data)
	}
	if got := agentParam(t, sa, "terminal.open", "user"); got != "u_bob" {
		t.Errorf("user = %v", got)
	}
	if got := agentParam(t, sa, "terminal.open", "cols"); got != float64(120) {
		t.Errorf("cols = %v", got)
	}
	if owner, _ := agentParam(t, sa, "terminal.open", "owner").(string); len(owner) != 32 {
		t.Errorf("owner = %q (want 32 hex)", owner)
	}
	if got := agentParam(t, sa, "terminal.open", "label"); got != "bob (#2)" {
		t.Errorf("label = %v", got)
	}
	if got := agentParam(t, sa, "terminal.open", "idle_minutes"); got != float64(15) {
		t.Errorf("idle_minutes = %v", got)
	}
	if got := agentParam(t, sa, "terminal.open", "max_minutes"); got != float64(480) {
		t.Errorf("max_minutes = %v", got)
	}
	var target, result string
	if err := db.QueryRow(`SELECT target_type, result FROM audit_logs WHERE action = 'terminal.open'`).Scan(&target, &result); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "terminal" || result != "success" {
		t.Errorf("audit = %s/%s", target, result)
	}
}

func TestTerminalOpenSiteRefusals(t *testing.T) {
	h, sa, _, _ := terminalEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/30/terminal")
	// No system account.
	rr := postJSON(t, h, "/sites/30/terminal/open", map[string]any{}, bob, token)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("no-account open = %d", rr.Code)
	}
	if body := jsonBody(t, rr); body["success"] != false {
		t.Errorf("body = %v", body)
	}
	// A suspended hosting account (an administrator sees the refusal).
	admin := loginAsUser(t, h, "admin")
	tokenA, admin := siteFormToken(t, h, admin, "/sites/40/terminal")
	rr = postJSON(t, h, "/sites/40/terminal/open", map[string]any{}, admin, tokenA)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("suspended open = %d", rr.Code)
	}
	if body := jsonBody(t, rr); body["error"].(map[string]any)["message"] != "The hosting account is suspended." {
		t.Errorf("body = %v", body)
	}
	// A foreign site is a 404 with the site message.
	alice := loginAsUser(t, h, "alice")
	tokenB, alice := siteFormToken(t, h, alice, "/sites/20/terminal")
	rr = postJSON(t, h, "/sites/10/terminal/open", map[string]any{}, alice, tokenB)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("foreign open = %d", rr.Code)
	}
	if body := jsonBody(t, rr); body["error"].(map[string]any)["message"] != "This website does not exist or you do not have access to it." {
		t.Errorf("body = %v", body)
	}
	for _, a := range sa.called() {
		if a == "terminal.open" {
			t.Errorf("a refused open reached the agent: %v", sa.called())
		}
	}
}

func TestTerminalUnlock(t *testing.T) {
	h, _, _, _ := terminalEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/terminal")
	// A wrong password is refused (and audited as a failure).
	rr := postJSON(t, h, "/terminal/unlock", map[string]any{"password": "nope"}, admin, token)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("unlock (wrong) = %d", rr.Code)
	}
	if body := jsonBody(t, rr); body["error"].(map[string]any)["message"] != "The password is wrong." {
		t.Errorf("body = %v", body)
	}
	admin = refreshCookie(admin, rr)
	// The right password unlocks for 15 minutes.
	rr = postJSON(t, h, "/terminal/unlock", map[string]any{"password": loginPassword}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("unlock (right) = %d: %s", rr.Code, rr.Body.String())
	}
	if body := jsonBody(t, rr); body["data"].(map[string]any)["minutes"] != float64(15) {
		t.Errorf("body = %v", body)
	}
	admin = refreshCookie(admin, rr)
	// Five more wrong attempts lock the endpoint for 15 minutes…
	for i := 0; i < 5; i++ {
		rr = postJSON(t, h, "/terminal/unlock", map[string]any{"password": "nope"}, admin, token)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("unlock %d = %d, want 403", i, rr.Code)
		}
		admin = refreshCookie(admin, rr)
	}
	// …the next one is the 429.
	rr = postJSON(t, h, "/terminal/unlock", map[string]any{"password": "nope"}, admin, token)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("locked = %d, want 429", rr.Code)
	}
	if body := jsonBody(t, rr); !strings.Contains(body["error"].(map[string]any)["message"].(string), "Too many wrong attempts") {
		t.Errorf("body = %v", body)
	}
}

func TestTerminalUnlockNeedsCode(t *testing.T) {
	h, _, _, s := terminalEnv(t)
	// Sign in first (password-only, 2FA is not on yet); the session stays
	// valid when the second factor is switched on afterwards.
	admin := loginAsUser(t, h, "admin")
	secret, err := twofactor.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	code, err := twofactor.Code(secret, time.Now().Unix()/30)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.twoFactor.Enable(context.Background(), 1, secret, code); err != nil {
		t.Fatal(err)
	}
	token, admin := siteFormToken(t, h, admin, "/terminal")
	// The page offers the code field…
	if rr := get(t, h, "/terminal", admin); !strings.Contains(rr.Body.String(), `name="code"`) {
		t.Errorf("unlock form missing the code field")
	}
	// …the right password without the code is refused (and named as such).
	rr := postJSON(t, h, "/terminal/unlock", map[string]any{"password": loginPassword}, admin, token)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("unlock (no code) = %d", rr.Code)
	}
	if body := jsonBody(t, rr); body["error"].(map[string]any)["message"] != "The password or the authenticator code is wrong." {
		t.Errorf("body = %v", body)
	}
	// …with the code it unlocks.
	code, _ = twofactor.Code(secret, time.Now().Unix()/30)
	rr = postJSON(t, h, "/terminal/unlock", map[string]any{"password": loginPassword, "code": code}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("unlock (with code) = %d: %s", rr.Code, rr.Body.String())
	}
}

func TestTerminalOpenRoot(t *testing.T) {
	h, sa, _, _ := terminalEnv(t)
	// A customer is refused (the guard: no security.manage).
	bob := loginAsUser(t, h, "bob")
	tokenB, bob := siteFormToken(t, h, bob, "/sites/10/terminal")
	if rr := postJSON(t, h, "/terminal/root/open", map[string]any{}, bob, tokenB); rr.Code != http.StatusForbidden {
		t.Fatalf("root (customer) = %d", rr.Code)
	}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/terminal")
	// An administrator without the confirmation gets the 428 unlock ask.
	rr := postJSON(t, h, "/terminal/root/open", map[string]any{"cols": 100, "rows": 30}, admin, token)
	if rr.Code != http.StatusPreconditionRequired {
		t.Fatalf("root (locked) = %d", rr.Code)
	}
	body := jsonBody(t, rr)
	errObj := body["error"].(map[string]any)
	if errObj["unlock"] != true || errObj["message"] != "Confirm your password to open the terminal." {
		t.Errorf("body = %v", body)
	}
	// Confirm the panel password, then open.
	admin = refreshCookie(admin, postJSON(t, h, "/terminal/unlock", map[string]any{"password": loginPassword}, admin, token))
	rr = postJSON(t, h, "/terminal/root/open", map[string]any{}, admin, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("root (unlocked) = %d: %s", rr.Code, rr.Body.String())
	}
	data := jsonBody(t, rr)["data"].(map[string]any)
	if data["user"] != "root" || data["mode"] != "root" || data["password_required"] != false {
		t.Errorf("data = %v", data)
	}
	if got := agentParam(t, sa, "terminal.open", "root"); got != true {
		t.Errorf("root = %v", got)
	}
}

func TestTerminalIndex(t *testing.T) {
	h, _, _, _ := terminalEnv(t)
	// A customer without security.manage is 403 by the guard.
	bob := loginAsUser(t, h, "bob")
	if rr := get(t, h, "/terminal", bob); rr.Code != http.StatusForbidden {
		t.Fatalf("index (customer) = %d", rr.Code)
	}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/terminal", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("index = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Open sessions", "<code>u_bob</code>", ">Jailed<", "bob (#2)", "1.0 KB in", "2.0 KB out",
		`data-wcp-confirm=`, "/terminal/sessions/abcdef0123456789/close",
		`data-auth="panel"`, `data-open-url="/terminal/root/open"`, "This is a root shell on the server",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if !strings.Contains(rr.Header().Get("Content-Security-Policy"), "style-src 'self' 'nonce-") {
		t.Errorf("CSP missing the nonce")
	}
}

func TestTerminalClose(t *testing.T) {
	h, sa, _, _ := terminalEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/terminal")
	rr := postForm(h, "/terminal/sessions/abcdef0123456789/close", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("close = %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/terminal" {
		t.Errorf("Location = %q", loc)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/terminal", "Terminal session closed.")
	if got := agentParam(t, sa, "terminal.kill", "id"); got != "abcdef0123456789" {
		t.Errorf("id = %v", got)
	}
	// A bad id never reaches the agent.
	rr = postForm(h, "/terminal/sessions/notanid/close", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("close (bad id) = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/terminal", "Unknown session.")
	// Zero closed sessions: the already-ended flash.
	sa.termKillClosed = 0
	rr = postForm(h, "/terminal/sessions/abcdef0123456789/close", url.Values{"_token": {token}}, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/terminal", "That session had already ended.")
}

func TestTerminalLogoutClosesSessions(t *testing.T) {
	h, sa, _, _ := terminalEnv(t)
	admin := loginAsUser(t, h, "admin")
	// Confirm the password and open a terminal (creates the owner token).
	token, admin := siteFormToken(t, h, admin, "/terminal")
	postJSON(t, h, "/terminal/unlock", map[string]any{"password": loginPassword}, admin, token)
	admin = refreshCookie(admin, postJSON(t, h, "/terminal/root/open", map[string]any{}, admin, token))
	// Sign out.
	logoutToken, admin := siteFormToken(t, h, admin, "/")
	rr := postForm(h, "/logout", url.Values{"_token": {logoutToken}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rr.Code)
	}
	if got := agentParam(t, sa, "terminal.kill", "owner"); got == nil || len(got.(string)) != 32 {
		t.Errorf("terminal.kill owner = %v", got)
	}
}

func TestTerminalOpenBadCSRF(t *testing.T) {
	h, _, _, _ := terminalEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := postJSON(t, h, "/sites/10/terminal/open", map[string]any{}, bob, "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("open without token = %d, want 400", rr.Code)
	}
}
