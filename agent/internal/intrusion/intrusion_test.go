package intrusion

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func setup(t *testing.T) (*testhost.Host, *actions.Registry) {
	t.Helper()
	h := testhost.New(t)
	for _, d := range []string{"usr/bin", "usr/sbin", "bin", "etc/audit/rules.d", "var/log/audit", "var/lib/nimvor-agent"} {
		os.MkdirAll(filepath.Join(h.Base, d), 0o755)
	}
	w := func(p string, mode os.FileMode) { os.WriteFile(filepath.Join(h.Base, p), []byte("ELF"), mode) }
	w("usr/bin/nc.openbsd", 0o755)
	w("usr/bin/nmap", 0o755)
	w("usr/bin/data-file", 0o644)
	w("etc/not-a-program", 0o755)
	os.Symlink("nc.openbsd", filepath.Join(h.Base, "usr/bin/nc"))
	os.Symlink("../../etc/not-a-program", filepath.Join(h.Base, "usr/bin/escape"))
	// The fixture gives www-data the test user's UID when the tests run
	// unprivileged (CI); service accounts have a low UID.
	pw := filepath.Join(h.Base, "etc/passwd")
	b, _ := os.ReadFile(pw)
	var keep []string
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if !strings.HasPrefix(l, "www-data:") {
			keep = append(keep, l)
		}
	}
	keep = append(keep, "www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin")
	os.WriteFile(pw, []byte(strings.Join(keep, "\n")+"\n"), 0o644)
	f, _ := os.OpenFile(pw, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("shop:x:1600:1600::/home/nimvor/shop.test:/bin/bash\nmysql:x:110:110:MySQL Server:/nonexistent:/bin/false\nalice:x:1700:1700::/home/alice:/bin/bash\n")
	f.Close()
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return h, r
}

