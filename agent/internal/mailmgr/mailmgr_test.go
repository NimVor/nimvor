package mailmgr

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestAddressesAndRendering(t *testing.T) {
	for _, ok := range []string{"a@example.com", "First.Last+tag@Sub.Example.co.uk", "x_y-z@a.io"} {
		if _, _, err := SplitAddress(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"a", "@example.com", "a@b", "a..b@example.com", ".a@example.com", "a b@example.com", "a@example.com\nRCPT", "a:b@example.com", "a@-x.com"} {
		if _, _, err := SplitAddress(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	d := newData()
	d.Domains["example.com"] = Domain{Selector: "whacp202609"}
	d.Mailboxes["alice@example.com"] = Mailbox{Hash: "$6$x$y", QuotaMB: 500}
	d.Mailboxes["bob@example.com"] = Mailbox{Hash: "$6$a$b"}
	d.Aliases["info@example.com"] = []string{"alice@example.com", "ext@other.net"}
	d.Aliases["@example.com"] = []string{"bob@example.com"}
	f := d.render("/etc/whacp-mail", "/var/vmail")
	for name, want := range map[string]string{
		"domains":           "example.com OK\n",
		"mailboxes":         "alice@example.com example.com/alice/\n",
		"aliases":           "info@example.com alice@example.com,ext@other.net\n",
		"users":             "alice@example.com:{SHA512-CRYPT}$6$x$y::::/var/vmail/example.com/alice::userdb_quota_rule=*:storage=500M\n",
		"senders":           "info@example.com alice@example.com\n",
		"dkim/KeyTable":     "whacp202609._domainkey.example.com example.com:whacp202609:/etc/whacp-mail/dkim/example.com.private\n",
		"dkim/SigningTable": "*@example.com whacp202609._domainkey.example.com\n",
	} {
		if !strings.Contains(f[name], want) {
			t.Errorf("%s lacks %q:\n%s", name, want, f[name])
		}
	}
	if strings.Contains(f["senders"], "\n@example.com ") {
		t.Error("a catch-all must not become a sender address")
	}
	if !strings.Contains(f["users"], "bob@example.com:{SHA512-CRYPT}$6$a$b::::/var/vmail/example.com/bob::\n") {
		t.Errorf("no quota for bob: %s", f["users"])
	}
}

func TestDisableSystemAuth(t *testing.T) {
	in := "auth_mechanisms = plain\n#!include auth-deny.conf.ext\n!include auth-system.conf.ext\n#!include auth-sql.conf.ext\n"
	out, changed := disableSystemAuth(in)
	if !changed || strings.Contains(out, "\n!include auth-system") || !strings.Contains(out, "#!include auth-system.conf.ext") {
		t.Fatalf("out = %q", out)
	}
	if _, again := disableSystemAuth(out); again {
		t.Fatal("not idempotent")
	}
	p := dovecotParams{Dir: "/etc/whacp-mail", Vmail: "/var/vmail", VmailUID: 5000, Hostname: "mail.example.com", TLS: tls{"/c", "/k"}}
	conf := renderDovecot(p)
	for _, want := range []string{"listen = *\n", "first_valid_uid = 5000", "args = scheme=SHA512-CRYPT username_format=%Lu /etc/whacp-mail/users", "home=/var/vmail/%Ld/%Ln", "ssl = required", "ssl_cert = </c\n", "quota_grace = 10%%", "port = 0", "postmaster_address = postmaster@mail.example.com", "sieve_before = /etc/whacp-mail/sieve/spam.sieve", "mail_plugins = $mail_plugins sieve", "special_use = \\Junk"} {
		if !strings.Contains(conf, want) {
			t.Errorf("2.3 conf lacks %q", want)
		}
	}
	p.V24, p.IPv6 = true, true
	conf = renderDovecot(p)
	for _, want := range []string{"listen = *, ::\n", "mail_driver = maildir\n", "mail_inbox_path = ~/Maildir\n", "auth_allow_cleartext = no", "ssl_server_cert_file = /c\n", "passwd_file_path = /etc/whacp-mail/users", "default_password_scheme = SHA512-CRYPT", "auth_username_format = %{user | lower}", "quota \"User quota\" {", "sieve_script whacp-spam {", "type = before", "sieve = yes"} {
		if !strings.Contains(conf, want) {
			t.Errorf("2.4 conf lacks %q", want)
		}
	}
	if strings.Contains(conf, "mail_location") || strings.Contains(conf, "disable_plaintext_auth") || strings.Contains(conf, "%L") {
		t.Error("2.4 conf uses 2.3 settings")
	}
	if !dovecotV24("2.4.2 (0962ed2104)") || dovecotV24("2.3.21 (47349e2482)") {
		t.Error("version detection")
	}
	d := newData()
	d.Dovecot24 = true
	d.Domains["e.test"] = Domain{Selector: "s"}
	d.Mailboxes["a@e.test"] = Mailbox{Hash: "$6$x", QuotaMB: 100}
	if !strings.HasSuffix(strings.TrimSpace(d.render("/m", "/v")["users"]), "::userdb_quota_storage_size=100M") {
		t.Errorf("2.4 users = %s", d.render("/m", "/v")["users"])
	}
}

func setup(t *testing.T) (*testhost.Host, *actions.Registry) {
	h := testhost.New(t)
	for _, f := range []string{"usr/sbin/postfix", "usr/sbin/dovecot", "usr/sbin/opendkim", "usr/lib/dovecot/imap", "usr/lib/dovecot/lmtp", "usr/lib/dovecot/modules/lib90_sieve_plugin.so"} {
		os.MkdirAll(filepath.Join(h.Base, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(h.Base, f), nil, 0o755)
	}
	os.MkdirAll(filepath.Join(h.Base, "etc/dovecot/conf.d"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "etc/postfix"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/dovecot/conf.d/10-auth.conf"), []byte("!include auth-system.conf.ext\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/postfix/main.cf"), []byte("myhostname = old\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/postfix/master.cf"), []byte("smtp inet n - y - - smtpd\n"), 0o644)
	f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("vmail:x:5000:5000::/var/vmail:/usr/sbin/nologin\n")
	f.Close()
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "dovecot" && len(args) > 0 && args[0] == "--version" {
			return "2.3.21 (47349e2482)\n", nil, true
		}
		return "", nil, false
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	old := probe
	oldTLS := startTLS
	probe = func(context.Context, string, string, bool) error { return nil }
	startTLS = func(context.Context, string) error { return nil }
	t.Cleanup(func() { probe, startTLS = old, oldTLS })
	return h, r
}

func call(t *testing.T, r *actions.Registry, name string, params any) (map[string]any, error) {
	t.Helper()
	a, _ := r.Lookup(name)
	b, _ := json.Marshal(params)
	res, err := a.Handler(context.Background(), b)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m, nil
}

func code(err error) string {
	var ae *actions.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestMailboxesAndAliases(t *testing.T) {
	h, r := setup(t)
	if _, err := call(t, r, "mail.domain_add", map[string]any{"domain": "example.com"}); code(err) != "conflict" {
		t.Fatalf("domain before setup: %v", err)
	}
	if _, err := call(t, r, "mail.configure", map[string]any{"hostname": "mail.example.com"}); code(err) != "conflict" {
		t.Fatalf("setup without confirmation: %v", err)
	}
	h.Env.Cfg.SimulateCommands = true // no ports to probe on the test host
	if _, err := call(t, r, "mail.configure", map[string]any{"hostname": "mail.example.com", "confirm_edit_package_config": true}); err != nil {
		t.Fatal(err)
	}
	h.Env.Cfg.SimulateCommands = false
	if !strings.Contains(h.Read("etc/dovecot/conf.d/10-auth.conf"), "#!include auth-system.conf.ext") || !h.Exists("etc/dovecot/conf.d/99-whacp.conf") {
		t.Fatal("dovecot not configured")
	}
	joined := strings.Join(h.Runner.Calls, "\n")
	for _, want := range []string{"postconf -e virtual_mailbox_domains=texthash:/etc/whacp-mail/domains", "postconf -e mynetworks=127.0.0.0/8", "postconf -M submission/inet=submission inet n - y - - smtpd", "postconf -P submission/inet/smtpd_sasl_auth_enable=yes", "postconf -P submission/inet/smtpd_sender_restrictions=reject_sender_login_mismatch", "postfix check", "doveconf -n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q", want)
		}
	}
	res, err := call(t, r, "mail.domain_add", map[string]any{"domain": "Example.com"})
	if err != nil || !strings.HasPrefix(res["dkim_txt"].(string), "v=DKIM1; k=rsa; p=") {
		t.Fatalf("domain = %v %v", res, err)
	}
	if !strings.Contains(h.Read("etc/whacp-mail/dkim/example.com.private"), "RSA PRIVATE KEY") {
		t.Fatal("DKIM key not written")
	}
	if _, err := call(t, r, "mail.mailbox_save", map[string]any{"address": "a@other.com", "password": "long-enough-pw"}); code(err) != "conflict" {
		t.Fatalf("mailbox for an unhosted domain: %v", err)
	}
	if _, err := call(t, r, "mail.mailbox_save", map[string]any{"address": "alice@example.com"}); code(err) != "invalid_params" {
		t.Fatal("mailbox without password")
	}
	if _, err := call(t, r, "mail.mailbox_save", map[string]any{"address": "alice@example.com", "password": "Alice-long-pass", "quota_mb": 200}); err != nil {
		t.Fatal(err)
	}
	users := h.Read("etc/whacp-mail/users")
	if strings.Contains(users, "Alice-long-pass") || !strings.Contains(users, "alice@example.com:{SHA512-CRYPT}$6$rounds=100000$") {
		t.Fatalf("users = %s", users)
	}
	if strings.Contains(h.Read("etc/whacp-mail/mail.json"), "Alice-long-pass") {
		t.Fatal("clear-text password stored")
	}
	if _, err := call(t, r, "mail.alias_save", map[string]any{"source": "alice@example.com", "destinations": []string{"x@y.com"}}); code(err) != "conflict" {
		t.Fatal("alias over a mailbox")
	}
	for _, bad := range [][]string{{}, {"not an address"}, {"a@b.com\nbcc@evil.com"}, {"info@example.com"}} {
		if _, err := call(t, r, "mail.alias_save", map[string]any{"source": "info@example.com", "destinations": bad}); err == nil {
			t.Errorf("destinations %q accepted", bad)
		}
	}
	if _, err := call(t, r, "mail.alias_save", map[string]any{"source": "info@example.com", "destinations": []string{"alice@example.com", "Ext@Other.net"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Read("etc/whacp-mail/aliases"), "info@example.com alice@example.com,ext@other.net") {
		t.Fatal("alias not written")
	}
	// A failing check restores the files.
	before := h.Read("etc/whacp-mail/mailboxes")
	h.Fail["postfix check"] = "fatal: bad table"
	if _, err := call(t, r, "mail.mailbox_save", map[string]any{"address": "bob@example.com", "password": "Bob-long-passwd"}); err == nil {
		t.Fatal("expected failure")
	}
	if h.Read("etc/whacp-mail/mailboxes") != before {
		t.Fatal("mailboxes not restored")
	}
	delete(h.Fail, "postfix check")
	st, _ := call(t, r, "mail.status", nil)
	if len(st["mailboxes"].([]any)) != 1 || strings.Contains(h.Read("etc/whacp-mail/users"), "bob@") {
		t.Fatalf("status = %v", st)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "$6$") {
		t.Fatal("status exposes password hashes")
	}
	if _, err := call(t, r, "mail.domain_remove", map[string]any{"domain": "example.com", "confirm": "example.com"}); code(err) != "conflict" {
		t.Fatal("domain with mailboxes removed")
	}
}

func TestSpamSettings(t *testing.T) {
	for _, bad := range []Spam{{AddHeader: 0, Reject: 15}, {AddHeader: 10, Reject: 10}, {AddHeader: 6, Reject: 101}, {AddHeader: 51, Reject: 60}} {
		if bad.validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	f := rspamdFiles(Spam{AddHeader: 5.5, Reject: 15})
	for name, want := range map[string]string{
		"actions.conf":        "reject = 15;\nadd_header = 5.5;\n",
		"worker-proxy.inc":    "bind_socket = \"127.0.0.1:11332\";\nmilter = yes;",
		"milter_headers.conf": "remove_upstream_spam_flag = true;",
		"dkim_signing.conf":   "enabled = false;",
	} {
		if !strings.Contains(f[name], want) || !strings.HasPrefix(f[name], spamMarker) {
			t.Errorf("%s = %q", name, f[name])
		}
	}
	if smtpdMilters(true) != "inet:127.0.0.1:11332, inet:127.0.0.1:8891" || smtpdMilters(false) != "inet:127.0.0.1:8891" {
		t.Error("milters")
	}
}
