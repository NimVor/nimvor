package http

// Tests for the DNS zone editor's write routes (DnsController::addRecord /
// updateRecord / deleteRecord / bulk / settings / sync / import / export /
// delete). The shared dnsMailEnv seeds zone 50 (bob.example, serial 7, the
// usual records incl. a private A) and the fake agent answers
// dns.zone_parse / dns.zone_render / dns.zone_delete.

import (
	"database/sql"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// zone50RecordID is a zone 50 record's id by name+type (the env seeds
// without explicit ids).
func zone50RecordID(t *testing.T, db *sql.DB, name, typ string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM dns_records WHERE zone_id = 50 AND name = ? AND type = ?`, name, typ).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDnsRecordAdd(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/dns/zones/50")
	rr := postForm(h, "/dns/zones/50/records", url.Values{
		"_token": {token}, "type": {"A"}, "name": {"mail"}, "ttl": {"0"}, "value": {"203.0.113.20"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dns/zones/50" {
		t.Fatalf("add record = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 5 {
		t.Fatalf("zone 50 records = %d, want 5", n)
	}
	if !containsAction(sa.called(), "dns.record_create") {
		t.Fatalf("agent actions %v missing dns.record_create", sa.called())
	}
	p := agentParams(t, sa, "dns.zone_apply")
	recs, _ := p["records"].([]map[string]any)
	found := false
	for _, r := range recs {
		if r["name"] == "mail" && r["type"] == "A" && r["value"] == "203.0.113.20" {
			found = true
		}
	}
	if !found {
		t.Fatalf("zone_apply records missing mail A: %v", recs)
	}
}

func TestDnsRecordAddValidation(t *testing.T) {
	h, cookie, _, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/dns/zones/50")
	rr := postForm(h, "/dns/zones/50/records", url.Values{
		"_token": {token}, "type": {"A"}, "name": {"mail"}, "value": {"999"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("invalid add = %d, want a 200 re-render", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Enter an IPv4 address such as 192.0.2.10.") {
		t.Fatalf("re-render missing the value error:\n%s", body)
	}
	if !strings.Contains(body, `data-wcp-dns-reopen="/dns/zones/50/records"`) {
		t.Fatalf("re-render missing the modal reopen:\n%s", body)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 4 {
		t.Fatalf("zone 50 records = %d after a refused add, want 4", n)
	}
}

func TestDnsRecordCnameConflict(t *testing.T) {
	h, cookie, _, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/dns/zones/50")
	// www already has a CNAME in zone 50.
	rr := postForm(h, "/dns/zones/50/records", url.Values{
		"_token": {token}, "type": {"CNAME"}, "name": {"www"}, "value": {"@"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("conflicting add = %d, want a 200 re-render", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "www already has a CNAME record") {
		t.Fatalf("re-render missing the conflict error:\n%s", rr.Body.String())
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 4 {
		t.Fatalf("zone 50 records = %d after a refused add, want 4", n)
	}
}

func TestDnsRecordUpdateAndDelete(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	aID := zone50RecordID(t, db, "@", "A")
	cnameID := zone50RecordID(t, db, "www", "CNAME")
	// Update the private A to a public address.
	token, cookie := pageToken(t, h, cookie, "/dns/zones/50")
	rr := postForm(h, "/dns/zones/50/records/"+idStr(aID), url.Values{
		"_token": {token}, "type": {"A"}, "name": {"@"}, "ttl": {"300"}, "value": {"203.0.113.10"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update record = %d", rr.Code)
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM dns_records WHERE id = ?`, aID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "203.0.113.10" {
		t.Fatalf("updated value = %q", value)
	}
	if !containsAction(sa.called(), "dns.record_update") {
		t.Fatalf("agent actions %v missing dns.record_update", sa.called())
	}
	// Delete the CNAME.
	token, cookie = pageToken(t, h, cookie, "/dns/zones/50")
	rr = postForm(h, "/dns/zones/50/records/"+idStr(cnameID)+"/delete", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete record = %d", rr.Code)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 3 {
		t.Fatalf("zone 50 records = %d after the delete, want 3", n)
	}
	if !containsAction(sa.called(), "dns.record_delete") {
		t.Fatalf("agent actions %v missing dns.record_delete", sa.called())
	}
	// A record of another zone is refused.
	token, cookie = pageToken(t, h, cookie, "/dns/zones/50")
	rr = postForm(h, "/dns/zones/50/records/9999/delete", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dns/zones/50" {
		t.Fatalf("delete of a foreign record = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	body := get(t, h, "/dns/zones/50", cookie).Body.String()
	if !strings.Contains(body, "The record no longer exists.") {
		t.Fatalf("missing the foreign-record error:\n%s", body)
	}
}

func TestDnsBulk(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	ns1 := zone50RecordID(t, db, "@", "NS")
	// Turn the two NS records off (the zone page still warns <2 NS… the
	// records exist, just disabled).
	token, cookie := pageToken(t, h, cookie, "/dns/zones/50")
	rr := postForm(h, "/dns/zones/50/bulk", url.Values{
		"_token": {token}, "ids[]": {idStr(ns1)}, "op": {"disable"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("bulk disable = %d", rr.Code)
	}
	body := get(t, h, "/dns/zones/50", cookie).Body.String()
	if !strings.Contains(body, "1 record(s) turned off; the zone was reloaded.") {
		t.Fatalf("missing the bulk flash:\n%s", body)
	}
	var disabled int
	if err := db.QueryRow(`SELECT disabled FROM dns_records WHERE id = ?`, ns1).Scan(&disabled); err != nil {
		t.Fatal(err)
	}
	if disabled != 1 {
		t.Fatalf("record %d disabled = %d", ns1, disabled)
	}
	// Delete one record through the bulk op.
	token, cookie = pageToken(t, h, cookie, "/dns/zones/50")
	rr = postForm(h, "/dns/zones/50/bulk", url.Values{
		"_token": {token}, "ids[]": {idStr(ns1)}, "op": {"delete"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("bulk delete = %d", rr.Code)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 3 {
		t.Fatalf("zone 50 records = %d after the bulk delete, want 3", n)
	}
	// Nothing selected: a user-facing error.
	token, cookie = pageToken(t, h, cookie, "/dns/zones/50")
	rr = postForm(h, "/dns/zones/50/bulk", url.Values{"_token": {token}, "op": {"delete"}}, cookie)
	body = get(t, h, "/dns/zones/50", cookie).Body.String()
	if !strings.Contains(body, "Select records first.") {
		t.Fatalf("missing the empty-selection error:\n%s", body)
	}
	if !containsAction(sa.called(), "dns.records_delete") {
		t.Fatalf("agent actions %v missing dns.records_delete", sa.called())
	}
}

func TestDnsSettingsAndSync(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/dns/zones/50")
	rr := postForm(h, "/dns/zones/50/settings", url.Values{
		"_token": {token}, "ttl": {"7200"}, "secondaries": {"198.51.100.1, 198.51.100.2"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("settings = %d", rr.Code)
	}
	var ttl int
	var secs string
	if err := db.QueryRow(`SELECT ttl, secondaries FROM dns_zones WHERE id = 50`).Scan(&ttl, &secs); err != nil {
		t.Fatal(err)
	}
	if ttl != 7200 || secs != "198.51.100.1,198.51.100.2" {
		t.Fatalf("zone 50 ttl/secondaries = %d / %q", ttl, secs)
	}
	if !containsAction(sa.called(), "dns.zone_settings") {
		t.Fatalf("agent actions %v missing dns.zone_settings", sa.called())
	}
	// A bad secondary re-renders with the field message.
	token, cookie = pageToken(t, h, cookie, "/dns/zones/50")
	rr = postForm(h, "/dns/zones/50/settings", url.Values{
		"_token": {token}, "ttl": {"7200"}, "secondaries": {"not-an-ip"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("invalid settings = %d, want a 200 re-render", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Enter IP addresses separated by commas.") {
		t.Fatalf("re-render missing the secondaries error:\n%s", rr.Body.String())
	}
	// Sync loads the zone again.
	token, cookie = pageToken(t, h, cookie, "/dns/zones/50")
	applies := len(filterActions(sa.called(), "dns.zone_apply"))
	rr = postForm(h, "/dns/zones/50/sync", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("sync = %d", rr.Code)
	}
	if n := len(filterActions(sa.called(), "dns.zone_apply")); n != applies+1 {
		t.Fatalf("dns.zone_apply calls = %d, want %d", n, applies+1)
	}
	body := get(t, h, "/dns/zones/50", cookie).Body.String()
	if !strings.Contains(body, "The zone was loaded again.") {
		t.Fatalf("missing the sync flash:\n%s", body)
	}
}

func TestDnsExport(t *testing.T) {
	h, cookie, _, _ := dnsMailEnv(t, "bob")
	rr := get(t, h, "/dns/zones/50/export", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("export = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("export content type = %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="bob.example.zone"` {
		t.Fatalf("export content disposition = %q", cd)
	}
	if !strings.Contains(rr.Body.String(), "$ORIGIN bob.example") {
		t.Fatalf("export body missing the zone file:\n%s", rr.Body.String())
	}
}

func TestDnsImport(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/dns/zones/50")
	rr := postForm(h, "/dns/zones/50/import", url.Values{
		"_token": {token}, "zonefile": {"ftp 3600 IN A 198.51.100.7"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dns/zones/50" {
		t.Fatalf("import = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 5 {
		t.Fatalf("zone 50 records = %d after the import, want 5 (ftp added, NS @ kept)", n)
	}
	var value string
	if err := db.QueryRow(`SELECT value FROM dns_records WHERE zone_id = 50 AND name = 'ftp'`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "198.51.100.7" {
		t.Fatalf("imported ftp = %q", value)
	}
	body := get(t, h, "/dns/zones/50", cookie).Body.String()
	if !strings.Contains(body, "1 record(s) imported, 1 already in the zone (name servers at @ are kept as they are); 1 line(s) skipped (listed below).") {
		t.Fatalf("missing the import flash:\n%s", body)
	}
	if !strings.Contains(body, "Lines not imported (1)") || !strings.Contains(body, "HINFO bob.example") {
		t.Fatalf("missing the skipped lines:\n%s", body)
	}
	if !containsAction(sa.called(), "dns.zone_parse") {
		t.Fatalf("agent actions %v missing dns.zone_parse", sa.called())
	}
	// An empty zone file re-renders with the field message.
	token, cookie = pageToken(t, h, cookie, "/dns/zones/50")
	rr = postForm(h, "/dns/zones/50/import", url.Values{"_token": {token}, "zonefile": {"   "}}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("empty import = %d, want a 200 re-render", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Paste or upload a zone file of up to 1 MB.") {
		t.Fatalf("re-render missing the zonefile error:\n%s", rr.Body.String())
	}
}

func TestDnsZoneDelete(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	// A wrong confirm re-renders with the field message.
	token, cookie := pageToken(t, h, cookie, "/dns/zones/50")
	rr := postForm(h, "/dns/zones/50/delete", url.Values{"_token": {token}, "confirm": {"nope"}}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("wrong confirm = %d, want a 200 re-render", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Type the zone name to confirm.") {
		t.Fatalf("re-render missing the confirm error:\n%s", rr.Body.String())
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_zones`); n != 2 {
		t.Fatalf("dns_zones = %d after a refused delete, want 2", n)
	}
	// The right confirm deletes; the agent is told first.
	token, cookie = pageToken(t, h, cookie, "/dns/zones/50")
	rr = postForm(h, "/dns/zones/50/delete", url.Values{"_token": {token}, "confirm": {"bob.example"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dns" {
		t.Fatalf("delete zone = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_zones`); n != 1 {
		t.Fatalf("dns_zones = %d after the delete, want 1", n)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_records WHERE zone_id = 50`); n != 0 {
		t.Fatalf("zone 50 records = %d after the delete, want 0", n)
	}
	if !containsAction(sa.called(), "dns.zone_delete") {
		t.Fatalf("agent actions %v missing dns.zone_delete", sa.called())
	}
	body := get(t, h, "/dns", cookie).Body.String()
	if !strings.Contains(body, "Zone bob.example deleted.") {
		t.Fatalf("missing the delete flash:\n%s", body)
	}
}

func TestDnsZoneDeleteAgentRefusedAnd404(t *testing.T) {
	h, cookie, sa, db := dnsMailEnv(t, "bob")
	// The agent refuses: the zone stays.
	sa.failActions = map[string]string{"dns.zone_delete": "the zone is in use"}
	token, cookie := pageToken(t, h, cookie, "/dns/zones/50")
	rr := postForm(h, "/dns/zones/50/delete", url.Values{"_token": {token}, "confirm": {"bob.example"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dns/zones/50" {
		t.Fatalf("refused delete = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	body := get(t, h, "/dns/zones/50", cookie).Body.String()
	if !strings.Contains(body, "the zone is in use") {
		t.Fatalf("missing the agent error:\n%s", body)
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_zones WHERE id = 50`); n != 1 {
		t.Fatalf("zone 50 must survive a refused delete, got %d", n)
	}
	// A zone the agent does not serve (404) is deleted anyway.
	sa.failStatus = map[string]int{"dns.zone_delete": 404}
	token, cookie = pageToken(t, h, cookie, "/dns/zones/50")
	rr = postForm(h, "/dns/zones/50/delete", url.Values{"_token": {token}, "confirm": {"bob.example"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/dns" {
		t.Fatalf("404 delete = %d loc %q", rr.Code, rr.Header().Get("Location"))
	}
	if n := rowCount(t, db, `SELECT COUNT(*) FROM dns_zones WHERE id = 50`); n != 0 {
		t.Fatalf("zone 50 must be deleted on a 404 agent reply, got %d", n)
	}
}

// idStr is the test-side int-to-string.
func idStr(n int64) string { return strconv.FormatInt(n, 10) }

// filterActions is the called actions of one kind.
func filterActions(acts []string, a string) []string {
	var out []string
	for _, x := range acts {
		if x == a {
			out = append(out, x)
		}
	}
	return out
}
