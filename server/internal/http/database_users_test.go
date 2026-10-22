package http

// This suite drives the database-user routes end to end: permission gates,
// tenant scoping, username/host validation, the generated-once password
// flash, encrypted storage, the remote-hosts setting, grant/revoke and the
// grant-copy on add-host. The agent is a recording fake on a Unix socket.

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
	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// dbUsersEnv seeds two customers (bob owns a_main + bob_db; alice owns
// b_main + alice_db, whose password is stored), an admin-owned database, a
// viewer (carol, databases.view only), and — when remoteSetting is "off" —
// the db_customer_remote_hosts setting disabled. Returns the handler, the
// recording agent and the DB; tests sign in with loginAsUser.
func dbUsersEnv(t *testing.T, remoteSetting string) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'administrator','Admin','all',''),(2,'customer','Customer','own',''),(3,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'databases.view','dv','db'),(3,'databases.manage','dm','db')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(2,1),(2,2),(2,3),(3,1),(3,2)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'alice','c@x','Alice',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(4,'carol','d@x','Carol',?,3,1,'2026-01-01T00:00:00Z','','',1)`,
		loginHash, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at) VALUES
		(10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(20,1,3,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES
		(1,10,'bob.example',1),(1,20,'alice.example',1)`)
	exec(`INSERT INTO databases (id,server_id,website_id,owner_id,name,created_at) VALUES
		(100,1,10,2,'a_main','2026-01-01'),(101,1,20,3,'b_main','2026-01-01'),(102,1,NULL,1,'c_main','2026-01-01')`)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	storedEnc, err := crypto.New(kf).Encrypt([]byte("stored-secret-123"))
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO database_users (id,server_id,owner_id,username,host,password_enc,created_at) VALUES
		(30,1,2,'bob_db','localhost','','2026-01-01'),
		(31,1,3,'alice_db','localhost',?,'2026-01-01')`, storedEnc)
	exec(`INSERT INTO database_grants (database_id,user_id,privileges) VALUES
		(100,30,'all'),(101,31,'all')`)
	if remoteSetting == "off" {
		exec(`INSERT INTO settings (key,value,updated_at,updated_by) VALUES ('db_customer_remote_hosts','false','',NULL)`)
	}

	sa, socket, tokenFile := startSitesAgent(t, false)
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

// loginAsUser signs in and returns the sealed session cookie.
func loginAsUser(t *testing.T, h http.Handler, username string) *http.Cookie {
	t.Helper()
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {username}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d", username, rr.Code)
	}
	return rr.Result().Cookies()[0]
}

// agentParam returns the last recorded parameter of an agent action.
func agentParam(t *testing.T, sa *sitesAgent, action, key string) any {
	t.Helper()
	sa.mu.Lock()
	defer sa.mu.Unlock()
	for i := len(sa.actions) - 1; i >= 0; i-- {
		if sa.actions[i] == action {
			return sa.params[i][key]
		}
	}
	t.Fatalf("agent action %s never called; got %v", action, sa.actions)
	return nil
}

func grantCount(t *testing.T, db *sql.DB, databaseID, userID int64) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM database_grants WHERE database_id = ? AND user_id = ?`, databaseID, userID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDatabasesPageShowsUsersAndGrants(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	admin := loginAsUser(t, h, "admin")
	body := get(t, h, "/databases", admin).Body.String()
	for _, want := range []string{"a_main", "b_main", "c_main", "bob_db", "alice_db", "All privileges", "This server"} {
		if !strings.Contains(body, want) {
			t.Errorf("admin page missing %q:\\n%s", want, body)
		}
	}
	// bob (scoped) sees only his database and user.
	bob := loginAsUser(t, h, "bob")
	bbody := get(t, h, "/databases", bob).Body.String()
	if strings.Contains(bbody, "alice_db") || strings.Contains(bbody, "b_main") || strings.Contains(bbody, "c_main") {
		t.Fatalf("TENANT LEAK: bob saw foreign data:\\n%s", bbody)
	}
	if !strings.Contains(bbody, "bob_db") || !strings.Contains(bbody, "a_main") {
		t.Fatalf("bob missing own data:\\n%s", bbody)
	}
	// bob_db already has his only database → no assign form for him.
	if strings.Contains(bbody, `/database-users/30/assign`) {
		t.Fatalf("bob_db has no free database of his own; assign form should be hidden:\\n%s", bbody)
	}
	// carol (viewer) sees the page read-only: no operation forms.
	carol := loginAsUser(t, h, "carol")
	cbody := get(t, h, "/databases", carol).Body.String()
	if strings.Contains(cbody, `/database-users/30/delete`) || strings.Contains(cbody, `action="/databases"`) {
		t.Fatalf("viewer page has operation forms:\\n%s", cbody)
	}
}

