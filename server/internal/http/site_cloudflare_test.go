package http

// This suite drives the per-website Cloudflare area (SiteCloudflareController +
// CloudflareSiteService): connecting the customer's own token (zone lookup by
// domain, encrypted storage), the zone settings page (SSL mode, Always Use
// HTTPS, per-record proxy, cache purge), disconnect, the API-error banner and
// the role split. The Cloudflare API is faked through the transport hook
// (srv.cfDo) — the sandbox has no egress to api.cloudflare.com.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

const cfZoneID = "0123456789abcdef0123456789abcdef"

var (
	cfRecA     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" // A bob.example (unproxied by default)
	cfRecWWW   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" // A www.bob.example (proxied by default)
	cfRecTXT   = "cccccccccccccccccccccccccccccccc" // TXT bob.example (not proxiable)
	cfRecOther = "dddddddddddddddddddddddddddddddd" // A other.example (not this domain)
)

// cfFake is the fake Cloudflare API (the PHP's setTransport double).
type cfFake struct {
	calls      []string // "METHOD path" in order
	token      string   // last token seen
	zones      []string
	ssl        string
	always     string
	proxied    map[string]bool
	purgeCalls int
	failStatus int
	failMsg    string
}

func newCFFake() *cfFake {
	return &cfFake{zones: []string{"example", "bob.example"}, ssl: "flexible", always: "off", proxied: map[string]bool{cfRecA: false, cfRecWWW: true}}
}

func (f *cfFake) captured(call string) bool {
	for _, c := range f.calls {
		if c == call {
			return true
		}
	}
	return false
}

func (f *cfFake) do(ctx context.Context, method, u string, body any, token string) (int, []byte, error) {
	path := strings.TrimPrefix(u, cfAPI)
	f.calls = append(f.calls, method+" "+path)
	f.token = token
	if f.failStatus != 0 {
		return f.failStatus, []byte(fmt.Sprintf(`{"success":false,"result":null,"errors":[{"code":10000,"message":%q}]}`, f.failMsg)), nil
	}
	ok := func(result string) (int, []byte, error) {
		return 200, []byte(`{"success":true,"result":` + result + `,"errors":[]}`), nil
	}
	zoneBase := "/zones/" + cfZoneID
	base := strings.SplitN(path, "?", 2)[0]
	switch base {
	case "/user/tokens/verify":
		return ok(`{"id":"tok","status":"active"}`)
	case "/zones":
		var zs []string
		for _, z := range f.zones {
			zs = append(zs, `{"id":"`+cfZoneID+`","name":`+strconv.Quote(z)+`}`)
		}
		return ok("[" + strings.Join(zs, ",") + "]")
	case zoneBase + "/dns_records":
		return ok(fmt.Sprintf(`[
			{"id":"%s","type":"A","name":"bob.example","content":"203.0.113.7","proxiable":true,"proxied":%s},
			{"id":"%s","type":"A","name":"www.bob.example","content":"203.0.113.7","proxiable":true,"proxied":%s},
			{"id":"%s","type":"TXT","name":"bob.example","content":"v=spf1 -all","proxiable":false,"proxied":false},
			{"id":"%s","type":"A","name":"other.example","content":"203.0.113.8","proxiable":true,"proxied":false}
		]`, cfRecA, boolJSON(f.proxied[cfRecA]), cfRecWWW, boolJSON(f.proxied[cfRecWWW]), cfRecTXT, cfRecOther))
	case zoneBase + "/settings/ssl":
		return ok(fmt.Sprintf(`{"id":"ssl","value":%q}`, f.ssl))
	case zoneBase + "/settings/always_use_https":
		return ok(fmt.Sprintf(`{"id":"always_use_https","value":%q}`, f.always))
	case zoneBase + "/purge_cache":
		f.purgeCalls++
		return ok(`{}`)
	}
	if strings.HasPrefix(base, zoneBase+"/dns_records/") || strings.HasPrefix(base, zoneBase+"/settings/") {
		return ok(`{}`)
	}
	return 404, []byte(`{"success":false,"result":null,"errors":[{"code":10000,"message":"not found"}]}`), nil
}

