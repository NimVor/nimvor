package http

// This suite drives the shell-access page (ShellController): the page
// (all-scope gate, the accounts table with owner/domains/login/mode, the
// unconfirmed-change banner, agent down), the change endpoint (one
// account, every account, the all-confirm word, the plan-SSH skip, the
// waiting-for-login note, the admin gate, bad input, CSRF) and the
// confirm/undo routes of the unconfirmed security change.

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

// shellEnv seeds an administrator, a customer (bob — owns site 10, has the
// security permissions but is not admin), a customer on a plan without SSH
// (frank — owns site 50) and a plain viewer. The fake agent answers the
// shell and security actions.
func shellEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
	// The admin role's name is "admin" (authz.SyncDefaults' seed, the name the
	// role-gated handlers compare against).
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own',''),(3,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'security.view','sv','security'),(3,'security.manage','sm','security'),(4,'files.manage','fm','files')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(1,4),(2,1),(2,2),(2,3),(2,4),(3,1)`)
	exec(`INSERT INTO feature_lists (id,name,created_at,updated_at) VALUES (1,'No SSH','','')`)
	exec(`INSERT INTO feature_list_disabled (list_id,feature) VALUES (1,'ssh')`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at,feature_list_id)
		VALUES (1,'Basic',5,3,5,10,'','',NULL),(2,'NoSSH',5,3,5,10,'','',1)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'alice','c@x','Alice',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(5,'frank','f@x','Frank',?,2,1,'2026-01-01T00:00:00Z','','',2)`,
		loginHash, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at) VALUES
		(10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(20,1,3,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active','',''),
		(50,1,5,'frank.example','u_frank','/home/whacp/frank.example','/home/whacp/frank.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES
		(1,10,'bob.example',1),(1,20,'alice.example',1),(1,50,'frank.example',1)`)

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

