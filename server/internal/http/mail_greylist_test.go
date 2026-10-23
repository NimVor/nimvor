package http

// Tests for the greylisting (MailGreylistController) and the outgoing mail
// limits (MailRateController) pages: the status bars, the settings and
// trusted-host CRUD, the provider table, the triplet report with its
// cleanup, and the limits save + reset.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMailGreylistPage(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	if rr := get(t, h, "/mail/admin/greylisting", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("bob's greylisting page = %d, want 403", rr.Code)
	}
	// carol has server.view but her role is own-scope: the all-scope gate
	// denies her (the PHP denied()).
	h, cookie, _, _ = dnsMailEnv(t, "carol")
	if rr := get(t, h, "/mail/admin/greylisting", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("carol's greylisting page = %d (want the all-scope 403)", rr.Code)
	}
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	rr := get(t, h, "/mail/admin/greylisting", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("greylisting page = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<span class="wcp-state is-ok">On</span>`,
		"Policy service", "Postfix check",
		`name="deferral" value="5"`, `name="resend" value="240"`, `name="expire" value="4320"`,
		"192.0.2.10", "office mail server",
		"Google (Gmail, Workspace)", "_spf.google.com",
		"1 · updated", "not read yet", "timeout",
		`id="trusted-new"`, `id="trusted-0"`, `id="trusted-1"`,
		`action="/mail/admin/greylisting/trusted/0/delete"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("greylisting page missing %q:\n%s", want, firstLines(body, 40))
		}
	}
	if !containsAction(sa.called(), "mail.greylist_get") {
		t.Fatalf("agent actions %v missing mail.greylist_get", sa.called())
	}
}

func TestMailGreylistSave(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/greylisting")
	rr := postForm(h, "/mail/admin/greylisting", url.Values{
		"_token": {token}, "enabled": {"1"}, "deferral": {"10"}, "resend": {"480"}, "expire": {"1440"},
		"spf_bypass": {"1"}, "auto_trust_new": {"1"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/greylisting")
	if !strings.Contains(body, "Greylisting settings saved; greylisting is on.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	p := agentParams(t, sa, "mail.greylist_set")
	if p["deferral"] != float64(10) || p["resend"] != float64(480) || p["expire"] != float64(1440) {
		t.Fatalf("agent settings = %v", p)
	}
	if p["enabled"] != true || p["spf_bypass"] != true || p["auto_trust_new"] != true {
		t.Fatalf("agent flags = %v", p)
	}
	// The trusted hosts and the provider choices travel with every change.
	trusted, _ := p["trusted"].([]any)
	if len(trusted) != 2 {
		t.Fatalf("agent trusted = %v", p["trusted"])
	}
	providers, _ := p["providers"].(map[string]any)
	prov, _ := providers["google"].(map[string]any)
	if prov == nil || prov["trust"] != true || prov["auto_update"] != true {
		t.Fatalf("agent providers = %v", p["providers"])
	}
	// Off again.
	rr = postForm(h, "/mail/admin/greylisting", url.Values{
		"_token": {token}, "enabled": {"0"}, "deferral": {"5"}, "resend": {"240"}, "expire": {"4320"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/greylisting")
	if !strings.Contains(body, "Greylisting settings saved; greylisting is off.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}

func TestMailGreylistTrusted(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/greylisting")
	// Empty range: the form error, no agent call.
	rr := postForm(h, "/mail/admin/greylisting/trusted", url.Values{
		"_token": {token}, "range": {"  "}, "comment": {"x"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/greylisting")
	if !strings.Contains(body, "Enter an IP address or range.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	// Add one.
	rr = postForm(h, "/mail/admin/greylisting/trusted", url.Values{
		"_token": {token}, "range": {"203.0.113.99"}, "comment": {"test server"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/greylisting")
	if !strings.Contains(body, "Trusted host 203.0.113.99 saved.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	p := agentParams(t, sa, "mail.greylist_set")
	trusted, _ := p["trusted"].([]any)
	if len(trusted) != 3 {
		t.Fatalf("agent trusted after add = %v", p["trusted"])
	}
	last, _ := trusted[2].(map[string]any)
	if last["range"] != "203.0.113.99" || last["comment"] != "test server" {
		t.Fatalf("agent trusted[2] = %v", last)
	}
	// Edit index 0.
	rr = postForm(h, "/mail/admin/greylisting/trusted", url.Values{
		"_token": {token}, "range": {"10.0.0.1"}, "comment": {"edited"}, "index": {"0"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/greylisting")
	if !strings.Contains(body, "Trusted host 10.0.0.1 saved.") || !strings.Contains(body, "edited") {
		t.Fatalf("edit missing:\n%s", firstLines(body, 40))
	}
	p = agentParams(t, sa, "mail.greylist_set")
	trusted, _ = p["trusted"].([]any)
	first, _ := trusted[0].(map[string]any)
	if first["range"] != "10.0.0.1" {
		t.Fatalf("agent trusted[0] after edit = %v", first)
	}
	// Delete index 1 (the original 198.51.100.0/24).
	rr = postForm(h, "/mail/admin/greylisting/trusted/1/delete", url.Values{"_token": {token}}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/greylisting")
	if !strings.Contains(body, "Trusted host removed.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	// Delete a missing one.
	rr = postForm(h, "/mail/admin/greylisting/trusted/9/delete", url.Values{"_token": {token}}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/greylisting")
	if !strings.Contains(body, "That trusted host no longer exists.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}

func TestMailGreylistProviders(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/greylisting")
	rr := postForm(h, "/mail/admin/greylisting/providers", url.Values{
		"_token": {token}, "trust[]": {"yahoo"}, "auto_update[]": {"google", "yahoo"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/greylisting")
	if !strings.Contains(body, "Mail providers saved.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	p := agentParams(t, sa, "mail.greylist_set")
	providers, _ := p["providers"].(map[string]any)
	yes, _ := providers["yahoo"].(map[string]any)
	no, _ := providers["google"].(map[string]any)
	ms, _ := providers["microsoft"].(map[string]any)
	if yes == nil || yes["trust"] != true || yes["auto_update"] != true {
		t.Fatalf("yahoo provider = %v", providers["yahoo"])
	}
	// Every known provider is sent; unchecked boxes mean off.
	if no == nil || no["trust"] != false || no["auto_update"] != true {
		t.Fatalf("google provider = %v", no)
	}
	if ms == nil || ms["trust"] != false || ms["auto_update"] != false {
		t.Fatalf("microsoft provider = %v", ms)
	}
}

func TestMailGreylistUpdateProviders(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/greylisting")
	rr := postForm(h, "/mail/admin/greylisting/providers/update", url.Values{"_token": {token}}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/greylisting")
	if !strings.Contains(body, "Provider address ranges updated from their SPF records.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	p := agentParams(t, sa, "mail.greylist_update_providers")
	if p["all"] != true {
		t.Fatalf("agent all = %v", p["all"])
	}
}

func TestMailGreylistReport(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	rr := get(t, h, "/mail/admin/greylisting/report", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("report page = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"2 record(s)",
		"Deferring", "Retry accepted", "Passed", "Expired",
		"203.0.113.5", "spam@example.com", "198.51.100.9", "boss@example.com",
		`value="[&quot;203.0.113.5&quot;,&quot;spam@example.com&quot;,&quot;info@bob.example&quot;]"`,
		"Sender IP address", "Block expire time",
		"Page 1 of 1",
		"Remove selected", "Remove expired", "Remove all",
		"dir=asc",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("report page missing %q:\n%s", want, firstLines(body, 60))
		}
	}
	p := agentParams(t, sa, "mail.greylist_report")
	if p["sort"] != "created" || p["desc"] != true || p["page"] != float64(1) || p["per"] != float64(50) {
		t.Fatalf("agent report query = %v", p)
	}
	// Unknown sort/status/per values get the defaults.
	rr = get(t, h, "/mail/admin/greylisting/report?sort=bogus&status=bogus&per=999&dir=asc&search=%20x%20", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("filtered report = %d", rr.Code)
	}
	p = agentParams(t, sa, "mail.greylist_report")
	if p["sort"] != "created" || p["status"] != "" || p["per"] != float64(50) || p["desc"] != false || p["search"] != "x" {
		t.Fatalf("agent filtered query = %v", p)
	}
}

func TestMailGreylistCleanup(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/greylisting/report")
	// purge
	rr := postForm(h, "/mail/admin/greylisting/report", url.Values{
		"_token": {token}, "op": {"purge"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/greylisting/report")
	if !strings.Contains(body, "1 record(s) removed.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	p := agentParams(t, sa, "mail.greylist_cleanup")
	if p["op"] != "purge" {
		t.Fatalf("agent op = %v", p["op"])
	}
	// delete with a selection
	rr = postForm(h, "/mail/admin/greylisting/report", url.Values{
		"_token": {token}, "op": {"delete"},
		"sel[]": {`["198.51.100.9","boss@example.com","info@bob.example"]`},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/greylisting/report")
	if !strings.Contains(body, "1 record(s) removed.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	p = agentParams(t, sa, "mail.greylist_cleanup")
	triplets, _ := p["triplets"].([]any)
	if len(triplets) != 1 {
		t.Fatalf("agent triplets = %v", p["triplets"])
	}
	// delete without a selection
	rr = postForm(h, "/mail/admin/greylisting/report", url.Values{
		"_token": {token}, "op": {"delete"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/greylisting/report")
	if !strings.Contains(body, "Select records first.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	// unknown op
	rr = postForm(h, "/mail/admin/greylisting/report", url.Values{
		"_token": {token}, "op": {"nuke"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/greylisting/report")
	if !strings.Contains(body, "Unknown operation.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}

func TestMailRatePage(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	if rr := get(t, h, "/mail/admin/sending-limits", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("bob's limits page = %d, want 403", rr.Code)
	}
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	rr := get(t, h, "/mail/admin/sending-limits", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("limits page = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`<span class="wcp-state is-ok">On</span>`,
		"u12", "u10",
		`name="accounts[u12]"`, `value="500"`,
		"Limit reached", "Let send again",
		"Limits reached", "Reset all this hour",
		"max=\"1000000\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("limits page missing %q:\n%s", want, firstLines(body, 60))
		}
	}
	if !containsAction(sa.called(), "mail.rate_get") {
		t.Fatalf("agent actions %v missing mail.rate_get", sa.called())
	}
}

func TestMailRateSave(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/sending-limits")
	rr := postForm(h, "/mail/admin/sending-limits", url.Values{
		"_token": {token}, "enabled": {"1"}, "per_hour": {"500"},
		"accounts[u12]": {"250"}, "accounts[u10]": {""},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/sending-limits")
	if !strings.Contains(body, "Mail sending limits saved; they are on.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	p := agentParams(t, sa, "mail.rate_set")
	if p["per_hour"] != float64(500) || p["enabled"] != true {
		t.Fatalf("agent settings = %v", p)
	}
	accounts, _ := p["accounts"].(map[string]any)
	if len(accounts) != 1 || accounts["u12"] != float64(250) {
		t.Fatalf("agent accounts = %v", p["accounts"])
	}
	// Invalid per_hour.
	rr = postForm(h, "/mail/admin/sending-limits", url.Values{
		"_token": {token}, "enabled": {"1"}, "per_hour": {"abc"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/sending-limits")
	if !strings.Contains(body, "Enter a number between 0 (no limit) and 1000000.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	// An unknown account.
	rr = postForm(h, "/mail/admin/sending-limits", url.Values{
		"_token": {token}, "enabled": {"1"}, "per_hour": {"100"},
		"accounts[ghost]": {"5"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/sending-limits")
	if !strings.Contains(body, "ghost is not a website account.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	// Off.
	rr = postForm(h, "/mail/admin/sending-limits", url.Values{
		"_token": {token}, "per_hour": {"100"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/sending-limits")
	if !strings.Contains(body, "Mail sending limits saved; they are off.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}

func TestMailRateSaveTurnsOn(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	// The limits are off now: turning them on re-applies every PHP website.
	sa.rateData = `{"config": {"enabled": false, "per_hour": 0, "accounts": {}},
	 "accounts": ["u10", "u12"],
	 "service": {"active": false, "postfix": false},
	 "configured": true}`
	token, cookie := pageToken(t, h, cookie, "/mail/admin/sending-limits")
	rr := postForm(h, "/mail/admin/sending-limits", url.Values{
		"_token": {token}, "enabled": {"1"}, "per_hour": {"300"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/sending-limits")
	if !strings.Contains(body, "Mail sending limits saved; they are on.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	acts := sa.called()
	n := 0
	for _, a := range acts {
		if a == "sites.apply" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("sites.apply count = %d, want 3 (the PHP websites): %v", n, acts)
	}
	if !containsAction(acts, "mail.rate_set") {
		t.Fatalf("mail.rate_set missing: %v", acts)
	}
}

func TestMailRateReset(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/sending-limits")
	rr := postForm(h, "/mail/admin/sending-limits/reset", url.Values{
		"_token": {token}, "account": {"u10"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/sending-limits")
	if !strings.Contains(body, "u10 may send again this hour.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	p := agentParams(t, sa, "mail.rate_reset")
	if p["account"] != "u10" {
		t.Fatalf("agent account = %v", p["account"])
	}
	rr = postForm(h, "/mail/admin/sending-limits/reset", url.Values{
		"_token": {token}, "account": {""},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/sending-limits")
	if !strings.Contains(body, "All accounts may send again this hour.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}