// cfSeed stores an encrypted connection for website 10 (bob.example) and
// wires the fake transport into the server.
func cfSeed(t *testing.T, srv *Server, db *sql.DB, f *cfFake) {
	t.Helper()
	enc, err := srv.crypto.Encrypt([]byte("test-token-1234567890abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cloudflare_sites (website_id,token_enc,zone_id,zone_name,verified_at,created_at,updated_at)
		VALUES (10,?,?,'bob.example','2026-01-01T00:00:00Z','','')`, enc, cfZoneID); err != nil {
		t.Fatal(err)
	}
	srv.cfDo = f.do
}

// cfPost fetches the site's Cloudflare page for a fresh CSRF token, then
// POSTs the form.
func cfPost(t *testing.T, h http.Handler, path string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/cloudflare")
	form.Set("_token", token)
	return postForm(h, path, form, cookie)
}

func TestSiteCloudflareNotConnected(t *testing.T) {
	h, _, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/cloudflare", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/cloudflare = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>Cloudflare · bob.example · WhaCP</title>",
		"Connect Cloudflare",
		`name="token"`,
		"WhaCP stores it encrypted",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.Contains(page, "Connected to the Cloudflare zone") {
		t.Error("not-connected page shows the connected banner")
	}
}

func TestSiteCloudflareConnect(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	srv.cfDo = f.do
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/connect", admin, url.Values{"token": {"abcdef0123456789abcdef0123456789"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/cloudflare" {
		t.Fatalf("connect POST = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "Cloudflare connected for bob.example.")
	if len(f.calls) < 2 || f.calls[0] != "GET /user/tokens/verify" {
		t.Fatalf("token not verified first: %v", f.calls)
	}
	if !strings.HasPrefix(f.calls[1], "GET /zones") {
		t.Errorf("zones not listed after verify: %v", f.calls)
	}
	var zoneName, tokenEnc string
	if err := db.QueryRow(`SELECT zone_name, token_enc FROM cloudflare_sites WHERE website_id = 10`).Scan(&zoneName, &tokenEnc); err != nil {
		t.Fatalf("row not stored: %v", err)
	}
	if zoneName != "bob.example" {
		t.Errorf("longest matching zone = %q, want bob.example", zoneName)
	}
	if tokenEnc == "" || tokenEnc == "abcdef0123456789abcdef0123456789" {
		t.Error("token not stored encrypted")
	}
	var tt, tid, det string
	if err := db.QueryRow(`SELECT target_type, target_id, details FROM audit_logs WHERE action = 'cloudflare.site_connect'`).Scan(&tt, &tid, &det); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if tt != "website" || tid != "10" {
		t.Errorf("audit target = %s/%s", tt, tid)
	}
	if !strings.Contains(det, `"zone":"bob.example"`) {
		t.Errorf("audit details = %s", det)
	}
}

func TestSiteCloudflareConnectBadToken(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	srv.cfDo = f.do
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/connect", admin, url.Values{"token": {"too-short"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("connect POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "That does not look like a Cloudflare API token.")
	if len(f.calls) != 0 {
		t.Errorf("API called for a malformed token: %v", f.calls)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cloudflare_sites`).Scan(&n); err != nil || n != 0 {
		t.Errorf("row stored for a malformed token: %d", n)
	}
}

func TestSiteCloudflareConnectNoZone(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	f.zones = []string{"other.example"}
	srv.cfDo = f.do
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/connect", admin, url.Values{"token": {"abcdef0123456789abcdef0123456789"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("connect POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "This token has no Cloudflare zone that matches bob.example.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cloudflare_sites`).Scan(&n); err != nil || n != 0 {
		t.Errorf("row stored with no matching zone: %d", n)
	}
}

func TestSiteCloudflareIndexConnected(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	cfSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/cloudflare", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/cloudflare = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"Connected to the Cloudflare zone <strong>bob.example</strong>",
		"www.bob.example",
		`name="record"`,
		`value="` + cfRecWWW + `"`,
		`value="flexible"`,
		"selected",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, banned := range []string{"TXT bob.example", "other.example", "could not be reached"} {
		if strings.Contains(page, banned) {
			t.Errorf("page must not contain %q", banned)
		}
	}
}

func TestSiteCloudflareSsl(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	cfSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/ssl", admin, url.Values{"mode": {"strict"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/cloudflare" {
		t.Fatalf("ssl POST = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "SSL/TLS mode updated.")
	if !f.captured("PATCH /zones/"+cfZoneID+"/settings/ssl") {
		t.Errorf("ssl PATCH not made: %v", f.calls)
	}
	var tt, tid, det string
	if err := db.QueryRow(`SELECT target_type, target_id, details FROM audit_logs WHERE action = 'cloudflare.site_ssl'`).Scan(&tt, &tid, &det); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if tt != "website" || tid != "10" || !strings.Contains(det, `"mode":"strict"`) {
		t.Errorf("audit = %s/%s %s", tt, tid, det)
	}
}

func TestSiteCloudflareSslUnknown(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	cfSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/ssl", admin, url.Values{"mode": {"bogus"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("ssl POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "Unknown SSL mode.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'cloudflare.site_ssl'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit row for an unknown mode: %d", n)
	}
}

