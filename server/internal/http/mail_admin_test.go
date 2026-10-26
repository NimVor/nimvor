package http

// Tests for the mail administration pages (MailAdminController): the mail
// queue (list, headers JSON, actions), the delivery reports + statistics,
// the mail server status + settings, and the tools (trace, repair).
//
// All of it is for server administrators: bob (role 2, own scope) gets the
// guard's 403, and the handlers additionally gate on the all scope.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestMailAdminQueue(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	// bob has no server.view: the route guard denies.
	if rr := get(t, h, "/mail/admin/queue", cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("bob's queue page = %d, want 403", rr.Code)
	}
	// carol has server.view but her role is own-scope: the all-scope gate
	// denies her with the message (the PHP denied()).
	h, cookie, _, _ = dnsMailEnv(t, "carol")
	rr := get(t, h, "/mail/admin/queue", cookie)
	if rr.Code != http.StatusForbidden || !pageHas(rr.Body.String(), "Email administration is for server administrators.") {
		t.Fatalf("carol's queue page = %d (want the all-scope 403)", rr.Code)
	}
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	rr = get(t, h, "/mail/admin/queue", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("queue page = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"2 message(s) in the queue",
		"A1B2C3D4E5F607",
		"F0E1D2C3B4A506",
		"Deferred",
		"Connection timed out",
		"(bounce)",
		`name="ids[]" value="A1B2C3D4E5F607"`,
		`data-wcp-mail-headers="/mail/admin/queue/A1B2C3D4E5F607"`,
		"Retry all deferred",
	} {
		if !pageHas(body, want) {
			t.Fatalf("queue page missing %q", want)
		}
	}
	if !containsAction(sa.called(), "mail.queue") {
		t.Fatalf("agent actions %v missing mail.queue", sa.called())
	}
}

