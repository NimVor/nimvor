package http

// This suite drives the server-side Cloudflare area (CloudflareController +
// CloudflareService): connecting the server's own API token (one per
// server, encrypted), the zone list, the zone detail (SSL mode, Always Use
// HTTPS, all DNS records with the proxy toggle, purge), disconnect, the
// read-only split (server.view vs server.manage) and the API-error banner.
// The Cloudflare API is faked through the transport hook (srv.cfDo).

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

// cfServerEnv seeds an administrator (server.view + server.manage), a viewer
// (carol, server.view only) and a customer (bob, websites.view only).
func cfServerEnv(t *testing.T) (http.Handler, *Server, *sql.DB) {
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
		(1,'admin','Administrator','all',''),(2,'viewer','Viewer','own',''),(3,'customer','Customer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'server.view','sv','srv'),(2,'server.manage','sm','srv'),(3,'websites.view','v','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(2,1),(3,3)`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at) VALUES
		(1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','',''),
		(2,'carol','c@x','Carol',?,2,1,'2026-01-01T00:00:00Z','',''),
		(3,'bob','b@x','Bob',?,3,1,'2026-01-01T00:00:00Z','','')`,
		loginHash, loginHash, loginHash)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, socket, tokenFile := startSitesAgent(t, false)
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), s, db
}