func TestShellPage(t *testing.T) {
	h, _, _ := shellEnv(t)
	// A customer (own scope) is refused by the all-scope check…
	bob := loginAsUser(t, h, "bob")
	if rr := get(t, h, "/security/shell", bob); rr.Code != http.StatusForbidden {
		t.Errorf("customer GET = %d, want 403", rr.Code)
	}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/shell", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Shell access", "Website accounts", "u_bob", ">bob<", "bob.example", "frank.example",
		"On (1 key, password)", "<span class=\"text-body-secondary\">Off</span>",
		"Apply to all accounts", "data-wcp-confirm-word=\"all\"", "Set</button>",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestShellPageAgentDown(t *testing.T) {
	h, sa, _ := shellEnv(t)
	sa.failActions = map[string]string{"shell.status": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/shell", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !pageHas(body, "the socket is closed") || pageHas(body, "Website accounts") {
		t.Errorf("agent-down page wrong: %s", body[:min(300, len(body))])
	}
}

func TestShellPagePendingBanner(t *testing.T) {
	h, sa, _ := shellEnv(t)
	sa.secPending = `{"id":"pend123","kind":"firewall","summary":"Firewall enabled","deadline":"2026-01-01T12:03:00Z"}`
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/shell", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Unconfirmed change:", "Firewall enabled", `data-wcp-countdown="2026-01-01T12:03:00Z"`,
		"/security/confirm", "/security/undo", "Keep change", "Undo now",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestShellUpdateOne(t *testing.T) {
	h, sa, db := shellEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/shell")
	rr := postForm(h, "/security/shell", url.Values{
		"_token": {token}, "users[]": {"u_bob"}, "mode": {"jailed"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell", "Shell access set to jailed for 1 account(s).")
	if got := agentParam(t, sa, "shell.set", "mode"); got != "jailed" {
		t.Errorf("mode = %v", got)
	}
	users, _ := agentParam(t, sa, "shell.set", "users").([]any)
	if len(users) != 1 || users[0] != "u_bob" {
		t.Errorf("users = %v", users)
	}
	var target, result string
	if err := db.QueryRow(`SELECT target_type, result FROM audit_logs WHERE action = 'shell.set'`).Scan(&target, &result); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "server" || result != "success" {
		t.Errorf("audit = %s/%s", target, result)
	}
}

func TestShellUpdateAll(t *testing.T) {
	h, sa, _ := shellEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/shell")
	rr := postForm(h, "/security/shell", url.Values{
		"_token": {token}, "scope": {"all"}, "confirm": {"all"}, "mode": {"normal"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update all = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell",
		"Shell access set to normal for 2 account(s). Skipped (their plan does not include SSH): u_frank.")
	// The plan without SSH is skipped: the agent gets the other two.
	users, _ := agentParam(t, sa, "shell.set", "users").([]any)
	got := []string{}
	for _, u := range users {
		got = append(got, u.(string))
	}
	if strings.Join(got, ",") != "u_bob,u_alice" {
		t.Errorf("agent users = %v", got)
	}
}

func TestShellUpdateAllNoConfirm(t *testing.T) {
	h, sa, _ := shellEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/shell")
	rr := postForm(h, "/security/shell", url.Values{
		"_token": {token}, "scope": {"all"}, "mode": {"normal"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update all (no confirm) = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell", "Confirm the change for every account.")
	for _, a := range sa.called() {
		if a == "shell.set" {
			t.Errorf("an unconfirmed all-change reached the agent: %v", sa.called())
		}
	}
}

func TestShellUpdateWaiting(t *testing.T) {
	h, sa, _ := shellEnv(t)
	// The mode takes effect only when SSH/SFTP login is on.
	sa.shellSetEffective = "disabled"
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/shell")
	rr := postForm(h, "/security/shell", url.Values{
		"_token": {token}, "users[]": {"u_bob"}, "mode": {"jailed"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell",
		"1 of them have no SSH/SFTP login yet: it takes effect once their login is turned on (FTP / SFTP page).")
}

func TestShellUpdateRefusals(t *testing.T) {
	h, sa, _ := shellEnv(t)
	// A customer with the security permissions is still not an admin. The
	// token comes from /websites — the shell page itself is 403 for bob.
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/websites")
	rr := postForm(h, "/security/shell", url.Values{
		"_token": {token}, "users[]": {"u_bob"}, "mode": {"jailed"},
	}, bob)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/security/shell" {
		t.Fatalf("update (customer) = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	// The flash lands on the next page bob may open (the shell page itself is 403 for him).
	flashAt(t, h, refreshCookie(bob, rr), "/websites", "Only administrators can change shell access.")

	admin := loginAsUser(t, h, "admin")
	tokenA, admin := siteFormToken(t, h, admin, "/security/shell")
	// A bad mode.
	rr = postForm(h, "/security/shell", url.Values{
		"_token": {tokenA}, "users[]": {"u_bob"}, "mode": {"root"},
	}, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell", "Choose disabled, jailed or normal.")
	// No accounts (and uncleanable names are dropped).
	rr = postForm(h, "/security/shell", url.Values{
		"_token": {tokenA}, "users[]": {"BOB"}, "mode": {"jailed"},
	}, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell", "Select at least one account.")
	for _, a := range sa.called() {
		if a == "shell.set" {
			t.Errorf("a refused change reached the agent: %v", sa.called())
		}
	}
}

func TestShellUpdateSkipAll(t *testing.T) {
	h, sa, _ := shellEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/shell")
	// Every selected account's plan lacks SSH.
	rr := postForm(h, "/security/shell", url.Values{
		"_token": {token}, "users[]": {"u_frank"}, "mode": {"jailed"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell",
		"The hosting plan of u_frank does not include SSH (Feature Manager).")
	for _, a := range sa.called() {
		if a == "shell.set" {
			t.Errorf("a fully skipped change reached the agent: %v", sa.called())
		}
	}
}

func TestSecurityConfirmUndo(t *testing.T) {
	h, sa, db := shellEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/shell")
	// Keep the unconfirmed change.
	rr := postForm(h, "/security/confirm", url.Values{
		"_token": {token}, "id": {"pend123"}, "return_to": {"/security/shell"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell", "Change confirmed and kept.")
	if got := agentParam(t, sa, "security.confirm", "id"); got != "pend123" {
		t.Errorf("id = %v", got)
	}
	// …and roll a different one back.
	rr = postForm(h, "/security/undo", url.Values{
		"_token": {token}, "id": {"pend456"},
	}, admin)
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell", "Change undone.")
	if got := agentParam(t, sa, "security.undo", "id"); got != "pend456" {
		t.Errorf("id = %v", got)
	}
	// An unknown return_to lands back on the shell page.
	rr = postForm(h, "/security/confirm", url.Values{
		"_token": {token}, "id": {"pend123"}, "return_to": {"/evil"},
	}, admin)
	if loc := rr.Header().Get("Location"); loc != "/security/shell" {
		t.Errorf("Location = %q", loc)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'security.confirm'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("security.confirm audits = %d, %v (want 2)", n, err)
	}
}

func TestSecurityConfirmAgentFailure(t *testing.T) {
	h, sa, _ := shellEnv(t)
	sa.failActions = map[string]string{"security.confirm": "Confirm or undo the previous change (Firewall enabled) first."}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/shell")
	rr := postForm(h, "/security/confirm", url.Values{
		"_token": {token}, "id": {"pend789"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("confirm = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/shell", "the previous change")
}

func TestShellUpdateBadCSRF(t *testing.T) {
	h, _, _ := shellEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := postForm(h, "/security/shell", url.Values{"mode": {"jailed"}}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("update without token = %d, want 400", rr.Code)
	}
}
