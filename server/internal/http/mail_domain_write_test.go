package http

// Tests for the mail domain page's write side (MailController::saveMailbox /
// updateMailbox / deleteMailbox / saveAlias / deleteAlias / publishDns /
// dnsCheck / delete, MailFilterController::routing): the mailbox + forwarder
// CRUD, routing, the DNS publish + checker and stopping hosting.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mailFollow follows the redirect with its refreshed cookie and returns the
// destination page's body (a #fragment in the location is expected to
// survive).
func mailFollow(t *testing.T, h http.Handler, rr *httptest.ResponseRecorder, path string) string {
	t.Helper()
	loc := rr.Header().Get("Location")
	locPath, _, _ := strings.Cut(loc, "#")
	if rr.Code != http.StatusSeeOther || locPath != path {
		t.Fatalf("want 303 to %s, got %d loc %q", path, rr.Code, loc)
	}
	var cookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == session.DefaultCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("redirect set no session cookie")
	}
	return get(t, h, path, cookie).Body.String()
}

// firstLines is the first n lines of a page, for failure messages.
func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		return strings.Join(lines[:n], "\n")
	}
	return s
}

func TestMailMailboxCreate(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	rr := postForm(h, "/mail/domains/60/mailboxes", url.Values{
		"_token": {token}, "local": {"info2"}, "password": {"s3cure-passw0rd"}, "quota_mb": {"512"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "Mailbox info2@bob.example created.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_mailboxes WHERE domain_id = 60 AND local = 'info2' AND quota_mb = 512`); n != 1 {
		t.Fatalf("mailbox row not stored: n=%d", n)
	}
	if !containsAction(sa.called(), "mail.mailbox_save") {
		t.Fatalf("agent actions %v missing mail.mailbox_save", sa.called())
	}
}

func TestMailMailboxCreateValidation(t *testing.T) {
	h, cookie, _, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	// An invalid local part and no password: the page comes back with the
	// field messages, the submitted value and the create modal open.
	rr := postForm(h, "/mail/domains/60/mailboxes", url.Values{
		"_token": {token}, "local": {"bad name"}, "quota_mb": {"512"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("want in-place 200 re-render, got %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Use letters, digits and . _ + - (not at the start or end).",
		"Enter a password or generate one.",
		"data-wcp-modal-open",
		`value="bad name"`,
	} {
		if !pageHas(body, want) {
			t.Fatalf("re-render missing %q", want)
		}
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_mailboxes`); n != 1 {
		t.Fatalf("refused save stored a row: n=%d", n)
	}
}

func TestMailMailboxCreateDuplicate(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	rr := postForm(h, "/mail/domains/60/mailboxes", url.Values{
		"_token": {token}, "local": {"info"}, "password": {"s3cure-passw0rd"},
	}, cookie)
	if rr.Code != http.StatusOK || !pageHas(rr.Body.String(), "This address exists already.") {
		t.Fatalf("duplicate not caught: %d\n%s", rr.Code, firstLines(rr.Body.String(), 5))
	}
	// An alias address is taken too.
	rr = postForm(h, "/mail/domains/60/mailboxes", url.Values{
		"_token": {token}, "local": {"sales"}, "password": {"s3cure-passw0rd"},
	}, cookie)
	if rr.Code != http.StatusOK || !pageHas(rr.Body.String(), "This address exists already.") {
		t.Fatalf("alias-taken not caught: %d", rr.Code)
	}
}

func TestMailMailboxGenerate(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	rr := postForm(h, "/mail/domains/60/mailboxes", url.Values{
		"_token": {token}, "local": {"gen"}, "generate": {"1"}, "quota_mb": {"0"},
	}, cookie)
	var cookie2 *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == session.DefaultCookieName {
			cookie2 = c
		}
	}
	if cookie2 == nil || rr.Code != http.StatusSeeOther {
		t.Fatalf("generate create = %d (no new cookie)", rr.Code)
	}
	body := get(t, h, "/mail/domains/60", cookie2).Body.String()
	for _, want := range []string{"Mailbox password", "gen@bob.example", "shown only once"} {
		if !pageHas(body, want) {
			t.Fatalf("generated password not flashed (missing %q):\n%s", want, firstLines(body, 12))
		}
	}
	// The agent got a generated password (10–128 chars, no colon).
	p := agentParams(t, sa, "mail.mailbox_save")
	if addr, _ := p["address"].(string); addr != "gen@bob.example" {
		t.Fatalf("mailbox_save address = %v", p["address"])
	}
	password, _ := p["password"].(string)
	if len(password) < 10 || strings.Contains(password, ":") {
		t.Fatalf("generated password unusable: %q", password)
	}
}

func TestMailMailboxUpdate(t *testing.T) {
	h, cookie, _, db := dnsMailEnv(t, "bob")
	// The fixture mailbox is id 1 (the first insert).
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60?edit=1")
	rr := postForm(h, "/mail/domains/60/mailboxes/1", url.Values{
		"_token": {token}, "quota_mb": {"2048"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "Mailbox saved.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_mailboxes WHERE id = 1 AND quota_mb = 2048`); n != 1 {
		t.Fatalf("quota not updated: n=%d", n)
	}
}

func TestMailMailboxDelete(t *testing.T) {
	h, cookie, _, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	// Wrong confirm word: refused, the row stays.
	rr := postForm(h, "/mail/domains/60/mailboxes/1/delete", url.Values{
		"_token": {token}, "confirm": {"wrong@bob.example"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "Type the address to confirm.") {
		t.Fatalf("confirm mismatch not reported:\n%s", firstLines(body, 5))
	}
	// The right word: gone.
	rr = postForm(h, "/mail/domains/60/mailboxes/1/delete", url.Values{
		"_token": {token}, "confirm": {"info@bob.example"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "Mailbox deleted. Its mail was moved to the server's trash folder.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_mailboxes`); n != 0 {
		t.Fatalf("mailbox row not removed: n=%d", n)
	}
}

func TestMailAliasSaveAndDelete(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	rr := postForm(h, "/mail/domains/60/aliases", url.Values{
		"_token": {token}, "alias_local": {"support"}, "destinations": {"ops@example.com, info@bob.example"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "Alias saved.") || !pageHas(body, "support@bob.example") {
		t.Fatalf("alias not saved:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_aliases WHERE domain_id = 60 AND local = 'support' AND destinations = 'ops@example.com, info@bob.example'`); n != 1 {
		t.Fatalf("alias row wrong: n=%d", n)
	}
	if !containsAction(sa.called(), "mail.alias_save") {
		t.Fatalf("agent actions %v missing mail.alias_save", sa.called())
	}
	// The catch-all uses local '' — '*' in the form.
	rr = postForm(h, "/mail/domains/60/aliases", url.Values{
		"_token": {token}, "alias_local": {"*"}, "destinations": {"postmaster@bob.example"},
	}, cookie)
	mailFollow(t, h, rr, "/mail/domains/60")
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_aliases WHERE domain_id = 60 AND local = '' AND destinations = 'postmaster@bob.example'`); n != 1 {
		t.Fatalf("catch-all not stored: n=%d", n)
	}
	// Delete the support alias (id 2: the fixture's alias is id 1).
	rr = postForm(h, "/mail/domains/60/aliases/2/delete", url.Values{"_token": {token}}, cookie)
	body = mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "Alias deleted.") {
		t.Fatalf("alias not deleted:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_aliases WHERE domain_id = 60 AND local = 'support'`); n != 0 {
		t.Fatalf("alias row not removed: n=%d", n)
	}
}

func TestMailAliasValidation(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	rr := postForm(h, "/mail/domains/60/aliases", url.Values{
		"_token": {token}, "alias_local": {"help"}, "destinations": {"not-an-email"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("want in-place 200 re-render, got %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"not-an-email is not an email address.", "data-wcp-modal-open"} {
		if !pageHas(body, want) {
			t.Fatalf("re-render missing %q", want)
		}
	}
	// A mailbox address may not become an alias.
	rr = postForm(h, "/mail/domains/60/aliases", url.Values{
		"_token": {token}, "alias_local": {"info"}, "destinations": {"ops@example.com"},
	}, cookie)
	if rr.Code != http.StatusOK || !pageHas(rr.Body.String(), "A mailbox has this address.") {
		t.Fatalf("mailbox/alias conflict not caught: %d", rr.Code)
	}
}

func TestMailRouting(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	rr := postForm(h, "/mail/domains/60/routing", url.Values{
		"_token": {token}, "routing": {"backup"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "Mail routing: Backup mail exchanger.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_domains WHERE id = 60 AND routing = 'backup'`); n != 1 {
		t.Fatalf("routing not stored: n=%d", n)
	}
	if !containsAction(sa.called(), "mail.routing_set") {
		t.Fatalf("agent actions %v missing mail.routing_set", sa.called())
	}
	// An unknown choice is refused.
	rr = postForm(h, "/mail/domains/60/routing", url.Values{
		"_token": {token}, "routing": {"nowhere"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "Choose local, backup or remote.") {
		t.Fatalf("bad routing not refused:\n%s", firstLines(body, 5))
	}
}

func TestMailPublishDns(t *testing.T) {
	h, cookie, _, db := dnsMailEnv(t, "bob")
	// Zone 50 (bob.example) starts with 4 records; the publish adds MX,
	// SPF, DKIM and DMARC.
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 4 {
		t.Fatalf("zone 50 starts with %d records, want 4", n)
	}
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	rr := postForm(h, "/mail/domains/60/dns", url.Values{"_token": {token}}, cookie)
	body := mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "4 record(s) added to the DNS zone.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 8 {
		t.Fatalf("records not added: n=%d, want 8", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50 AND type = 'MX' AND name = '@' AND value = 'mail.example.com.' AND priority = 10`); n != 1 {
		t.Fatalf("MX record wrong: n=%d", n)
	}
	for q, what := range map[string]string{
		`SELECT COUNT(*) FROM dns_records WHERE zone_id = 50 AND type = 'TXT' AND value = 'v=spf1 a mx ~all'`:                   "SPF",
		`SELECT COUNT(*) FROM dns_records WHERE zone_id = 50 AND type = 'TXT' AND value = 'v=DKIM1; k=rsa; p=MEow'`:             "DKIM",
		`SELECT COUNT(*) FROM dns_records WHERE zone_id = 50 AND type = 'TXT' AND value LIKE 'v=DMARC1%postmaster@bob.example'`: "DMARC",
	} {
		if n := rowCount(t, db, q); n != 1 {
			t.Fatalf("%s not published: n=%d", what, n)
		}
	}
	// Publishing again changes nothing.
	rr = postForm(h, "/mail/domains/60/dns", url.Values{"_token": {token}}, cookie)
	body = mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "The DNS zone already has these records.") {
		t.Fatalf("second publish not idempotent:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 8 {
		t.Fatalf("second publish added records: n=%d", n)
	}
}

func TestMailDnsCheck(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	rr := get(t, h, "/mail/domains/60/dns-check", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("check = %d, want 200: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("check content type = %q", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{`"checks"`, `"MX"`, `"pass"`, `"v=spf1 a mx ~all"`} {
		if !pageHas(body, want) {
			t.Fatalf("check reply missing %q: %s", want, body)
		}
	}
	// A domain the user does not see is a 404.
	rr = get(t, h, "/mail/domains/999/dns-check", cookie)
	if rr.Code != http.StatusNotFound || !pageHas(rr.Body.String(), `"error"`) {
		t.Fatalf("check of unknown domain = %d %s", rr.Code, rr.Body.String())
	}
}

func TestMailDomainDelete(t *testing.T) {
	// Carol's domain 65 has no mailboxes or aliases.
	h, cookie, sa, db := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/domains/65")
	rr := postForm(h, "/mail/domains/65/delete", url.Values{
		"_token": {token}, "confirm": {"carol.example"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail")
	if !pageHas(body, "Email is no longer hosted for carol.example.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_domains WHERE id = 65`); n != 0 {
		t.Fatalf("domain row not removed: n=%d", n)
	}
	if !containsAction(sa.called(), "mail.domain_remove") {
		t.Fatalf("agent actions %v missing mail.domain_remove", sa.called())
	}
	// Bob's domain 60 still has a mailbox and an alias: refused.
	h, cookie, _, db = dnsMailEnv(t, "bob")
	token, cookie = pageToken(t, h, cookie, "/mail/domains/60")
	rr = postForm(h, "/mail/domains/60/delete", url.Values{
		"_token": {token}, "confirm": {"bob.example"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "Delete the domain's mailboxes and aliases first.") {
		t.Fatalf("occupied domain not refused:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_domains WHERE id = 60`); n != 1 {
		t.Fatalf("occupied domain removed: n=%d", n)
	}
}

func TestMailMailboxAgentFailure(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	sa.failActions = map[string]string{"mail.mailbox_save": "the agent refused"}
	token, cookie := pageToken(t, h, cookie, "/mail/domains/60")
	rr := postForm(h, "/mail/domains/60/mailboxes", url.Values{
		"_token": {token}, "local": {"info2"}, "password": {"s3cure-passw0rd"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/domains/60")
	if !pageHas(body, "The mailbox could not be saved.") {
		t.Fatalf("agent failure not flashed:\n%s", firstLines(body, 5))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM mail_mailboxes`); n != 1 {
		t.Fatalf("failed save stored a row: n=%d", n)
	}
}
