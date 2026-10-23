package http

// Tests for the per-mailbox e-mail filters + autoresponder page
// (MailFiltersController::show / save / toggle / delete / responder).

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const filtersPage = "/mail/domains/60/mailboxes/1/filters"

func TestMailFiltersPage(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	rr := get(t, h, filtersPage, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("filters page = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Filters for <span class=\"font-monospace\">info@bob.example</span>",
		"To spam",
		`Subject contains “invoice”`,
		"Move to folder spam",
		`id="filter-new"`,
		`id="filter-0"`,
		"Autoresponder",
		"Out of office",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("filters page missing %q", want)
		}
	}
	// A mailbox the user does not have is a redirect with the message.
	rr = get(t, h, "/mail/domains/60/mailboxes/999/filters", cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/mail" {
		t.Fatalf("unknown mailbox = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	// The filters page is gated by mail.manage (carol has none).
	h, cookie, _, _ = dnsMailEnv(t, "carol")
	if rr := get(t, h, filtersPage, cookie); rr.Code != http.StatusForbidden {
		t.Fatalf("carol's filters page = %d, want 403", rr.Code)
	}
}

func TestMailFilterSave(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, filtersPage)
	rr := postForm(h, filtersPage, url.Values{
		"_token": {token}, "name": {"Vacation"}, "enabled": {"1"}, "any": {"0"},
		"rules[0][part]": {"from"}, "rules[0][match]": {"contains"}, "rules[0][value]": {"boss@example.com"},
		"actions[0][type]": {"fileinto"}, "actions[0][value]": {"boss"},
	}, cookie)
	body := mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "Filter “Vacation” saved.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	if !strings.Contains(body, "Vacation") || !strings.Contains(body, "To spam") {
		t.Fatalf("new filter not listed:\n%s", firstLines(body, 12))
	}
	// The agent stored both filters.
	if !containsAction(sa.called(), "mail.filters_save") {
		t.Fatalf("agent actions %v missing mail.filters_save", sa.called())
	}
	rr = get(t, h, filtersPage, cookie)
	if !strings.Contains(rr.Body.String(), "From contains “boss@example.com”") {
		t.Fatalf("saved filter not listed:\n%s", firstLines(rr.Body.String(), 12))
	}
}

func TestMailFilterEdit(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, filtersPage)
	rr := postForm(h, filtersPage, url.Values{
		"_token": {token}, "index": {"0"}, "name": {"Renamed"}, "enabled": {"1"}, "any": {"1"},
		"rules[0][part]": {"subject"}, "rules[0][match]": {"contains"}, "rules[0][value]": {"invoice"},
		"actions[0][type]": {"fileinto"}, "actions[0][value]": {"spam"},
	}, cookie)
	body := mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "Filter “Renamed” saved.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	if strings.Contains(body, "To spam") {
		t.Fatalf("edit replaced the filter, the old one must not remain:\n%s", firstLines(body, 12))
	}
	if !strings.Contains(body, "any condition matches") {
		t.Fatalf("any-mode not stored")
	}
}

func TestMailFilterSaveValidation(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, filtersPage)
	// Nothing at all: all three field messages, joined, on the redirect.
	rr := postForm(h, filtersPage, url.Values{"_token": {token}}, cookie)
	body := mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "Name the filter. Add at least one condition. Add at least one action.") {
		t.Fatalf("validation messages missing:\n%s", firstLines(body, 5))
	}
	// A name without a condition and without an action.
	rr = postForm(h, filtersPage, url.Values{
		"_token": {token}, "name": {"Empty"},
	}, cookie)
	body = mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "Add at least one condition. Add at least one action.") {
		t.Fatalf("partial validation missing:\n%s", firstLines(body, 5))
	}
}

func TestMailFilterToggle(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, filtersPage)
	rr := postForm(h, filtersPage+"/0/toggle", url.Values{"_token": {token}}, cookie)
	body := mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "Filter “To spam” disabled.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	rr = get(t, h, filtersPage, cookie)
	if !strings.Contains(rr.Body.String(), "text-body-secondary") || !strings.Contains(rr.Body.String(), ">off<") {
		t.Fatalf("filter not shown disabled:\n%s", firstLines(rr.Body.String(), 12))
	}
	// Back on.
	rr = postForm(h, filtersPage+"/0/toggle", url.Values{"_token": {token}}, cookie)
	body = mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "Filter “To spam” enabled.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	// Toggling a filter that is gone.
	rr = postForm(h, filtersPage+"/5/toggle", url.Values{"_token": {token}}, cookie)
	body = mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "The filter no longer exists.") {
		t.Fatalf("missing index not caught:\n%s", firstLines(body, 5))
	}
}

func TestMailFilterDelete(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, filtersPage)
	rr := postForm(h, filtersPage+"/0/delete", url.Values{"_token": {token}}, cookie)
	body := mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "Filter “To spam” deleted.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	rr = get(t, h, filtersPage, cookie)
	if !strings.Contains(rr.Body.String(), "No filters") {
		t.Fatalf("filter list not empty after delete:\n%s", firstLines(rr.Body.String(), 12))
	}
	if !containsAction(sa.called(), "mail.filters_save") {
		t.Fatalf("agent actions %v missing mail.filters_save", sa.called())
	}
}

func TestMailResponder(t *testing.T) {
	h, cookie, sa, _ := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, filtersPage)
	rr := postForm(h, "/mail/domains/60/mailboxes/1/responder", url.Values{
		"_token": {token}, "enabled": {"1"}, "subject": {"Out of office"},
		"body": {"Back next week."}, "days": {"3"}, "start": {"2026-10-26"}, "end": {"2026-10-31"},
	}, cookie)
	body := mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "Autoresponder on.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
	rr = get(t, h, filtersPage, cookie)
	page := rr.Body.String()
	for _, want := range []string{`name="subject" maxlength="200" value="Out of office"`, "Back next week.", `name="days" value="3"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("responder not stored (missing %q)", want)
		}
	}
	if !containsAction(sa.called(), "mail.filters_save") {
		t.Fatalf("agent actions %v missing mail.filters_save", sa.called())
	}
	// Switching it off.
	rr = postForm(h, "/mail/domains/60/mailboxes/1/responder", url.Values{
		"_token": {token}, "enabled": {"0"}, "subject": {"Out of office"}, "body": {"Back next week."}, "days": {"3"},
	}, cookie)
	body = mailFollow(t, h, rr, filtersPage)
	if !strings.Contains(body, "Autoresponder off.") {
		t.Fatalf("flash missing:\n%s", firstLines(body, 5))
	}
}
