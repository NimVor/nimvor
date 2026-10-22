package http

// This suite drives the per-website SSL/HTTPS area (SslController +
// SslService): the certificate page (inspected from the agent, valid /
// expiring / expired states, the certbot warning), issuing Let's Encrypt
// certificates (aliases, the disabled-site guard), custom uploads, renewal,
// disabling HTTPS (with certificate deletion), the HTTPS options and the
// security headers, plus the role split and the 404s. The agent is the
// recording fake (ssl.* fixtures in startSitesAgent).

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

// sslEnv seeds an administrator (ssl.view + ssl.manage + website
// permissions), a customer (bob, ssl.view only, owns website 10 with the
// www alias) and a viewer (carol, no ssl permission).
func sslEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own',''),(5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'ssl.view','sv','ssl'),(4,'ssl.manage','sm','ssl')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(1,4),(2,1),(2,3),(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1),(1,10,'www.bob.example',0)`)

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

// sslEnable moves website 10 to a Let's Encrypt certificate (as after an
// issue), so the cert page renders the connected state.
func sslEnable(t *testing.T, db *sql.DB, mode string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE websites SET https=1, force_https=1, ssl_mode=?,
		cert_file='/etc/letsencrypt/live/bob.example/fullchain.pem',
		key_file='/etc/letsencrypt/live/bob.example/privkey.pem' WHERE id=10`, mode); err != nil {
		t.Fatal(err)
	}
}

// sslPost fetches the SSL page for a fresh CSRF token, then POSTs the form.
func sslPost(t *testing.T, h http.Handler, path string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/ssl")
	form.Set("_token", token)
	return postForm(h, path, form, cookie)
}

func TestSiteSslPageNoHttps(t *testing.T) {
	h, _, _ := sslEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/ssl", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/ssl = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>SSL / HTTPS · bob.example · WhaCP</title>",
		"HTTPS is not enabled",
		"Free certificate from Let's Encrypt",
		`name="include_aliases"`,
		"Include aliases (www.bob.example)",
		"Security headers",
		"Upload your own certificate",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, banned := range []string{"Renew now", "ssl/disable", "ssl/options"} {
		if strings.Contains(page, banned) {
			t.Errorf("not-HTTPS page shows %q", banned)
		}
	}
}

func TestSiteSslPageCertValid(t *testing.T) {
	h, _, db := sslEnv(t)
	sslEnable(t, db, "letsencrypt")
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/ssl", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/ssl = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		">Secure<",
		"Let's Encrypt",
		"expires 2026-12-30 (129 days)",
		"Covers bob.example, www.bob.example",
		"R3, Let's Encrypt",
		"active (certbot.timer)",
		"Renew now",
		"ssl/options",
		"ssl/disable",
		`name="delete_certificate"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestSiteSslPageCertExpiring(t *testing.T) {
	h, sa, db := sslEnv(t)
	sa.sslInspectJSON = `{"dns_names":["bob.example"],"issuer":"R3, Let's Encrypt","not_after":"2026-11-05T00:00:00Z","days_remaining":10,"expired":false}`
	sslEnable(t, db, "letsencrypt")
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/ssl", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/ssl = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), ">Expires soon<") {
		t.Errorf("expiring cert not flagged:\n%s", rr.Body.String())
	}
}

func TestSiteSslPageCertExpired(t *testing.T) {
	h, sa, db := sslEnv(t)
	sa.sslInspectJSON = `{"dns_names":["bob.example"],"issuer":"R3, Let's Encrypt","not_after":"2026-01-01T00:00:00Z","days_remaining":-30,"expired":true}`
	sslEnable(t, db, "letsencrypt")
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/ssl", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/ssl = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), ">Expired<") {
		t.Errorf("expired cert not flagged:\n%s", rr.Body.String())
	}
}

func TestSiteSslPageNoCertbot(t *testing.T) {
	h, sa, db := sslEnv(t)
	sa.sslRenewalJSON = `{"certbot_installed":false,"timer_active":false,"method":"none"}`
	sslEnable(t, db, "letsencrypt")
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/ssl", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/ssl = %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "certbot is not installed on this server") {
		t.Error("certbot warning missing")
	}
}

func TestSiteSslPageReadOnly(t *testing.T) {
	h, _, db := sslEnv(t)
	sslEnable(t, db, "letsencrypt")
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/ssl", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/ssl (bob) = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	if !strings.Contains(page, ">Secure<") {
		t.Error("read-only page missing the certificate state")
	}
	for _, banned := range []string{"ssl/issue", "ssl/renew", "ssl/disable", "ssl/options", "security-headers", "Upload your own"} {
		if strings.Contains(page, banned) {
			t.Errorf("read-only page shows %q", banned)
		}
	}
}

