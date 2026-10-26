package http

// Tests for the website-facing DNS and mail pages (DnsController::site/store/
// show, MailController::site/store/show): the set-up cards, the redirects to
// the detail pages, the create flows and the read-only detail pages.

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

// dnsMailEnv seeds two customers (bob with two websites — one already
// hosted for DNS and mail, one bare; carol with a website, no permissions),
// an admin, the DNS/mail settings and a zone + mail domain with a mailbox
// and an alias. The agent serves bob.example at serial 7 for dns.status and
// answers mail.domain_add with a fixed DKIM key.
func dnsMailEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
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
		(1,'dns.manage','DNS','Hosting'),(2,'mail.manage','Email','Hosting'),(3,'server.manage','Server','Admin'),(4,'server.view','Server','Admin')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(1,4),(2,1),(2,2),(3,4)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','',''),
		(3,'carol','c@x','Carol',?,3,1,'2026-01-01T00:00:00Z','','')`, loginHash, loginHash, loginHash)
	// bob: 10 = the bare site (set-up pages), 12 = already hosted (redirects).
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,status,system_user,root,document_root,kind,created_at,updated_at) VALUES
		(10,1,2,'shop.bob.example','active','u10','/srv/10','/srv/10/pub','php','',''),
		(12,1,2,'bob.example','active','u12','/srv/12','/srv/12/pub','php','',''),
		(14,1,3,'carol.example','active','u14','/srv/14','/srv/14/pub','php','','')`)
	exec(`INSERT INTO settings (key,value,updated_at) VALUES
		('dns_nameservers','ns1.example.com ns2.example.com',''),
		('dns_default_ipv4','203.0.113.10','')`)
	// Zone 50: bob's site 12, served at serial 7, with a private A record
	// (one warning) and the rest of the usual records.
	exec(`INSERT INTO dns_zones (id,server_id,name,website_id,owner_id,ttl,primary_ns,hostmaster,secondaries,serial,applied_at,created_at,updated_at) VALUES
		(50,1,'bob.example',12,2,3600,'ns1.example.com','hostmaster@bob.example','',7,'2026-01-02T00:00:00Z','',''),
		(70,1,'carol.example',NULL,3,3600,'ns1.example.com','hostmaster@carol.example','',7,'2026-01-02T00:00:00Z','','')`)
	exec(`INSERT INTO dns_records (zone_id,name,type,ttl,value,priority,weight,port,disabled) VALUES
		(50,'@','NS',0,'ns1.example.com.',0,0,0,0),
		(50,'@','NS',0,'ns2.example.com.',0,0,0,0),
		(50,'@','A',0,'10.0.0.5',0,0,0,0),
		(50,'www','CNAME',0,'@',0,0,0,0)`)
	// Mail domain 60 for bob's site 12, with a mailbox and an alias; 65 is
	// carol's (cross-tenant 404s).
	exec(`INSERT INTO mail_domains (id,server_id,domain,website_id,owner_id,dkim_selector,dkim_txt,created_at) VALUES
		(60,1,'bob.example',12,2,'whacp202601','v=DKIM1; k=rsa; p=MEow',''),
		(65,1,'carol.example',14,3,'sel','txt','')`)
	exec(`INSERT INTO mail_mailboxes (domain_id,local,quota_mb,created_at) VALUES (60,'info',1024,'')`)
	exec(`INSERT INTO mail_aliases (domain_id,local,destinations,created_at) VALUES (60,'sales','info@bob.example','')`)

	sa, socket, tokenFile := startSitesAgent(t, false)
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
	cookie, token := getLogin(t, h)
	rr := postForm(h, "/login", url.Values{"_token": {token}, "username": {loginAs}, "password": {loginPassword}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login %s = %d", loginAs, rr.Code)
	}
	return h, rr.Result().Cookies()[0], sa, db
}

func TestSiteDnsSetupPage(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	body := get(t, h, "/sites/10/dns", cookie).Body.String()
	for _, want := range []string{
		"DNS for shop.bob.example is not hosted here",
		`<form method="post" action="/dns/zones"`,
		`<input type="hidden" name="website_id" value="10">`,
		"Create DNS zone",
		"<code>ns1.example.com</code>", "<code>ns2.example.com</code>",
	} {
		if !pageHas(body, want) {
			t.Fatalf("site dns page missing %q:\n%s", want, body)
		}
	}
}

func TestSiteDnsRedirectsToZone(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	rr := get(t, h, "/sites/12/dns", cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dns/zones/50" {
		t.Fatalf("/sites/12/dns = %d loc %q, want 303 /dns/zones/50", rr.Code, rr.Header().Get("Location"))
	}
}

