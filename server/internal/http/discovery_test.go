package http

// This suite drives the discovery pages (batch 65): the read-only inventory
// overview with its snapshot history, the PHP/certificates/cron pages, the
// tenant scoping of the inventory, the explicit re-scan and its return_to
// allowlist, and the discovery-unavailable fallback.

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// discoveryEnv is importEnv plus the permissions the discovery pages need.
// The customer (bob) holds websites.view so the scoping tests can sign in as
// a tenant; only the administrator holds the rest.
func discoveryEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
	t.Helper()
	h, cookie, sa, db := importEnv(t, loginAs)
	if _, err := db.Exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(21,'php.view','pv','server'),(22,'ssl.view','sv','server'),
		(23,'cron.view','cv','server'),(24,'discovery.run','dr','server')`); err != nil {
		t.Fatal(err)
	}
	// Both roles may read the inventory pages (a tenant's view is scoped,
	// not refused); only the administrator may re-scan.
	if _, err := db.Exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES
		(1,21),(1,22),(1,23),(1,24),(2,21),(2,22),(2,23)`); err != nil {
		t.Fatal(err)
	}
	return h, cookie, sa, db
}

// discoveryInventoryFull is a richer inventory than the fake's default: two
// websites (one of them bob's own domain), PHP versions and pools, a certbot
// lineage and a cron job, so every page has something to render.
const discoveryInventoryFull = `{
  "os":{"pretty_name":"Ubuntu 24.04","supported":true},
  "websites":{"websites":[
    {"id":"w1","domain":"bob.example","aliases":["www.bob.example"],"kind":"php","enabled":true,
     "https":true,"https_redirect":true,"document_root":"/home/whacp/bob.example/public_html",
     "document_root_exists":true,"listen":["80","443 ssl"],"config_files":["/home/whacp/nginx/bob.example.conf"],
     "php":{"version":"8.3.1","pool":"bob","user":"u_bob","listen":"127.0.0.1:9001","resolved":true},
     "ssl":{"status":"valid","error":"","not_after":"2026-11-05T00:00:00Z","days_remaining":10,
            "issuer":"Let's Encrypt","cert_file":"/etc/letsencrypt/live/bob.example/fullchain.pem",
            "key_file":"/etc/letsencrypt/live/bob.example/privkey.pem","lets_encrypt":true,"covers_domain":true}},
    {"id":"w2","domain":"other.example","aliases":[],"kind":"php","enabled":true,
     "https":false,"document_root":"/var/www/other","document_root_exists":true,
     "config_files":["/etc/nginx/sites-available/other"],
     "php":{"version":"8.2","pool":"other","user":"other","listen":"127.0.0.1:9002","resolved":true},
     "ssl":{"status":"expired","error":"expired","days_remaining":-3,"issuer":"Let's Encrypt",
            "cert_file":"/etc/letsencrypt/live/other.example/fullchain.pem","covers_domain":false}}
  ],"total":2,"catch_all_vhosts":0,"warnings":["one website has no listen directive"]},
  "php":{"versions":[
      {"version":"8.3.1","cli_binary":"/usr/bin/php8.3","fpm_binary":"/usr/sbin/php-fpm8.3","pool_count":1,"is_default":true},
      {"version":"8.2","cli_binary":"/usr/bin/php8.2","fpm_binary":"","pool_count":1}
    ],
    "pools":[
      {"name":"bob","version":"8.3.1","user":"u_bob","group":"u_bob","listen":"127.0.0.1:9001","pm":"dynamic","max_children":10,"open_basedir":"/home/whacp/bob.example","file":"/etc/php/8.3/fpm/pool.d/bob.conf"},
      {"name":"other","version":"8.2","user":"other","group":"other","listen":"127.0.0.1:9002","pm":"ondemand","file":"/etc/php/8.2/fpm/pool.d/other.conf"}
    ],
    "warnings":["a pool has no open_basedir"]},
  "databases":{"installed":true,"schemas":[{"name":"a_main"},{"name":"b_main"}],"warnings":[]},
  "certificates":[
    {"name":"bob.example","domains":["bob.example","www.bob.example"],
     "certificate":{"status":"valid","error":"","not_after":"2026-11-05T00:00:00Z","days_remaining":10,
                    "expired":false,"key_exists":true,"key_world_readable":false,
                    "names":["bob.example","www.bob.example"]},
     "authenticator":"nginx"}
  ],
  "cron":{"jobs":[
      {"schedule":"0 3 * * *","user":"u_bob","command":"php /home/whacp/bob.example/cron.php","source":"/var/spool/cron/crontabs/u_bob","line":4}
    ],"warnings":[]},
  "services":[{"unit":"nginx.service","active_state":"active"},{"unit":"php8.3-fpm.service","active_state":"active"}],
  "users":[{"name":"u_bob","system":true},{"name":"root","system":false}]
}`

