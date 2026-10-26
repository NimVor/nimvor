package http

// Tests for the per-website security and health pages (site_health.go +
// hosting/site_health.go): the check lists (HTTPS states, the certificate
// day windows, the malware-scan states, the old-PHP warning, the enabled
// and backup states), the "who can fix it" badges, the infobar count and
// the scoped 404.

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"encoding/base64"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// healthEnv seeds an administrator, a customer (bob, owns website 10 — a
// PHP 8.0 site without HTTPS) and a read-only viewer (carol, owns website
// 20 — a clean PHP 8.3 site).
func healthEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'websites.view','v','web'),(2,'websites.manage','m','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(2,1),(2,2),(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,https,force_https,enabled,cert_file,key_file,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.0.30',0,0,1,'','','active','',''),
		(20,1,3,'carol.example','u_carol','/home/whacp/carol.example','/home/whacp/carol.example/public_html','php','8.3.1',0,0,1,'','','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1),(1,20,'carol.example',1)`)

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

func healthScan(t *testing.T, db *sql.DB, websiteID int64, infected int) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO av_scans (server_id,website_id,source,user_id,status,scanned,infected,findings,error,started_at)
		VALUES (1,?, 'manual', 2, 'completed', 100, ?, '[]', '', '2026-10-20T12:00:00Z')`, websiteID, infected); err != nil {
		t.Fatalf("seed scan: %v", err)
	}
}

func healthBackup(t *testing.T, db *sql.DB, websiteID int64, status, createdAt string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO backups (server_id,website_id,schedule_id,kind,domain,backup_id,label,manifest,size,encrypted,local,status,error,job_id,user_id,created_at)
		VALUES (1,?, NULL, 'site', 'bob.example', '20261020T120000Z-a1b2c3', 'seeded', '', 11, 1, 1, ?, '', '', 2, ?)`, websiteID, status, createdAt); err != nil {
		t.Fatalf("seed backup: %v", err)
	}
}

// TestSiteSecurityPage covers the plain checks: HTTPS off, no malware scan
// yet and an old PHP version — all customer-side, so the infobar counts
// three problems and every badge is "You can fix this".
func TestSiteSecurityPage(t *testing.T) {
	h, _, _ := healthEnv(t)
	bob := loginAsUser(t, h, "bob")

	rr := get(t, h, "/sites/10/security", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/10/security = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"HTTPS is off",
		"No malware scan has run yet",
		"PHP 8.0.30 no longer gets security updates",
		"3 item(s) need attention",
		"You can fix this",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// No certificate line without HTTPS, and no provider-side badge.
	if pageHas(body, "Certificate") || pageHas(body, "Ask your provider") {
		t.Errorf("page has an unexpected check:\n%s", body)
	}
	// Carol (viewer) sees her own site's page.
	carol := loginAsUser(t, h, "carol")
	if rr := get(t, h, "/sites/20/security", carol); rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/20/security (carol) = %d", rr.Code)
	}
	// …and her page is clean except for HTTPS being off and the missing
	// scan (fresh PHP, no certificate).
	body = get(t, h, "/sites/20/security", carol).Body.String()
	if pageHas(body, "no longer gets security updates") {
		t.Errorf("PHP 8.3 flagged as old:\n%s", body)
	}
	if !pageHas(body, "2 item(s) need attention") {
		t.Errorf("carol page infobar wrong:\n%s", body)
	}
}

func healthEnableHTTPS(t *testing.T, db *sql.DB, force int) {
	t.Helper()
	if _, err := db.Exec(`UPDATE websites SET https = 1, cert_file = '/etc/letsencrypt/live/bob.example/fullchain.pem',
		key_file = '/etc/letsencrypt/live/bob.example/privkey.pem', force_https = ? WHERE id = 10`, force); err != nil {
		t.Fatalf("enable https: %v", err)
	}
}

