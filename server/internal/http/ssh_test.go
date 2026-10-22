package http

// This suite drives the SSH area (SecurityController::ssh et al.): the page
// (status bar, the settings form, the administrator key table, the
// generate/add forms, agent down), the guarded ssh.configure, the key
// changes (add, generate with the once-shown private key, remove — all
// role-admin), and their validations.

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

// sshEnv seeds an administrator and a customer with security.manage (bob —
// the settings form, but the key endpoints are role-admin). The fake agent
// answers the ssh actions.
func sshEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(2,1),(2,2),(2,3)`)
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
	sa.guardChanged = true
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

func TestSSHPage(t *testing.T) {
	h, _, _ := sshEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/ssh", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		// Status bar.
		"SSH server", "Port</span> 22", "Root login</span> With SSH keys only",
		`<span class="wcp-good">off</span>`, "Keys installed</span> root",
		// The settings form (the effective values).
		"Validate and apply", "sshd -t", "/etc/ssh/sshd_config.d/50-whacp.conf",
		`value="22"`, `value="4"`, `value="prohibit-password" selected`,
		// The key table.
		"Administrator SSH keys", "ssh-ed25519", "root@whacp", "SHA256:RootKeyFingerprint",
		"wheel-admin", "No keys (2 other line(s) kept as they are)", "Remove",
		// The key forms (role admin).
		"Generate a new key", "Or add an existing public key",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestSSHPageCustomer(t *testing.T) {
	h, _, _ := sshEnv(t)
	// bob has security.manage (the settings form) but is not role admin.
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/security/ssh", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"SSH server", "Validate and apply", "Only administrators add or remove SSH keys.",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, not := range []string{"Generate a new key", "Or add an existing public key"} {
		if strings.Contains(body, not) {
			t.Errorf("a key form shows for a non-admin: %q", not)
		}
	}
}

func TestSSHPageAgentDown(t *testing.T) {
	h, sa, _ := sshEnv(t)
	sa.failActions = map[string]string{"ssh.status": "the socket is closed"}
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/security/ssh", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "the socket is closed") || strings.Contains(body, "SSH server") {
		t.Errorf("agent-down page wrong: %s", body[:min(300, len(body))])
	}
}

func TestSSHConfigure(t *testing.T) {
	h, sa, db := sshEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/ssh")
	rr := postForm(h, "/security/ssh", url.Values{
		"_token": {token}, "port": {"2222"}, "permit_root_login": {"no"},
		"password_authentication": {"0"}, "max_auth_tries": {"6"}, "x11_forwarding": {"1"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/ssh",
		"Applied. Check that you can still connect, then confirm within 2 minutes — otherwise the change is undone automatically.")
	if got := agentParam(t, sa, "ssh.configure", "port"); got != float64(2222) {
		t.Errorf("port = %v", got)
	}
	if got := agentParam(t, sa, "ssh.configure", "permit_root_login"); got != "no" {
		t.Errorf("permit_root_login = %v", got)
	}
	if got := agentParam(t, sa, "ssh.configure", "password_authentication"); got != false {
		t.Errorf("password_authentication = %v", got)
	}
	if got := agentParam(t, sa, "ssh.configure", "x11_forwarding"); got != true {
		t.Errorf("x11_forwarding = %v", got)
	}
	var target, result string
	if err := db.QueryRow(`SELECT target_type, result FROM audit_logs WHERE action = 'ssh.configure'`).Scan(&target, &result); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if target != "server" || result != "success" {
		t.Errorf("audit = %s/%s", target, result)
	}
}

func TestSSHConfigureDefaults(t *testing.T) {
	h, sa, _ := sshEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/ssh")
	rr := postForm(h, "/security/ssh", url.Values{"_token": {token}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d", rr.Code)
	}
	if got := agentParam(t, sa, "ssh.configure", "port"); got != float64(22) {
		t.Errorf("port = %v", got)
	}
	if got := agentParam(t, sa, "ssh.configure", "permit_root_login"); got != "prohibit-password" {
		t.Errorf("permit_root_login = %v", got)
	}
	if got := agentParam(t, sa, "ssh.configure", "max_auth_tries"); got != float64(4) {
		t.Errorf("max_auth_tries = %v", got)
	}
}

func TestSSHAddKey(t *testing.T) {
	h, sa, db := sshEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/ssh")
	rr := postForm(h, "/security/ssh/keys", url.Values{
		"_token": {token}, "user": {"root"}, "key": {"ssh-ed25519 AAAA you@laptop "},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("add key = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/ssh",
		"Key SHA256:AddedKeyFingerprint00000000000000000000000 added for root. Test a new SSH connection with it before turning off password logins.")
	if got := agentParam(t, sa, "ssh.key_add", "key"); got != "ssh-ed25519 AAAA you@laptop" {
		t.Errorf("key = %v (the agent gets the trimmed key)", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'ssh.key_add'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("ssh.key_add audits = %d, %v", n, err)
	}
}

func TestSSHGenerateKey(t *testing.T) {
	h, sa, db := sshEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/ssh")
	rr := postForm(h, "/security/ssh/keys/generate", url.Values{
		"_token": {token}, "user": {"root"}, "comment": {"office laptop"},
		"passphrase": {"12345678"}, "passphrase_confirmation": {"12345678"},
	}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("generate = %d: %s", rr.Code, rr.Body.String())
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store, max-age=0" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if p := rr.Header().Get("Pragma"); p != "no-cache" {
		t.Errorf("Pragma = %q", p)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"New SSH key", "Download the private key now.", "BEGIN ENCRYPTED PRIVATE KEY",
		"It is protected with the passphrase you chose.",
		"whacp-root-example.com.pem", "ssh -i ~/.ssh/whacp-root-example.com.pem root@example.com",
		"allowed for <strong>root</strong>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The allowed public key: ssh-rsa, the sanitized comment.
	if got, _ := agentParam(t, sa, "ssh.key_add", "key").(string); !strings.HasPrefix(got, "ssh-rsa ") || !strings.HasSuffix(got, " office-laptop") {
		t.Errorf("public key = %q", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'ssh.key_generate'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("ssh.key_generate audits = %d, %v", n, err)
	}
	// Without a passphrase the file is not encrypted (and says so).
	rr = postForm(h, "/security/ssh/keys/generate", url.Values{
		"_token": {token}, "user": {"root"}, "comment": {"server"},
	}, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("generate (plain) = %d", rr.Code)
	}
	body = rr.Body.String()
	if !strings.Contains(body, "BEGIN PRIVATE KEY") || !strings.Contains(body, "It has no passphrase") {
		t.Errorf("the unencrypted key page is wrong")
	}
}

func TestSSHGenerateKeyFailures(t *testing.T) {
	h, sa, _ := sshEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/ssh")
	cases := []struct {
		extra url.Values
		want  string
	}{
		{url.Values{"passphrase": {"12345678"}, "passphrase_confirmation": {"87654321"}}, "The passphrases do not match."},
		{url.Values{"passphrase": {"1234567"}, "passphrase_confirmation": {"1234567"}}, "Use a passphrase of at least 8 characters, or leave it empty."},
	}
	for _, c := range cases {
		form := url.Values{"_token": {token}, "user": {"root"}}
		for k, v := range c.extra {
			form[k] = v
		}
		rr := postForm(h, "/security/ssh/keys/generate", form, admin)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("generate (%s) = %d", c.want, rr.Code)
		}
		flashAt(t, h, refreshCookie(admin, rr), "/security/ssh", c.want)
	}
	// A non-admin (with security.manage) is refused by the role gate.
	bob := loginAsUser(t, h, "bob")
	tokenB, bob := siteFormToken(t, h, bob, "/security/ssh")
	rr := postForm(h, "/security/ssh/keys/generate", url.Values{"_token": {tokenB}, "user": {"root"}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("generate (bob) = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(bob, rr), "/security/ssh", "Only administrators manage the server's SSH keys.")
	for _, a := range sa.called() {
		if a == "ssh.key_add" {
			t.Errorf("a refused generation reached the agent: %v", sa.called())
		}
	}
}

func TestSSHRemoveKey(t *testing.T) {
	h, sa, db := sshEnv(t)
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/ssh")
	rr := postForm(h, "/security/ssh/keys/remove", url.Values{
		"_token": {token}, "user": {"root"}, "fingerprint": {"SHA256:RootKeyFingerprint000000000000000000000"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("remove = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/ssh", "Key removed from root.")
	if got := agentParam(t, sa, "ssh.key_remove", "fingerprint"); got != "SHA256:RootKeyFingerprint000000000000000000000" {
		t.Errorf("fingerprint = %v", got)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'ssh.key_remove'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("ssh.key_remove audits = %d, %v", n, err)
	}
}

func TestSSHAgentFailure(t *testing.T) {
	h, sa, _ := sshEnv(t)
	sa.failActions = map[string]string{"ssh.configure": "sshd -t failed: bad port"}
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/security/ssh")
	rr := postForm(h, "/security/ssh", url.Values{"_token": {token}, "port": {"99999"}}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("configure = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/security/ssh", "sshd -t failed: bad port")
}

func TestSSHBadCSRF(t *testing.T) {
	h, _, _ := sshEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := postForm(h, "/security/ssh", url.Values{"port": {"2222"}}, admin)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("configure without token = %d, want 400", rr.Code)
	}
}
