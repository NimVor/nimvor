package hardening

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func setup(t *testing.T) (*testhost.Host, *actions.Registry) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("changes owners: needs root")
	}
	h := testhost.New(t)
	for _, d := range []string{"usr/bin", "usr/sbin", "usr/lib/gcc", "etc/security/limits.d"} {
		os.MkdirAll(filepath.Join(h.Base, d), 0o755)
	}
	w := func(p string) {
		os.WriteFile(filepath.Join(h.Base, p), []byte("ELF"), 0o755)
	}
	w("usr/bin/gcc-12")
	w("usr/bin/x86_64-linux-gnu-gcc-12")
	w("usr/bin/as")
	w("usr/bin/traceroute.db")
	w("usr/bin/tracepath")
	w("usr/bin/grep")
	os.Symlink("gcc-12", filepath.Join(h.Base, "usr/bin/gcc"))
	os.Symlink("gcc", filepath.Join(h.Base, "usr/bin/cc"))
	os.Symlink("traceroute.db", filepath.Join(h.Base, "usr/bin/traceroute"))
	// A compiler name pointing outside /usr is never changed.
	w("etc/evil")
	os.Symlink("../../etc/evil", filepath.Join(h.Base, "usr/bin/clang"))
	f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("shop:x:1600:1600::/home/whacp/shop.test:/bin/bash\npostfix:x:110:115::/var/spool/postfix:/usr/sbin/nologin\nwhacp:x:998:998::/var/lib/whacp:/usr/sbin/nologin\n")
	f.Close()
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "groupadd" {
			g, _ := os.OpenFile(filepath.Join(h.Base, "etc/group"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			g.WriteString(args[len(args)-1] + ":x:990:\n")
			g.Close()
			return "", nil, true
		}
		return "", nil, false
	}
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

func stat(t *testing.T, h *testhost.Host, p string) (uint32, uint32) {
	var st syscall.Stat_t
	if err := syscall.Lstat(filepath.Join(h.Base, p), &st); err != nil {
		t.Fatal(err)
	}
	return st.Mode & 0o7777, st.Gid
}

func TestCompilersRestrictedAndRestored(t *testing.T) {
	h, r := setup(t)
	res, err := call(t, r, "hardening.compilers", map[string]any{"restricted": true, "users": []string{"shop"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"usr/bin/gcc-12", "usr/bin/x86_64-linux-gnu-gcc-12", "usr/bin/as"} {
		if mode, gid := stat(t, h, p); mode != 0o750 || gid != 990 {
			t.Errorf("%s: %o gid %d", p, mode, gid)
		}
	}
	for _, p := range []string{"usr/bin/grep", "etc/evil", "usr/bin/traceroute.db"} {
		if mode, _ := stat(t, h, p); mode != 0o755 {
			t.Errorf("%s changed: %o", p, mode)
		}
	}
	if !strings.Contains(strings.Join(h.Runner.Calls, "\n"), "gpasswd --members shop whacp-compiler") {
		t.Fatalf("members: %v", h.Runner.Calls)
	}
	if progs := res["programs"].([]any); len(progs) != 3 {
		t.Fatalf("programs: %v", progs)
	}
	if _, err := call(t, r, "hardening.compilers", map[string]any{"restricted": true, "users": []string{"root"}}); err == nil {
		t.Fatal("root as an allowed user")
	}
	if _, err := call(t, r, "hardening.compilers", map[string]any{"restricted": false}); err != nil {
		t.Fatal(err)
	}
	if mode, gid := stat(t, h, "usr/bin/gcc-12"); mode != 0o755 || gid != 0 {
		t.Fatalf("not restored: %o %d", mode, gid)
	}
}

func TestTraceroute(t *testing.T) {
	h, r := setup(t)
	if _, err := call(t, r, "hardening.traceroute", map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"usr/bin/traceroute.db", "usr/bin/tracepath"} {
		if mode, _ := stat(t, h, p); mode != 0o700 {
			t.Errorf("%s: %o", p, mode)
		}
	}
	res, _ := call(t, r, "hardening.status", nil)
	if res["traceroute"].(map[string]any)["enabled"] != false {
		t.Fatalf("status: %v", res["traceroute"])
	}
	// A package update resets the mode; Reapply restricts it again.
	os.Chmod(filepath.Join(h.Base, "usr/bin/tracepath"), 0o755)
	call(t, r, "hardening.reapply", nil)
	if mode, _ := stat(t, h, "usr/bin/tracepath"); mode != 0o700 {
		t.Fatalf("not reapplied: %o", mode)
	}
	call(t, r, "hardening.traceroute", map[string]any{"enabled": true})
	if mode, _ := stat(t, h, "usr/bin/traceroute.db"); mode != 0o755 {
		t.Fatalf("not restored: %o", mode)
	}
}

func TestForkBomb(t *testing.T) {
	h, r := setup(t)
	call(t, r, "hardening.forkbomb", map[string]any{"enabled": true})
	body := h.Read(ForkBombFile)
	if !strings.Contains(body, "1000: hard nproc 300") || strings.Contains(body, "root") && !strings.Contains(body, "not root") {
		t.Fatalf("limits file:\n%s", body)
	}
	call(t, r, "hardening.forkbomb", map[string]any{"enabled": false})
	if h.Exists(ForkBombFile) {
		t.Fatal("file left")
	}
}

func TestSMTPRestrictions(t *testing.T) {
	h, r := setup(t)
	res, err := call(t, r, "hardening.smtp", map[string]any{"enabled": true})
	if err != nil {
		t.Fatal(err)
	}
	rules := h.Read(execx.NftRulesFile)
	if !strings.Contains(rules, "meta skuid { 0, 110, 998 } accept") || !strings.Contains(rules, "tcp dport { 25, 465, 587 }") || !strings.Contains(rules, `oifname "lo" accept`) {
		t.Fatalf("rules:\n%s", rules)
	}
	calls := strings.Join(h.Runner.Calls, "\n")
	for _, want := range []string{"nft -c -f " + execx.NftRulesFile, "systemctl enable -- " + SMTPUnit, "systemctl restart -- " + SMTPUnit} {
		if !strings.Contains(calls, want) {
			t.Errorf("missing %q in %s", want, calls)
		}
	}
	if res["enabled"] != true {
		t.Fatalf("status: %v", res)
	}
	// nft refusing the rules: nothing kept.
	call(t, r, "hardening.smtp", map[string]any{"enabled": false})
	h.Fail["nft -c"] = "Error: syntax error"
	if _, err := call(t, r, "hardening.smtp", map[string]any{"enabled": true}); err == nil || h.Exists(execx.NftRulesFile) {
		t.Fatalf("accepted rules nft refused: %v", err)
	}
}

func TestNftOnlyRunsWhaCPsTable(t *testing.T) {
	var runner execx.SimRunner
	for _, args := range [][]string{{"-f", "/tmp/x.nft"}, {"flush", "ruleset"}, {"delete", "table", "inet", "firewalld"}} {
		if _, err := runner.RunWith(context.Background(), "nft", execx.Opts{}, args...); err == nil {
			t.Errorf("nft %v allowed", args)
		}
	}
}
