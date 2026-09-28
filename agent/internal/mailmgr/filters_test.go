package mailmgr

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sampleFilters() ([]Filter, *Responder) {
	return []Filter{
		{Name: "Invoices", Enabled: true, Rules: []Rule{{Part: "from", Match: "ends", Value: "@billing.example.com"}, {Part: "subject", Match: "contains", Value: `Invoice "Q3" \ *urgent?`}}, Actions: []FilterAction{{Type: "fileinto", Value: "Invoices"}, {Type: "mark_read"}, {Type: "stop"}}},
		{Name: "Newsletters", Enabled: true, Any: true, Rules: []Rule{{Part: "list-id", Match: "contains", Value: "news"}, {Part: "body", Match: "not-contains", Value: "unsubscribe"}}, Actions: []FilterAction{{Type: "redirect_copy", Value: "Archive@Example.org"}, {Type: "discard"}}},
		{Name: "Off", Enabled: false, Rules: []Rule{{Part: "to", Match: "is", Value: "x@y.z"}}, Actions: []FilterAction{{Type: "discard"}}},
	}, &Responder{Enabled: true, Subject: "Out of office", Body: "I am away.\nBack on \"Monday\".", Days: 3, Start: "2026-10-01", End: "2026-10-15"}
}

func TestValidateFilters(t *testing.T) {
	f, r := sampleFilters()
	if err := ValidateFilters(f, r); err != nil {
		t.Fatal(err)
	}
	if f[1].Actions[0].Value != "archive@example.org" {
		t.Errorf("address not normalised: %q", f[1].Actions[0].Value)
	}
	bad := []Filter{
		{Name: "", Rules: []Rule{{Part: "from", Match: "is", Value: "a"}}, Actions: []FilterAction{{Type: "stop"}}},
		{Name: "x", Rules: []Rule{{Part: "x-evil", Match: "is", Value: "a"}}, Actions: []FilterAction{{Type: "stop"}}},
		{Name: "x", Rules: []Rule{{Part: "from", Match: "regex", Value: "a"}}, Actions: []FilterAction{{Type: "stop"}}},
		{Name: "x", Rules: []Rule{{Part: "from", Match: "is", Value: "a\nb"}}, Actions: []FilterAction{{Type: "stop"}}},
		{Name: "x", Rules: []Rule{{Part: "from", Match: "is", Value: "a"}}, Actions: []FilterAction{{Type: "fileinto", Value: "../etc"}}},
		{Name: "x", Rules: []Rule{{Part: "from", Match: "is", Value: "a"}}, Actions: []FilterAction{{Type: "redirect", Value: "not an address"}}},
		{Name: "x", Rules: []Rule{{Part: "from", Match: "is", Value: "a"}}, Actions: []FilterAction{{Type: "execute", Value: "sh"}}},
		{Name: "x", Rules: []Rule{{Part: "from", Match: "is", Value: "a"}}},
	}
	for i, b := range bad {
		if err := ValidateFilters([]Filter{b}, nil); err == nil {
			t.Errorf("bad filter %d accepted", i)
		}
	}
	for _, r := range []*Responder{{Enabled: true, Body: ""}, {Enabled: true, Body: "x", Start: "2026-13-01"}, {Enabled: true, Body: "x", Start: "2026-10-10", End: "2026-10-01"}, {Enabled: true, Body: "x", Subject: "a\nBcc: x"}} {
		if err := ValidateFilters(nil, r); err == nil {
			t.Errorf("bad responder accepted: %+v", r)
		}
	}
}

