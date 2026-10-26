package http

// This suite drives the FTP area (batch 56): the server's pure-ftpd page
// (install as a job, configure, disable — server.manage / packages.manage)
// and the website's FTP/SFTP tab (the virtual accounts + SFTP — ftp.manage),
// including the plan limit on ftp_accounts (the external counter through
// the agent's ftp.status).

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// ftpEnv seeds the FTP permissions on top of the import environment:
// administrators get server.manage + packages.manage + ftp.manage, the
// customer (bob, website 10 / u_bob) gets ftp.manage.
func ftpEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
	t.Helper()
	h, cookie, sa, db := importEnv(t, loginAs)
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(91,'server.manage','sm','server'),(92,'packages.manage','pm','server'),(93,'ftp.manage','fm','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,91),(1,92),(1,93),(2,93)`)
	return h, cookie, sa, db
}

func TestFtpIndexServer(t *testing.T) {
	h, cookie, _, _ := ftpEnv(t, "admin")
	rr := get(t, h, "/ftp", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ftp = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"FTP server", "FTP address", "ftp://",
		"Pure-FTPd running",
		"designer@bob.example", "bob.example", "public_html", "100.0 MB", // the joined website row
		"40110–40210", "TLS required (FTPS)",
		"CN=whacp-ftp", "aabb:ccdd:eeff", // the certificate line
		"/sites/10/ftp#new-ftp", // the Add FTP menu
		"Save and restart",      // configured → the modal offers a restart
	} {
		if !pageHas(body, want) {
			t.Errorf("server page missing %q", want)
		}
	}
	if pageHas(body, "copy the password now") {
		t.Error("no secret may flash without a generated password")
	}

	// The customer cannot see the server page.
	h2, c2, _, _ := ftpEnv(t, "bob")
	if rr2 := get(t, h2, "/ftp", c2); rr2.Code != http.StatusForbidden {
		t.Errorf("customer GET /ftp = %d, want 403", rr2.Code)
	}
}

func TestFtpIndexNotInstalled(t *testing.T) {
	h, cookie, sa, _ := ftpEnv(t, "admin")
	sa.ftpStatus = "missing"
	rr := get(t, h, "/ftp", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ftp = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"not installed", "Install pure-ftpd", "vsftpd"} {
		if !pageHas(body, want) {
			t.Errorf("not-installed page missing %q", want)
		}
	}

	// Stopped: configured but not running, no Stop form.
	sa.ftpStatus = "stopped"
	rr = get(t, h, "/ftp", cookie)
	body = rr.Body.String()
	if !pageHas(body, "Pure-FTPd stopped") || pageHas(body, "Stop the FTP server?") {
		t.Error("stopped state not rendered")
	}
}