func TestDBUserRoutesRequireManage(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	carol := loginAsUser(t, h, "carol") // databases.view, not databases.manage
	if rr := get(t, h, "/databases", carol); rr.Code != http.StatusOK {
		t.Fatalf("viewer GET /databases = %d, want 200", rr.Code)
	}
	token, carol := siteFormToken(t, h, carol, "/databases")
	for _, path := range []string{
		"/database-users",
		"/database-users/30/password",
		"/database-users/30/reveal",
		"/database-users/30/delete",
		"/database-users/30/assign",
		"/database-users/30/hosts",
	} {
		if rr := postForm(h, path, url.Values{"_token": {token}}, carol); rr.Code != http.StatusForbidden {
			t.Errorf("POST %s for a viewer = %d, want 403", path, rr.Code)
		}
	}
}

func TestDBUserCreateFlashesSecretAndAudits(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases")
	rr := postForm(h, "/database-users", url.Values{
		"_token": {token}, "username": {"newuser"},
		"host_choice": {"any"}, "store_password": {"1"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", rr.Code)
	}
	admin = refreshCookie(admin, rr)
	page := get(t, h, "/databases", admin).Body.String()
	pw := agentParam(t, sa, "db.user_create", "password").(string)
	for _, want := range []string{"Database user created", "copy the password now", "newuser", pw} {
		if !strings.Contains(page, want) {
			t.Errorf("secret flash missing %q in:\\n%s", want, page)
		}
	}
	if acts := sa.called(); len(acts) != 1 || acts[0] != "db.user_create" {
		t.Fatalf("agent actions = %v", acts)
	}
	if sa.param(0, "user") != "newuser" || sa.param(0, "host") != "%" {
		t.Fatalf("db.user_create params = %v", sa.params[0])
	}
	var enc string
	if err := db.QueryRow(`SELECT password_enc FROM database_users WHERE username = 'newuser' AND host = '%'`).Scan(&enc); err != nil || enc == "" {
		t.Fatalf("stored password_enc missing or empty: %q %v", enc, err)
	}
	var targetType, target string
	if err := db.QueryRow(`SELECT target_type, target_id FROM audit_logs WHERE action = 'database.user_create'`).Scan(&targetType, &target); err != nil || targetType != "database_user" || target != "newuser@%" {
		t.Fatalf("audit row = %q %q %v", targetType, target, err)
	}
}

func TestDBUserCreateValidationErrors(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	// Uppercase / space in the username → field error, agent untouched.
	token, bob := siteFormToken(t, h, bob, "/databases")
	rr := postForm(h, "/database-users", url.Values{"_token": {token}, "username": {"Bad Name"}, "host_choice": {"localhost"}}, bob)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "Use up to 32 lowercase letters, digits or underscores (not already used).")
	// "db" prefixes to "bob_db" — taken.
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/database-users", url.Values{"_token": {token}, "username": {"db"}, "host_choice": {"localhost"}}, bob)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "Use up to 32 lowercase letters, digits or underscores (not already used).")
	// A bad IP → the customer host wording.
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/database-users", url.Values{"_token": {token}, "username": {"okname"}, "host_choice": {"ip"}, "host_ip": {"999.1.1.1"}}, bob)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "Enter an IP address (e.g. 203.0.113.5) or a network with % (e.g. 203.0.113.%).")
	if len(sa.called()) != 0 {
		t.Fatalf("agent called on validation failure: %v", sa.called())
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM database_users`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("database_users count after rejected creates = %d (%v), want 2", n, err)
	}
}

func TestDBUserCreatePrefixedForCustomer(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/databases")
	rr := postForm(h, "/database-users", url.Values{"_token": {token}, "username": {"appuser"}, "host_choice": {"localhost"}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	page := get(t, h, "/databases", bob).Body.String()
	if !strings.Contains(page, "Database user created") || !strings.Contains(page, "bob_appuser") {
		t.Fatalf("prefixed secret flash missing:\\n%s", page)
	}
	if sa.param(0, "user") != "bob_appuser" {
		t.Fatalf("agent user = %v", sa.param(0, "user"))
	}
	// No "keep password" checkbox → not stored; owner is bob.
	var enc string
	var owner sql.NullInt64
	if err := db.QueryRow(`SELECT password_enc, owner_id FROM database_users WHERE username = 'bob_appuser'`).Scan(&enc, &owner); err != nil || enc != "" || !owner.Valid || owner.Int64 != 2 {
		t.Fatalf("row = enc %q owner %v err %v", enc, owner, err)
	}
	if !strings.Contains(page, "Not stored") {
		t.Fatalf("user table missing 'Not stored':\\n%s", page)
	}
}

func TestDBUserRemoteHostsSettingOff(t *testing.T) {
	h, sa, _ := dbUsersEnv(t, "off")
	bob := loginAsUser(t, h, "bob")
	// Setting off: a customer is localhost-only — "any host" is refused…
	token, bob := siteFormToken(t, h, bob, "/databases")
	rr := postForm(h, "/database-users", url.Values{"_token": {token}, "username": {"r1"}, "host_choice": {"any"}}, bob)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "Database users may connect from this server only (your hosting provider's setting).")
	// …and so is a specific address.
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/database-users", url.Values{"_token": {token}, "username": {"r2"}, "host_choice": {"ip"}, "host_ip": {"203.0.113.5"}}, bob)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "Database users may connect from this server only (your hosting provider's setting).")
	if len(sa.called()) != 0 {
		t.Fatalf("agent called despite the setting: %v", sa.called())
	}
	// The form itself hides the host choice for this customer (a hidden
	// localhost input only; no "any host" option, no per-user remote form).
	body := get(t, h, "/databases", bob).Body.String()
	if strings.Contains(body, "Any host (%)") || strings.Contains(body, `/database-users/30/hosts`) {
		t.Fatalf("customer with remote hosts disabled sees host options:\\n%s", body)
	}
}

func TestDBUserRemoteHostsStaffBypass(t *testing.T) {
	h, sa, _ := dbUsersEnv(t, "off")
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases")
	rr := postForm(h, "/database-users", url.Values{"_token": {token}, "username": {"staffany"}, "host_choice": {"any"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("admin create = %d", rr.Code)
	}
	admin = refreshCookie(admin, rr)
	flashAt(t, h, admin, "/databases", "Database user created")
	if sa.param(0, "host") != "%" {
		t.Fatalf("agent host = %v", sa.param(0, "host"))
	}
}

func TestDBUserPasswordReset(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/databases")
	rr := postForm(h, "/database-users/30/password", url.Values{"_token": {token}, "store_password": {"1"}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("reset = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	page := get(t, h, "/databases", bob).Body.String()
	pw, _ := agentParam(t, sa, "db.user_password", "password").(string)
	if !strings.Contains(page, "New password") || !strings.Contains(page, "bob_db") || !strings.Contains(page, pw) {
		t.Fatalf("reset flash missing:\\n%s", page)
	}
	if sa.param(0, "user") != "bob_db" || sa.param(0, "host") != "localhost" {
		t.Fatalf("db.user_password params = %v", sa.params[0])
	}
	var enc string
	if err := db.QueryRow(`SELECT password_enc FROM database_users WHERE id = 30`).Scan(&enc); err != nil || enc == "" {
		t.Fatalf("stored enc after reset = %q %v", enc, err)
	}
	// Out of scope: alice's user (31) is "not found" for bob.
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/database-users/31/password", url.Values{"_token": {token}}, bob)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "User not found.")
}

func TestDBUserRevealStoredAndMissing(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	// 30 has no stored password → the reset-it hint.
	token, bob := siteFormToken(t, h, bob, "/databases")
	rr := postForm(h, "/database-users/30/reveal", url.Values{"_token": {token}}, bob)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "No stored password for this user. Reset it to get a new one.")
	// Reset (kept), then reveal returns the same password, decrypted.
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/database-users/30/password", url.Values{"_token": {token}, "store_password": {"1"}}, bob)
	bob = refreshCookie(bob, rr)
	newPw, _ := agentParam(t, sa, "db.user_password", "password").(string)
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/database-users/30/reveal", url.Values{"_token": {token}}, bob)
	bob = refreshCookie(bob, rr)
	page := get(t, h, "/databases", bob).Body.String()
	if !strings.Contains(page, "Stored password") || !strings.Contains(page, newPw) {
		t.Fatalf("reveal flash missing:\\n%s", page)
	}
	// The reveal is audited (target_type database_user).
	var targetType, target string
	if err := db.QueryRow(`SELECT target_type, target_id FROM audit_logs WHERE action = 'database.password_reveal'`).Scan(&targetType, &target); err != nil || targetType != "database_user" || target != "bob_db@localhost" {
		t.Fatalf("reveal audit = %q %q %v", targetType, target, err)
	}
}

func TestDBUserDelete(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	// alice cannot touch bob's user (30) — out of scope reads as not found.
	alice := loginAsUser(t, h, "alice")
	token, alice := siteFormToken(t, h, alice, "/databases")
	rr := postForm(h, "/database-users/30/delete", url.Values{"_token": {token}}, alice)
	alice = refreshCookie(alice, rr)
	flashAt(t, h, alice, "/databases", "User not found.")
	// bob deletes his own: agent drop, record and grants gone.
	bob := loginAsUser(t, h, "bob")
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/database-users/30/delete", url.Values{"_token": {token}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "Database user removed.")
	if acts := sa.called(); len(acts) == 0 || acts[len(acts)-1] != "db.user_drop" {
		t.Fatalf("agent actions = %v", acts)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM database_users WHERE id = 30`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("user 30 still present: %d %v", n, err)
	}
	if n := grantCount(t, db, 100, 30); n != 0 {
		t.Fatalf("grants of the deleted user remain: %d", n)
	}
}

func TestDBUserAssignAndRevoke(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	admin := loginAsUser(t, h, "admin")
	// Assign bob_db (30) to b_main (101) with readwrite.
	token, admin := siteFormToken(t, h, admin, "/databases")
	rr := postForm(h, "/database-users/30/assign", url.Values{
		"_token": {token}, "database_id": {"101"}, "privileges": {"readwrite"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("assign = %d", rr.Code)
	}
	admin = refreshCookie(admin, rr)
	flashAt(t, h, admin, "/databases", "bob_db may now use b_main.")
	if got := agentParam(t, sa, "db.grant", "database"); got != "b_main" {
		t.Fatalf("db.grant database = %v", got)
	}
	if got := agentParam(t, sa, "db.grant", "privileges"); got != "readwrite" {
		t.Fatalf("db.grant privileges = %v", got)
	}
	if got := agentParam(t, sa, "db.grant", "revoke"); got != false {
		t.Fatalf("db.grant revoke = %v", got)
	}
	var priv string
	if err := db.QueryRow(`SELECT privileges FROM database_grants WHERE database_id = 101 AND user_id = 30`).Scan(&priv); err != nil || priv != "readwrite" {
		t.Fatalf("grant row = %q %v", priv, err)
	}
	// Revoke via the database's grant route (privileges=none → db.grant revoke).
	token, admin = siteFormToken(t, h, admin, "/databases")
	rr = postForm(h, "/databases/101/grant", url.Values{"_token": {token}, "user_id": {"30"}, "privileges": {"none"}}, admin)
	admin = refreshCookie(admin, rr)
	flashAt(t, h, admin, "/databases", "Privileges updated.")
	if got := agentParam(t, sa, "db.grant", "revoke"); got != true {
		t.Fatalf("revoke flag = %v", got)
	}
	if got := agentParam(t, sa, "db.grant", "privileges"); got != "all" {
		t.Fatalf("revoke privileges (agent default) = %v", got)
	}
	if n := grantCount(t, db, 101, 30); n != 0 {
		t.Fatalf("revoked grant row remains: %d", n)
	}
	// Both audit actions recorded for the database.
	var grants, revokes int
	if err := db.QueryRow(`SELECT
		SUM(action = 'database.grant' AND target_id = 'b_main'),
		SUM(action = 'database.revoke' AND target_id = 'b_main')
		FROM audit_logs`).Scan(&grants, &revokes); err != nil || grants != 1 || revokes != 1 {
		t.Fatalf("audit grant/revoke counts = %d/%d %v", grants, revokes, err)
	}
	// An unknown privilege set is a validation error.
	token, admin = siteFormToken(t, h, admin, "/databases")
	rr = postForm(h, "/databases/101/grant", url.Values{"_token": {token}, "user_id": {"30"}, "privileges": {"superuser"}}, admin)
	admin = refreshCookie(admin, rr)
	flashAt(t, h, admin, "/databases", "Choose a privilege set.")
}

func TestDBUserAddHost(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	// A specific IP: a separate login with the same access, new password.
	token, bob := siteFormToken(t, h, bob, "/databases")
	rr := postForm(h, "/database-users/30/hosts", url.Values{
		"_token": {token}, "host_choice": {"ip"}, "host_ip": {"203.0.113.5"},
	}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("add host = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	page := get(t, h, "/databases", bob).Body.String()
	pw, _ := agentParam(t, sa, "db.user_create", "password").(string)
	for _, want := range []string{"Remote access added: bob_db from 203.0.113.5", pw} {
		if !strings.Contains(page, want) {
			t.Errorf("add-host flash missing %q in:\\n%s", want, page)
		}
	}
	if got := agentParam(t, sa, "db.user_create", "host"); got != "203.0.113.5" {
		t.Fatalf("agent host = %v", got)
	}
	// The grants were copied to the new login.
	var newID int64
	if err := db.QueryRow(`SELECT id FROM database_users WHERE username = 'bob_db' AND host = '203.0.113.5'`).Scan(&newID); err != nil {
		t.Fatalf("new login missing: %v", err)
	}
	if n := grantCount(t, db, 100, newID); n != 1 {
		t.Fatalf("copied grants = %d, want 1", n)
	}
	// The same host again → already-connects error.
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/database-users/30/hosts", url.Values{
		"_token": {token}, "host_choice": {"ip"}, "host_ip": {"203.0.113.5"},
	}, bob)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "bob_db may already connect from 203.0.113.5.")
	// Any host → the "any host" wording.
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/database-users/30/hosts", url.Values{"_token": {token}, "host_choice": {"any"}}, bob)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/databases", "Remote access added: bob_db from any host")
}
