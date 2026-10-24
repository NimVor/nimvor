package http

// This suite drives the /ips administration area (batch 64): the pool page
// (agent addresses, unpooled, add/edit/delete/rebuild), the routed IPv6
// ranges (CRUD + allocate) and the migration wizard. ipEnv's fixture: the
// pool rows 10.10.0.99 (used by bob.example), 10.10.0.15 (free),
// 192.168.1.20 (reserved "Staging"), 172.16.0.9 (dedicated, used by
// alice.example); the fake agent's server has 203.0.113.20,
// 2001:db8:100::5 and a link-local.

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// ipFlash follows a 303 and returns the page's flash text.
func ipFlash(t *testing.T, h http.Handler, c *http.Cookie, page string) string {
	t.Helper()
	return get(t, h, page, c).Body.String()
}

func TestIpPagesAdminOnly(t *testing.T) {
	h, cookie, sa, _ := ipEnv(t, "admin")
	body := ipFlash(t, h, cookie, "/ips")
	for _, want := range []string{
		"IP addresses", "Addresses and usage",
		"10.10.0.99", "10.10.0.15", "192.168.1.20", "172.16.0.9", // the pool
		"203.0.113.20", "2001:db8:100::5", // unpooled
		"203.0.113.1",                  // the gateway
		"Staging",                      // the reserved reason
		"Not on the server",            // pooled but not on the interfaces
		"bob.example", "alice.example", // the bound sites
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pool page missing %q", want)
		}
	}
	if strings.Contains(body, "fe80::1") {
		t.Error("the link-local address must not be offered")
	}
	if strings.Contains(body, "Rebuild pool") == false {
		t.Error("the rebuild action must show for administrators")
	}
	if !strings.Contains(body, "Edit") {
		t.Error("the edit modal trigger must show for administrators")
	}

	// The customer is denied the whole area.
	h2, c2, _, _ := ipEnv(t, "bob")
	for _, page := range []string{"/ips", "/ips/ranges", "/ips/migrate"} {
		if rr := get(t, h2, page, c2); rr.Code != http.StatusForbidden {
			t.Errorf("customer %s = %d, want 403", page, rr.Code)
		}
	}
	_ = sa
}

func TestIpStoreValidationAndAdd(t *testing.T) {
	h, cookie, _, db := ipEnv(t, "admin")

	post := func(values url.Values) (*http.Cookie, string) {
		t.Helper()
		token, cookie := pageToken(t, h, cookie, "/ips")
		values.Set("_token", token)
		rr := postForm(h, "/ips", values, cookie)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("store = %d, want 303", rr.Code)
		}
		body := ipFlash(t, h, cookie, "/ips")
		return cookie, body
	}

	// Not on the server.
	_, body := post(url.Values{"address": {"8.8.8.8"}, "kind": {"shared"}})
	if !strings.Contains(body, "8.8.8.8 is not configured on this server.") {
		t.Errorf("not-on-server flash missing:\n%s", body)
	}
	// Already pooled.
	_, body = post(url.Values{"address": {"10.10.0.99"}, "kind": {"shared"}})
	if !strings.Contains(body, "10.10.0.99 is already in the pool.") {
		t.Error("already-pooled flash missing")
	}
	// A wrong kind.
	_, body = post(url.Values{"address": {"203.0.113.20"}, "kind": {"weird"}})
	if !strings.Contains(body, "Choose shared or dedicated.") {
		t.Error("kind flash missing")
	}
	// The good one.
	_, body = post(url.Values{"address": {"203.0.113.20"}, "kind": {"dedicated"}})
	if !strings.Contains(body, "Address added to the pool.") {
		t.Errorf("success flash missing:\n%s", body)
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM ip_addresses WHERE address = '203.0.113.20'`).Scan(&kind); err != nil || kind != "dedicated" {
		t.Fatalf("stored row kind = %q (%v)", kind, err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ip.add'`) != 1 {
		t.Error("ip.add audit missing")
	}
}