func TestFtpInstallTracksJob(t *testing.T) {
	h, cookie, sa, db := ftpEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/ftp")
	rr := postForm(h, "/ftp/install", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ftp" {
		t.Fatalf("install = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if got := agentParam(t, sa, "packages.install", "name"); got != "pure-ftpd" {
		t.Errorf("packages.install name = %v", got)
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM agent_jobs WHERE job_id = 'abcdef0123456789'`).Scan(&kind); err != nil {
		t.Errorf("job not tracked: %v", err)
	} else if kind != "Install pure-ftpd" {
		t.Errorf("job kind = %q", kind)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'packages.install' AND target_id = 'ftp'`) != 1 {
		t.Error("packages.install audit missing")
	}
	rr = get(t, h, "/ftp", cookie)
	body := rr.Body.String()
	if !pageHas(body, "Installing pure-ftpd") || !pageHas(body, "abcdef0123456789") {
		t.Error("install flash missing")
	}

	// A customer cannot install (the agent is never reached).
	h2, c2, sa2, _ := ftpEnv(t, "bob")
	token2, c2 := pageToken(t, h2, c2, "/sites/10/ftp")
	if rr := postForm(h2, "/ftp/install", url.Values{"_token": {token2}}, c2); rr.Code != http.StatusForbidden {
		t.Errorf("customer install = %d, want 403", rr.Code)
	}
	for _, a := range sa2.called() {
		if a == "packages.install" {
			t.Error("the customer's install must not reach the agent")
		}
	}
}

func TestFtpConfigure(t *testing.T) {
	h, cookie, sa, db := ftpEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/ftp")
	rr := postForm(h, "/ftp/configure", url.Values{
		"_token": {token}, "passive_min": {"40110"}, "passive_max": {"40210"},
		"max_clients": {"50"}, "max_per_ip": {"8"}, "passive_ip": {"203.0.113.9"},
		"tls": {"optional"}, "confirm_replace": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ftp" {
		t.Fatalf("configure = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if got := agentParam(t, sa, "ftp.configure", "passive_min"); got != float64(40110) {
		t.Errorf("passive_min = %v", got)
	}
	if got := agentParam(t, sa, "ftp.configure", "passive_ip"); got != "203.0.113.9" {
		t.Errorf("passive_ip = %v", got)
	}
	if got := agentParam(t, sa, "ftp.configure", "tls"); got != "optional" {
		t.Errorf("tls = %v", got)
	}
	if got := agentParam(t, sa, "ftp.configure", "confirm_replace_service"); got != true {
		t.Errorf("confirm_replace_service = %v", got)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ftp.configure'`) != 1 {
		t.Error("ftp.configure audit missing")
	}
	rr = get(t, h, "/ftp", cookie)
	if !pageHas(rr.Body.String(), "The FTP server is running.") {
		t.Error("configure success flash missing")
	}

	// An out-of-range span is rejected without the agent call.
	before := len(sa.called())
	token, cookie = pageToken(t, h, cookie, "/ftp")
	rr = postForm(h, "/ftp/configure", url.Values{
		"_token": {token}, "passive_min": {"1024"}, "passive_max": {"20000"},
		"max_clients": {"50"}, "max_per_ip": {"8"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("bad configure = %d", rr.Code)
	}
	rr = get(t, h, "/ftp", cookie)
	if !pageHas(rr.Body.String(), "at most 5000 ports wide") {
		t.Error("span error flash missing")
	}
	if len(sa.called()) != before {
		t.Error("a rejected configure must not reach the agent")
	}
	// A per-address limit above the total is rejected.
	token, cookie = pageToken(t, h, cookie, "/ftp")
	rr = postForm(h, "/ftp/configure", url.Values{
		"_token": {token}, "passive_min": {"1024"}, "passive_max": {"2048"},
		"max_clients": {"10"}, "max_per_ip": {"50"},
	}, cookie)
	rr = get(t, h, "/ftp", cookie)
	if !pageHas(rr.Body.String(), "no more per address than in total") {
		t.Error("per-ip error flash missing")
	}
}

func TestFtpDisable(t *testing.T) {
	h, cookie, sa, _ := ftpEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/ftp")
	rr := postForm(h, "/ftp/disable", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ftp" {
		t.Fatalf("disable = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	found := false
	for _, a := range sa.called() {
		if a == "ftp.disable" {
			found = true
		}
	}
	if !found {
		t.Fatal("ftp.disable never called")
	}
	rr = get(t, h, "/ftp", cookie)
	if !pageHas(rr.Body.String(), "The FTP server was stopped. Accounts are kept.") {
		t.Error("disable flash missing")
	}
}

func TestSiteFtpPage(t *testing.T) {
	h, cookie, sa, _ := ftpEnv(t, "bob")
	rr := get(t, h, "/sites/10/ftp", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/10/ftp = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"FTP accounts", "1 of 25",
		"designer@bob.example", "public_html", "100.0 MB",
		"Add FTP account",
		"FTP with explicit TLS", // TLS required (the default settings)
		"example.com",           // the connect hint uses the request host
		"Allow SFTP for", "u_bob",
		"SSH, SFTP and the web terminal",
		"SHA256:ftptest", "bob@laptop", // the existing key line
	} {
		if !pageHas(body, want) {
			t.Errorf("site page missing %q", want)
		}
	}
	if pageHas(body, "copy the password now") {
		t.Error("an ungenerated secret must not show")
	}

	// A stopped server: no Add button, SFTP off, the hint points at /ftp.
	sa.ftpStatus = "stopped"
	rr = get(t, h, "/sites/10/ftp", cookie)
	body = rr.Body.String()
	if !pageHas(body, "The FTP server is not running on this server") {
		t.Error("stopped hint missing")
	}
	if pageHas(body, "Add FTP account") {
		t.Error("the Add button must be hidden while the server is stopped")
	}
	if !pageHas(body, `>off<`) {
		t.Error("the SFTP badge must be off")
	}
}

func TestSiteFtpSaveCreate(t *testing.T) {
	h, cookie, sa, db := ftpEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/ftp")
	rr := postForm(h, "/sites/10/ftp/accounts", url.Values{
		"_token": {token}, "name": {"designer2"}, "dir": {"public_html"},
		"quota_mb": {"50"}, "password": {"longenough123"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/ftp" {
		t.Fatalf("save = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if got := agentParam(t, sa, "ftp.account_save", "name"); got != "designer2@bob.example" {
		t.Errorf("account login = %v", got)
	}
	if got := agentParam(t, sa, "ftp.account_save", "user"); got != "u_bob" {
		t.Errorf("account user = %v", got)
	}
	if got := agentParam(t, sa, "ftp.account_save", "dir"); got != "public_html" {
		t.Errorf("account dir = %v", got)
	}
	if got := agentParam(t, sa, "ftp.account_save", "quota_mb"); got != float64(50) {
		t.Errorf("account quota = %v", got)
	}
	if got := agentParam(t, sa, "ftp.account_save", "password"); got != "longenough123" {
		t.Errorf("account password = %v", got)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ftp.account_create'`) != 1 {
		t.Error("ftp.account_create audit missing")
	}
	rr = get(t, h, "/sites/10/ftp", cookie)
	if !pageHas(rr.Body.String(), "FTP account designer2@bob.example created.") {
		t.Error("create flash missing")
	}

	// A generated password flashes the one-time secret.
	token, cookie = pageToken(t, h, cookie, "/sites/10/ftp")
	rr = postForm(h, "/sites/10/ftp/accounts", url.Values{
		"_token": {token}, "name": {"intern"}, "dir": {""}, "quota_mb": {"0"}, "generate": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("generate save = %d", rr.Code)
	}
	pw, _ := agentParam(t, sa, "ftp.account_save", "password").(string)
	if len(pw) < 10 {
		t.Errorf("generated password = %q", pw)
	}
	rr = get(t, h, "/sites/10/ftp", cookie)
	body := rr.Body.String()
	if !pageHas(body, "FTP password for intern@bob.example") || !pageHas(body, "copy the password now") {
		t.Error("the one-time secret must flash on the next page load")
	}
}

func TestSiteFtpSaveValidation(t *testing.T) {
	h, cookie, sa, _ := ftpEnv(t, "bob")
	post := func(values url.Values, c *http.Cookie) *http.Cookie {
		token, c := pageToken(t, h, c, "/sites/10/ftp")
		values.Set("_token", token)
		rr := postForm(h, "/sites/10/ftp/accounts", values, c)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("save = %d", rr.Code)
		}
		return c
	}
	body := func(c *http.Cookie) string {
		return get(t, h, "/sites/10/ftp", c).Body.String()
	}

	// A name with a disallowed character (the input is lower-cased first,
	// so only the shape is rejected).
	cookie = post(url.Values{"name": {"bad name"}, "dir": {"public_html"}, "quota_mb": {"0"}}, cookie)
	if msg := body(cookie); !strings.Contains(msg, "lower-case letters") {
		t.Error("name error flash missing")
	}
	// A path escape in the folder.
	cookie = post(url.Values{"name": {"ok"}, "dir": {"../etc"}, "quota_mb": {"0"}}, cookie)
	if msg := body(cookie); !strings.Contains(msg, "Enter a folder inside the website") {
		t.Error("dir error flash missing")
	}
	// A short password.
	cookie = post(url.Values{"name": {"ok"}, "dir": {"public_html"}, "quota_mb": {"0"}, "password": {"short"}}, cookie)
	if msg := body(cookie); !strings.Contains(msg, "10–128 characters") {
		t.Error("password error flash missing")
	}
	// A new account needs a password (or a generated one).
	cookie = post(url.Values{"name": {"ok"}, "dir": {"public_html"}, "quota_mb": {"0"}}, cookie)
	if msg := body(cookie); !strings.Contains(msg, "Enter a password or generate one") {
		t.Error("missing-password error flash missing")
	}
	// Editing a foreign account.
	cookie = post(url.Values{"existing": {"1"}, "name": {"x@other.example"}, "dir": {""}, "quota_mb": {"0"}}, cookie)
	if msg := body(cookie); !strings.Contains(msg, "does not belong to the website") {
		t.Error("foreign-account error flash missing")
	}
	for _, a := range sa.called() {
		if a == "ftp.account_save" {
			t.Error("no rejected form may reach the agent")
		}
	}
}

func TestSiteFtpSaveExisting(t *testing.T) {
	h, cookie, sa, db := ftpEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/ftp")
	rr := postForm(h, "/sites/10/ftp/accounts", url.Values{
		"_token": {token}, "existing": {"1"}, "name": {"designer@bob.example"},
		"dir": {"public_html/sub"}, "quota_mb": {"0"}, "generate": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("existing save = %d", rr.Code)
	}
	if got := agentParam(t, sa, "ftp.account_save", "name"); got != "designer@bob.example" {
		t.Errorf("existing login = %v", got)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ftp.account_update'`) != 1 {
		t.Error("ftp.account_update audit missing")
	}
	rr = get(t, h, "/sites/10/ftp", cookie)
	body := rr.Body.String()
	if !pageHas(body, "FTP account designer@bob.example saved.") {
		t.Error("existing-save flash missing")
	}
	if !pageHas(body, "copy the password now") {
		t.Error("a regenerated password must flash the secret")
	}
}

func TestSiteFtpPlanLimit(t *testing.T) {
	h, cookie, sa, db := ftpEnv(t, "bob")
	if _, err := db.Exec(`UPDATE plans SET max_ftp_accounts = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	token, cookie := pageToken(t, h, cookie, "/sites/10/ftp")
	rr := postForm(h, "/sites/10/ftp/accounts", url.Values{
		"_token": {token}, "name": {"another"}, "dir": {"public_html"}, "quota_mb": {"0"}, "password": {"longenough123"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("save at the limit = %d", rr.Code)
	}
	rr = get(t, h, "/sites/10/ftp", cookie)
	if !pageHas(rr.Body.String(), "allows at most 1 FTP accounts") {
		t.Errorf("plan limit flash missing:\n%s", rr.Body.String())
	}
	for _, a := range sa.called() {
		if a == "ftp.account_save" {
			t.Error("an over-limit account must not reach the agent")
		}
	}
}

func TestSiteFtpDelete(t *testing.T) {
	h, cookie, sa, db := ftpEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/ftp")
	rr := postForm(h, "/sites/10/ftp/accounts/delete", url.Values{
		"_token": {token}, "name": {"designer@bob.example"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/ftp" {
		t.Fatalf("delete = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if got := agentParam(t, sa, "ftp.account_delete", "name"); got != "designer@bob.example" {
		t.Errorf("deleted login = %v", got)
	}
	if got := agentParam(t, sa, "ftp.account_delete", "user"); got != "u_bob" {
		t.Errorf("deleted user = %v", got)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ftp.account_delete'`) != 1 {
		t.Error("ftp.account_delete audit missing")
	}
	rr = get(t, h, "/sites/10/ftp", cookie)
	if !pageHas(rr.Body.String(), "FTP account designer@bob.example deleted. Its files are kept.") {
		t.Error("delete flash missing")
	}

	// A foreign account cannot be deleted.
	token, cookie = pageToken(t, h, cookie, "/sites/10/ftp")
	rr = postForm(h, "/sites/10/ftp/accounts/delete", url.Values{
		"_token": {token}, "name": {"x@other.example"},
	}, cookie)
	rr = get(t, h, "/sites/10/ftp", cookie)
	if !pageHas(rr.Body.String(), "does not belong to the website") {
		t.Error("foreign delete must be rejected")
	}
}

func TestSiteSftp(t *testing.T) {
	h, cookie, sa, db := ftpEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/ftp")
	rr := postForm(h, "/sites/10/sftp", url.Values{
		"_token": {token}, "enabled": {"1"},
		"keys":           {"ssh-ed25519 AAAA bob@laptop\n# a comment\n\n"},
		"password_login": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/ftp" {
		t.Fatalf("sftp = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if got := agentParam(t, sa, "sftp.configure", "user"); got != "u_bob" {
		t.Errorf("sftp user = %v", got)
	}
	if got := agentParam(t, sa, "sftp.configure", "enabled"); got != true {
		t.Errorf("sftp enabled = %v", got)
	}
	if got := agentParam(t, sa, "sftp.configure", "password_login"); got != true {
		t.Errorf("sftp password_login = %v", got)
	}
	keys, _ := agentParam(t, sa, "sftp.configure", "keys").([]any)
	if len(keys) != 1 || keys[0] != "ssh-ed25519 AAAA bob@laptop" {
		t.Errorf("sftp keys = %v", agentParam(t, sa, "sftp.configure", "keys"))
	}
	sa.mu.Lock()
	last := sa.params[len(sa.params)-1]
	sa.mu.Unlock()
	if _, present := last["password"]; present {
		t.Error("an empty password must not be sent")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'sftp.configure'`) != 1 {
		t.Error("sftp.configure audit missing")
	}
	rr = get(t, h, "/sites/10/ftp", cookie)
	if !pageHas(rr.Body.String(), "SFTP access saved.") {
		t.Error("sftp flash missing")
	}

	// A short password is rejected without the agent call.
	before := len(sa.called())
	token, cookie = pageToken(t, h, cookie, "/sites/10/ftp")
	rr = postForm(h, "/sites/10/sftp", url.Values{
		"_token": {token}, "sftp_password": {"short"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("short-password sftp = %d", rr.Code)
	}
	if len(sa.called()) != before {
		t.Error("a rejected sftp form must not reach the agent")
	}
	rr = get(t, h, "/sites/10/ftp", cookie)
	if !pageHas(rr.Body.String(), "10–128 characters") {
		t.Error("sftp password error flash missing")
	}
}
