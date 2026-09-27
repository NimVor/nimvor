package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

const ufwActive = "Status: active\nLogging: on (low)\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n"

func setup(t *testing.T, added string) (*testhost.Host, *Service) {
	t.Helper()
	h := testhost.New(t)
	h.Runner.Outputs = map[string]string{
		"sshd -T":                     "port 22\npermitrootlogin without-password\npasswordauthentication yes\nmaxauthtries 6\nx11forwarding yes\n",
		"ufw status verbose":          ufwActive,
		"ufw show added":              "Added user rules (see 'ufw status' for running firewall):\n" + added,
		"ufw --force enable":          "Firewall is active",
		"fail2ban-client status":      "Status\n|- Number of jail:\t1\n`- Jail list:\tsshd\n",
		"fail2ban-client status sshd": "Status for the jail: sshd\n|- Filter\n|  |- Currently failed:\t2\n|  |- Total failed:\t40\n`- Actions\n   |- Currently banned:\t1\n   |- Total banned:\t7\n   `- Banned IP list:\t203.0.113.9\n",
	}
	return h, NewService(h.Env)
}

// waitRollback waits (through the guard's lock) until no change is pending.
func waitRollback(t *testing.T, s *Service) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if s.guard.Current() == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("pending change was not rolled back")
}

func TestParseAddedAndCanonical(t *testing.T) {
	rules := ParseAdded("Added user rules:\nufw allow 22/tcp\nufw allow from 10.0.0.0/8 to any port 3306 proto tcp comment 'db access'\nufw limit OpenSSH\n", 22)
	if len(rules) != 3 || !rules[0].SSH || rules[1].SSH || !rules[2].SSH {
		t.Fatalf("rules = %+v", rules)
	}
	if !reflect.DeepEqual(rules[1].Args, []string{"allow", "from", "10.0.0.0/8", "to", "any", "port", "3306", "proto", "tcp", "comment", "db access"}) {
		t.Fatalf("args = %q", rules[1].Args)
	}
	args, _ := RuleInput{Action: "allow", Port: "3306", Proto: "tcp", From: "10.0.0.0/8"}.Args()
	if !sameRule(rules[1], args) {
		t.Error("reordered rule not recognised as the same")
	}
	args22, _ := RuleInput{Action: "allow", Port: "22", Proto: "tcp"}.Args()
	if !sameRule(rules[0], args22) {
		t.Error("22/tcp not recognised")
	}
	if sameRule(rules[0], args) {
		t.Error("different rules compared equal")
	}
}

func TestRuleValidation(t *testing.T) {
	for _, in := range []RuleInput{
		{Action: "allow", Port: "0"},
		{Action: "allow", Port: "70000"},
		{Action: "allow", Port: "8000:7000", Proto: "tcp"},
		{Action: "allow", Port: "8000:8100"},
		{Action: "open", Port: "80"},
		{Action: "allow", Port: "80", From: "evil; rm -rf"},
		{Action: "allow", Port: "80", Comment: "x'; y"},
		{Action: "allow", Port: "80", Proto: "icmp"},
	} {
		if _, err := in.Args(); err == nil {
			t.Errorf("accepted %+v", in)
		}
	}
}