func call(t *testing.T, r *actions.Registry, name string, params any) (map[string]any, error) {
	t.Helper()
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
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

func calls(h *testhost.Host, prefix string) []string {
	var out []string
	for _, c := range h.Runner.Calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func TestParseAudit(t *testing.T) {
	log := strings.Join([]string{
		`type=SYSCALL msg=audit(1788264000.100:900): arch=c000003e syscall=59 success=yes exit=0 ppid=10 pid=11 auid=4294967295 uid=33 gid=33 comm="nc" exe="/usr/bin/nc.openbsd" key="nimvor-exec"`,
		`type=EXECVE msg=audit(1788264000.100:900): argc=3 a0="nc" a1="-e" a2=2F62696E2F7368`,
		`type=SYSCALL msg=audit(1788264060.200:901): arch=c000003e syscall=59 success=no exit=-13 ppid=10 pid=12 auid=4294967295 uid=1600 gid=1600 comm="nmap" exe="(null)" key="nimvor-exec"`,
		`type=EXECVE msg=audit(1788264060.200:901): argc=2 a0="nmap" a1="10.0.0.1"`,
		// Another rule's record, and an account that is not under watch.
		`type=SYSCALL msg=audit(1788264100.300:902): arch=c000003e syscall=59 success=yes exit=0 uid=33 comm="x" exe="/usr/bin/x" key="other"`,
		`type=SYSCALL msg=audit(1788264200.400:903): arch=c000003e syscall=59 success=yes exit=0 uid=999 comm="y" exe="/usr/bin/y" key="nimvor-exec"`,
		`type=EXECVE msg=audit(1788264200.400:903): argc=1 a0="password=hunter2secret"`,
		// A refused start as the kernel writes it: exe is the caller (the
		// shell), the program is in the PATH record.
		`type=SYSCALL msg=audit(1788264300.500:904): arch=c000003e syscall=59 success=no exit=-13 ppid=6621 pid=6622 auid=4294967295 uid=33 gid=33 comm="sh" exe="/usr/bin/dash" key="nimvor-exec"` + "\x1d" + `ARCH=x86_64 SYSCALL=execve UID="www-data"`,
		`type=PATH msg=audit(1788264300.500:904): item=0 name="/usr/bin/id" inode=210022 dev=fe:00 mode=0100755 ouid=0 ogid=0`,
		`garbage line`,
	}, "\n")
	got := ParseAudit([]byte(log), map[int]string{33: "www-data", 1600: "shop"})
	if len(got) != 3 {
		t.Fatalf("got %d executions: %+v", len(got), got)
	}
	// Newest first.
	if got[0].User != "www-data" || !got[0].Blocked || got[0].Program != "/usr/bin/id" || got[0].Command != "/usr/bin/id" {
		t.Errorf("refused start names the program, not the caller: %+v", got[0])
	}
	if got[1].User != "shop" || !got[1].Blocked || got[1].Program != "nmap" || got[1].Command != "nmap 10.0.0.1" {
		t.Errorf("second = %+v", got[1])
	}
	if got[2].User != "www-data" || got[2].Blocked || got[2].Program != "/usr/bin/nc.openbsd" || got[2].Command != "nc -e /bin/sh" {
		t.Errorf("third (hex argument decoded) = %+v", got[2])
	}
	if got[2].Time != "2026-09-01T12:00:00Z" {
		t.Errorf("time = %s", got[2].Time)
	}
}

func TestValidateProgram(t *testing.T) {
	h, _ := setup(t)
	for in, want := range map[string]string{
		"nc":            "/usr/bin/nc.openbsd", // a name, links followed
		"/usr/bin/nmap": "/usr/bin/nmap",
		"/usr/bin/nc":   "/usr/bin/nc.openbsd",
	} {
		got, err := ValidateProgram(h.Env, in)
		if err != nil || got != want {
			t.Errorf("%q → %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "missing", "../bin/nc", "usr/bin/nc", "/usr/bin/../bin/nc", "/usr/bin/data-file", "/usr/bin/escape", "/etc/not-a-program", "/usr/bin", "nc nmap", "/usr/bin/nc\n"} {
		if got, err := ValidateProgram(h.Env, in); err == nil {
			t.Errorf("%q was accepted (%q)", in, got)
		}
	}
}

func TestBlacklistProtectionAndLogging(t *testing.T) {
	h, r := setup(t)
	if _, err := call(t, r, "intrusion.blacklist", map[string]any{"programs": []string{"nc", "/usr/bin/nmap", "/usr/bin/nc.openbsd"}}); err != nil {
		t.Fatal(err)
	}
	st, _ := call(t, r, "intrusion.status", nil)
	bl := st["blacklist"].([]any)
	if len(bl) != 2 {
		t.Fatalf("blacklist = %v (duplicates by link must collapse)", bl)
	}
	// Nothing is protected yet: no ACL entries.
	if c := calls(h, "setfacl"); len(c) != 0 {
		t.Fatalf("setfacl before any account is protected: %v", c)
	}

	res, err := call(t, r, "intrusion.configure", map[string]any{"enabled": true, "accounts": map[string]any{
		"www-data": map[string]any{"log": true, "protect": true},
		"shop":     map[string]any{"log": true},
		"mysql":    map[string]any{"protect": true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"setfacl -m u:mysql:--- /usr/bin/nc.openbsd", "setfacl -m u:www-data:--- /usr/bin/nc.openbsd",
		"setfacl -m u:mysql:--- /usr/bin/nmap", "setfacl -m u:www-data:--- /usr/bin/nmap",
	}
	got := calls(h, "setfacl")
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("missing %q in %v", w, got)
		}
	}
	rules, err := os.ReadFile(filepath.Join(h.Base, RulesFile))
	if err != nil {
		t.Fatalf("audit rules not written: %v", err)
	}
	for _, line := range []string{"-F uid=33 -k nimvor-exec", "-F uid=1600 -k nimvor-exec"} {
		if !strings.Contains(string(rules), line) {
			t.Errorf("rules lack %q:\n%s", line, rules)
		}
	}
	if strings.Contains(string(rules), "uid=110") {
		t.Error("an account that is only protected (not logged) is in the audit rules")
	}
	if len(calls(h, "augenrules --load")) == 0 {
		t.Error("the audit rules were not loaded")
	}
	if res["enabled"] != true {
		t.Errorf("status = %v", res["enabled"])
	}

	// Protection off for one account: only its entries go.
	h.Runner.Calls = nil
	if _, err := call(t, r, "intrusion.configure", map[string]any{"enabled": true, "accounts": map[string]any{
		"www-data": map[string]any{"log": true, "protect": true},
		"shop":     map[string]any{"log": true},
	}}); err != nil {
		t.Fatal(err)
	}
	removed := calls(h, "setfacl -x")
	if len(removed) != 2 || !strings.Contains(strings.Join(removed, "\n"), "u:mysql /usr/bin/nmap") {
		t.Errorf("removed = %v", removed)
	}
	for _, c := range removed {
		if strings.Contains(c, "www-data") {
			t.Errorf("entry of a still protected account removed: %s", c)
		}
	}

	// A program leaves the list: its entries go.
	h.Runner.Calls = nil
	if _, err := call(t, r, "intrusion.blacklist", map[string]any{"programs": []string{"nc"}}); err != nil {
		t.Fatal(err)
	}
	if c := calls(h, "setfacl -x"); len(c) != 1 || c[0] != "setfacl -x u:www-data /usr/bin/nmap" {
		t.Errorf("removed after the list changed = %v", c)
	}

	// Master switch off: every entry and the audit rules are removed.
	h.Runner.Calls = nil
	if _, err := call(t, r, "intrusion.configure", map[string]any{"enabled": false, "accounts": map[string]any{
		"www-data": map[string]any{"log": true, "protect": true},
	}}); err != nil {
		t.Fatal(err)
	}
	if c := calls(h, "setfacl -x"); len(c) != 1 || c[0] != "setfacl -x u:www-data /usr/bin/nc.openbsd" {
		t.Errorf("removed on master off = %v", c)
	}
	if _, err := os.Stat(filepath.Join(h.Base, RulesFile)); err == nil {
		t.Error("the audit rules file is still there with the master switch off")
	}
	st, _ = call(t, r, "intrusion.status", nil)
	ops, _ := json.Marshal(st["operations"])
	for _, w := range []string{"master switch on", "Added /usr/bin/nmap to the blacklist", "Protection on for www-data", "master switch off"} {
		if !strings.Contains(string(ops), w) {
			t.Errorf("operation log lacks %q: %s", w, ops)
		}
	}
}

func TestReconcileAfterPackageUpdate(t *testing.T) {
	h, r := setup(t)
	call(t, r, "intrusion.blacklist", map[string]any{"programs": []string{"nmap"}})
	call(t, r, "intrusion.configure", map[string]any{"enabled": true, "accounts": map[string]any{"www-data": map[string]any{"protect": true}}})
	h.Runner.Calls = nil
	// A package update replaced the file and dropped its entry: set again.
	if err := Reconcile(context.Background(), h.Env); err != nil {
		t.Fatal(err)
	}
	if c := calls(h, "setfacl -m"); len(c) != 1 || c[0] != "setfacl -m u:www-data:--- /usr/bin/nmap" {
		t.Errorf("reapplied = %v", c)
	}
}

func TestConfigureRefusals(t *testing.T) {
	_, r := setup(t)
	for name, accounts := range map[string]map[string]any{
		"root":        {"root": map[string]any{"protect": true}},
		"missing":     {"nobody-here": map[string]any{"log": true}},
		"bad name":    {"../etc": map[string]any{"log": true}},
		"human login": {"alice": map[string]any{"protect": true}},
	} {
		if _, err := call(t, r, "intrusion.configure", map[string]any{"enabled": true, "accounts": accounts}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := call(t, r, "intrusion.blacklist", map[string]any{"programs": []string{"/etc/passwd"}}); err == nil {
		t.Error("a file outside the program directories was accepted")
	}
}

func TestToolsMissing(t *testing.T) {
	h, r := setup(t)
	call(t, r, "intrusion.blacklist", map[string]any{"programs": []string{"nmap"}})
	h.Runner.Unavailable["setfacl"] = true
	h.Runner.Unavailable["augenrules"] = true
	h.Runner.Handler = nil // Available() now follows Unavailable and Outputs
	_, err := call(t, r, "intrusion.configure", map[string]any{"enabled": true, "accounts": map[string]any{"www-data": map[string]any{"log": true, "protect": true}}})
	var ae *actions.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Details, "install") {
		t.Errorf("no friendly hint when the tools are missing: %v", err)
	}
}
