package http

// This suite drives the server-administration SSL/TLS area
// (SslCenterController): the hosts and certificates tabs, certbot renewal,
// the keys and CSR pages (download, create, install, delete), the
// server-wide TLS configuration, the panel/mail service certificates and the
// ACME account, plus AutoSSL. The agent is the shared recording fake; its
// ssl.* replies live in websites_write_test.go.

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// sslCenterEnv seeds an administrator with the whole SSL/TLS area, a
// read-only visitor (customer with ssl.view only) and a scoped customer
// (bob) who owns website 10. Returns the handler, the login cookie for
// loginAs, the recording agent and the DB.
func sslCenterEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
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
		(1,'administrator','Admin','all',''),(2,'customer','Customer','own',''),
		(3,'viewer','Viewer','all','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'ssl.view','v','ssl'),(2,'ssl.manage','m','ssl'),
		(3,'server.manage','s','server'),(4,'websites.view','w','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(1,4),(2,4),(3,1),(3,4)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','admin@whacp.test','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','bob@whacp.test','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'vera','vera@whacp.test','Vera',?,3,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,https,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active',1,'','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1)`)

	sa, socket, tokenFile := startSitesAgent(t, false)
	h, cookie := sslCenterServer(t, db, socket, tokenFile, loginAs)
	return h, cookie, sa, db
}

// sslCenterServer builds the handler and signs in loginAs.
func sslCenterServer(t *testing.T, db *sql.DB, socket, tokenFile, loginAs string) (http.Handler, *http.Cookie) {
	t.Helper()
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
	c, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {loginAs}, "password": {loginPassword}}, c)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d", loginAs, rr.Code)
	}
	return h, rr.Result().Cookies()[0]
}

// sslCenterPost submits a CSRF-checked form.
func sslCenterPost(t *testing.T, h http.Handler, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := pageToken(t, h, cookie, "/ssl/hosts")
	form.Set("_token", token)
	return postForm(h, path, form, cookie)
}

// sslAction returns the params of the last call to an agent action.
func sslAction(sa *sitesAgent, action string) (map[string]any, bool) {
	sa.mu.Lock()
	defer sa.mu.Unlock()
	for i := len(sa.actions) - 1; i >= 0; i-- {
		if sa.actions[i] == action {
			return sa.params[i], true
		}
	}
	return nil, false
}

// sslActionRan reports whether the agent was asked for an action.
func sslActionRan(sa *sitesAgent, action string) bool {
	_, ok := sslAction(sa, action)
	return ok
}

func TestSslCenterDeniedForScopedAccount(t *testing.T) {
	h, cookie, _, _ := sslCenterEnv(t, "bob")
	for _, path := range []string{"/ssl/hosts", "/ssl/certificates", "/ssl/keys", "/ssl/autossl", "/ssl/tls", "/ssl/services"} {
		if rr := get(t, h, path, cookie); rr.Code != http.StatusForbidden {
			t.Fatalf("GET %s as a scoped customer = %d, want 403", path, rr.Code)
		}
	}
}

func TestSslCenterHosts(t *testing.T) {
	h, cookie, _, _ := sslCenterEnv(t, "admin")
	rr := get(t, h, "/ssl/hosts", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ssl/hosts = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"SSL/TLS", "bob.example", "old.example", "self.example",
		"10 days left", "without HTTPS", "self-signed",
		`href="/sites/10/ssl"`, "AutoSSL for new websites is",
		"Renewal of Let's Encrypt certificates:",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("hosts page is missing %q", want)
		}
	}
	// The renewal timer is active in the fake, so no "turn on" button.
	if strings.Contains(body, `action="/ssl/renewal"`) {
		t.Error("renewal button shown although the timer is active")
	}
}

func TestSslCenterCertificates(t *testing.T) {
	h, cookie, _, _ := sslCenterEnv(t, "admin")
	rr := get(t, h, "/ssl/certificates", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ssl/certificates = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"unused.example", "Let's Encrypt", "Installed in WhaCP", "Elsewhere (read only)",
		"in use", "chain of 2", "Details",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("certificates page is missing %q", want)
		}
	}
	// Only the unused WhaCP certificate gets a delete button.
	if n := strings.Count(body, `action="/ssl/certificates/delete"`); n != 1 {
		t.Errorf("delete buttons = %d, want 1", n)
	}
}

