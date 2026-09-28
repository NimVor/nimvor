package dnsmgr

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const exported = `;; Exported zone
$ORIGIN example.com.
$TTL 3600
@	3600	IN	SOA	ns1.host.net. admin.example.com. (
		2024010101 ; serial
		7200 3600 1209600 300 )
@	IN	NS	ns1.host.net.
	IN	NS	ns2.host.net.
@	300	IN	A	192.0.2.10
www	IN	CNAME	@
mail.example.com.	IN	A	192.0.2.20
@	IN	MX	10 mail
@	IN	TXT	"v=spf1 mx " "~all"
_dmarc	IN	TXT	"v=DMARC1; p=none"
_sip._tcp	IN	SRV	10 60 5060 sip.example.com.
@	IN	CAA	0 issue "letsencrypt.org"
other.org.	IN	A	192.0.2.1
@	IN	HINFO	"x" "y"
$INCLUDE other.zone
@	IN	A	999.1.1.1
`

func TestParseZoneFile(t *testing.T) {
	r := ParseZoneFile(exported, "example.com")
	got := map[string]Record{}
	for _, x := range r.Records {
		got[x.Name+" "+x.Type+" "+x.Value] = x
	}
	for _, want := range []string{
		"@ NS ns1.host.net.", "@ NS ns2.host.net.", "@ A 192.0.2.10", "www CNAME example.com.", "mail A 192.0.2.20",
		"@ MX mail.example.com.", "@ TXT v=spf1 mx ~all", "_dmarc TXT v=DMARC1; p=none", "_sip._tcp SRV sip.example.com.", "@ CAA issue letsencrypt.org",
	} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %q in %+v", want, r.Records)
		}
	}
	if len(r.Records) != 10 {
		t.Errorf("%d records: %+v", len(r.Records), r.Records)
	}
	if a := got["@ A 192.0.2.10"]; a.TTL != 300 {
		t.Errorf("explicit TTL lost: %+v", a)
	}
	if ns := got["@ NS ns1.host.net."]; ns.TTL != 0 {
		t.Errorf("default TTL should be 0 (zone default): %+v", ns)
	}
	if mx := got["@ MX mail.example.com."]; mx.Priority != 10 {
		t.Errorf("mx: %+v", mx)
	}
	if srv := got["_sip._tcp SRV sip.example.com."]; srv.Priority != 10 || srv.Weight != 60 || srv.Port != 5060 {
		t.Errorf("srv: %+v", srv)
	}
	reasons := ""
	for _, s := range r.Skipped {
		reasons += s.Reason + "\n"
	}
	for _, want := range []string{"outside the zone", "HINFO records are not supported", "$INCLUDE is not supported", "IPv4"} {
		if !strings.Contains(reasons, want) {
			t.Errorf("skipped lacks %q:\n%s", want, reasons)
		}
	}
	// The result validates as a zone.
	z := Zone{Name: "example.com", SOA: SOA{PrimaryNS: "ns1.host.net.", Hostmaster: "admin@example.com"}, Records: r.Records}
	if err := z.Validate(); err != nil {
		t.Fatalf("imported zone invalid: %v", err)
	}
	if !strings.Contains(z.Render(1), "_sip._tcp\t\tIN\tSRV\t10 60 5060 sip.example.com.") {
		t.Errorf("render:\n%s", z.Render(1))
	}
	// An exported zone imports back as the same records.
	back := ParseZoneFile(z.Render(1), "example.com")
	key := func(rs []Record) []string {
		var out []string
		for _, r := range rs {
			out = append(out, fmt.Sprintf("%+v", r))
		}
		sort.Strings(out)
		return out
	}
	if len(back.Skipped) != 0 || !reflect.DeepEqual(key(back.Records), key(z.Records)) {
		t.Errorf("round trip:\nskipped %+v\ngot  %+v\nwant %+v", back.Skipped, back.Records, z.Records)
	}
}

func TestPTROnlyInReverseZones(t *testing.T) {
	z := Zone{Name: "example.com", SOA: SOA{PrimaryNS: "ns1.host.net.", Hostmaster: "a@b.c"}, Records: []Record{{Name: "@", Type: "NS", Value: "ns1.host.net."}, {Name: "10", Type: "PTR", Value: "host.example.com."}}}
	if err := z.Validate(); err == nil {
		t.Fatal("PTR accepted in a forward zone")
	}
	z.Name = "2.0.192.in-addr.arpa"
	z.Records[1].Value = "host.example.com."
	if err := z.Validate(); err != nil {
		t.Fatal(err)
	}
}
