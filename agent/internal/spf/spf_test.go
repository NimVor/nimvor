package spf

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"testing"
)

type fakeDNS struct {
	txt map[string][]string
	ip  map[string][]string
	mx  map[string][]string
	n   int
}

func nf(name string) error { return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true} }

func (f *fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	f.n++
	if v, ok := f.txt[name]; ok {
		return v, nil
	}
	return nil, nf(name)
}

func (f *fakeDNS) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	f.n++
	var out []netip.Addr
	for _, s := range f.ip[host] {
		a := netip.MustParseAddr(s)
		if network == "ip4" && !a.Is4() {
			continue
		}
		out = append(out, a)
	}
	if out == nil {
		return nil, nf(host)
	}
	return out, nil
}

func (f *fakeDNS) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	f.n++
	var out []*net.MX
	for _, h := range f.mx[name] {
		out = append(out, &net.MX{Host: h + ".", Pref: 10})
	}
	if out == nil {
		return nil, nf(name)
	}
	return out, nil
}

func dns() *fakeDNS {
	return &fakeDNS{
		txt: map[string][]string{
			"example.com":        {"google-site-verification=x", "v=spf1 ip4:192.0.2.0/24 a mx include:_spf.provider.test ~all"},
			"_spf.provider.test": {"v=spf1 ip4:198.51.100.0/25 ip6:2001:db8::/32 -all"},
			"strict.test":        {"v=spf1 -all"},
			"redir.test":         {"v=spf1 redirect=example.com"},
			"two.test":           {"v=spf1 -all", "v=spf1 +all"},
			"macro.test":         {"v=spf1 exists:%{ir}.%{l1r+-}._spf.%{d} -all"},
			"loop.test":          {"v=spf1 include:loop.test -all"},
			"neutral.test":       {"v=spf1 ?all"},
			"broken.test":        {"v=spf1 include:missing.test -all"},
			"bad.test":           {"v=spf1 foo:bar -all"},
		},
		ip: map[string][]string{
			"example.com":                            {"203.0.113.10"},
			"mail.example.com":                       {"203.0.113.25", "2001:db8:1::25"},
			"4.3.2.1.bob.macro.test._spf.macro.test": nil,
			"1.2.0.192.bob._spf.macro.test":          {"127.0.0.2"},
		},
		mx: map[string][]string{"example.com": {"mail.example.com"}},
	}
}

func TestCheck(t *testing.T) {
	cases := []struct {
		ip, sender, helo string
		want             Result
	}{
		{"192.0.2.77", "a@example.com", "", Pass},
		{"203.0.113.10", "a@example.com", "", Pass},   // a
		{"203.0.113.25", "a@example.com", "", Pass},   // mx
		{"2001:db8:1::25", "a@example.com", "", Pass}, // mx, IPv6
		{"198.51.100.5", "a@EXAMPLE.com", "", Pass},   // include
		{"198.51.100.200", "a@example.com", "", SoftFail},
		{"192.0.2.1", "a@strict.test", "", Fail},
		{"192.0.2.1", "a@redir.test", "", Pass},
		{"192.0.2.1", "a@two.test", "", PermError},
		{"192.0.2.1", "a@nothing.test", "", None},
		{"192.0.2.1", "", "example.com", Pass}, // null sender: HELO identity
		{"192.0.2.1", "bob@macro.test", "", Pass},
		{"192.0.2.2", "bob@macro.test", "", Fail},
		{"192.0.2.1", "a@loop.test", "", PermError},
		{"192.0.2.1", "a@neutral.test", "", Neutral},
		{"192.0.2.1", "a@broken.test", "", PermError},
		{"192.0.2.1", "a@bad.test", "", PermError},
		{"::ffff:192.0.2.9", "a@example.com", "", Pass}, // mapped address
	}
	for _, c := range cases {
		got, _ := Check(context.Background(), dns(), netip.MustParseAddr(c.ip), c.sender, c.helo)
		if got != c.want {
			t.Errorf("%s %s: %s, want %s", c.ip, c.sender, got, c.want)
		}
	}
}

func TestLookupLimit(t *testing.T) {
	d := dns()
	rec := "v=spf1"
	for i := 0; i < 11; i++ {
		rec += " a:h" + string(rune('a'+i)) + ".test"
	}
	d.txt["many.test"] = []string{rec + " -all"}
	if got, _ := Check(context.Background(), d, netip.MustParseAddr("192.0.2.1"), "x@many.test", ""); got != PermError {
		t.Fatalf("11 lookups: %s", got)
	}
}

func TestRanges(t *testing.T) {
	got, err := Ranges(context.Background(), dns(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	var s []string
	for _, p := range got {
		s = append(s, p.String())
	}
	for _, want := range []string{"192.0.2.0/24", "203.0.113.10/32", "203.0.113.25/32", "2001:db8:1::25/128", "198.51.100.0/25", "2001:db8::/32"} {
		if !slices.Contains(s, want) {
			t.Errorf("missing %s in %v", want, s)
		}
	}
	if _, err := Ranges(context.Background(), dns(), "nothing.test"); err == nil {
		t.Fatal("no record accepted")
	}
}