func TestSslCenterCertificateDelete(t *testing.T) {
	h, cookie, sa, db := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/certificates/delete",
		url.Values{"source": {"letsencrypt"}, "name": {"old.example"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/certificates" {
		t.Fatalf("delete = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	params, ok := sslAction(sa, "ssl.delete")
	if !ok {
		t.Fatal("ssl.delete was not called")
	}
	if params["cert_name"] != "old.example" {
		t.Errorf("cert_name = %v", params["cert_name"])
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'ssl.certificate_delete'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
}

func TestSslCenterCertificateDeleteUnknownSource(t *testing.T) {
	h, cookie, sa, _ := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/certificates/delete",
		url.Values{"source": {"other"}, "name": {"other.example"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	if sslActionRan(sa, "ssl.delete") || sslActionRan(sa, "ssl.custom_delete") {
		t.Error("an 'other' certificate was deleted")
	}
}

func TestSslCenterKeysAndCSR(t *testing.T) {
	h, cookie, _, _ := sslCenterEnv(t, "admin")
	rr := get(t, h, "/ssl/keys", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ssl/keys = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"bob.example-20261024000000", "ecdsa256", "New key and CSR",
		"Domain names", "ECDSA P-256 (recommended)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("keys page is missing %q", want)
		}
	}

	rr = get(t, h, "/ssl/keys?id=bob.example-20261024000000", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ssl/keys?id = %d", rr.Code)
	}
	body = rr.Body.String()
	for _, want := range []string{
		"BEGIN CERTIFICATE REQUEST", `href="/ssl/keys/csr?id=bob.example-20261024000000"`,
		"Install on a website", "bob.example",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("key page is missing %q", want)
		}
	}
}

func TestSslCSRDownload(t *testing.T) {
	h, cookie, _, _ := sslCenterEnv(t, "admin")
	rr := get(t, h, "/ssl/keys/csr?id=bob.example-20261024000000", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET csr = %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/pkcs10" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rr.Header().Get("Content-Disposition"); !strings.Contains(got, "bob.example-20261024000000.csr") {
		t.Errorf("Content-Disposition = %q", got)
	}
	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if !strings.Contains(rr.Body.String(), "BEGIN CERTIFICATE REQUEST") {
		t.Error("body is not a CSR")
	}
}

func TestSslCSRDownloadMissing(t *testing.T) {
	h, cookie, _, _ := sslCenterEnv(t, "admin")
	rr := get(t, h, "/ssl/keys/csr?id=nope", cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/keys" {
		t.Fatalf("missing csr = %d %q", rr.Code, rr.Header().Get("Location"))
	}
}

func TestSslKeyCreate(t *testing.T) {
	h, cookie, sa, _ := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/keys", url.Values{
		"names":       {"shop.example, www.shop.example"},
		"country":     {"de"},
		"key_type":    {"rsa3072"},
		"email":       {"admin@whacp.test"},
		"self_signed": {"1"}, "self_signed_days": {"4000"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("create key = %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/ssl/keys?id=shop.example-20261024010101" {
		t.Errorf("Location = %q", loc)
	}
	params, ok := sslAction(sa, "ssl.csr_create")
	if !ok {
		t.Fatal("ssl.csr_create was not called")
	}
	names, _ := params["names"].([]any)
	if len(names) != 2 || names[0] != "shop.example" || names[1] != "www.shop.example" {
		t.Errorf("names = %v", params["names"])
	}
	if params["country"] != "DE" {
		t.Errorf("country = %v, want DE", params["country"])
	}
	if params["self_signed_days"] != float64(825) {
		t.Errorf("self_signed_days = %v, want 825 (clamped)", params["self_signed_days"])
	}
}

func TestSslKeyDelete(t *testing.T) {
	h, cookie, sa, db := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/keys/delete",
		url.Values{"id": {"bob.example-20261024000000"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/keys" {
		t.Fatalf("delete key = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	params, _ := sslAction(sa, "ssl.key_delete")
	if params["id"] != "bob.example-20261024000000" {
		t.Errorf("id = %v", params["id"])
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'ssl.key_delete'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
}

func TestSslKeyInstall(t *testing.T) {
	h, cookie, _, db := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/keys/install", url.Values{
		"key_id":      {"bob.example-20261024000000"},
		"website_id":  {"10"},
		"certificate": {"-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----"},
	})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/ssl" {
		t.Fatalf("install key = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	var https int
	if err := db.QueryRow(`SELECT https FROM websites WHERE id = 10`).Scan(&https); err != nil {
		t.Fatal(err)
	}
	if https != 1 {
		t.Error("HTTPS was not enabled on the website")
	}
}

func TestSslKeyInstallNeedsCertificate(t *testing.T) {
	h, cookie, _, _ := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/keys/install", url.Values{
		"key_id": {"bob.example-20261024000000"}, "website_id": {"10"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("install without certificate = %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); !strings.Contains(loc, "/ssl/keys?id=") {
		t.Errorf("Location = %q, want back to the key", loc)
	}
}

func TestSslCenterTLS(t *testing.T) {
	h, cookie, sa, _ := sslCenterEnv(t, "admin")
	rr := get(t, h, "/ssl/tls", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ssl/tls = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"ssl_protocols TLSv1.2 TLSv1.3;", "/etc/nginx/snippets/whacp-tls.conf",
		"3 Nginx files", "1 file(s)", "TLS 1.2 and 1.3", "Modern ciphers only",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("tls page is missing %q", want)
		}
	}

	rr = sslCenterPost(t, h, cookie, "/ssl/tls", url.Values{
		"protocols": {"tls13"}, "ciphers": {"intermediate"}, "session_tickets": {"1"},
	})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/tls" {
		t.Fatalf("save tls = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	params, ok := sslAction(sa, "ssl.tls_set")
	if !ok {
		t.Fatal("ssl.tls_set was not called")
	}
	if params["protocols"] != "tls13" || params["ciphers"] != "intermediate" || params["session_tickets"] != true {
		t.Errorf("tls_set params = %v", params)
	}
}

func TestSslCenterServices(t *testing.T) {
	h, cookie, sa, db := sslCenterEnv(t, "admin")
	rr := get(t, h, "/ssl/services", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ssl/services = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"srv1.whacp.test", "Let's Encrypt certificate present",
		"WhaCP panel (web services)", "Mail server (SMTP, submission, IMAP)",
		"Where each SSL setting is", "registered", "active",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("services page is missing %q", want)
		}
	}

	// Choosing a certificate for the mail server.
	rr = sslCenterPost(t, h, cookie, "/ssl/services/cert", url.Values{
		"service": {"mail"}, "cert_name": {"bob.example"},
	})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/services" {
		t.Fatalf("service cert = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	params, ok := sslAction(sa, "ssl.service_cert")
	if !ok {
		t.Fatal("ssl.service_cert was not called")
	}
	if params["service"] != "mail" || params["cert_name"] != "bob.example" {
		t.Errorf("service_cert params = %v", params)
	}

	// Going back to the previous certificate (empty name).
	rr = sslCenterPost(t, h, cookie, "/ssl/services/cert", url.Values{
		"service": {"panel"}, "cert_name": {""},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("revert cert = %d", rr.Code)
	}

	// Registering a ZeroSSL account: the EAB HMAC key is never audited.
	rr = sslCenterPost(t, h, cookie, "/ssl/services/account", url.Values{
		"provider": {"zerossl"}, "email": {"Ops@Whacp.test"}, "agree_tos": {"1"},
		"eab_kid": {"KID1"}, "eab_hmac_key": {"SECRET-HMAC"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("register = %d", rr.Code)
	}
	params, ok = sslAction(sa, "ssl.account_register")
	if !ok {
		t.Fatal("ssl.account_register was not called")
	}
	if params["provider"] != "zerossl" || params["email"] != "ops@whacp.test" {
		t.Errorf("account_register params = %v", params)
	}
	var raw string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key = 'acme_provider'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != `"zerossl"` {
		t.Errorf("acme_provider = %q", raw)
	}
	var details string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action = 'ssl.account_register'`).Scan(&details); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(details, "SECRET-HMAC") {
		t.Error("the EAB HMAC key was written to the audit log")
	}
}

func TestSslCenterServicesProviderPreview(t *testing.T) {
	h, cookie, sa, _ := sslCenterEnv(t, "admin")
	rr := get(t, h, "/ssl/services?provider=zerossl", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("preview = %d", rr.Code)
	}
	params, ok := sslAction(sa, "ssl.account")
	if !ok {
		t.Fatal("ssl.account was not called")
	}
	if params["provider"] != "zerossl" {
		t.Errorf("provider = %v", params["provider"])
	}
}

func TestSslMailTLS(t *testing.T) {
	h, cookie, sa, _ := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/services/mail-tls",
		url.Values{"protocols": {"tls13"}, "ciphers": {"intermediate"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/services" {
		t.Fatalf("mail tls = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	params, ok := sslAction(sa, "ssl.mail_tls")
	if !ok {
		t.Fatal("ssl.mail_tls was not called")
	}
	if params["protocols"] != "tls13" || params["ciphers"] != "intermediate" {
		t.Errorf("mail_tls params = %v", params)
	}
}

func TestSslHostnameIssue(t *testing.T) {
	h, cookie, sa, db := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/services/hostname", url.Values{})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/services" {
		t.Fatalf("hostname = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	params, ok := sslAction(sa, "ssl.hostname_issue")
	if !ok {
		t.Fatal("ssl.hostname_issue was not called")
	}
	if params["email"] != "admin@whacp.test" || params["provider"] != "letsencrypt" {
		t.Errorf("hostname_issue params = %v", params)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM agent_jobs WHERE job_id = '0123456789a0b001'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("tracked jobs = %d, want 1", n)
	}
}

func TestSslRenewalEnable(t *testing.T) {
	h, cookie, sa, _ := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/renewal", url.Values{})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/autossl" {
		t.Fatalf("renewal = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	if !sslActionRan(sa, "ssl.renewal_enable") {
		t.Error("ssl.renewal_enable was not called")
	}
}

func TestSslCenterAutossl(t *testing.T) {
	h, cookie, _, _ := sslCenterEnv(t, "admin")
	rr := get(t, h, "/ssl/autossl", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /ssl/autossl = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Current provider: Let's Encrypt", "AutoSSL log", "Renewal log (certbot)",
		"Run AutoSSL for all websites", "renewing bob.example",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("autossl page is missing %q", want)
		}
	}
}

func TestSslAutosslRun(t *testing.T) {
	h, cookie, _, db := sslCenterEnv(t, "admin")
	rr := sslCenterPost(t, h, cookie, "/ssl/autossl/run", url.Values{})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/autossl" {
		t.Fatalf("run = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'ssl.autossl_run'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("audit rows = %d, want 1", n)
	}
}

func TestSslCenterRequiresPermission(t *testing.T) {
	h, cookie, _, _ := sslCenterEnv(t, "vera")
	// ssl.view, but not ssl.manage or server.manage.
	if rr := get(t, h, "/ssl/hosts", cookie); rr.Code != http.StatusOK {
		t.Fatalf("viewer GET /ssl/hosts = %d", rr.Code)
	}
	if rr := sslCenterPost(t, h, cookie, "/ssl/tls", url.Values{"protocols": {"tls13"}}); rr.Code != http.StatusForbidden {
		t.Errorf("viewer save tls = %d, want 403", rr.Code)
	}
	if rr := sslCenterPost(t, h, cookie, "/ssl/renewal", url.Values{}); rr.Code != http.StatusForbidden {
		t.Errorf("viewer renewal = %d, want 403", rr.Code)
	}
}

func TestSslCenterCSRF(t *testing.T) {
	h, cookie, sa, _ := sslCenterEnv(t, "admin")
	rr := postForm(h, "/ssl/tls", url.Values{"protocols": {"tls13"}}, cookie)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("POST without token = %d, want 400", rr.Code)
	}
	if sslActionRan(sa, "ssl.tls_set") {
		t.Error("ssl.tls_set ran without a CSRF token")
	}
}