// discoveryFlash GETs path and returns the page body along with the flashed
// messages the page rendered.
func discoveryFlash(t *testing.T, h http.Handler, cookie *http.Cookie, path string) string {
	t.Helper()
	rr := get(t, h, path, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200: %s", path, rr.Code, rr.Body.String())
	}
	return rr.Body.String()
}

func TestDiscoveryIndex(t *testing.T) {
	h, cookie, sa, db := discoveryEnv(t, "admin")
	sa.discoveryInventory = discoveryInventoryFull

	// A stored snapshot so the history table has a row.
	if _, err := db.Exec(`INSERT INTO discovery_snapshots (server_id, created_at, created_by, website_count, checksum, data)
		VALUES (1, '2026-10-20T10:00:00Z', 1, 2, 'abcdef0123456789', '{}')`); err != nil {
		t.Fatal(err)
	}

	body := discoveryFlash(t, h, cookie, "/discovery")
	for _, want := range []string{
		"Discovery", "read-only snapshot, nothing on the server was changed.",
		"read-only view of the filesystem enforced by systemd",
		"Websites", "PHP versions", "PHP-FPM pools", "Databases",
		"Let's Encrypt certificates", "Cron jobs", "Services", "System accounts",
		"one website has no listen directive", // the merged warnings
		"Snapshot history", "abcdef012345",    // the checksum, truncated to 12
	} {
		if !pageHas(body, want) {
			t.Errorf("discovery page missing %q", want)
		}
	}
	// Two websites, two PHP versions, two pools, two databases, one
	// certificate, one cron job, two services and one system account.
	for _, want := range []string{`<div class="wcp-kpi">2</div>`, `<div class="wcp-kpi">1</div>`} {
		if !pageHas(body, want) {
			t.Errorf("discovery counters missing %q", want)
		}
	}
}

func TestDiscoveryIndexScoped(t *testing.T) {
	h, cookie, sa, _ := discoveryEnv(t, "bob")
	sa.discoveryInventory = discoveryInventoryFull

	body := discoveryFlash(t, h, cookie, "/discovery")
	// bob owns bob.example only: one website, one PHP pool and the
	// certificate naming his domain; the other site's cron job is hidden.
	if !pageHas(body, "Discovered") {
		t.Fatalf("no discovery bar:\n%s", body)
	}
	// forScope drops every section warning, so no tenant sees another's.
	if pageHas(body, "a pool has no open_basedir") ||
		pageHas(body, "one website has no listen directive") {
		t.Errorf("the scope filter kept another tenant's warning")
	}

	// The PHP page lists only bob's pool.
	phpBody := discoveryFlash(t, h, cookie, "/php")
	if !strings.Contains(phpBody, "8.3.1") {
		t.Errorf("php page missing bob's version")
	}
	if !strings.Contains(phpBody, "not installed") {
		t.Errorf("php page should mark the CLI-only 8.2 as not installed")
	}
	if strings.Contains(phpBody, "127.0.0.1:9002") {
		t.Errorf("php page leaked another tenant's pool")
	}

	// The cron page has nothing for a tenant (forScope empties cron).
	cronBody := discoveryFlash(t, h, cookie, "/cron")
	if !strings.Contains(cronBody, "No cron jobs were found.") {
		t.Errorf("cron page should be empty for a tenant:\n%s", cronBody)
	}
	if strings.Contains(cronBody, "/home/whacp/bob.example/cron.php") {
		t.Errorf("cron page leaked a job for a tenant")
	}
}

func TestDiscoverySSLRedirectsAdministrators(t *testing.T) {
	h, cookie, sa, _ := discoveryEnv(t, "admin")
	sa.discoveryInventory = discoveryInventoryFull
	rr := get(t, h, "/ssl", cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/ssl/hosts" {
		t.Fatalf("admin GET /ssl = %d -> %q, want 303 /ssl/hosts", rr.Code, rr.Header().Get("Location"))
	}
}

func TestDiscoverySSLForCustomer(t *testing.T) {
	h, cookie, sa, _ := discoveryEnv(t, "bob")
	sa.discoveryInventory = discoveryInventoryFull
	body := discoveryFlash(t, h, cookie, "/ssl")
	for _, want := range []string{
		"SSL certificates", "Website certificates", "bob.example",
		"2026-11-05", "expiring", // 10 days, inside the 14-day warning window
		"Let's Encrypt (certbot) lineages", "nginx",
	} {
		if !pageHas(body, want) {
			t.Errorf("ssl page missing %q", want)
		}
	}
	// The other tenant's expired certificate must not appear.
	if pageHas(body, "other.example") {
		t.Errorf("ssl page leaked another tenant's certificate")
	}
}