func TestSiteSslIssue(t *testing.T) {
	h, sa, db := sslEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := sslPost(t, h, "/sites/10/ssl/issue", admin, url.Values{"include_aliases": {"1"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/ssl" {
		t.Fatalf("issue POST = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "Certificate issued. The website now uses HTTPS and HTTP requests are redirected.")
	acts := sa.called()
	i, found := -1, false
	for j, a := range acts {
		if a == "ssl.issue" {
			i, found = j, true
			break
		}
	}
	if !found {
		t.Fatalf("ssl.issue not called: %v", acts)
	}
	if names, _ := sa.param(i, "names").([]any); len(names) != 2 || names[0] != "bob.example" || names[1] != "www.bob.example" {
		t.Errorf("names = %v", sa.param(i, "names"))
	}
	if sa.param(i, "provider") != "letsencrypt" {
		t.Errorf("provider = %v", sa.param(i, "provider"))
	}
	var https, forceHTTPS int
	var sslMode, certFile string
	if err := db.QueryRow(`SELECT https, force_https, ssl_mode, cert_file FROM websites WHERE id=10`).Scan(&https, &forceHTTPS, &sslMode, &certFile); err != nil {
		t.Fatal(err)
	}
	if https != 1 || forceHTTPS != 1 || sslMode != "letsencrypt" || certFile == "" {
		t.Errorf("site not switched to HTTPS: %d %d %q %q", https, forceHTTPS, sslMode, certFile)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='ssl.issue' AND result='success'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("ssl.issue audit = %d: %v", n, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='ssl.enable'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("ssl.enable audit = %d: %v", n, err)
	}
}

func TestSiteSslIssueDisabled(t *testing.T) {
	h, sa, db := sslEnv(t)
	if _, err := db.Exec(`UPDATE websites SET enabled=0 WHERE id=10`); err != nil {
		t.Fatal(err)
	}
	admin := loginAsUser(t, h, "admin")
	rr := sslPost(t, h, "/sites/10/ssl/issue", admin, url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("issue POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "Enable the website first.")
	for _, a := range sa.called() {
		if a == "ssl.issue" {
			t.Errorf("ssl.issue called for a disabled site: %v", sa.called())
		}
	}
}

func TestSiteSslCustom(t *testing.T) {
	h, sa, db := sslEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := sslPost(t, h, "/sites/10/ssl/custom", admin, url.Values{
		"certificate": {"-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----"},
		"private_key": {"-----BEGIN PRIVATE KEY-----\nxyz\n-----END PRIVATE KEY-----"},
		"chain":       {""},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("custom POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "Certificate installed and HTTPS enabled.")
	found := false
	for _, a := range sa.called() {
		if a == "ssl.custom" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ssl.custom not called: %v", sa.called())
	}
	var https int
	var sslMode string
	if err := db.QueryRow(`SELECT https, ssl_mode FROM websites WHERE id=10`).Scan(&https, &sslMode); err != nil {
		t.Fatal(err)
	}
	if https != 1 || sslMode != "custom" {
		t.Errorf("site = https %d mode %q", https, sslMode)
	}
}

func TestSiteSslCustomEmpty(t *testing.T) {
	h, sa, db := sslEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := sslPost(t, h, "/sites/10/ssl/custom", admin, url.Values{"certificate": {""}, "private_key": {""}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("custom POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "Paste both the certificate and its private key.")
	for _, a := range sa.called() {
		if a == "ssl.custom" {
			t.Errorf("ssl.custom called with empty inputs: %v", sa.called())
		}
	}
}

func TestSiteSslRenew(t *testing.T) {
	h, sa, db := sslEnv(t)
	sslEnable(t, db, "letsencrypt")
	admin := loginAsUser(t, h, "admin")
	rr := sslPost(t, h, "/sites/10/ssl/renew", admin, url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("renew POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "Certificate renewed and Nginx reloaded.")
	found := false
	for _, a := range sa.called() {
		if a == "ssl.renew" {
			found = true
		}
	}
	if !found {
		t.Errorf("ssl.renew not called: %v", sa.called())
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='ssl.renew'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("ssl.renew audit = %d: %v", n, err)
	}
}

func TestSiteSslRenewCustom(t *testing.T) {
	h, sa, db := sslEnv(t)
	sslEnable(t, db, "custom")
	admin := loginAsUser(t, h, "admin")
	rr := sslPost(t, h, "/sites/10/ssl/renew", admin, url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("renew POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "Only Let's Encrypt certificates can be renewed automatically.")
	for _, a := range sa.called() {
		if a == "ssl.renew" {
			t.Errorf("ssl.renew called for an uploaded certificate: %v", sa.called())
		}
	}
}

func TestSiteSslDisable(t *testing.T) {
	h, sa, db := sslEnv(t)
	sslEnable(t, db, "letsencrypt")
	admin := loginAsUser(t, h, "admin")
	rr := sslPost(t, h, "/sites/10/ssl/disable", admin, url.Values{"delete_certificate": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("disable POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "HTTPS disabled for this website.")
	var https int
	var sslMode, certFile string
	if err := db.QueryRow(`SELECT https, ssl_mode, cert_file FROM websites WHERE id=10`).Scan(&https, &sslMode, &certFile); err != nil {
		t.Fatal(err)
	}
	if https != 0 || sslMode != "none" || certFile != "" {
		t.Errorf("site not disabled: %d %q %q", https, sslMode, certFile)
	}
	found := false
	for _, a := range sa.called() {
		if a == "ssl.delete" {
			found = true
		}
	}
	if !found {
		t.Errorf("ssl.delete not called: %v", sa.called())
	}
}

func TestSiteSslOptions(t *testing.T) {
	h, _, db := sslEnv(t)
	sslEnable(t, db, "letsencrypt")
	admin := loginAsUser(t, h, "admin")
	// HSTS without the redirect is forced off (hsts && force).
	rr := sslPost(t, h, "/sites/10/ssl/options", admin, url.Values{"force_https": {"0"}, "hsts": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("options POST = %d", rr.Code)
	}
	var force, hsts int
	if err := db.QueryRow(`SELECT force_https, hsts FROM websites WHERE id=10`).Scan(&force, &hsts); err != nil {
		t.Fatal(err)
	}
	if force != 0 || hsts != 0 {
		t.Errorf("hsts without force kept: %d %d", force, hsts)
	}
	// Then both on.
	rr = sslPost(t, h, "/sites/10/ssl/options", admin, url.Values{"force_https": {"1"}, "hsts": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("options POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "HTTPS options saved and applied.")
	if err := db.QueryRow(`SELECT force_https, hsts FROM websites WHERE id=10`).Scan(&force, &hsts); err != nil {
		t.Fatal(err)
	}
	if force != 1 || hsts != 1 {
		t.Errorf("options not saved: %d %d", force, hsts)
	}
	// Without HTTPS the options are refused.
	if _, err := db.Exec(`UPDATE websites SET https=0 WHERE id=10`); err != nil {
		t.Fatal(err)
	}
	rr = sslPost(t, h, "/sites/10/ssl/options", admin, url.Values{"force_https": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("options POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "Enable HTTPS first.")
}

func TestSiteSslSecurityHeaders(t *testing.T) {
	h, _, db := sslEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := sslPost(t, h, "/sites/10/security-headers", admin, url.Values{"security_headers": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("security-headers POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "Security headers are on.")
	var on int
	if err := db.QueryRow(`SELECT security_headers FROM websites WHERE id=10`).Scan(&on); err != nil || on != 1 {
		t.Errorf("security_headers = %d: %v", on, err)
	}
	rr = sslPost(t, h, "/sites/10/security-headers", admin, url.Values{"security_headers": {"0"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("security-headers POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/ssl", "Security headers are off.")
	if err := db.QueryRow(`SELECT security_headers FROM websites WHERE id=10`).Scan(&on); err != nil || on != 0 {
		t.Errorf("security_headers = %d: %v", on, err)
	}
}

func TestSiteSslForbidden(t *testing.T) {
	h, _, _ := sslEnv(t)
	carol := loginAsUser(t, h, "carol")
	rr := get(t, h, "/sites/10/ssl", carol)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("/sites/10/ssl (carol) = %d: %s", rr.Code, rr.Body.String())
	}
	token, cookie := siteFormToken(t, h, carol, "/websites")
	rr = postForm(h, "/sites/10/ssl/issue", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("issue POST (carol) = %d", rr.Code)
	}
}

func TestSiteSslNotFound(t *testing.T) {
	h, _, _ := sslEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/99/ssl", admin)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/sites/99/ssl = %d: %s", rr.Code, rr.Body.String())
	}
	token, cookie := siteFormToken(t, h, admin, "/sites/10/ssl")
	rr = postForm(h, "/sites/99/ssl/disable", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("disable (99) POST = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/websites", "Website not found.")
}