func TestIpUpdateAndDelete(t *testing.T) {
	h, cookie, _, db := ipEnv(t, "admin")
	idOf := func(addr string) string {
		t.Helper()
		var id int64
		if err := db.QueryRow(`SELECT id FROM ip_addresses WHERE address = ?`, addr).Scan(&id); err != nil {
			t.Fatalf("pool id for %s: %v", addr, err)
		}
		return strconv.FormatInt(id, 10)
	}

	// Reserved without a reason.
	token, cookie := pageToken(t, h, cookie, "/ips")
	rr := postForm(h, "/ips/"+idOf("10.10.0.15"), url.Values{
		"_token": {token}, "kind": {"shared"}, "reserved": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update = %d", rr.Code)
	}
	body := ipFlash(t, h, cookie, "/ips")
	if !strings.Contains(body, "Say why the address is reserved.") {
		t.Errorf("reason flash missing:\n%s", body)
	}
	// Reserved with a reason.
	token, cookie = pageToken(t, h, cookie, "/ips")
	postForm(h, "/ips/"+idOf("10.10.0.15"), url.Values{
		"_token": {token}, "kind": {"shared"}, "reserved": {"1"}, "reason": {"Mail server"},
	}, cookie)
	body := ipFlash(t, h, cookie, "/ips")
	if !strings.Contains(body, "Address saved.") {
		t.Errorf("saved flash missing:\n%s", body)
	}
	var reserved int
	var reason string
	if err := db.QueryRow(`SELECT reserved, reason FROM ip_addresses WHERE address = '10.10.0.15'`).Scan(&reserved, &reason); err != nil || reserved != 1 || reason != "Mail server" {
		t.Fatalf("row after update = %d %q (%v)", reserved, reason, err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ip.update'`) != 1 {
		t.Error("ip.update audit missing")
	}
	// A used address cannot be reserved.
	token, cookie = pageToken(t, h, cookie, "/ips")
	postForm(h, "/ips/"+idOf("10.10.0.99"), url.Values{
		"_token": {token}, "kind": {"shared"}, "reserved": {"1"}, "reason": {"x"},
	}, cookie)
	body := ipFlash(t, h, cookie, "/ips")
	if !strings.Contains(body, "10.10.0.99 is used by bob.example; move those websites first.") {
		t.Errorf("in-use reserved flash missing:\n%s", body)
	}

	// The free row can be removed.
	token, cookie = pageToken(t, h, cookie, "/ips")
	rr = postForm(h, "/ips/"+idOf("10.10.0.15")+"/delete", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	body = ipFlash(t, h, cookie, "/ips")
	if !strings.Contains(body, "Address removed from the pool (the server keeps it on its interface).") {
		t.Errorf("delete flash missing:\n%s", body)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM ip_addresses WHERE address = '10.10.0.15'`) != 0 {
		t.Error("the row must be gone")
	}
	// The used one cannot.
	token, _ = pageToken(t, h, cookie, "/ips")
	postForm(h, "/ips/"+idOf("10.10.0.99")+"/delete", url.Values{"_token": {token}}, cookie)
	body = ipFlash(t, h, cookie, "/ips")
	if !strings.Contains(body, "10.10.0.99 is used by bob.example; it cannot be removed.") {
		t.Errorf("in-use delete flash missing:\n%s", body)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM ip_addresses WHERE address = '10.10.0.99'`) != 1 {
		t.Error("the used row must remain")
	}
}

func TestIpRebuild(t *testing.T) {
	h, cookie, _, db := ipEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/ips")
	rr := postForm(h, "/ips/rebuild", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("rebuild = %d", rr.Code)
	}
	body := ipFlash(t, h, cookie, "/ips")
	// The two unpooled addresses are added; the four old pool rows are no
	// longer on the server (the fixture's agent has only the new ones).
	if !strings.Contains(body, "Pool rebuilt: 2 address(es) added, 4 no longer on the server (kept, listed below).") {
		t.Errorf("rebuild flash missing:\n%s", body)
	}
	if !strings.Contains(body, "No longer on the server") || !strings.Contains(body, "10.10.0.99") {
		t.Error("the kept-but-missing notice is missing")
	}
	for _, addr := range []string{"203.0.113.20", "2001:db8:100::5"} {
		if rowCount(t, db, `SELECT COUNT(*) FROM ip_addresses WHERE address = ?`, addr) != 1 {
			t.Errorf("%s must be pooled after the rebuild", addr)
		}
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ip.rebuild'`) != 1 {
		t.Error("ip.rebuild audit missing")
	}

	// Agent down: the rebuild is a plain failure flash.
	h2, c2, sa2, _ := ipEnv(t, "admin")
	sa2.failActions["net.addresses"] = "the agent is down"
	token2, c2 := pageToken(t, h2, c2, "/ips")
	postForm(h2, "/ips/rebuild", url.Values{"_token": {token2}}, c2)
	body2 := ipFlash(t, h2, c2, "/ips")
	if !strings.Contains(body2, "The change could not be made.") {
		t.Errorf("agent-down rebuild flash missing:\n%s", body2)
	}
	// The page itself degrades: the agent-error card, unknown presence.
	body2 = ipFlash(t, h2, c2, "/ips")
	if !strings.Contains(body2, "the agent is down") || !strings.Contains(body2, "unknown") {
		t.Error("the pool page must degrade with the agent down")
	}
}

func TestIpRanges(t *testing.T) {
	h, cookie, sa, db := ipEnv(t, "admin")

	// Empty at first.
	body := ipFlash(t, h, cookie, "/ips/ranges")
	if !strings.Contains(body, "No IPv6 range yet.") {
		t.Error("the empty state must show")
	}

	// Add a range.
	token, cookie := pageToken(t, h, cookie, "/ips/ranges")
	rr := postForm(h, "/ips/ranges", url.Values{
		"_token": {token}, "prefix": {"2001:db8:100::/64"}, "note": {"Provider block"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("store range = %d", rr.Code)
	}
	body = ipFlash(t, h, cookie, "/ips/ranges")
	if !strings.Contains(body, "Range added; its addresses are now local to this server.") {
		t.Errorf("add flash missing:\n%s", body)
	}
	if sa.ipv6Ranges != "2001:db8:100::/64" {
		t.Errorf("pushed ranges = %q", sa.ipv6Ranges)
	}
	var id int64
	if err := db.QueryRow(`SELECT id FROM ipv6_ranges WHERE prefix = '2001:db8:100::/64'`).Scan(&id); err != nil {
		t.Fatalf("range row: %v", err)
	}
	idStr := strconv.FormatInt(id, 10)

	// A v4 prefix is refused, and so is a duplicate.
	post := func(values url.Values) (string, *http.Cookie) {
		t.Helper()
		token, cookie := pageToken(t, h, cookie, "/ips/ranges")
		values.Set("_token", token)
		postForm(h, "/ips/ranges", values, cookie)
		body := ipFlash(t, h, cookie, "/ips/ranges")
		return body, cookie
	}
	body, cookie = post(url.Values{"prefix": {"203.0.113.0/24"}})
	if !strings.Contains(body, "Enter an IPv6 range such as 2001:db8:100::/64.") {
		t.Errorf("v4 prefix flash missing:\n%s", body)
	}
	body, cookie = post(url.Values{"prefix": {"2001:db8:100::/64"}})
	if !strings.Contains(body, "That range is already added.") {
		t.Errorf("duplicate flash missing:\n%s", body)
	}

	// Save the note.
	token, cookie = pageToken(t, h, cookie, "/ips/ranges")
	postForm(h, "/ips/ranges/"+idStr, url.Values{"_token": {token}, "note": {"Updated note"}}, cookie)
	body = ipFlash(t, h, cookie, "/ips/ranges")
	if !strings.Contains(body, "Range saved.") {
		t.Errorf("note flash missing:\n%s", body)
	}
	var note string
	if err := db.QueryRow(`SELECT note FROM ipv6_ranges WHERE id = ?`, idStr).Scan(&note); err != nil || note != "Updated note" {
		t.Fatalf("note after save = %q (%v)", note, err)
	}

	// Allocate the next free (from the range's ::10 up).
	token, cookie = pageToken(t, h, cookie, "/ips/ranges")
	postForm(h, "/ips/ranges/"+idStr+"/allocate", url.Values{"_token": {token}}, cookie)
	body = ipFlash(t, h, cookie, "/ips/ranges")
	if !strings.Contains(body, "2001:db8:100::10 allocated.") {
		t.Errorf("allocate flash missing:\n%s", body)
	}
	var rangeID *int64
	if err := db.QueryRow(`SELECT range_id FROM ip_addresses WHERE address = '2001:db8:100::10'`).Scan(&rangeID); err != nil || rangeID == nil {
		t.Fatalf("allocation row: %v (%v)", rangeID, err)
	}

	// Allocate to a website: the address follows the DNS.
	token, cookie = pageToken(t, h, cookie, "/ips/ranges")
	postForm(h, "/ips/ranges/"+idStr+"/allocate", url.Values{
		"_token": {token}, "website_id": {"10"}, "dns": {"1"},
	}, cookie)
	body = ipFlash(t, h, cookie, "/ips/ranges")
	if !strings.Contains(body, "2001:db8:100::11 allocated and assigned to bob.example; 1 DNS record(s) updated.") {
		t.Errorf("assign flash missing:\n%s", body)
	}
	var v6 string
	if err := db.QueryRow(`SELECT ipv6 FROM websites WHERE id = 10`).Scan(&v6); err != nil || v6 != "2001:db8:100::11" {
		t.Fatalf("website ipv6 = %q (%v)", v6, err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dns_records WHERE zone_id = 1 AND type = 'AAAA' AND value = '2001:db8:100::11'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("AAAA record count = %d (%v)", count, err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ip.allocate'`) != 2 {
		t.Error("ip.allocate audits missing")
	}

	// An unknown website is a user-facing error.
	token, cookie = pageToken(t, h, cookie, "/ips/ranges")
	postForm(h, "/ips/ranges/"+idStr+"/allocate", url.Values{
		"_token": {token}, "website_id": {"999"},
	}, cookie)
	body = ipFlash(t, h, cookie, "/ips/ranges")
	if !strings.Contains(body, "That website does not exist.") {
		t.Errorf("unknown website flash missing:\n%s", body)
	}

	// The range with a used address cannot be removed.
	token, cookie = pageToken(t, h, cookie, "/ips/ranges")
	postForm(h, "/ips/ranges/"+idStr+"/delete", url.Values{"_token": {token}}, cookie)
	body = ipFlash(t, h, cookie, "/ips/ranges")
	if !strings.Contains(body, "Addresses of this range are used by bob.example; move those websites first.") {
		t.Errorf("in-use remove flash missing:\n%s", body)
	}
	// ...but once the websites are off it, it goes with its free rows.
	if _, err := db.Exec(`UPDATE websites SET ipv6 = NULL WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	token, cookie = pageToken(t, h, cookie, "/ips/ranges")
	postForm(h, "/ips/ranges/"+idStr+"/delete", url.Values{"_token": {token}}, cookie)
	body = ipFlash(t, h, cookie, "/ips/ranges")
	if !strings.Contains(body, "Range removed.") {
		t.Errorf("remove flash missing:\n%s", body)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM ipv6_ranges WHERE id = ?`, idStr) != 0 {
		t.Error("the range must be gone")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM ip_addresses WHERE address LIKE '2001:db8:100::%'`) != 0 {
		t.Error("the range's free allocations must be gone")
	}
	if sa.ipv6Ranges != "" {
		t.Errorf("the agent must be left with no ranges: %q", sa.ipv6Ranges)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ip.range_remove'`) != 1 {
		t.Error("ip.range_remove audit missing")
	}
}

func TestIpMigratePlanValidation(t *testing.T) {
	h, cookie, _, _ := ipEnv(t, "admin")

	// No query: the form without a plan.
	body := ipFlash(t, h, cookie, "/ips/migrate")
	if !strings.Contains(body, "Choose the addresses") || strings.Contains(body, "Changes:") {
		t.Error("the bare wizard must show the form only")
	}
	// A plan: one website follows.
	body = ipFlash(t, h, cookie, "/ips/migrate?source=10.10.0.99&dest=10.10.0.15")
	if !strings.Contains(body, "Changes:") || !strings.Contains(body, "Websites (1)") || !strings.Contains(body, "bob.example") {
		t.Errorf("the plan must list the website:\n%s", body)
	}
	if !strings.Contains(body, "its own A records follow") {
		t.Error("the plan must say the A records follow")
	}
	// Reserved destination.
	body = ipFlash(t, h, cookie, "/ips/migrate?source=10.10.0.99&dest=192.168.1.20")
	if !strings.Contains(body, "192.168.1.20 is reserved.") {
		t.Errorf("reserved dest error missing:\n%s", body)
	}
	// Same address.
	body = ipFlash(t, h, cookie, "/ips/migrate?source=10.10.0.99&dest=10.10.0.99")
	if !strings.Contains(body, "Choose a different address.") {
		t.Errorf("same-address error missing:\n%s", body)
	}
	// Missing source.
	body = ipFlash(t, h, cookie, "/ips/migrate?dest=10.10.0.15")
	if !strings.Contains(body, "Choose the address to move from (from the pool).") {
		t.Errorf("missing source error missing:\n%s", body)
	}
	// A dedicated destination that already has a website.
	body = ipFlash(t, h, cookie, "/ips/migrate?source=10.10.0.99&dest=172.16.0.9")
	if !strings.Contains(body, "172.16.0.9 is dedicated; it can take only one website.") {
		t.Errorf("dedicated dest error missing:\n%s", body)
	}
}

func TestIpMigrateRun(t *testing.T) {
	h, cookie, _, db := ipEnv(t, "admin")

	// Without the confirmation checkbox.
	token, cookie := pageToken(t, h, cookie, "/ips/migrate")
	rr := postForm(h, "/ips/migrate", url.Values{
		"_token": {token}, "source": {"10.10.0.99"}, "dest": {"10.10.0.15"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("unconfirmed migration = %d", rr.Code)
	}
	body := ipFlash(t, h, cookie, "/ips/migrate")
	if !strings.Contains(body, "Confirm the migration first.") {
		t.Errorf("confirm flash missing:\n%s", body)
	}

	// The real run.
	token, cookie = pageToken(t, h, cookie, "/ips/migrate")
	postForm(h, "/ips/migrate", url.Values{
		"_token": {token}, "source": {"10.10.0.99"}, "dest": {"10.10.0.15"}, "confirm": {"1"},
	}, cookie)
	body = ipFlash(t, h, cookie, "/ips/migrate")
	if !strings.Contains(body, "Migration complete.") {
		t.Errorf("success flash missing:\n%s", body)
	}
	var v4 string
	if err := db.QueryRow(`SELECT ipv4 FROM websites WHERE id = 10`).Scan(&v4); err != nil || v4 != "10.10.0.15" {
		t.Fatalf("website ipv4 after migration = %q (%v)", v4, err)
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM dns_records WHERE zone_id = 1 AND type = 'A'`).Scan(&value); err != nil || value != "10.10.0.15" {
		t.Fatalf("A record after migration = %q (%v)", value, err)
	}
	// The results are listed per website.
	if !strings.Contains(body, "Results") || !strings.Contains(body, "bob.example") || !strings.Contains(body, "Done") {
		t.Errorf("the results card is missing:\n%s", body)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ip.migrate' AND result = 'success'`) != 1 {
		t.Error("ip.migrate audit missing")
	}
}