// TestSiteSecurityHTTPSStates covers the HTTPS/redirect branch and the
// certificate day windows (the default fixture cert has 129 days left).
func TestSiteSecurityHTTPSStates(t *testing.T) {
	h, sa, db := healthEnv(t)
	bob := loginAsUser(t, h, "bob")

	// HTTPS on, not forced: the redirect warning, cert valid for 129 days.
	healthEnableHTTPS(t, db, 0)
	body := get(t, h, "/sites/10/security", bob).Body.String()
	for _, want := range []string{
		"Visitors can still use plain http://",
		"Certificate is valid",
		"Valid for 129 more day(s).",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}

	// Forced: the good line.
	if _, err := db.Exec(`UPDATE websites SET force_https = 1 WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	body = get(t, h, "/sites/10/security", bob).Body.String()
	if !pageHas(body, "HTTPS is on and enforced") {
		t.Errorf("forced HTTPS not reported:\n%s", body)
	}

	// Expiring certificate (10 days): a warning.
	sa.sslInspectJSON = `{"dns_names":["bob.example"],"issuer":"R3, Let's Encrypt","not_after":"2026-11-05T00:00:00Z","days_remaining":10,"expired":false}`
	body = get(t, h, "/sites/10/security", bob).Body.String()
	if !pageHas(body, "Your certificate expires soon") || !pageHas(body, "It is valid for 10 more day(s).") {
		t.Errorf("expiring cert not flagged:\n%s", body)
	}

	// Critically expiring (2 days): a bad check.
	sa.sslInspectJSON = `{"dns_names":["bob.example"],"issuer":"R3, Let's Encrypt","not_after":"2026-10-28T00:00:00Z","days_remaining":2,"expired":false}`
	body = get(t, h, "/sites/10/security", bob).Body.String()
	if !pageHas(body, "It is valid for 2 more day(s).") {
		t.Errorf("critical cert not flagged:\n%s", body)
	}

	// Expired (-30 days): a bad check with the day count clamped at zero.
	sa.sslInspectJSON = `{"dns_names":["bob.example"],"issuer":"R3, Let's Encrypt","not_after":"2026-09-23T00:00:00Z","days_remaining":-30,"expired":true}`
	body = get(t, h, "/sites/10/security", bob).Body.String()
	if !pageHas(body, "It is valid for 0 more day(s).") {
		t.Errorf("expired cert not flagged:\n%s", body)
	}

	// HTTPS on but no certificate file stored: no certificate line at all.
	if _, err := db.Exec(`UPDATE websites SET cert_file = '', key_file = '' WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	body = get(t, h, "/sites/10/security", bob).Body.String()
	if pageHas(body, "Certificate is valid") || pageHas(body, "expires soon") {
		t.Errorf("certificate line without a cert file:\n%s", body)
	}
}

// TestSiteSecurityMalware covers the scan states: an infected last scan is
// flagged, a clean one clears it.
func TestSiteSecurityMalware(t *testing.T) {
	h, _, db := healthEnv(t)
	bob := loginAsUser(t, h, "bob")

	healthScan(t, db, 10, 2)
	body := get(t, h, "/sites/10/security", bob).Body.String()
	if !pageHas(body, "Malware was found in your files") || !pageHas(body, "2 infected file(s) in the last scan.") {
		t.Errorf("infected scan not flagged:\n%s", body)
	}

	// A clean scan clears it.
	healthScan(t, db, 10, 0)
	body = get(t, h, "/sites/10/security", bob).Body.String()
	if !pageHas(body, "No malware found") {
		t.Errorf("clean scan not reported:\n%s", body)
	}
}

func TestSiteHealthPage(t *testing.T) {
	h, _, db := healthEnv(t)
	bob := loginAsUser(t, h, "bob")

	// Enabled site, no backup yet: one problem, the backup warning.
	body := get(t, h, "/sites/10/health", bob).Body.String()
	for _, want := range []string{
		"Your website is active",
		"No successful backup yet",
		"1 item(s) need attention",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}

	// A failed backup does not count.
	healthBackup(t, db, 10, "failed", "2026-10-19T12:00:00Z")
	body = get(t, h, "/sites/10/health", bob).Body.String()
	if !pageHas(body, "No successful backup yet") {
		t.Errorf("failed backup counted:\n%s", body)
	}

	// A completed backup clears it.
	healthBackup(t, db, 10, "completed", "2026-10-20T12:00:00Z")
	body = get(t, h, "/sites/10/health", bob).Body.String()
	for _, want := range []string{
		"You have a recent backup",
		"Last backup: 2026-10-20T12:00:00Z",
		"Everything here looks good.",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

// TestSiteHealthDisabled covers the provider-side check.
func TestSiteHealthDisabled(t *testing.T) {
	h, _, db := healthEnv(t)
	bob := loginAsUser(t, h, "bob")

	if _, err := db.Exec(`UPDATE websites SET enabled = 0 WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, "/sites/10/health", bob).Body.String()
	for _, want := range []string{
		"Your website is turned off",
		"Ask your provider",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if !pageHas(body, "2 item(s) need attention") {
		t.Errorf("infobar count wrong:\n%s", body)
	}
}

// TestSiteSecurityNotFound: a foreign id 404s (scoped lookup), a missing id
// 404s too, with the shared message.
func TestSiteSecurityNotFound(t *testing.T) {
	h, _, _ := healthEnv(t)

	carol := loginAsUser(t, h, "carol")
	for _, path := range []string{"/sites/10/security", "/sites/10/health"} {
		rr := get(t, h, path, carol)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("GET %s (carol) = %d, want 404", path, rr.Code)
		}
		if !pageHas(rr.Body.String(), "This website does not exist or you do not have access to it.") {
			t.Errorf("GET %s 404 page missing message", path)
		}
	}
	admin := loginAsUser(t, h, "admin")
	if rr := get(t, h, "/sites/99/security", admin); rr.Code != http.StatusNotFound {
		t.Fatalf("GET /sites/99/security (admin) = %d, want 404", rr.Code)
	}
}