func TestAddRuleRollsBackUnlessConfirmed(t *testing.T) {
	old := ConfirmWindow
	ConfirmWindow = 100 * time.Millisecond
	defer func() { ConfirmWindow = old }()
	h, s := setup(t, "ufw allow 22/tcp\n")
	ctx := context.Background()
	p, err := s.AddRule(ctx, RuleInput{Action: "allow", Port: "8080", Proto: "tcp", Comment: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("ufw allow proto tcp from any to any port 8080 comment whacp: app") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	if !h.Exists("var/lib/whacp-agent/security-pending.json") {
		t.Fatal("pending change not persisted")
	}
	if _, err := s.AddRule(ctx, RuleInput{Action: "allow", Port: "9090"}); err == nil {
		t.Error("a second change was accepted while one is pending")
	}
	waitRollback(t, s)
	if !h.Runner.Called("ufw --force delete allow proto tcp from any to any port 8080") {
		t.Fatalf("not rolled back: %v", h.Runner.Calls)
	}
	if h.Exists("var/lib/whacp-agent/security-pending.json") || s.guard.Current() != nil {
		t.Error("pending state not cleared")
	}
	if err := s.guard.Confirm(p.ID); err == nil {
		t.Error("confirming a rolled-back change should fail")
	}

	// Confirmed changes stay.
	ConfirmWindow = time.Hour
	p, err = s.AddRule(ctx, RuleInput{Action: "allow", Port: "443", Proto: "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.guard.Confirm(p.ID); err != nil {
		t.Fatal(err)
	}
	if h.Runner.Called("ufw --force delete allow proto tcp from any to any port 443") {
		t.Error("confirmed change was rolled back")
	}
}

func TestPendingRollbackSurvivesRestart(t *testing.T) {
	old := ConfirmWindow
	ConfirmWindow = time.Hour
	defer func() { ConfirmWindow = old }()
	h, s := setup(t, "ufw allow 22/tcp\n")
	if p, err := s.SetEnabled(context.Background(), true); err != nil || p != nil {
		t.Fatal("ufw is already active; nothing should change")
	}
	h.Runner.Outputs["ufw status verbose"] = "Status: inactive\n"
	if _, err := s.SetEnabled(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	// Simulate an agent restart after the deadline.
	f := filepath.Join(h.Base, "var/lib/whacp-agent/security-pending.json")
	b, _ := os.ReadFile(f)
	b = []byte(strings.Replace(string(b), `"deadline":"2026`, `"deadline":"2000`, 1))
	os.WriteFile(f, b, 0o600)
	g := NewGuard(h.Env)
	for i := 0; i < 100 && g.Current() != nil; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !h.Runner.Called("ufw disable") {
		t.Fatalf("expired change not rolled back at startup: %v", h.Runner.Calls)
	}
}

func TestLockoutProtection(t *testing.T) {
	h, s := setup(t, "ufw allow 80/tcp\n")
	h.Runner.Outputs["ufw status verbose"] = "Status: inactive\n"
	if _, err := s.SetEnabled(context.Background(), true); err == nil || !strings.Contains(err.Error(), "SSH") {
		t.Fatalf("enabling without an SSH rule: %v", err)
	}
	h.Runner.Outputs["ufw status verbose"] = ufwActive
	h.Runner.Outputs["ufw show added"] = "ufw allow 22/tcp\n"
	if _, err := s.DeleteRule(context.Background(), "allow 22/tcp"); err == nil {
		t.Fatal("deleting the only SSH rule was allowed")
	}
	// Password logins cannot be turned off without an administrator key.
	_, err := s.ConfigureSSH(context.Background(), SSHSettings{Port: 22, PermitRootLogin: "no", MaxAuthTries: 4})
	if err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("expected lockout refusal, got %v", err)
	}
	if h.Exists("etc/ssh/sshd_config.d/00-whacp.conf") {
		t.Error("drop-in written despite refusal")
	}
}

func adminWithKey(t *testing.T, h *testhost.Host) {
	f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("ops:x:1001:1001::/home/ops:/bin/bash\n")
	f.Close()
	os.WriteFile(filepath.Join(h.Base, "etc/group"), []byte("sudo:x:27:ops\n"), 0o644)
	os.MkdirAll(filepath.Join(h.Base, "home/ops/.ssh"), 0o700)
	os.WriteFile(filepath.Join(h.Base, "home/ops/.ssh/authorized_keys"), []byte("ssh-ed25519 AAAA ops\n"), 0o600)
}

func TestConfigureSSH(t *testing.T) {
	old := ConfirmWindow
	ConfirmWindow = 100 * time.Millisecond
	defer func() { ConfirmWindow = old }()
	h, s := setup(t, "ufw allow 22/tcp\n")
	adminWithKey(t, h)
	want := SSHSettings{Port: 22, PermitRootLogin: "prohibit-password", PasswordAuthentication: false, MaxAuthTries: 4}

	// Another file overrides our setting: refused and rolled back.
	if _, err := s.ConfigureSSH(context.Background(), want); err == nil || !strings.Contains(err.Error(), "would not take effect") {
		t.Fatalf("expected override detection, got %v", err)
	}
	if h.Exists("etc/ssh/sshd_config.d/00-whacp.conf") {
		t.Fatal("drop-in left behind")
	}

	h.Runner.Outputs["sshd -T"] = "port 22\npermitrootlogin without-password\npasswordauthentication no\nmaxauthtries 4\nx11forwarding no\n"
	p, err := s.ConfigureSSH(context.Background(), want)
	if err != nil {
		t.Fatal(err)
	}
	body := h.Read("etc/ssh/sshd_config.d/00-whacp.conf")
	if !strings.Contains(body, "PasswordAuthentication no") || !strings.Contains(body, "KbdInteractiveAuthentication no") {
		t.Fatalf("drop-in = %s", body)
	}
	if !h.Runner.Called("sshd -t") || !h.Runner.Called("systemctl reload -- sshd.service") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	if p.Kind != "ssh" {
		t.Fatal(p)
	}
	waitRollback(t, s)
	if h.Exists("etc/ssh/sshd_config.d/00-whacp.conf") {
		t.Error("unconfirmed SSH change not rolled back")
	}

	// sshd -t failure: nothing stays.
	h.Fail["sshd -t"] = "Bad configuration option"
	if _, err := s.ConfigureSSH(context.Background(), want); err == nil {
		t.Fatal("invalid config accepted")
	}
	if h.Exists("etc/ssh/sshd_config.d/00-whacp.conf") {
		t.Error("invalid drop-in left behind")
	}
}

func TestFail2ban(t *testing.T) {
	h, s := setup(t, "")
	st := Fail2ban(context.Background(), h.Env)
	if !st.Running || len(st.Jails) != 1 || st.Jails[0].Banned != 1 || st.Jails[0].BannedIPs[0] != "203.0.113.9" {
		t.Fatalf("status = %+v", st)
	}
	in := Fail2banSettings{Jails: []string{"sshd", "recidive"}, BanMinutes: 60, FindMinutes: 10, MaxRetry: 5, IgnoreAddress: []string{"198.51.100.0/24"}}
	if err := s.ConfigureFail2ban(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	body := h.Read("etc/fail2ban/jail.d/whacp.local")
	if !strings.Contains(body, "ignoreip = 127.0.0.1/8 ::1 198.51.100.0/24") || !strings.Contains(body, "[recidive]") {
		t.Fatalf("jail = %s", body)
	}
	if !h.Runner.Called("fail2ban-client --test") || !h.Runner.Called("fail2ban-client reload") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	h.Fail["fail2ban-client --test"] = "bad"
	in.MaxRetry = 3
	if err := s.ConfigureFail2ban(context.Background(), in); err == nil {
		t.Fatal("failed test accepted")
	}
	if !strings.Contains(h.Read("etc/fail2ban/jail.d/whacp.local"), "maxretry = 5") {
		t.Error("previous jail file not restored")
	}
	for _, bad := range []Fail2banSettings{{Jails: []string{"postfix"}, BanMinutes: 1, FindMinutes: 1, MaxRetry: 1}, {BanMinutes: 0, FindMinutes: 1, MaxRetry: 1}, {BanMinutes: 1, FindMinutes: 1, MaxRetry: 1, IgnoreAddress: []string{"x y"}}} {
		if _, err := bad.Render(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := s.Unban(context.Background(), "sshd", "not-an-ip"); err == nil {
		t.Error("invalid IP accepted")
	}
}

func TestAudit(t *testing.T) {
	h, _ := setup(t, "ufw allow 22/tcp\n")
	h.Runner.Outputs["apt-get -s -o Debug::NoLocking=1 upgrade"] = "Inst openssl [3.0.2-0ubuntu1.14] (3.0.2-0ubuntu1.15 Ubuntu:22.04/jammy-updates, Ubuntu:22.04/jammy-security [amd64])\n"
	checks := Audit(context.Background(), h.Env)
	got := map[string]string{}
	for _, c := range checks {
		got[c.ID] = c.Status
	}
	if got["firewall"] != "pass" || got["ssh_password"] != "warn" || got["updates"] != "fail" || got["fail2ban"] != "pass" {
		t.Fatalf("audit = %+v", got)
	}
	// Unreadable data is never reported as passing.
	h.Runner.Outputs["sshd -T"] = ""
	h.Runner.Outputs["fail2ban-client status"] = "Status\n`- Jail list:\t\n"
	got = map[string]string{}
	for _, c := range Audit(context.Background(), h.Env) {
		got[c.ID] = c.Status
	}
	if got["ssh"] != "info" || got["ssh_password"] != "" || got["fail2ban"] != "warn" {
		t.Fatalf("audit with missing data = %+v", got)
	}
}

func TestActionsRegistered(t *testing.T) {
	h, _ := setup(t, "")
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	for _, n := range []string{"security.audit", "firewall.status", "ssh.status", "fail2ban.status"} {
		if a, err := r.Lookup(n); err != nil || a.Mutating {
			t.Errorf("%s should be a read-only action", n)
		}
	}
	for _, n := range []string{"firewall.rule_add", "ssh.configure", "fail2ban.configure", "security.confirm"} {
		if a, err := r.Lookup(n); err != nil || !a.Mutating {
			t.Errorf("%s should be mutating", n)
		}
	}
}

// fail2ban stopped (reported on a real server: "Could not find server"
// from a reload): saving starts it and waits until it answers; when it
// does not start, its own log is shown and the previous settings return.
func TestFail2banStartsWhenStopped(t *testing.T) {
	h, s := setup(t, "")
	fail2banWait = time.Millisecond
	t.Cleanup(func() { fail2banWait = 500 * time.Millisecond })
	state := "inactive"
	next := h.Respond
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "systemctl" && len(args) == 3 && args[0] == "is-active" && args[2] == "fail2ban.service" {
			return state, nil, true
		}
		if id == "systemctl" && len(args) == 3 && args[0] == "restart" && args[2] == "fail2ban.service" {
			state = "active"
		}
		if next == nil {
			return "", nil, false
		}
		return next(id, args)
	}
	in := Fail2banSettings{Jails: []string{"sshd"}, BanMinutes: 60, FindMinutes: 10, MaxRetry: 5}
	if err := s.ConfigureFail2ban(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("systemctl restart -- fail2ban.service") || !h.Runner.Called("fail2ban-client ping") || h.Runner.Called("fail2ban-client reload") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}

	// Unchanged settings, fail2ban stopped again: saving starts it.
	state = "inactive"
	h.Runner.Calls = nil
	if err := s.ConfigureFail2ban(context.Background(), in); err != nil || !h.Runner.Called("systemctl restart -- fail2ban.service") {
		t.Fatalf("unchanged settings did not start fail2ban: %v %v", err, h.Runner.Calls)
	}

	// It does not start: the reason from its log; the previous settings back.
	state = "inactive"
	h.Fail["fail2ban-client ping"] = ""
	h.Runner.Outputs["journalctl --no-pager -o cat -n 15 -u fail2ban.service"] = "ERROR   Failed during configuration: Have not found any log file for sshd jail"
	in.MaxRetry = 3
	err := s.ConfigureFail2ban(context.Background(), in)
	var ae *actions.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Details, "Have not found any log file for sshd jail") {
		t.Fatalf("got %#v", err)
	}
	if !strings.Contains(h.Read("etc/fail2ban/jail.d/whacp.local"), "maxretry = 5") {
		t.Error("previous jail file not restored")
	}
}