func TestSiteDnsNotReadyCustomer(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "bob")
	sa.dnsData = `{"installed":true,"configured":false}`
	body := get(t, h, "/sites/10/dns", cookie).Body.String()
	if !pageHas(body, "DNS hosting is not available on this server") {
		t.Fatalf("not-ready site dns page missing the customer text:\n%s", body)
	}
	if pageHas(body, `action="/dns/zones"`) {
		t.Fatalf("not-ready page must not offer the create form:\n%s", body)
	}
}

func TestSiteDnsRequiresPermission(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "carol")
	if rr := get(t, h, "/sites/14/dns", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("/sites/14/dns without dns.manage = %d, want 403", rr.Code)
	}
}

func TestDnsZoneCreateFromSite(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/dns")
	rr := postForm(h, "/dns/zones", url.Values{
		"_token": {token}, "website_id": {"10"}, "name": {"shop.bob.example"},
		"template": {"1"}, "return_to": {"/sites/10/dns"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dns/zones/71" { // AUTOINCREMENT: the seed has zones 50 and 70
		t.Fatalf("create zone = %d loc %q, want 303 /dns/zones/71", rr.Code, rr.Header().Get("Location"))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_zones`); n != 3 { // the seed's two zones + the new one
		t.Fatalf("dns_zones = %d, want 3", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 71`); n != 6 {
		t.Fatalf("zone 71 records = %d, want 6", n)
	}
	var hostmaster, spf string
	if err := db.QueryRow(`SELECT hostmaster FROM dns_zones WHERE id = 71`).Scan(&hostmaster); err != nil {
		t.Fatal(err)
	}
	if hostmaster != "hostmaster@shop.bob.example" {
		t.Fatalf("hostmaster = %q", hostmaster)
	}
	if err := db.QueryRow(`SELECT value FROM dns_records WHERE zone_id = 71 AND type = 'TXT' AND name = '@'`).Scan(&spf); err != nil {
		t.Fatal(err)
	}
	if spf != "v=spf1 +a +mx +ip4:203.0.113.10 ~all" {
		t.Fatalf("SPF = %q", spf)
	}
	acts := sa.called()
	for _, want := range []string{"dns.zone_apply", "dns.zone_create"} {
		if !containsAction(acts, want) {
			t.Fatalf("agent actions %v missing %q", acts, want)
		}
	}
	p := agentParams(t, sa, "dns.zone_apply")
	soa, _ := p["soa"].(map[string]any)
	if soa["primary_ns"] != "ns1.example.com." || soa["hostmaster"] != "hostmaster@shop.bob.example" {
		t.Fatalf("soa = %v", soa)
	}
	if recs, _ := p["records"].([]map[string]any); len(recs) != 6 {
		t.Fatalf("zone_apply records = %d, want 6", len(recs))
	}
}

func TestDnsZoneCreateCustomerValidation(t *testing.T) {
	h, cookie, _, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/dns")
	// No name, no website: the name check fires first, as in the PHP.
	rr := postForm(h, "/dns/zones", url.Values{"_token": {token}, "return_to": {"/sites/10/dns"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/dns" {
		t.Fatalf("invalid create = %d loc %q, want 303 /sites/10/dns", rr.Code, rr.Header().Get("Location"))
	}
	body := get(t, h, "/sites/10/dns", cookie).Body.String()
	if !pageHas(body, "Enter a domain name such as example.com.") {
		t.Fatalf("site dns page missing the name error:\n%s", body)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_zones`); n != 2 {
		t.Fatalf("dns_zones = %d after a failed create, want 2", n)
	}
}

func TestDnsZoneCreateTaken(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/dns")
	rr := postForm(h, "/dns/zones", url.Values{"_token": {token}, "website_id": {"12"}, "name": {"bob.example"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dns" {
		t.Fatalf("taken create = %d loc %q, want 303 /dns", rr.Code, rr.Header().Get("Location"))
	}
	body := get(t, h, "/dns", cookie).Body.String()
	if !pageHas(body, "This zone exists already.") {
		t.Fatalf("dns index missing the taken error:\n%s", body)
	}
}

func TestDnsZonePage(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	body := get(t, h, "/dns/zones/50", cookie).Body.String()
	for _, want := range []string{
		"bob.example",
		"<span class=\"wcp-state is-ok\">Live</span>",
		"private or reserved address (10.0.0.5)",
		">CNAME</span>",
		"Zone default",
	} {
		if !pageHas(body, want) {
			t.Fatalf("zone page missing %q:\n%s", want, body)
		}
	}
	// Carol's zone is invisible to bob.
	if rr := get(t, h, "/dns/zones/70", cookie); rr.Code != http.StatusNotFound {
		t.Fatalf("/dns/zones/70 for bob = %d, want 404", rr.Code)
	}
}

func TestDnsZonePagePending(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "bob")
	sa.dnsData = `{"installed":true,"configured":true,"running":true,"zones":[{"zone":"bob.example","serial":9}]}`
	body := get(t, h, "/dns/zones/50", cookie).Body.String()
	if !pageHas(body, "Update pending (serving serial 9)") {
		t.Fatalf("zone page missing the pending state:\n%s", body)
	}
}

func TestSiteMailSetupPage(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "bob")
	// Not ready: the mail server is configured but nothing runs.
	body := get(t, h, "/sites/10/mail", cookie).Body.String()
	if !pageHas(body, "Email hosting is not available on this server") {
		t.Fatalf("not-ready mail page missing the customer text:\n%s", body)
	}
	// Ready: Postfix runs — the set-up form appears.
	sa.mailData = `{"installed":true,"configured":true,"hostname":"mail.example.com","running":{"postfix":true,"dovecot":true,"opendkim":true}}`
	body = get(t, h, "/sites/10/mail", cookie).Body.String()
	for _, want := range []string{
		"Email is not set up for shop.bob.example",
		`<form method="post" action="/mail/domains"`,
		`<input type="hidden" name="website_id" value="10">`,
		"Set up email",
	} {
		if !pageHas(body, want) {
			t.Fatalf("site mail page missing %q:\n%s", want, body)
		}
	}
}

func TestMailDomainCreateFromSite(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/mail")
	// No website: the customer check fires first, as in the PHP.
	rr := postForm(h, "/mail/domains", url.Values{"_token": {token}, "return_to": {"/sites/10/mail"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/mail" {
		t.Fatalf("invalid create = %d loc %q, want 303 /sites/10/mail", rr.Code, rr.Header().Get("Location"))
	}
	body := get(t, h, "/sites/10/mail", cookie).Body.String()
	if !pageHas(body, "Choose the website whose domain receives email.") {
		t.Fatalf("site mail page missing the website error:\n%s", body)
	}
	token, cookie = pageToken(t, h, cookie, "/sites/10/mail")
	rr = postForm(h, "/mail/domains", url.Values{"_token": {token}, "website_id": {"10"}, "return_to": {"/sites/10/mail"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/mail/domains/66" {
		t.Fatalf("create domain = %d loc %q, want 303 /mail/domains/66", rr.Code, rr.Header().Get("Location"))
	}
	var sel, txt string
	if err := db.QueryRow(`SELECT dkim_selector, dkim_txt FROM mail_domains WHERE id = 66`).Scan(&sel, &txt); err != nil {
		t.Fatal(err)
	}
	if sel != "whacp202601" || txt != "v=DKIM1; k=rsa; p=MEow" {
		t.Fatalf("dkim = %q / %q", sel, txt)
	}
	var websiteID *int64
	if err := db.QueryRow(`SELECT website_id FROM mail_domains WHERE id = 66`).Scan(&websiteID); err != nil {
		t.Fatal(err)
	}
	if websiteID == nil || *websiteID != 10 {
		t.Fatalf("domain 61 website_id = %v, want 10", websiteID)
	}
	acts := sa.called()
	for _, want := range []string{"mail.domain_add", "mail.spam_configure"} {
		if !containsAction(acts, want) {
			t.Fatalf("agent actions %v missing %q", acts, want)
		}
	}
}

func TestMailDomainPage(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "bob")
	sa.mailData = `{"installed":true,"configured":true,"hostname":"mail.example.com","running":{"postfix":true,"dovecot":true,"opendkim":true},"webmail":{"enabled":true,"hostname":"webmail.example.com"},"mailboxes":[{"address":"info@bob.example","quota_mb":1024,"bytes":524288000}]}`
	body := get(t, h, "/mail/domains/60", cookie).Body.String()
	for _, want := range []string{
		"bob.example",
		"info@bob.example",
		"1.0 GB",
		"sales@bob.example",
		"info@bob.example",
		"mail.example.com.",
		"v=spf1 a mx ~all",
		"whacp202601._domainkey",
		"v=DMARC1; p=none; rua=mailto:postmaster@bob.example",
		"webmail.example.com",
		"993",
		"587",
	} {
		if !pageHas(body, want) {
			t.Fatalf("mail domain page missing %q:\n%s", want, body)
		}
	}
	// Carol's domain is invisible to bob.
	if rr := get(t, h, "/mail/domains/65", cookie); rr.Code != http.StatusNotFound {
		t.Fatalf("/mail/domains/65 for bob = %d, want 404", rr.Code)
	}
}
