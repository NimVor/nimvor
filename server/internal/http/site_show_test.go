package http

// Tests for the website hub page (site_show.go + hosting/site_overview.go)
// and the advanced page: the hero facts (live disk/database sizes, server
// IP, SSL label, last backup), the permission-gated tile grid and feature
// search, the failure/bandwidth/missing-config alerts, the advanced
// runtime card with the recent tasks, the danger zone and the scoped 404 /
// 403.

import (
	"database/sql"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// showEnv seeds an administrator, a customer (bob, owns website 10 — a
// PHP 8.0 site with HTTPS, one linked database, a completed backup and one
// tracked job) and a read-only viewer (carol, owns website 20 — a static
// site, no extras).
func showEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'files.manage','fm','files'),
		(4,'databases.view','dv','db'),(5,'databases.manage','dm','db'),(6,'ssl.view','sv','ssl'),
		(7,'ssl.manage','sm','ssl'),(8,'backups.view','bv','web'),(9,'backups.manage','bm','web'),
		(10,'logs.view','lv','logs'),(11,'cron.view','cv','cron'),(12,'cron.manage','cm','cron')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,1),(1,2),(1,3),(1,4),(1,5),(1,6),(1,7),(1,8),(1,9),(1,10),(1,11),(1,12),
		(2,1),(2,2),(2,3),(2,4),(2,5),(2,6),(2,7),(2,8),(2,9),(2,10),(2,11),(2,12),
		(5,1)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,https,force_https,ssl_mode,cert_file,key_file,enabled,status,applied_at,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.0.30',1,1,'letsencrypt',
		        '/etc/letsencrypt/live/bob.example/fullchain.pem','/etc/letsencrypt/live/bob.example/privkey.pem',1,'active','2026-10-20T09:30:00Z','',''),
		(20,1,3,'carol.example','u_carol','/home/whacp/carol.example','/home/whacp/carol.example/public_html','static','',0,0,'none','','',1,'active','','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1),(1,20,'carol.example',1)`)
	exec(`INSERT INTO databases (server_id,website_id,owner_id,name,created_at) VALUES (1,10,2,'a_main','2026-10-01T00:00:00Z')`)
	exec(`INSERT INTO backups (server_id,website_id,schedule_id,kind,domain,backup_id,label,manifest,size,encrypted,local,status,error,job_id,user_id,created_at)
		VALUES (1,10,NULL,'site','bob.example','20261020T120000Z-a1b2c3','', '',11,1,1,'completed','','',2,'2026-10-20T12:00:00Z')`)
	exec(`INSERT INTO agent_jobs (server_id,job_id,kind,target_type,target_id,user_id,status,created_at)
		VALUES (1,'feedfacefeedface','Back up bob.example','website','10',2,'succeeded','2026-10-20T12:00:00Z')`)

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

func TestSiteShowPage(t *testing.T) {
	h, _, _ := showEnv(t)
	bob := loginAsUser(t, h, "bob")

	rr := get(t, h, "/sites/10", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/10 = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"u_bob",
		"/home/whacp/bob.example/public_html",
		"10.10.0.15", // the agent's first global IPv4 (loopback skipped)
		"PHP-FPM · PHP 8.0.30",
		"123 MB", // the agent's disk_bytes, complete
		"10 MB",  // the linked database's live size (db.overview fixture)
		"(1 database)",
		"Let's Encrypt",
		"Last backup",
		"Site managing",
		"Disable the website",
		"Delete the website",
		"data-wcp-tile=\"files\"",
		"data-wcp-tile=\"databases\"",
		"data-wcp-tile=\"manual-settings\"",
		"data-wcp-feature=\"create database mysql mariadb new add\"",
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The tile notes: disk usage on Files, the database count on Databases,
	// "On" on SSL.
	if !pageHas(body, `data-wcp-tile="files"`) || !pageHas(body, "123 MB") {
		t.Errorf("Files tile missing the disk note:\n%s", body)
	}
	// A customer without deploy.manage gets no Git tile.
	if pageHas(body, `data-wcp-tile="git-deployment"`) {
		t.Error("Git tile shown without deploy.manage")
	}
	// Carol (viewer) sees her own hub without the management tiles or the
	// enable/disable button.
	carol := loginAsUser(t, h, "carol")
	body = get(t, h, "/sites/20", carol).Body.String()
	for _, banned := range []string{"data-wcp-tile=\"manual-settings\"", "data-wcp-tile=\"settings\"", "Disable the website", "Delete the website"} {
		if pageHas(body, banned) {
			t.Errorf("viewer page shows %q", banned)
		}
	}
	// …but her hub still lists the areas she may open.
	if !pageHas(body, `data-wcp-tile="web-firewall"`) {
		t.Errorf("viewer page missing the firewall tile (websites.view):\n%s", body)
	}
}

func TestSiteShowAlerts(t *testing.T) {
	h, sa, db := showEnv(t)
	bob := loginAsUser(t, h, "bob")

	// A failed change: the error banner with the re-apply link.
	if _, err := db.Exec(`UPDATE websites SET status = 'error', last_error = 'The nginx reload failed.' WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	body := get(t, h, "/sites/10", bob).Body.String()
	if !pageHas(body, "The last change failed:") || !pageHas(body, "The nginx reload failed.") ||
		!pageHas(body, "Re-apply configuration") {
		t.Errorf("failed-change banner missing:\n%s", body)
	}
	if _, err := db.Exec(`UPDATE websites SET status = 'active', last_error = '' WHERE id = 10`); err != nil {
		t.Fatal(err)
	}

	// The bandwidth block: the owner is over the monthly limit.
	if _, err := db.Exec(`UPDATE users SET bw_blocked_month = strftime('%Y-%m','now') WHERE id = 2`); err != nil {
		t.Fatal(err)
	}
	body = get(t, h, "/sites/10", bob).Body.String()
	if !pageHas(body, "Monthly bandwidth used up.") {
		t.Errorf("bandwidth banner missing:\n%s", body)
	}
	if _, err := db.Exec(`UPDATE users SET bw_blocked_month = '' WHERE id = 2`); err != nil {
		t.Fatal(err)
	}

	// The agent down: the inspect error banner, no disk fact.
	sa.failActions["sites.inspect"] = "The agent is not running."
	body = get(t, h, "/sites/10", bob).Body.String()
	if !pageHas(body, "The agent is not running.") {
		t.Errorf("agent error banner missing:\n%s", body)
	}
	if pageHas(body, "123 MB") {
		t.Error("disk fact shown while the agent is down")
	}
	delete(sa.failActions, "sites.inspect")

	// A missing server config: the warning with the re-apply link.
	sa.sitesInspectJSON = `{"domain":"bob.example","config_exists":false,"enabled":false,"pool_versions":[],"root_exists":true,"disk_bytes":129184560,"disk_complete":true}`
	body = get(t, h, "/sites/10", bob).Body.String()
	if !pageHas(body, "This website's configuration is missing on the server.") {
		t.Errorf("missing-config warning absent:\n%s", body)
	}
}

func TestSiteAdvancedPage(t *testing.T) {
	h, _, _ := showEnv(t)
	bob := loginAsUser(t, h, "bob")

	rr := get(t, h, "/sites/10/advanced", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/10/advanced = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"PHP application",
		"8.0.30 · 256 MB · up to 5 PHP-FPM processes (ondemand)",
		"u_bob",
		"/home/whacp/bob.example",
		"123 MB",
		"Last applied",
		"Generated Nginx configuration",
		"listen 80",
		"Recent tasks",
		"Back up bob.example",
		">succeeded<",
		"Directory listing",
		"Turn on", // stored as off
		"Disable website",
		"Reload PHP",
		"Re-apply",
		`name="confirm_domain"`,
		`name="remove_files"`,
		`name="remove_user"`,
	} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The IP card is part of the IP pool area, not ported yet.
	if pageHas(body, "id=\"ip\"") {
		t.Error("IP card rendered before the IP pool area is ported")
	}

	// A viewer gets a 403 (the route is websites.manage).
	carol := loginAsUser(t, h, "carol")
	if rr := get(t, h, "/sites/20/advanced", carol); rr.Code != http.StatusForbidden {
		t.Fatalf("GET /sites/20/advanced (carol) = %d, want 403", rr.Code)
	}
}

func TestSiteShowNotFound(t *testing.T) {
	h, _, _ := showEnv(t)

	carol := loginAsUser(t, h, "carol")
	for _, path := range []string{"/sites/10", "/sites/10/advanced"} {
		rr := get(t, h, path, carol)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("GET %s (carol) = %d, want 404", path, rr.Code)
		}
		if !pageHas(rr.Body.String(), "This website does not exist or you do not have access to it.") {
			t.Errorf("GET %s 404 page missing message", path)
		}
	}
	admin := loginAsUser(t, h, "admin")
	if rr := get(t, h, "/sites/99", admin); rr.Code != http.StatusNotFound {
		t.Fatalf("GET /sites/99 (admin) = %d, want 404", rr.Code)
	}
}
