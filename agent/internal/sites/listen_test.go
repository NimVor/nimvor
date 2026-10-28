package sites

import (
	"strings"
	"testing"
)

func TestListenAddresses(t *testing.T) {
	s := Spec{Domain: "shop.test", User: "shop", Kind: "static", Enabled: true, IPv4: "203.0.113.11", IPv6: "2001:DB8:5::0010", HTTPS: true, CertFile: "/etc/letsencrypt/live/shop.test/fullchain.pem", KeyFile: "/etc/letsencrypt/live/shop.test/privkey.pem"}
	if err := s.Normalize("/home/nimvor", "/var/lib/nimvor-agent"); err != nil {
		t.Fatal(err)
	}
	if s.IPv6 != "2001:db8:5::10" {
		t.Fatalf("not canonical: %s", s.IPv6)
	}
	out := RenderNginx(s, Layout{IPv6: true})
	for _, want := range []string{"listen 203.0.113.11:80;", "listen [2001:db8:5::10]:80;", "listen 203.0.113.11:443 ssl http2;", "listen [2001:db8:5::10]:443 ssl http2;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "listen 80;") || strings.Contains(out, "[::]") {
		t.Errorf("wildcard listener left:\n%s", out)
	}
	// Only IPv4 set: IPv6 stays on every address.
	s.IPv6 = ""
	if out := RenderNginx(s, Layout{IPv6: true}); !strings.Contains(out, "listen [::]:80;") {
		t.Errorf("IPv6 wildcard missing:\n%s", out)
	}
	for _, bad := range []Spec{{IPv4: "2001:db8::1"}, {IPv4: "127.0.0.1"}, {IPv4: "1.2.3.4; include /etc/x"}, {IPv6: "203.0.113.1"}, {IPv6: "fe80::1"}, {IPv6: "fe80::1%eth0"}, {IPv4: "0.0.0.0"}} {
		b := Spec{Domain: "x.test", User: "x", Kind: "static", IPv4: bad.IPv4, IPv6: bad.IPv6}
		if err := b.Normalize("/home/nimvor", "/var/lib/nimvor-agent"); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
