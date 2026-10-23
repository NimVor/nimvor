package http

// This suite drives the website's IP card (batch 55): the pool the advanced
// page offers (administrators only), POST /sites/{id}/ip (the agent
// re-application, the DNS records following, the rollback when that fails),
// and the validation errors (not pooled, reserved, dedicated to another).

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// ipEnv seeds an administrator (server.manage), a customer (bob) who owns
// website 10 (bob.example), a second customer (alice) with website 20, the
// IP pool (one shared free, one shared reserved, one dedicated used by
// alice's site) and bob's DNS zone with an A record on 10.10.0.99.
func ipEnv(t *testing.T, loginAs string) (http.Handler, *http.Cookie, *sitesAgent, *sql.DB) {
	t.Helper()
	h, cookie, sa, db := importEnv(t, loginAs)
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(9,'websites.manage','wm','web'),(10,'server.manage','sm','server')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,9),(1,10),(2,9)`)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,ipv4,created_at,updated_at)
		VALUES (20,1,2,'alice.example','u_alice','/home/whacp/alice.example','/home/whacp/alice.example/public_html','php','8.3.1','active',NULL,'','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,20,'alice.example',1)`)
	exec(`INSERT INTO ip_addresses (server_id,address,family,kind,reserved,reason,created_at) VALUES
		(1,'10.10.0.99',4,'shared',0,'',''),
		(1,'10.10.0.15',4,'shared',0,'',''),
		(1,'192.168.1.20',4,'shared',1,'Staging',''),
		(1,'172.16.0.9',4,'dedicated',0,'','')`)
	exec(`UPDATE websites SET ipv4 = '172.16.0.9' WHERE id = 20`)
	// bob's zone: an A record on the website's old address.
	exec(`INSERT INTO dns_zones (id,server_id,name,website_id,owner_id,ttl,primary_ns,hostmaster,secondaries,serial,applied_at,created_at,updated_at)
		VALUES (1,1,'bob.example',10,2,3600,'ns1.bob.example.','admin@bob.example.','',0,'','','')`)
	exec(`INSERT INTO dns_records (zone_id,name,type,ttl,value,priority,weight,port)
		VALUES (1,'@','A',0,'10.10.0.99',0,0,0)`)
	exec(`UPDATE websites SET ipv4 = '10.10.0.99' WHERE id = 10`)
	return h, cookie, sa, db
}