func TestRenderSieveCompiles(t *testing.T) {
	f, r := sampleFilters()
	if err := ValidateFilters(f, r); err != nil {
		t.Fatal(err)
	}
	s := RenderSieve("me@example.com", f, r)
	for _, want := range []string{
		`require ["fileinto", "mailbox", "copy", "imap4flags", "body", "vacation", "date", "relational"];`,
		`address :all :matches "from" "*@billing.example.com"`,
		`header :contains "subject" "Invoice \"Q3\" \\ *urgent?"`,
		`fileinto :create "Invoices";`, `addflag "\\Seen";`, `redirect :copy "archive@example.org";`,
		`not body :text :contains`, `if anyof (`,
		`vacation :days 3 :subject "Out of office" :from "me@example.com" "I am away.` + "\n" + `Back on \"Monday\".";`,
		`currentdate :value "ge" "date" "2026-10-01"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %s\n%s", want, s)
		}
	}
	if strings.Contains(s, "x@y.z") {
		t.Error("a disabled filter was rendered")
	}
	if RenderSieve("me@example.com", nil, &Responder{}) != "" {
		t.Error("nothing to do should render nothing")
	}
	// Compile with Pigeonhole when it is installed.
	if _, err := exec.LookPath("sievec"); err != nil {
		t.Skip("sievec not installed")
	}
	p := filepath.Join(t.TempDir(), "test.sieve")
	os.WriteFile(p, []byte(s), 0o644)
	if out, err := exec.Command("sievec", p).CombinedOutput(); err != nil {
		t.Fatalf("sievec: %v\n%s\n%s", err, out, s)
	}
}

// The rendered script does what the filters say (Pigeonhole's sieve-test
// runs it against messages and prints the resulting actions).
func TestSieveBehaviour(t *testing.T) {
	if _, err := exec.LookPath("sieve-test"); err != nil {
		t.Skip("sieve-test not installed")
	}
	f, r := sampleFilters()
	r.Start, r.End = "", ""
	if err := ValidateFilters(f, r); err != nil {
		t.Fatal(err)
	}
	// sieve-test drops privileges to mail_uid (nobody), which must reach the files.
	dir, _ := os.MkdirTemp("/tmp", "whacp-sieve-")
	os.Chmod(dir, 0o777)
	t.Cleanup(func() { os.RemoveAll(dir) })
	script := filepath.Join(dir, "s.sieve")
	os.WriteFile(script, []byte(RenderSieve("me@example.com", f, r)), 0o644)
	run := func(msg string) string {
		p := filepath.Join(dir, "m.eml")
		os.WriteFile(p, []byte(strings.ReplaceAll(msg, "\n", "\r\n")), 0o644)
		out, err := exec.Command("sieve-test", "-o", "mail_uid=nobody", "-o", "mail_gid=nogroup", "-o", "mail_location=maildir:"+filepath.Join(dir, "maildir"), "-a", "me@example.com", script, p).CombinedOutput()
		if err != nil {
			t.Fatalf("sieve-test: %v\n%s", err, out)
		}
		return string(out)
	}
	inv := run("From: Billing <pay@billing.example.com>\nTo: me@example.com\nSubject: Invoice \"Q3\" \\ *urgent? now\nMessage-ID: <1@x>\n\nPlease pay.\n")
	if !strings.Contains(inv, "store message in folder: Invoices") || !strings.Contains(inv, `add IMAP flags: \seen`) {
		t.Errorf("invoice:\n%s", inv)
	}
	if strings.Contains(inv, "send vacation") {
		t.Error("stop did not stop")
	}
	news := run("From: News <n@lists.example.net>\nTo: me@example.com\nList-Id: <news.example.net>\nSubject: Weekly\nMessage-ID: <2@x>\n\nHello\n")
	if !strings.Contains(news, "redirect message to: <archive@example.org>") || !strings.Contains(news, "discard") {
		t.Errorf("newsletter:\n%s", news)
	}
	plain := run("From: Friend <f@friend.example>\nTo: me@example.com\nSubject: Hi\nMessage-ID: <3@x>\n\nhello unsubscribe\n")
	if !strings.Contains(plain, "send vacation message") || !strings.Contains(plain, "store message in folder: INBOX") {
		t.Errorf("plain:\n%s", plain)
	}
}

// Real Dovecot accepts the rendered settings.
func TestDovecotTuningParses(t *testing.T) {
	if _, err := exec.LookPath("doveconf"); err != nil {
		t.Skip("doveconf not installed")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "dovecot.conf")
	body := "protocols = imap\nbase_dir = " + dir + "\n" + renderDovecotTuning(map[string]string{"mail_max_userip_connections": "20", "auth_failure_delay": "4s", "ssl_min_protocol": "TLSv1.3", "login_greeting": "Mail ready"})
	os.WriteFile(conf, []byte(body), 0o644)
	out, err := exec.Command("doveconf", "-c", conf, "-n").CombinedOutput()
	if err != nil {
		t.Fatalf("doveconf: %v\n%s", err, out)
	}
	for _, want := range []string{"auth_failure_delay = 4 secs", "login_greeting = Mail ready", "ssl_min_protocol = TLSv1.3", "mail_max_userip_connections = 20"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("doveconf lacks %q:\n%s", want, out)
		}
	}
}
