package http

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// convertEnv builds a server with an administrator, a reseller (no
// customers), and two customers: bob, who owns website 10 (bob.example) and
// the addon 30 (addon.example) under it, and alice, who owns website 20
// (alice.example) and the addon 21 (another.example). The agent is the
// recording fake. Returns the handler, the ADMIN session cookie, the agent
// and the DB.
func convertEnv(t *testing.T, failFTP bool) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
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
		(1,'administrator','Admin','all',''),
		(2,'reseller','Reseller','own',''),
		(3,'customer','Customer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'users.manage','um','admin')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(2,1),(2,2),(2,3),(3,1),(3,2)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','adm@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,3,1,'2026-01-01T00:00:00Z','','',1),
		(3,'alice','a@x','Alice',?,3,1,'2026-01-01T00:00:00Z','','',1),
		(4,'dave','d@x','Dave',?,2,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,parent_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,NULL,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(30,1,2,10,'addon.example','u_bob','/home/whacp/addon.example','/home/whacp/addon.example/public_html','php','8.3.1','active','',''),
		(20,1,3,NULL,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active','',''),
		(21,1,3,20,'another.example','u_alice','/home/whacp/another.example','/home/whacp/another.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES
		(1,10,'bob.example',1),(1,30,'addon.example',1),(1,20,'alice.example',1),(1,21,'another.example',1)`)
	// The addon's database, cron job and worker (moved by the conversion).
	exec(`INSERT INTO databases (id, server_id, website_id, owner_id, name, created_at) VALUES (30, 1, 30, 2, 'addon_db', '')`)
	exec(`INSERT INTO cron_jobs (id, server_id, website_id, schedule, command, description, email, enabled, created_at, updated_at)
		VALUES (30, 1, 30, '0 * * * *', 'php backup.php', 'Nightly', 'ok@example.com', 1, '', '')`)
	exec(`INSERT INTO workers (id, server_id, website_id, slug, name, command, restart, enabled, created_at, updated_at)
		VALUES (30, 1, 30, 'addon-worker', 'worker', 'node app.js', 'always', 1, '', '')`)

	sa, socket, tokenFile := startSitesAgent(t, false)
	if failFTP {
		sa.failActions = map[string]string{"ftp.move_site": "the ftp move failed"}
	}
	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	return h, loginAs(t, h, "admin"), sa, db
}

// loginAs signs in as the given seeded user and returns the session cookie.
func loginAs(t *testing.T, h http.Handler, username string) *http.Cookie {
	t.Helper()
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {username}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d: %s", username, rr.Code, rr.Body.String())
	}
	return rr.Result().Cookies()[0]
}

// TestConvertRequiresPermission: the customer (no users.manage) is refused
// both the page and the POST.
func TestConvertRequiresPermission(t *testing.T) {
	h, _, _, _ := convertEnv(t, false)
	bob := loginAs(t, h, "bob")
	if rr := get(t, h, "/sites/30/convert", bob); rr.Code != http.StatusForbidden {
		t.Fatalf("GET /sites/30/convert (customer) = %d, want 403", rr.Code)
	}
	// A customer may not even open the form, so the POST is 403 before CSRF.
	req := postForm(h, "/sites/30/convert", url.Values{"username": {"newacct"}}, bob)
	if req.Code != http.StatusForbidden {
		t.Fatalf("POST /sites/30/convert (customer) = %d, want 403", req.Code)
	}
}

// TestConvertTenantGate: the reseller has users.manage but the addon belongs
// to someone else's customer — 404, not 403 or 200.
func TestConvertTenantGate(t *testing.T) {
	h, _, _, _ := convertEnv(t, false)
	dave := loginAs(t, h, "dave")
	if rr := get(t, h, "/sites/21/convert", dave); rr.Code != http.StatusNotFound {
		t.Fatalf("GET foreign addon (reseller) = %d, want 404", rr.Code)
	}
}

// TestConvertPage shows the preview and the suggested username.
func TestConvertPage(t *testing.T) {
	h, admin, _, _ := convertEnv(t, false)
	rr := get(t, h, "/sites/30/convert", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/30/convert = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		`name="username" value="addon"`,
		"Websites: addon.example",
		"Databases: addon_db",
		"DNS zone: none here",
		"Cron jobs: 1 · workers: 1",
		`action="/sites/30/convert"`,
	} {
		if !pageHas(body, want) {
			t.Fatalf("page missing %q", want)
		}
	}
	// The parent's addons page links here.
	rr = get(t, h, "/sites/10/addons", admin)
	if rr.Code != http.StatusOK || !pageHas(rr.Body.String(), `/sites/30/convert`) {
		t.Fatalf("addons page missing the convert link (status %d)", rr.Code)
	}
}

// TestConvertValidationReRenders: a bad username re-renders the page with the
// field error and the old input (minus the passwords).
func TestConvertValidationReRenders(t *testing.T) {
	h, admin, _, _ := convertEnv(t, false)
	token, admin := siteFormToken(t, h, admin, "/sites/30/convert")
	rr := postForm(h, "/sites/30/convert", url.Values{
		"_token":                {token},
		"username":              {"root"},
		"email":                 {"keep@example.test"},
		"password":              {"somepass123"},
		"password_confirmation": {"somepass123"},
	}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-render = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Please correct the following",
		"also the website's system account",
		`name="email" value="keep@example.test"`,
	} {
		if !pageHas(body, want) {
			t.Fatalf("re-render missing %q", want)
		}
	}
	if pageHas(body, "somepass123") {
		t.Fatal("the submitted password was echoed back")
	}
}

// TestConvertSuccess converts the addon: the redirect, the flash, the
// re-owned row and the agent steps.
func TestConvertSuccess(t *testing.T) {
	h, admin, sa, db := convertEnv(t, false)
	token, admin := siteFormToken(t, h, admin, "/sites/30/convert")
	rr := postForm(h, "/sites/30/convert", url.Values{
		"_token":                {token},
		"username":              {"newacct"},
		"email":                 {"newacct@example.test"},
		"password":              {"Tr0ub4dor&3xtra-long"},
		"password_confirmation": {"Tr0ub4dor&3xtra-long"},
	}, admin)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/addons" {
		t.Fatalf("redirect = %d %q, want 303 /sites/10/addons", rr.Code, rr.Header().Get("Location"))
	}
	// The addon is re-owned: new system user and owner, detached from the
	// parent. The parent's row is untouched.
	var sysUser string
	var parent sql.NullInt64
	var owner int64
	if err := db.QueryRow(`SELECT system_user, owner_id, parent_id FROM websites WHERE id = 30`).Scan(&sysUser, &owner, &parent); err != nil {
		t.Fatal(err)
	}
	if sysUser != "newacct" || parent.Valid {
		t.Fatalf("addon after convert = user %q parent %+v", sysUser, parent)
	}
	var newUsername string
	if err := db.QueryRow(`SELECT username FROM users WHERE id = ?`, owner).Scan(&newUsername); err != nil {
		t.Fatal(err)
	}
	if newUsername != "newacct" {
		t.Fatalf("owner %d is user %q", owner, newUsername)
	}
	// The database moved with it.
	var dbOwner int64
	if err := db.QueryRow(`SELECT owner_id FROM databases WHERE id = 30`).Scan(&dbOwner); err != nil {
		t.Fatal(err)
	}
	if dbOwner != owner {
		t.Fatalf("database owner = %d, want %d", dbOwner, owner)
	}
	// Agent steps: re-apply, permissions, FTP move, cron, worker.
	actions := sa.called()
	for _, want := range []string{"sites.apply", "sites.permissions", "ftp.move_site", "cron.apply", "workers.apply"} {
		found := false
		for _, a := range actions {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("agent never called %q; got %v", want, actions)
		}
	}
	// The flash lands on the parent's addons page (the flashed state is
	// sealed into the cookie the POST response sets).
	rr = get(t, h, "/sites/10/addons", refreshCookie(admin, rr))
	if rr.Code != http.StatusOK || !pageHas(rr.Body.String(), "addon.example is now the hosting account newacct") {
		t.Fatalf("addons page flash missing (status %d)", rr.Code)
	}
	// The audit trail.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'account.convert_addon' AND result = 'success'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("audit rows = %d, want 1", n)
	}
}

// TestConvertAgentFailureFlash: when the FTP move fails, the sites are put
// back and the parent's addons page carries the failure message.
func TestConvertAgentFailureFlash(t *testing.T) {
	h, admin, _, db := convertEnv(t, true)
	token, admin := siteFormToken(t, h, admin, "/sites/30/convert")
	rr := postForm(h, "/sites/30/convert", url.Values{
		"_token":                {token},
		"username":              {"newacct"},
		"email":                 {"newacct@example.test"},
		"password":              {"Tr0ub4dor&3xtra-long"},
		"password_confirmation": {"Tr0ub4dor&3xtra-long"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("redirect = %d, want 303 (the failure is flashed)", rr.Code)
	}
	rr = get(t, h, "/sites/10/addons", refreshCookie(admin, rr))
	body := rr.Body.String()
	if !pageHas(body, "addon.example was not converted; everything was put back as it was.") {
		t.Fatalf("failure flash missing: %s", body)
	}
	// The row is back where it started.
	var sysUser string
	var parent sql.NullInt64
	var owner int64
	if err := db.QueryRow(`SELECT system_user, owner_id, parent_id FROM websites WHERE id = 30`).Scan(&sysUser, &owner, &parent); err != nil {
		t.Fatal(err)
	}
	if sysUser != "u_bob" || owner != 2 || !parent.Valid || parent.Int64 != 10 {
		t.Fatalf("addon after failed convert = user %q owner %d parent %+v", sysUser, owner, parent)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = 'newacct'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("the new login survived the failed convert")
	}
}

// TestConvertCSRF: a POST without a token is refused.
func TestConvertCSRF(t *testing.T) {
	h, admin, _, _ := convertEnv(t, false)
	rr := postForm(h, "/sites/30/convert", url.Values{"username": {"newacct"}}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST without CSRF = %d, want 400", rr.Code)
	}
}