// cfServerSeed stores an encrypted server token and wires the fake transport.
func cfServerSeed(t *testing.T, srv *Server, db *sql.DB, f *cfFake) {
	t.Helper()
	enc, err := srv.crypto.Encrypt([]byte("test-token-1234567890abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO cloudflare_accounts (server_id,token_enc,verified_at,created_at,updated_at)
		VALUES (1,?,'2026-01-01T00:00:00Z','','')`, enc); err != nil {
		t.Fatal(err)
	}
	srv.cfDo = f.do
}

// cfSrvPost fetches /cloudflare for a fresh CSRF token, then POSTs the form.
func cfSrvPost(t *testing.T, h http.Handler, path string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/cloudflare")
	form.Set("_token", token)
	return postForm(h, path, form, cookie)
}

func TestCloudflareServerNotConnected(t *testing.T) {
	h, _, _ := cfServerEnv(t)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/cloudflare", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/cloudflare = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>Cloudflare · WhaCP</title>",
		"Connect Cloudflare",
		`name="token"`,
		"stored encrypted",
	} {
		if !pageHas(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if pageHas(page, ">Zones<") {
		t.Error("not-connected page shows the zones card")
	}
	// A read-only user can view the page but not the token form.
	carol := loginAsUser(t, h, "carol")
	rr = get(t, h, "/cloudflare", carol)
	if rr.Code != http.StatusOK {
		t.Fatalf("/cloudflare (carol) = %d: %s", rr.Code, rr.Body.String())
	}
	page = rr.Body.String()
	if !pageHas(page, "Ask an administrator to connect Cloudflare.") {
		t.Error("viewer page missing the ask-an-admin hint")
	}
	if pageHas(page, `name="token"`) {
		t.Error("viewer page shows the token form")
	}
}

func TestCloudflareServerConnect(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	srv.cfDo = f.do
	admin := loginAsUser(t, h, "admin")
	rr := cfSrvPost(t, h, "/cloudflare/connect", admin, url.Values{"token": {"abcdef0123456789abcdef0123456789"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/cloudflare" {
		t.Fatalf("connect POST = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/cloudflare", "Cloudflare connected.")
	if len(f.calls) < 1 || f.calls[0] != "GET /user/tokens/verify" {
		t.Fatalf("token not verified first: %v", f.calls)
	}
	var tokenEnc, verifiedAt string
	if err := db.QueryRow(`SELECT token_enc, verified_at FROM cloudflare_accounts WHERE server_id = 1`).Scan(&tokenEnc, &verifiedAt); err != nil {
		t.Fatalf("row not stored: %v", err)
	}
	if tokenEnc == "" || tokenEnc == "abcdef0123456789abcdef0123456789" {
		t.Error("token not stored encrypted")
	}
	if verifiedAt == "" {
		t.Error("verified_at not set")
	}
	var tt, tid string
	if err := db.QueryRow(`SELECT target_type, target_id FROM audit_logs WHERE action = 'cloudflare.connect'`).Scan(&tt, &tid); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if tt != "server" || tid != "1" {
		t.Errorf("audit target = %s/%s", tt, tid)
	}
}

func TestCloudflareServerConnectBadToken(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	srv.cfDo = f.do
	admin := loginAsUser(t, h, "admin")
	rr := cfSrvPost(t, h, "/cloudflare/connect", admin, url.Values{"token": {"short"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("connect POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/cloudflare", "That does not look like a Cloudflare API token.")
	if len(f.calls) != 0 {
		t.Errorf("API called for a malformed token: %v", f.calls)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cloudflare_accounts`).Scan(&n); err != nil || n != 0 {
		t.Errorf("row stored for a malformed token: %d", n)
	}
}

func TestCloudflareServerZonesList(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/cloudflare", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/cloudflare = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		">Zones<",
		"example",
		"bob.example",
		`href="/cloudflare?zone=` + cfZoneID + `"`,
	} {
		if !pageHas(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestCloudflareServerZoneSelected(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/cloudflare?zone="+cfZoneID, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/cloudflare?zone = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		`id="cf-zone"`,
		"www.bob.example",
		"other.example",
		`value="flexible"`,
		"Purge cache",
		`name="record"`,
	} {
		if !pageHas(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The TXT record is not proxiable: no proxy form for it.
	if strings.Count(page, `name="record"`) != 3 {
		t.Errorf("proxy forms = %d, want 3 (TXT excluded)", strings.Count(page, `name="record"`))
	}
}

func TestCloudflareServerZonePending(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	f.zoneStatus = "pending"
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/cloudflare?zone="+cfZoneID, admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/cloudflare?zone = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{"This zone is pending.", "ns-one.example-dns.com"} {
		if !pageHas(page, want) {
			t.Errorf("pending page missing %q", want)
		}
	}
}

func TestCloudflareServerZoneError(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	f.failStatus = 403
	f.failMsg = "Access denied to this account."
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := get(t, h, "/cloudflare", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("/cloudflare = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	if !pageHas(page, "Cloudflare: Access denied to this account.") {
		t.Error("API error infobar missing")
	}
	if pageHas(page, `id="cf-zone"`) || pageHas(page, ">Zones<") {
		t.Error("error page still shows the zones/zone cards")
	}
}

func TestCloudflareServerSsl(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfSrvPost(t, h, "/cloudflare/ssl", admin, url.Values{"zone": {cfZoneID}, "mode": {"strict"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/cloudflare?zone="+cfZoneID {
		t.Fatalf("ssl POST = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/cloudflare?zone="+cfZoneID, "SSL mode updated.")
	if !f.captured("PATCH /zones/" + cfZoneID + "/settings/ssl") {
		t.Errorf("ssl PATCH not made: %v", f.calls)
	}
	var tt, tid, det string
	if err := db.QueryRow(`SELECT target_type, target_id, details FROM audit_logs WHERE action = 'cloudflare.ssl'`).Scan(&tt, &tid, &det); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if tt != "server" || tid != "1" || !strings.Contains(det, `"mode":"strict"`) || !strings.Contains(det, `"zone":"`+cfZoneID+`"`) {
		t.Errorf("audit = %s/%s %s", tt, tid, det)
	}
}

func TestCloudflareServerSslUnknown(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfSrvPost(t, h, "/cloudflare/ssl", admin, url.Values{"zone": {cfZoneID}, "mode": {"bogus"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("ssl POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/cloudflare?zone="+cfZoneID, "Unknown SSL mode.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action = 'cloudflare.ssl'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit row for an unknown mode: %d", n)
	}
}

func TestCloudflareServerAlwaysHttps(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfSrvPost(t, h, "/cloudflare/always-https", admin, url.Values{"zone": {cfZoneID}, "on": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("always-https POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/cloudflare?zone="+cfZoneID, "Always Use HTTPS updated.")
	if !f.captured("PATCH /zones/" + cfZoneID + "/settings/always_use_https") {
		t.Errorf("always_use_https PATCH not made: %v", f.calls)
	}
	var det string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action = 'cloudflare.always_https'`).Scan(&det); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(det, `"on":true`) {
		t.Errorf("audit details = %s", det)
	}
}

func TestCloudflareServerProxy(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfSrvPost(t, h, "/cloudflare/proxy", admin, url.Values{"zone": {cfZoneID}, "record": {cfRecA}, "proxied": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("proxy POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/cloudflare?zone="+cfZoneID, "DNS record updated.")
	if !f.captured("PATCH /zones/" + cfZoneID + "/dns_records/" + cfRecA) {
		t.Errorf("record PATCH not made: %v", f.calls)
	}
	var det string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action = 'cloudflare.proxy'`).Scan(&det); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(det, `"record":"`+cfRecA+`"`) || !strings.Contains(det, `"proxied":true`) {
		t.Errorf("audit details = %s", det)
	}
}

func TestCloudflareServerProxyBadID(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfSrvPost(t, h, "/cloudflare/proxy", admin, url.Values{"zone": {cfZoneID}, "record": {"../etc/passwd"}, "proxied": {"1"}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("proxy POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/cloudflare?zone="+cfZoneID, "Invalid Cloudflare identifier.")
	rr = cfSrvPost(t, h, "/cloudflare/purge", admin, url.Values{"zone": {"bad"}})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/cloudflare" {
		t.Fatalf("purge (bad zone) POST = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	// The pages read the account's zones and records; a bad id must change nothing.
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") || strings.Contains(c, "passwd") || strings.Contains(c, "/bad") {
			t.Errorf("API called for a bad id: %v", f.calls)
			break
		}
	}
}

func TestCloudflareServerPurge(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfSrvPost(t, h, "/cloudflare/purge", admin, url.Values{"zone": {cfZoneID}})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("purge POST = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(admin, rr), "/cloudflare?zone="+cfZoneID, "Cache purged.")
	if f.purgeCalls != 1 {
		t.Errorf("purge calls = %d", f.purgeCalls)
	}
	var det string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action = 'cloudflare.purge'`).Scan(&det); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if !strings.Contains(det, `"zone":"`+cfZoneID+`"`) {
		t.Errorf("audit details = %s", det)
	}
}

func TestCloudflareServerDisconnect(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	admin := loginAsUser(t, h, "admin")
	rr := cfSrvPost(t, h, "/cloudflare/disconnect", admin, url.Values{})
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/cloudflare" {
		t.Fatalf("disconnect POST = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(admin, rr), "/cloudflare", "Cloudflare disconnected.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cloudflare_accounts`).Scan(&n); err != nil || n != 0 {
		t.Errorf("row not deleted: %d", n)
	}
	// Disconnecting only forgets the token: nothing is changed at Cloudflare.
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "GET ") {
			t.Errorf("disconnect must not change anything at Cloudflare: %v", f.calls)
			break
		}
	}
	var tid string
	if err := db.QueryRow(`SELECT target_id FROM audit_logs WHERE action = 'cloudflare.disconnect'`).Scan(&tid); err != nil {
		t.Fatalf("audit: %v", err)
	}
	if tid != "1" {
		t.Errorf("audit target = %s", tid)
	}
}

func TestCloudflareServerForbidden(t *testing.T) {
	h, _, _ := cfServerEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/cloudflare", bob)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("/cloudflare (bob) = %d: %s", rr.Code, rr.Body.String())
	}
	carol := loginAsUser(t, h, "carol")
	rr = cfSrvPost(t, h, "/cloudflare/connect", carol, url.Values{"token": {"abcdef0123456789abcdef0123456789"}})
	if rr.Code != http.StatusForbidden {
		t.Fatalf("connect POST (carol) = %d", rr.Code)
	}
}

func TestCloudflareServerReadOnly(t *testing.T) {
	h, srv, db := cfServerEnv(t)
	f := newCFFake()
	cfServerSeed(t, srv, db, f)
	carol := loginAsUser(t, h, "carol")
	rr := get(t, h, "/cloudflare?zone="+cfZoneID, carol)
	if rr.Code != http.StatusOK {
		t.Fatalf("/cloudflare?zone (carol) = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, banned := range []string{
		"cloudflare/disconnect",
		"cloudflare/purge",
		"cloudflare/proxy",
		`name="token"`,
	} {
		if pageHas(page, banned) {
			t.Errorf("read-only page shows %q", banned)
		}
	}
	for _, want := range []string{
		`name="mode" disabled`,
		"Always Use HTTPS: off",
		">Proxied<",
		">DNS only<",
	} {
		if !pageHas(page, want) {
			t.Errorf("read-only page missing %q", want)
		}
	}
}