func TestDiscoveryCronForAdministrator(t *testing.T) {
	h, cookie, sa, _ := discoveryEnv(t, "admin")
	sa.discoveryInventory = discoveryInventoryFull
	body := discoveryFlash(t, h, cookie, "/cron")
	for _, want := range []string{
		"Cron jobs", "0 3 * * *", "u_bob", "php /home/whacp/bob.example/cron.php",
		"/var/spool/cron/crontabs/u_bob:4",
		"redacted by the agent",
	} {
		if !pageHas(body, want) {
			t.Errorf("cron page missing %q", want)
		}
	}
}

func TestDiscoveryRun(t *testing.T) {
	h, cookie, sa, db := discoveryEnv(t, "admin")
	sa.discoveryInventory = discoveryInventoryFull

	token, cookie := pageToken(t, h, cookie, "/discovery")
	rr := postForm(h, "/discovery/run", url.Values{"_token": {token}, "return_to": {"/discovery"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/discovery" {
		t.Fatalf("run = %d -> %q", rr.Code, rr.Header().Get("Location"))
	}
	body := discoveryFlash(t, h, refreshCookie(cookie, rr), "/discovery")
	if !pageHas(body, "Discovery completed: 2 website(s) found.") {
		t.Errorf("run flash missing:\n%s", body)
	}
	if !pageHas(body, "Nothing on the server was changed.") {
		t.Errorf("run flash should state that nothing changed")
	}

	// The re-scan stored a snapshot created by the actor.
	var count, by int
	if err := db.QueryRow(`SELECT website_count, created_by FROM discovery_snapshots ORDER BY id DESC LIMIT 1`).
		Scan(&count, &by); err != nil {
		t.Fatal(err)
	}
	if count != 2 || by != 1 {
		t.Errorf("snapshot = %d websites by %d, want 2 by 1", count, by)
	}
}

func TestDiscoveryRunReturnToAllowlist(t *testing.T) {
	h, cookie, sa, _ := discoveryEnv(t, "admin")
	sa.discoveryInventory = discoveryInventoryFull

	token, cookie := pageToken(t, h, cookie, "/discovery")
	// A target outside the allowlist falls back to /discovery.
	for _, bad := range []string{"https://evil.test/", "//evil.test", "/Discovery", "/a?b=1"} {
		rr := postForm(h, "/discovery/run", url.Values{"_token": {token}, "return_to": {bad}}, cookie)
		if loc := rr.Header().Get("Location"); loc != "/discovery" {
			t.Errorf("return_to %q -> %q, want /discovery", bad, loc)
		}
	}
	// An allowed path is honoured.
	token, cookie = pageToken(t, h, cookie, "/discovery")
	rr := postForm(h, "/discovery/run", url.Values{"_token": {token}, "return_to": {"/php"}}, cookie)
	if loc := rr.Header().Get("Location"); loc != "/php" {
		t.Errorf("return_to /php -> %q", loc)
	}
}

func TestDiscoveryRunAgentFailure(t *testing.T) {
	h, cookie, sa, _ := discoveryEnv(t, "admin")
	sa.failActions["discovery.inventory"] = "the agent refused"

	token, cookie := pageToken(t, h, cookie, "/discovery")
	rr := postForm(h, "/discovery/run", url.Values{"_token": {token}, "return_to": {"/discovery"}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("run on agent failure = %d, want 303", rr.Code)
	}
	body := discoveryFlash(t, h, refreshCookie(cookie, rr), "/discovery")
	if !pageHas(body, "the agent refused") {
		t.Errorf("failure flash missing:\n%s", body)
	}
}

func TestDiscoveryUnavailable(t *testing.T) {
	h, cookie, sa, _ := discoveryEnv(t, "admin")
	sa.failActions["discovery.inventory"] = "the agent is unreachable"

	rr := get(t, h, "/discovery", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /discovery with the agent down = %d, want 200", rr.Code)
	}
	body := rr.Body.String()
	if !pageHas(body, "This page needs the WhaCP agent") {
		t.Errorf("unavailable fallback missing:\n%s", body)
	}
	if !pageHas(body, "the agent is unreachable") {
		t.Errorf("the agent's message should be shown")
	}
}

func TestDiscoveryRequiresPermission(t *testing.T) {
	h, cookie, _, _ := discoveryEnv(t, "bob")
	// The re-scan is administrator-only (the tenant may read the pages).
	if rr := postForm(h, "/discovery/run", url.Values{}, cookie); rr.Code != http.StatusForbidden {
		t.Errorf("customer POST /discovery/run = %d, want 403", rr.Code)
	}
}

func TestDiscoveryRunRequiresCSRF(t *testing.T) {
	h, cookie, sa, _ := discoveryEnv(t, "admin")
	sa.discoveryInventory = discoveryInventoryFull
	rr := postForm(h, "/discovery/run", url.Values{"return_to": {"/discovery"}}, cookie)
	if rr.Code != http.StatusForbidden && rr.Code != http.StatusBadRequest {
		t.Fatalf("run without a token = %d, want 403/400", rr.Code)
	}
}
