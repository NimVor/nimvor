package mailmgr

import (
	"strings"
	"testing"
)

func set(ipv6 bool, listens ...string) listenSet {
	ls := listenSet{v4: map[string][]string{}, v6: map[string][]string{}, wild4: map[string]bool{}, wild6: map[string]bool{}, ipv6: ipv6}
	for _, l := range listens {
		ls.add(l)
	}
	return ls
}

// A server whose websites listen on its own addresses (as NimVor creates them): a block with a plain "listen
// 443" could not be started by a reload ("bind() to 0.0.0.0:443 failed (98: Address already in use)"), so the
// webmail and mail names blocks use the same addresses, and the check connects to one of them.
func TestListenOnTheAddressesTheOtherSitesHold(t *testing.T) {
	ls := set(true,
		"161.97.165.17:443 ssl http2", "[2a02:c207:2370:8029::1]:443 ssl http2", "161.97.165.17:80", "[2a02:c207:2370:8029::1]:80",
		"80 default_server", "[::]:80 default_server", "8443 ssl", "[::]:8443 ssl")
	got := ls.lines("443", "ssl http2")
	if got != "    listen 161.97.165.17:443 ssl http2;\n    listen [2a02:c207:2370:8029::1]:443 ssl http2;\n" {
		t.Fatalf("443:\n%s", got)
	}
	// Port 80 has wildcard listeners already: a wildcard block can start there.
	if got := ls.lines("80", ""); got != "    listen 80;\n    listen [::]:80;\n" {
		t.Fatalf("80:\n%s", got)
	}
	if ls.dial() != "161.97.165.17:443" {
		t.Fatalf("dial = %s", ls.dial())
	}
}

func TestListenFallsBackToEveryAddress(t *testing.T) {
	// Nothing else listens: every address, IPv6 only where the server has it.
	if got := set(false).lines("443", "ssl"); got != "    listen 443 ssl;\n" {
		t.Fatalf("none: %q", got)
	}
	if got := set(true).lines("443", "ssl"); got != "    listen 443 ssl;\n    listen [::]:443 ssl;\n" {
		t.Fatalf("none, IPv6: %q", got)
	}
	// A wildcard listener on the port is shared.
	ls := set(false, "443 ssl", "161.97.165.17:443 ssl")
	if got := ls.lines("443", "ssl"); strings.Contains(got, "161.97") || ls.dial() != "127.0.0.1:443" {
		t.Fatalf("wildcard: %q %s", got, ls.dial())
	}
	// IPv4 specific only: no IPv6 line unless the server has it.
	if got := set(false, "203.0.113.5:443 ssl").lines("443", "ssl"); got != "    listen 203.0.113.5:443 ssl;\n" {
		t.Fatalf("v4 only: %q", got)
	}
}

func TestRenderedSitesUseThoseAddresses(t *testing.T) {
	h, _ := mailWithDomain(t)
	e := env{h.Env}
	ls := set(true, "161.97.165.17:443 ssl http2", "[2a02:c207:2370:8029::1]:443 ssl http2", "80 default_server", "[::]:80")
	vh := e.renderWebmailVhost(vhostParams{Hostname: "ns1.example.net", Cert: "/c", Key: "/k", Listen: ls, IPv6: true})
	for _, want := range []string{"    listen 161.97.165.17:443 ssl http2;\n", "    listen [2a02:c207:2370:8029::1]:443 ssl http2;\n", "    listen 80;\n"} {
		if !strings.Contains(vh, want) {
			t.Errorf("webmail site lacks %q:\n%s", want, vh)
		}
	}
	if strings.Contains(vh, "listen 443") || strings.Contains(vh, "listen [::]:443") {
		t.Errorf("a wildcard 443 listener cannot be started by a reload where the addresses are held:\n%s", vh)
	}
	idle := e.renderNamesIdle("mail.abc.com", "/etc/letsencrypt/live/mail.abc.com", false, ls)
	if !strings.Contains(idle, "listen 161.97.165.17:443 ssl http2;") || strings.Contains(idle, "listen 443") {
		t.Errorf("names block:\n%s", idle)
	}
}
