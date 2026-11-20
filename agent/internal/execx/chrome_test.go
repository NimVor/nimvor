package execx

import "testing"

func TestChromeAllowedAcceptsOnlyTheScreenshotCommandLine(t *testing.T) {
	good := []string{
		"--headless=new", "--disable-gpu", "--no-sandbox", "--window-size=1280,800", "--virtual-time-budget=6000",
		"--user-data-dir=/tmp/nimvor-shot-0123456789abcdef/profile", "--screenshot=/tmp/nimvor-shot-0123456789abcdef/shot.png",
		"--host-resolver-rules=MAP shop.test 127.0.0.1", "https://shop.test/",
	}
	if !chromeAllowed(good) {
		t.Fatal("the screenshot command line was refused")
	}
	bad := map[string][]string{
		"no page":                  good[:len(good)-1],
		"another host":             append(append([]string{}, good[:len(good)-1]...), "https://evil.test/"),
		"two pages":                append(append([]string{}, good...), "https://shop.test/"),
		"a path elsewhere":         {"--screenshot=/etc/cron.d/x", "--host-resolver-rules=MAP a.test 127.0.0.1", "https://a.test/"},
		"a path out of the folder": {"--screenshot=/tmp/nimvor-shot-0123456789abcdef/../x", "--host-resolver-rules=MAP a.test 127.0.0.1", "https://a.test/"},
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
