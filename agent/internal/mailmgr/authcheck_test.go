package mailmgr

import (
	"context"
	"net"
	"net/netip"
	"testing"
)

type authDNS struct {
	txt map[string][]string
	ip  map[string][]string
	mx  map[string][]string
	ptr map[string][]string
}

func dnsNF(n string) error { return &net.DNSError{Name: n, IsNotFound: true} }

func (d authDNS) LookupTXT(_ context.Context, n string) ([]string, error) {
	if v, ok := d.txt[n]; ok {
		return v, nil
	}
	return nil, dnsNF(n)
}
func (d authDNS) LookupNetIP(_ context.Context, network, n string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, s := range d.ip[n] {
		a := netip.MustParseAddr(s)
		if network == "ip4" && !a.Is4() {
			continue
		}
		out = append(out, a)
	}
	if out == nil {
		return nil, dnsNF(n)
	}
	return out, nil
}
func (d authDNS) LookupMX(_ context.Context, n string) ([]*net.MX, error) {
	var out []*net.MX
	for _, h := range d.mx[n] {
		out = append(out, &net.MX{Host: h + ".", Pref: 10})
	}
	if out == nil {
		return nil, dnsNF(n)
	}
	return out, nil
}
func (d authDNS) LookupAddr(_ context.Context, a string) ([]string, error) {
	if v, ok := d.ptr[a]; ok {
		return v, nil
	}
	return nil, dnsNF(a)
}

func statuses(cs []AuthCheck) map[string]string {
	m := map[string]string{}
	for _, c := range cs {
		m[c.ID] = c.Status
		if c.Detail == "" {
			panic("check without explanation: " + c.ID)
		}
	}
	return m
}

func TestCheckAuth(t *testing.T) {
	good := authDNS{
		txt: map[string][]string{
			"shop.test":                 {"v=spf1 a mx ~all"},
			"mail._domainkey.shop.test": {"v=DKIM1; k=rsa; p=MIIBIjAN BgkqhkiG9w0"},
			"_dmarc.shop.test":          {"v=DMARC1; p=quarantine; rua=mailto:postmaster@shop.test"},
		},
		ip:  map[string][]string{"mx.host.test": {"203.0.113.5"}, "shop.test": {"198.51.100.1"}},
		mx:  map[string][]string{"shop.test": {"mx.host.test"}},
		ptr: map[string][]string{"203.0.113.5": {"mx.host.test."}},
	}
	in := AuthCheckInput{Domain: "shop.test", Hostname: "mx.host.test", IPs: []string{"203.0.113.5"}, Selector: "mail", DKIMTXT: "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0"}
	got := statuses(CheckAuth(context.Background(), good, in))
	want := map[string]string{"host": "pass", "mx": "pass", "spf": "pass", "dkim": "pass", "dmarc": "pass", "rdns": "pass"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("good %s: %s, want %s", k, got[k], v)
		}
	}

	bad := authDNS{
		txt: map[string][]string{
			"shop.test":                 {"v=spf1 -all"},
			"mail._domainkey.shop.test": {"v=DKIM1; p=OTHERKEY"},
			"_dmarc.shop.test":          {"v=DMARC1; p=none"},
		},
		ip:  map[string][]string{"mx.host.test": {"192.0.2.99"}, "elsewhere.test": {"192.0.2.50"}, "ptr.provider.test": {"192.0.2.1"}},
		mx:  map[string][]string{"shop.test": {"elsewhere.test"}},
		ptr: map[string][]string{"203.0.113.5": {"ptr.provider.test."}},
	}
	got = statuses(CheckAuth(context.Background(), bad, in))
	want = map[string]string{"host": "fail", "mx": "fail", "spf": "fail", "dkim": "fail", "dmarc": "warning", "rdns": "fail"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("bad %s: %s, want %s", k, got[k], v)
		}
	}
	in.Routing = "remote"
	if statuses(CheckAuth(context.Background(), bad, in))["mx"] != "info" {
		t.Error("remote routing: MX elsewhere is expected")
	}

	got = statuses(CheckAuth(context.Background(), authDNS{}, AuthCheckInput{Domain: "none.test", Hostname: "mx.none.test", IPs: []string{"203.0.113.5"}, Selector: "mail"}))
	for _, k := range []string{"host", "mx", "spf", "dkim", "dmarc", "rdns"} {
		if got[k] != "missing" {
			t.Errorf("empty DNS %s: %s", k, got[k])
		}
	}
	twice := authDNS{txt: map[string][]string{"x.test": {"v=spf1 a", "v=spf1 mx"}, "_dmarc.x.test": {"v=DMARC1; p=reject", "v=DMARC1; p=none"}}}
	got = statuses(CheckAuth(context.Background(), twice, AuthCheckInput{Domain: "x.test", IPs: []string{"10.0.0.5"}}))
	if got["spf"] != "fail" || got["dmarc"] != "fail" || got["rdns"] != "info" {
		t.Errorf("duplicates / private address: %v", got)
	}
	// Confirmed PTR naming another host: a warning.
	other := good
	other.ptr = map[string][]string{"203.0.113.5": {"vps-1.provider.test."}}
	other.ip = map[string][]string{"mx.host.test": {"203.0.113.5"}, "vps-1.provider.test": {"203.0.113.5"}}
	if s := statuses(CheckAuth(context.Background(), other, in))["rdns"]; s != "warning" {
		t.Errorf("PTR of another name: %s", s)
	}
}