func TestAdvancedIPCardAdminOnly(t *testing.T) {
	h, cookie, _, _ := ipEnv(t, "admin")
	rr := get(t, h, "/sites/10/advanced", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("advanced = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"IP addresses", "Every address of the server", "10.10.0.15",
		"Save addresses", "10.10.0.99", // the website's current address is preselected
	} {
		if !strings.Contains(body, want) {
			t.Errorf("IP card missing %q", want)
		}
	}
	if strings.Contains(body, "192.168.1.20") {
		t.Error("a reserved address must not be offered")
	}
	if strings.Contains(body, "172.16.0.9") {
		t.Error("an address dedicated to another website must not be offered")
	}

	// The customer does not see the card.
	h2, c2, _, _ := ipEnv(t, "bob")
	body = get(t, h2, "/sites/10/advanced", c2).Body.String()
	if strings.Contains(body, "IP addresses") || strings.Contains(body, "Save addresses") {
		t.Error("the customer must not see the IP card")
	}
}

func TestIPAssignSavedAndDNSFollowed(t *testing.T) {
	h, cookie, sa, db := ipEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/sites/10/advanced")
	rr := postForm(h, "/sites/10/ip", url.Values{
		"_token": {token}, "ipv4": {"10.10.0.15"}, "ipv6": {""}, "dns": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("assign = %d", rr.Code)
	}
	if rr.Header().Get("Location") != "/sites/10/advanced#ip" {
		t.Errorf("assign back = %q", rr.Header().Get("Location"))
	}
	var ip4 string
	if err := db.QueryRow(`SELECT ipv4 FROM websites WHERE id = 10`).Scan(&ip4); err != nil || ip4 != "10.10.0.15" {
		t.Fatalf("ipv4 after assign = %q (%v)", ip4, err)
	}
	// The A record followed, and the zone was pushed.
	var value string
	if err := db.QueryRow(`SELECT value FROM dns_records WHERE zone_id = 1 AND type = 'A'`).Scan(&value); err != nil || value != "10.10.0.15" {
		t.Fatalf("A record = %q (%v)", value, err)
	}
	var serial int
	if err := db.QueryRow(`SELECT serial FROM dns_zones WHERE id = 1`).Scan(&serial); err != nil || serial != 7 {
		t.Fatalf("zone serial = %d (%v), the push must store the agent's serial", serial, err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'website.ip'`) != 1 {
		t.Error("website.ip audit missing")
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'ip.assign'`) != 1 {
		t.Error("ip.assign audit missing")
	}
	// The success flash counts the DNS record.
	rr = get(t, h, "/sites/10/advanced", cookie)
	if !strings.Contains(rr.Body.String(), "Addresses saved and the website reloaded; 1 DNS record(s) updated") {
		t.Errorf("success flash missing:\\n%s", rr.Body.String())
	}
	_ = sa
}

func TestIPAssignValidationErrors(t *testing.T) {
	h, cookie, _, db := ipEnv(t, "admin")

	post := func(ipv4 string, c *http.Cookie) (*httptest.ResponseRecorder, *http.Cookie) {
		token, c := pageToken(t, h, c, "/sites/10/advanced")
		return postForm(h, "/sites/10/ip", url.Values{
			"_token": {token}, "ipv4": {ipv4}, "ipv6": {""},
		}, c), c
	}

	// Not in the pool.
	rr, cookie = post("8.8.8.8", cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("not-pooled assign = %d", rr.Code)
	}
	rr = get(t, h, "/sites/10/advanced", cookie)
	if !strings.Contains(rr.Body.String(), "8.8.8.8 is not in the IP pool.") {
		t.Error("not-pooled error flash missing")
	}

	// Reserved.
	rr, cookie = post("192.168.1.20", cookie)
	rr = get(t, h, "/sites/10/advanced", cookie)
	if !strings.Contains(rr.Body.String(), "192.168.1.20 is reserved (Staging).") {
		t.Error("reserved error flash missing")
	}

	// Dedicated to another website.
	rr, cookie = post("172.16.0.9", cookie)
	rr = get(t, h, "/sites/10/advanced", cookie)
	if !strings.Contains(rr.Body.String(), "172.16.0.9 is dedicated to alice.example.") {
		t.Error("dedicated error flash missing")
	}

	// A wrong family is rejected.
	rr, cookie = post("2001:db8::1", cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("wrong-family assign = %d", rr.Code)
	}
	rr = get(t, h, "/sites/10/advanced", cookie)
	if !strings.Contains(rr.Body.String(), "Choose an IPv4 address from the pool.") {
		t.Error("wrong-family error flash missing")
	}

	var ip4 string
	if err := db.QueryRow(`SELECT ipv4 FROM websites WHERE id = 10`).Scan(&ip4); err != nil || ip4 != "10.10.0.99" {
		t.Fatalf("ipv4 must be unchanged after rejected forms: %q (%v)", ip4, err)
	}
}

func TestIPAssignNoChangeDoesNotApply(t *testing.T) {
	h, cookie, sa, _ := ipEnv(t, "admin")
	token, cookie := pageToken(t, h, cookie, "/sites/10/advanced")
	rr := postForm(h, "/sites/10/ip", url.Values{
		"_token": {token}, "ipv4": {"10.10.0.99"}, "ipv6": {""},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("no-op assign = %d", rr.Code)
	}
	for _, a := range sa.called() {
		if a == "sites.apply" {
			t.Error("an unchanged assignment must not re-apply the website")
		}
	}
}

func TestIPAssignDNSFailureRollsBack(t *testing.T) {
	h, cookie, sa, db := ipEnv(t, "admin")
	sa.failActions["dns.zone_apply"] = "the agent refused the zone"
	token, cookie := pageToken(t, h, cookie, "/sites/10/advanced")
	rr := postForm(h, "/sites/10/ip", url.Values{
		"_token": {token}, "ipv4": {"10.10.0.15"}, "ipv6": {""}, "dns": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("failing assign = %d", rr.Code)
	}
	// The website is back on its previous address.
	var ip4 string
	if err := db.QueryRow(`SELECT ipv4 FROM websites WHERE id = 10`).Scan(&ip4); err != nil || ip4 != "10.10.0.99" {
		t.Fatalf("ipv4 after rollback = %q (%v)", ip4, err)
	}
	if rowCount(t, db, `SELECT COUNT(*) FROM audit_logs WHERE action = 'website.ip_rollback'`) != 1 {
		t.Error("website.ip_rollback audit missing")
	}
	rr = get(t, h, "/sites/10/advanced", cookie)
	if !strings.Contains(rr.Body.String(), "The DNS records could not be updated, so the website was moved back to its previous address.") {
		t.Error("rollback flash missing")
	}
}

func TestIPAssignForbiddenForCustomer(t *testing.T) {
	h, cookie, _, db := ipEnv(t, "bob")
	token, cookie := pageToken(t, h, cookie, "/sites/10/advanced")
	rr := postForm(h, "/sites/10/ip", url.Values{
		"_token": {token}, "ipv4": {"10.10.0.15"}, "ipv6": {""},
	}, cookie)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("customer assign = %d, want 403", rr.Code)
	}
	var ip4 string
	if err := db.QueryRow(`SELECT ipv4 FROM websites WHERE id = 10`).Scan(&ip4); err != nil || ip4 != "10.10.0.99" {
		t.Error("the customer must not change addresses")
	}
}