func TestSiteCloudflareAlwaysHttps(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	cfSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/always-https", admin, url.Values{"on": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("always-https POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "Always Use HTTPS updated.")
	if !f.captured("PATCH /zones/"+cfZoneID+"/settings/always_use_https") {
		t.Errorf("always_use_https PATCH not made: %v", f.calls)
	}
	var det string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action = 'cloudflare.site_always_https'`).Scan(&det); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(det, `"on":true`) {
		t.Errorf("audit details = %s", det)
	}
}

func TestSiteCloudflareProxy(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	cfSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/proxy", admin, url.Values{"record": {cfRecA}, "on": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("proxy POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "Proxy setting updated.")
	if !f.captured("PATCH /zones/"+cfZoneID+"/dns_records/"+cfRecA) {
		t.Errorf("record PATCH not made: %v", f.calls)
	}
	var det string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action = 'cloudflare.site_proxy'`).Scan(&det); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(det, `"record":"`+cfRecA+`"`) || !strings.Contains(det, `"proxied":true`) {
		t.Errorf("audit details = %s", det)
	}
}

func TestSiteCloudflareProxyBadID(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	cfSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/proxy", admin, url.Values{"record": {"../etc/passwd"}, "on": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("proxy POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "Invalid Cloudflare identifier.")
	if len(f.calls) != 0 {
		t.Errorf("API called for a bad record id: %v", f.calls)
	}
}

func TestSiteCloudflarePurge(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	cfSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/purge", admin, url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("purge POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "Cloudflare cache purged.")
	if f.purgeCalls != 1 {
		t.Errorf("purge calls = %d", f.purgeCalls)
	}
	var tt, tid string
	if err := db.QueryRow(`SELECT target_type, target_id FROM audit_logs WHERE action = 'cloudflare.site_purge'`).Scan(&tt, &tid); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if tt != "website" || tid != "10" {
		t.Errorf("audit target = %s/%s", tt, tid)
	}
}

func TestSiteCloudflareDisconnect(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	cfSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfPost(t, h, "/sites/10/cloudflare/disconnect", admin, url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("disconnect POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/sites/10/cloudflare", "Cloudflare disconnected.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cloudflare_sites`).Scan(&n); err != nil || n != 0 {
		t.Errorf("row not deleted: %d", n)
	}
	if len(f.calls) != 0 {
		t.Errorf("disconnect must not call the API: %v", f.calls)
	}
	var tid string
	if err := db.QueryRow(`SELECT target_id FROM audit_logs WHERE action = 'cloudflare.site_disconnect'`).Scan(&tid); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if tid != "10" {
		t.Errorf("audit target = %s", tid)
	}
}

func TestSiteCloudflareApiError(t *testing.T) {
	h, srv, _, db := cacheEnv(t)
	f := newCFFake()
	f.failStatus = 403
	f.failMsg = "Access denied to this zone."
	cfSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/10/cloudflare", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/cloudflare = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	if !strings.Contains(page, "Cloudflare could not be reached with your token: Cloudflare: Access denied to this zone.") {
		t.Error("API error banner missing")
	}
	for _, banned := range []string{"cloudflare/ssl", "cloudflare/proxy", "cloudflare/purge", "cloudflare/always-https"} {
		if strings.Contains(page, banned) {
			t.Errorf("error page still shows the %s form", banned)
		}
	}
}

func TestSiteCloudflareForbidden(t *testing.T) {
	h, _, _, _ := cacheEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/cloudflare", bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("/sites/10/cloudflare (bob) = %d: %s", rr.Code, rr.Body.String())
	}
	token, cookie := siteFormToken(t, h, bob, "/sites/10/memcached")
	rr = postForm(h, "/sites/10/cloudflare/disconnect", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("disconnect POST (bob) = %d", rr.Code)
	}
}

func TestSiteCloudflareNotFound(t *testing.T) {
	h, _, _, _ := cacheEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/sites/99/cloudflare", admin)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/sites/99/cloudflare = %d: %s", rr.Code, rr.Body.String())
	}
	rr = get(t, h, "/sites/x/cloudflare", admin)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/sites/x/cloudflare = %d: %s", rr.Code, rr.Body.String())
	}
	token, cookie := siteFormToken(t, h, admin, "/sites/10/memcached")
	rr = postForm(h, "/sites/99/cloudflare/disconnect", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("disconnect (99) POST = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/websites", "Website not found.")
}