func TestMailAdminMessage(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "admin")
	rr := get(t, h, "/mail/admin/queue/A1B2C3D4E5F607", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("message = %d", rr.Code)
	}
	var got struct {
		Success bool              `json:"success"`
		Headers map[string]string `json:"headers"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil || !got.Success {
		t.Fatalf("message json = %+v (%v)", got, err)
	}
	if got.Headers["subject"] != "Job failed" || got.Headers["from"] != "<bounce+job@example.com>" {
		t.Fatalf("headers = %v", got.Headers)
	}
	// A queue id the agent refuses: the 404 JSON the modal shows.
	rr = get(t, h, "/mail/admin/queue/short", cookie)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("bad id = %d, want 404", rr.Code)
	}
}

func TestMailAdminQueueAction(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/queue")
	// No messages selected (and not a flush).
	rr := postForm(h, "/mail/admin/queue", url.Values{
		"_token": {token}, "action": {"delete"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/queue")
	if !pageHas(body, "Select messages first.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	// Delete one message.
	rr = postForm(h, "/mail/admin/queue", url.Values{
		"_token": {token}, "action": {"delete"}, "ids[]": {"A1B2C3D4E5F607"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/queue")
	if !pageHas(body, "1 message(s) deleted.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	params := agentParams(t, sa, "mail.queue_action")
	ids, _ := params["ids"].([]any)
	if len(ids) != 1 || ids[0] != "A1B2C3D4E5F607" {
		t.Fatalf("agent ids = %v", params["ids"])
	}
	// Flush (retry every deferred message), no selection needed.
	rr = postForm(h, "/mail/admin/queue", url.Values{
		"_token": {token}, "action": {"flush"},
	}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/queue")
	if !pageHas(body, "Delivery of every deferred message is being retried.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}

func TestMailAdminReports(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	rr := get(t, h, "/mail/admin/reports", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("reports = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"2026-10-22 13:05:00",
		"Delivered here",
		"job@example.com",
		"mail.example.com[10.0.0.2]:25",
	} {
		if !pageHas(body, want) {
			t.Fatalf("reports page missing %q", want)
		}
	}
	if !pageHas(body, "2026-10-22 13:06:00") || !pageHas(body, "Connection timed out") {
		t.Fatalf("deferred delivery missing:\n%s", firstLines(body, 20))
	}
	// The agent got the default 24h window.
	params := agentParams(t, sa, "mail.report")
	if lim, _ := params["limit"].(float64); int(lim) != 500 {
		t.Fatalf("agent limit = %v", params["limit"])
	}
	// The statistics tab: totals, over time, relayers, senders.
	rr = get(t, h, "/mail/admin/reports?view=stats&range=7d", cookie)
	body = rr.Body.String()
	for _, want := range []string{
		"Statistics",
		"4 in · 3 out · 1 failed",
		"2026-10-22", // a 7-day range shows daily buckets (the date only)
		"www-data",
		"server account",
		"bob.example",
	} {
		if !pageHas(body, want) {
			t.Fatalf("stats page missing %q", want)
		}
	}
	if !pageHas(body, ">12</div>") {
		t.Fatalf("received total missing:\n%s", firstLines(body, 20))
	}
	// A filtered query reaches the agent.
	rr = get(t, h, "/mail/admin/reports?range=1h&address=a@bob.example&status=bounced", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("filtered reports = %d", rr.Code)
	}
	params = agentParams(t, sa, "mail.report")
	if params["address"] != "a@bob.example" || params["status"] != "bounced" {
		t.Fatalf("agent filters = %v", params)
	}
}

func TestMailAdminServer(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "admin")
	rr := get(t, h, "/mail/admin/server", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("server page = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Postfix 3.8.0",
		"Running",
		"starts at boot",
		"Not installed",
		`name="postfix[message_size_limit]" value="10"`,
		`name="current[message_size_limit]" value="10"`,
		`name="postfix[smtpd_helo_required]"`,
		`name="dovecot[auth_failure_delay]" value="5s"`, // Dovecot values are shown as the server has them
		"connect from mail.example.com[10.0.0.2]",
		"(journal)",
		"Check and apply",
	} {
		if !pageHas(body, want) {
			t.Fatalf("server page missing %q", want)
		}
	}
}

func TestMailAdminSaveSettings(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/server")
	rr := postForm(h, "/mail/admin/server", url.Values{
		"_token": {token},
		// Unchanged: omitted from the agent call.
		"current[message_size_limit]": {"10"},
		"postfix[message_size_limit]": {"10"},
		// Changed: sent.
		"current[smtpd_helo_required]": {"yes"},
		"postfix[smtpd_helo_required]": {"no"},
		// Dovecot is always sent.
		"dovecot[auth_failure_delay]": {"10"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/server")
	if !pageHas(body, "Mail server settings applied.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	params := agentParams(t, sa, "mail.settings_set")
	pf, _ := params["postfix"].(map[string]any)
	if len(pf) != 1 || pf["smtpd_helo_required"] != "no" {
		t.Fatalf("changed Postfix fields = %v", pf)
	}
	dc, _ := params["dovecot"].(map[string]any)
	if dc["auth_failure_delay"] != "10" {
		t.Fatalf("Dovecot fields = %v", dc)
	}
}

func TestMailAdminSaveSettingsFailure(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	sa.failActions["mail.settings_set"] = "Postfix refused the settings."
	token, cookie := pageToken(t, h, cookie, "/mail/admin/server")
	rr := postForm(h, "/mail/admin/server", url.Values{
		"_token":             {token},
		"current[relayhost]": {""},
		"postfix[relayhost]": {"smtp.example.com"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/server")
	if !pageHas(body, "Postfix refused the settings.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}

func TestMailAdminTools(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	rr := get(t, h, "/mail/admin/tools", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("tools = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Mail troubleshooter",
		"Repair mailbox permissions",
		`value="bob.example"`,
		`value="carol.example"`,
		"All domains",
	} {
		if !pageHas(body, want) {
			t.Fatalf("tools page missing %q", want)
		}
	}
	// A trace: the agent's steps.
	rr = get(t, h, "/mail/admin/tools?address=info@bob.example", cookie)
	body = rr.Body.String()
	for _, want := range []string{
		"info@bob.example",
		"A mailbox on this server.",
	} {
		if !pageHas(body, want) {
			t.Fatalf("trace missing %q", want)
		}
	}
	params := agentParams(t, sa, "mail.trace")
	if params["address"] != "info@bob.example" {
		t.Fatalf("agent address = %v", params["address"])
	}
}

func TestMailAdminRepair(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/mail/admin/tools")
	rr := postForm(h, "/mail/admin/tools/repair", url.Values{
		"_token": {token}, "domain": {"bob.example"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/tools")
	if !pageHas(body, "Checked 4 folder(s) and 9 file(s); fixed 2.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	if !pageHas(body, "Skipped 1 link(s) or special file(s): Maildir/info/cur.") {
		t.Fatalf("skipped list missing:\n%s", firstLines(body, 5))
	}
	params := agentParams(t, sa, "mail.repair_permissions")
	if params["domain"] != "bob.example" {
		t.Fatalf("agent domain = %v", params["domain"])
	}
	// All domains: an empty domain.
	rr = postForm(h, "/mail/admin/tools/repair", url.Values{"_token": {token}}, cookie)
	body = mailFollow(t, h, rr, "/mail/admin/tools")
	if !pageHas(body, "Checked 4 folder(s) and 9 file(s); fixed 2.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}

func TestMailAdminRepairFailure(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "admin")
	sa.failActions["mail.repair_permissions"] = "The vmail user does not exist."
	token, cookie := pageToken(t, h, cookie, "/mail/admin/tools")
	rr := postForm(h, "/mail/admin/tools/repair", url.Values{
		"_token": {token}, "domain": {"bob.example"},
	}, cookie)
	body := mailFollow(t, h, rr, "/mail/admin/tools")
	if !pageHas(body, "The vmail user does not exist.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}
