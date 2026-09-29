package hulk

import (
	"strings"
	"testing"
	"time"
)

func TestParseJournal(t *testing.T) {
	log := strings.Join([]string{
		"1790000000.1 srv sshd[11]: Failed password for root from 203.0.113.5 port 5000 ssh2",
		"1790000001.1 srv sshd-session[12]: Failed password for invalid user oracle from 2001:db8::7 port 5001 ssh2",
		"1790000002.1 srv sshd[13]: Accepted publickey for root from 198.51.100.4 port 5002 ssh2: ED25519 SHA256:x",
		"1790000003.1 srv dovecot[14]: imap-login: Disconnected (auth failed, 1 attempts in 2 secs): user=<bob@shop.test>, method=PLAIN, rip=203.0.113.6, lip=10.0.0.1, TLS, session=<a>",
		"1790000004.1 srv dovecot[15]: pop3-login: Login aborted: Connection closed (auth failed, 1 attempts in 2 secs) (auth_failed): user=<amy@shop.test>, method=PLAIN, rip=203.0.113.7, lip=10.0.0.1",
		"1790000005.1 srv dovecot[16]: imap-login: Disconnected (auth failed, 1 attempts in 2 secs): user=<web@shop.test>, method=PLAIN, rip=127.0.0.1, lip=127.0.0.1",
		"1790000006.1 srv postfix/submission/smtpd[17]: warning: unknown[203.0.113.8]: SASL LOGIN authentication failed: UGFzc3dvcmQ6",
		"1790000007.1 srv pure-ftpd[18]: (?@203.0.113.9) [WARNING] Authentication failed for user [shop]",
		"1790000008.1 srv dovecot[19]: imap-login: Login: user=<ok@shop.test>, method=PLAIN, rip=203.0.113.10",
		"1780000000.1 srv sshd[20]: Failed password for root from 203.0.113.11 port 5000 ssh2",
	}, "\n")
	evs := parseJournal([]byte(log), 1789999999)
	want := []string{
		"system sshd root 203.0.113.5 false",
		"system sshd oracle 2001:db8::7 false",
		"system sshd root 198.51.100.4 true",
		"mail dovecot (imap) bob@shop.test 203.0.113.6 false",
		"mail dovecot (pop3) amy@shop.test 203.0.113.7 false",
		"mail postfix (SMTP AUTH)  203.0.113.8 false",
		"ftp pure-ftpd shop 203.0.113.9 false",
	}
	if len(evs) != len(want) {
		t.Fatalf("%d events: %+v", len(evs), evs)
	}
	for i, e := range evs {
		got := strings.Join([]string{e.Service, e.AuthService, e.User, e.IP, map[bool]string{true: "true", false: "false"}[e.OK]}, " ")
		if got != want[i] || e.ID == "" {
			t.Errorf("%d: %q, want %q", i, got, want[i])
		}
	}
	if evs[0].ID == evs[1].ID {
		t.Fatal("ids repeat")
	}
}

func TestParseWebmail(t *testing.T) {
	log := "[29-Sep-2026 17:39:56 +0000]: <abc> IMAP Error: Login failed for bob@shop.test against localhost from 203.0.113.5. AUTHENTICATE PLAIN: Authentication failed. in /x.php on line 1\n" +
		"[29-Sep-2026 17:39:57 +0000]: <abc> Something else\n"
	evs := parseWebmail([]byte(log), 0)
	if len(evs) != 1 || evs[0].User != "bob@shop.test" || evs[0].IP != "203.0.113.5" || evs[0].AuthService != "webmaild" {
		t.Fatalf("%+v", evs)
	}
}

func TestRulesAndValidation(t *testing.T) {
	now := time.Now().Unix()
	s := State{Enabled: true,
		Blocks:    []Block{{IP: "203.0.113.5", Until: now + 600}, {IP: "2001:db8::1", Until: now + 86400, All: true}},
		Countries: []Range{{From: "198.51.100.0", To: "198.51.100.255"}},
		Whitelist: []string{"192.0.2.0/24"},
	}
	if err := s.validate(now); err != nil {
		t.Fatal(err)
	}
	r := rules(s, []int{2222}, now)
	for _, want := range []string{"203.0.113.5 timeout 600s", "2001:db8::1 timeout 86400s", "198.51.100.0-198.51.100.255", "192.0.2.0/24", "tcp dport { 21, 110, 143, 465, 587, 993, 995, 2222, 4190 }", "priority -5"} {
		if !strings.Contains(r, want) {
			t.Errorf("missing %q in\n%s", want, r)
		}
	}
	for _, bad := range []State{
		{Blocks: []Block{{IP: "x; flush ruleset", Until: now + 10}}},
		{Blocks: []Block{{IP: "203.0.113.5", Until: now + 400*86400}}},
		{Countries: []Range{{From: "10.0.0.9", To: "10.0.0.1"}}},
		{Countries: []Range{{From: "10.0.0.1", To: "::1"}}},
		{Whitelist: []string{"}"}},
	} {
		if bad.validate(now) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
