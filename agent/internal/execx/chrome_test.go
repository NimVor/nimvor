package execx

import (
	"net"
	"testing"
)

func TestChromeAllowedAcceptsOnlyTheScreenshotCommandLine(t *testing.T) {
	good := []string{
		"--headless=new", "--disable-gpu", "--no-sandbox", "--window-size=1280,800", "--virtual-time-budget=6000",
		"--user-data-dir=/run/nimvor-run/shot-0123456789abcdef/profile", "--screenshot=/run/nimvor-run/shot-0123456789abcdef/shot.png",
		"--host-resolver-rules=MAP shop.test 127.0.0.1", "https://shop.test/",
	}
	if !chromeAllowed(good) {
		t.Fatal("the screenshot command line was refused")
	}
	// A website without a certificate is opened over plain HTTP.
	if plain := append(append([]string{}, good[:len(good)-1]...), "http://shop.test/"); !chromeAllowed(plain) {
		t.Fatal("the plain-HTTP command line was refused")
	}
	// Any other scheme is not a page of the website.
	if other := append(append([]string{}, good[:len(good)-1]...), "file://shop.test/"); chromeAllowed(other) {
		t.Fatal("a file: URL was accepted")
	}
	bad := map[string][]string{
		"no page":                  good[:len(good)-1],
		"another host":             append(append([]string{}, good[:len(good)-1]...), "https://evil.test/"),
		"two pages":                append(append([]string{}, good...), "https://shop.test/"),
		"a path elsewhere":         {"--screenshot=/etc/cron.d/x", "--host-resolver-rules=MAP a.test 127.0.0.1", "https://a.test/"},
		"a path out of the folder": {"--screenshot=/run/nimvor-run/shot-0123456789abcdef/../x", "--host-resolver-rules=MAP a.test 127.0.0.1", "https://a.test/"},
		"an unknown flag":          {"--remote-debugging-port=9222", "--host-resolver-rules=MAP a.test 127.0.0.1", "https://a.test/"},
		"a file URL":               {"--host-resolver-rules=MAP a.test 127.0.0.1", "file:///etc/passwd"},
		"another address mapped":   {"--host-resolver-rules=MAP a.test 10.0.0.5", "https://a.test/"},
		"a path in the URL":        {"--host-resolver-rules=MAP a.test 127.0.0.1", "https://a.test/admin"},
	}
	for name, args := range bad {
		if chromeAllowed(args) {
			t.Errorf("%s was accepted: %v", name, args)
		}
	}
}

func TestChromeOnlyRunsAsAnUnprivilegedAccount(t *testing.T) {
	args := []string{"--host-resolver-rules=MAP a.test 127.0.0.1", "https://a.test/"}
	if err := checkArgs("chrome", Opts{}, args); err == nil {
		t.Error("chrome ran as root")
	}
	if err := checkArgs("chrome", Opts{As: &Credential{UID: 1500, GID: 1500}}, args); err != nil {
		t.Errorf("chrome as an account: %v", err)
	}
}

func TestChromeMayOnlyBePointedAtThisServer(t *testing.T) {
	old := interfaceAddrs
	defer func() { interfaceAddrs = old }()
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{&net.IPNet{IP: net.ParseIP("192.0.2.7"), Mask: net.CIDRMask(24, 32)}}, nil
	}
	mk := func(ip string) []string {
		return []string{"--host-resolver-rules=MAP shop.test " + ip, "https://shop.test/"}
	}
	for ip, want := range map[string]bool{
		"127.0.0.1": true, "127.0.0.2": true, "192.0.2.7": true, // loopback and the server's own address
		"192.0.2.8": false, "10.0.0.5": false, "8.8.8.8": false, "999.1.1.1": false,
	} {
		if got := chromeAllowed(mk(ip)); got != want {
			t.Errorf("%s: allowed=%v, want %v", ip, got, want)
		}
	}
}
