package mailmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSMTPSTurnsPort465OnAndOff(t *testing.T) {
	h, c := mailWithDomain(t)
	master := filepath.Join(h.Base, "etc/postfix/master.cf")
	os.MkdirAll(filepath.Dir(master), 0o755)
	os.WriteFile(master, []byte("smtp inet n - y - - smtpd\n"), 0o644)
	if st, _ := c("mail.status", nil); st["smtps"] != false {
		t.Fatalf("status before: %v", st["smtps"])
	}
	res, err := c("mail.smtps_set", map[string]any{"enabled": true})
	if err != nil || res["enabled"] != true {
		t.Fatalf("turn on: %v %v", res, err)
	}
	for _, want := range []string{"postconf -M smtps/inet=smtps inet n - y - - smtpd", "postconf -P smtps/inet/smtpd_tls_wrappermode=yes", "postconf -P smtps/inet/smtpd_sasl_auth_enable=yes", "postconf -P smtps/inet/syslog_name=postfix/smtps", "postfix check"} {
		if !h.Runner.Called(want) {
			t.Errorf("%q missing: %v", want, h.Runner.Calls)
		}
	}
	// Turning it off when it is not there changes nothing.
	if res, err := c("mail.smtps_set", map[string]any{"enabled": false}); err != nil || res["changed"] != false {
		t.Fatalf("off when off: %v %v", res, err)
	}
	// With the service in master.cf it is removed.
	os.WriteFile(master, []byte("smtp inet n - y - - smtpd\nsmtps inet n - y - - smtpd\n"), 0o644)
	if st, _ := c("mail.status", nil); st["smtps"] != true {
		t.Fatalf("status with the service: %v", st["smtps"])
	}
	if res, err := c("mail.smtps_set", map[string]any{"enabled": false}); err != nil || res["changed"] != true || !h.Runner.Called("postconf -MX smtps/inet") {
		t.Fatalf("turn off: %v %v %v", res, err, h.Runner.Calls)
	}
}

// A change Postfix does not accept leaves master.cf as it was.
func TestSMTPSRestoresMasterCfWhenPostfixRefuses(t *testing.T) {
	h, c := mailWithDomain(t)
	master := filepath.Join(h.Base, "etc/postfix/master.cf")
	os.MkdirAll(filepath.Dir(master), 0o755)
	const orig = "smtp inet n - y - - smtpd\n"
	os.WriteFile(master, []byte(orig), 0o644)
	h.Fail["postfix check"] = "master.cf: bad"
	if _, err := c("mail.smtps_set", map[string]any{"enabled": true}); code(err) != "failed" {
		t.Fatalf("a refused change was accepted: %v", err)
	}
	if b, _ := os.ReadFile(master); strings.TrimSpace(string(b)) != strings.TrimSpace(orig) {
		t.Fatalf("master.cf not restored:\n%s", b)
	}
}

func TestSMTPSNeedsEmailSetUp(t *testing.T) {
	h, r := setup(t)
	_ = h
	if _, err := call(t, r, "mail.smtps_set", map[string]any{"enabled": true}); code(err) != "conflict" {
		t.Fatalf("before set-up: %v", err)
	}
}
