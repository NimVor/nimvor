//go:build realhost

package terminal

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ftpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/limits"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

const termSites = "/srv/whacp-termtest"

// realAccounts creates two website accounts, terma and termb, each with a
// private file, and an agent with the SFTP/shell, limits and terminal
// actions on the real system.
func realAccounts(t *testing.T) (*host.Env, *actions.Registry, *slogBuf) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("sshd"); err != nil {
		t.Fatal("the OpenSSH server is required (SSH/SFTP passwords are set through its configuration)")
	}
	os.MkdirAll(termSites, 0o755)
	for i, u := range []string{"terma", "termb"} {
		home := filepath.Join(termSites, strings.ToLower(u)+".test")
		exec.Command("pkill", "-KILL", "-u", u).Run()
		exec.Command("userdel", "-f", u).Run()
		if out, err := exec.Command("useradd", "--home-dir", home, "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", "--uid", []string{"2891", "2892"}[i], u).CombinedOutput(); err != nil {
			t.Fatalf("useradd: %s", out)
		}
		os.MkdirAll(filepath.Join(home, "public_html"), 0o750)
		os.WriteFile(filepath.Join(home, "private.txt"), []byte("SECRET-OF-"+u+"\n"), 0o600)
		exec.Command("chown", "-R", u+":"+u, home).Run()
		os.Chmod(home, 0o710)
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.SitesRoot = termSites
	cfg.StateDir = t.TempDir()
	logs := &slogBuf{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	r := actions.NewRegistry(true)
	ftpmgr.Register(r, env)
	limits.Register(r, env)
	Register(r, env)
	t.Cleanup(func() {
		Shared.CloseAll("test done")
		for _, u := range []string{"terma", "termb"} {
			call(r, "sftp.configure", map[string]any{"user": u, "enabled": false})
			exec.Command("pkill", "-KILL", "-u", u).Run()
			time.Sleep(200 * time.Millisecond)
			exec.Command("userdel", "-f", u).Run()
		}
		os.Remove(env.M.SFTPDropIn)
		os.RemoveAll(termSites)
		ftpmgr.CredentialsChanged = nil
	})
	return env, r, logs
}

type slogBuf = syncBuf

func call(r *actions.Registry, name string, params any) (map[string]any, error) {
	a, err := r.Lookup(name)
	if err != nil {
		return nil, err
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

func must(t *testing.T, r *actions.Registry, name string, params any) {
	t.Helper()
	if _, err := call(r, name, params); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// The terminal's password is the Linux account's: the one WhaCP sets for
// SSH/SFTP (in /etc/shadow). Changing it changes what the terminal
// accepts; a locked or suspended account is refused. Checked with the real
// unix_chkpwd; no systemd needed.
func TestRealHostLinuxPassword(t *testing.T) {
	env, r, logs := realAccounts(t)
	ctx := context.Background()
	check := func(user, pw string) error { return checkPassword(ctx, env, user, pw) }

	must(t, r, "sftp.configure", map[string]any{"user": "terma", "enabled": true, "password_login": true, "password": "First-Linux-Pass-1"})
	must(t, r, "sftp.configure", map[string]any{"user": "termb", "enabled": true, "password_login": true, "password": "Bee-Linux-Pass-22"})
	if err := check("terma", "First-Linux-Pass-1"); err != nil {
		t.Fatalf("the account's password was refused: %v", err)
	}
	for _, pw := range []string{"wrong-password", "", "Bee-Linux-Pass-22"} {
		if check("terma", pw) == nil {
			t.Errorf("terma accepted %q", pw)
		}
	}
	// A new password replaces the old one at once.
	must(t, r, "sftp.configure", map[string]any{"user": "terma", "enabled": true, "password_login": true, "password": "Second-Linux-Pass-2"})
	if check("terma", "First-Linux-Pass-1") == nil || check("terma", "Second-Linux-Pass-2") != nil {
		t.Fatal("the password change did not take effect")
	}
	// A password set outside WhaCP (passwd/chpasswd, yescrypt on Ubuntu) works too.
	if out, err := exec.Command("sh", "-c", "echo 'terma:Set-By-Chpasswd-3' | chpasswd").CombinedOutput(); err != nil {
		t.Fatalf("chpasswd: %s", out)
	}
	if check("terma", "Set-By-Chpasswd-3") != nil || check("terma", "Second-Linux-Pass-2") == nil {
		t.Fatal("a password set with chpasswd is not the one checked")
	}
	// Password login off locks the account; suspension expires it.
	keyFile := filepath.Join(t.TempDir(), "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", keyFile).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	pub, _ := os.ReadFile(keyFile + ".pub")
	must(t, r, "sftp.configure", map[string]any{"user": "terma", "enabled": true, "password_login": false, "keys": []string{string(pub)}})
	if check("terma", "Set-By-Chpasswd-3") == nil {
		t.Fatal("a locked account was accepted")
	}
	must(t, r, "sftp.configure", map[string]any{"user": "terma", "enabled": true, "password_login": true, "password": "Third-Linux-Pass-4"})
	must(t, r, "account.suspend", map[string]any{"user": "terma", "suspended": true})
	if check("terma", "Third-Linux-Pass-4") == nil {
		t.Fatal("a suspended account was accepted")
	}
	must(t, r, "account.suspend", map[string]any{"user": "terma", "suspended": false})
	if check("terma", "Third-Linux-Pass-4") != nil {
		t.Fatal("a resumed account was refused")
	}
	for _, secret := range []string{"First-Linux-Pass-1", "Second-Linux-Pass-2", "Third-Linux-Pass-4", "Bee-Linux-Pass-22", "Set-By-Chpasswd-3"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("a password is in the agent's log")
		}
	}
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)

var statusRe = regexp.MustCompile(`(?m)^(NoNewPrivs|CapInh|CapPrm|CapEff|CapBnd|CapAmb):\s*(\S+)`)

// A real session per account: its own systemd unit as the account, no
// privileges at all (no_new_privs, every capability set empty), in the
// account's resource limits, unable to become root or read another
// account's files; jailed accounts see only their own website; closing the
// terminal stops the unit.
func TestRealHostTerminalSession(t *testing.T) {
	env, r, logs := realAccounts(t)
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Log("systemd is not running here: sessions are checked on the systemd test machines")
		return
	}
	m := Shared
	m.Enabled = true
	srv := serve(t, m)
	ctx := context.Background()
	must(t, r, "sftp.configure", map[string]any{"user": "terma", "enabled": true, "password_login": true, "password": "Terminal-A-Pass-1"})
	must(t, r, "sftp.configure", map[string]any{"user": "termb", "enabled": true, "password_login": true, "password": "Terminal-B-Pass-2"})
	must(t, r, "shell.set", map[string]any{"users": []string{"terma", "termb"}, "mode": "normal"})
	must(t, r, "limits.set", map[string]any{"account": "acct991", "users": []string{"terma"}, "memory_mb": 256, "tasks_max": 64})
	t.Cleanup(func() { call(r, "limits.set", map[string]any{"account": "acct991", "users": []string{"terma"}}) })

	open := func(user, pw string) (*client, string) {
		t.Helper()
		res, err := m.Open(OpenParams{User: user, ClientIP: "127.0.0.1"})
		if err != nil {
			t.Fatal(err)
		}
		c, code := dial(t, srv, "https://HOST")
		if code != 101 {
			t.Fatalf("handshake %d", code)
		}
		c.send(opText, authMsg(res["ticket"].(string), pw))
		_, ready := c.until(t, "ready", "")
		return c, ready["session_id"].(string)
	}
	run := func(c *client, script string) string {
		t.Helper()
		// The terminal echoes the typed line: the marker is printed only
		// when the shell runs it (the quotes split it in the typed text).
		c.send(opBinary, []byte(script+"; echo END-OF-'CHECKS'\n"))
		out, _ := c.until(t, "", "END-OF-CHECKS\r\n")
		// Only what the commands printed: not the echoed command line
		// (it names the other account's files), without colours (RHEL's
		// interactive grep colours its matches).
		if i := strings.LastIndex(out, "END-OF-'CHECKS'"); i >= 0 {
			out = out[i+len("END-OF-'CHECKS'"):]
		}
		return ansiRe.ReplaceAllString(out, "")
	}

	// A wrong password (here: the other account's) gets no shell.
	res, _ := m.Open(OpenParams{User: "terma", ClientIP: "127.0.0.1"})
	bad, _ := dial(t, srv, "https://HOST")
	bad.send(opText, authMsg(res["ticket"].(string), "Terminal-B-Pass-2"))
	bad.until(t, "auth_failed", "")
	bad.c.Close()

	a, idA := open("terma", "Terminal-A-Pass-1")
	b, idB := open("termb", "Terminal-B-Pass-2")
	homeB := filepath.Join(termSites, "termb.test")
	out := run(a, `echo user=$(id -un) uid=$(id -u); echo "IDLINE $(id)"; echo "GROUPSLINE $(groups)"; grep -E '^(NoNewPrivs|Cap(Inh|Prm|Eff|Bnd|Amb))' /proc/self/status; cat /proc/self/cgroup; g=$(cut -d: -f3 /proc/self/cgroup); echo pids=$(cat /sys/fs/cgroup$g/pids.max) slicemem=$(cat /sys/fs/cgroup${g%/*}/memory.max); sudo -n id -u 2>&1; echo sudo-rc=$?; su -c 'id -u' root </dev/null 2>&1; echo su-rc=$?; cat `+homeB+`/private.txt 2>&1; cat /etc/shadow 2>&1 | head -1`)
	t.Logf("terma session:\n%s", out)
	for _, want := range []string{"user=terma uid=2891", UnitName(idA), "whacp-acct991.slice", "pids=512", "slicemem=268435456", "Permission denied"} {
		if !strings.Contains(out, want) {
			t.Errorf("terma session lacks %q", want)
		}
	}
	caps := statusRe.FindAllStringSubmatch(out, -1)
	if len(caps) != 6 {
		t.Fatalf("status lines: %v", caps)
	}
	for _, c := range caps {
		want := "0000000000000000"
		if c[1] == "NoNewPrivs" {
			want = "1"
		}
		if c[2] != want {
			t.Errorf("%s = %s, want %s", c[1], c[2], want)
		}
	}
	for _, bad := range []string{"SECRET-OF-termb", "uid=0", "\n0\r\n", "root:"} {
		if strings.Contains(out, bad) {
			t.Errorf("terma session shows %q (root or another account's data)", bad)
		}
	}
	if strings.Contains(out, "sudo-rc=0") || strings.Contains(out, "su-rc=0") {
		t.Error("sudo or su succeeded")
	}
	idLines := regexp.MustCompile(`(?m)^(IDLINE|GROUPSLINE) .*$`).FindAllString(out, -1)
	if len(idLines) != 2 {
		t.Fatalf("id/groups output missing:\n%s", out)
	}
	for _, g := range []string{"sudo", "wheel", "docker", "lxd", "disk", "adm", "shadow", "root", "systemd-journal"} {
		re := regexp.MustCompile(`[ (,]` + regexp.QuoteMeta(g) + `[ ),\r]|[ (,]` + regexp.QuoteMeta(g) + `$`)
		for _, l := range idLines {
			if re.MatchString(strings.TrimRight(l, "\r")) {
				t.Errorf("terma is in the privileged group %s: %s", g, l)
			}
		}
	}
	// The other account's session is its own.
	outB := run(b, "id -un; cat private.txt")
	if !strings.Contains(outB, "termb") || !strings.Contains(outB, "SECRET-OF-termb") || strings.Contains(outB, "user=terma") {
		t.Errorf("termb session:\n%s", outB)
	}

	// Changing terma's password ends its session, not termb's.
	must(t, r, "sftp.configure", map[string]any{"user": "terma", "enabled": true, "password_login": true, "password": "Terminal-A-Pass-NEW"})
	if _, e := a.until(t, "exit", ""); !strings.Contains(e["reason"].(string), "password") {
		t.Fatalf("%v", e)
	}
	if !env.UnitActive(ctx, UnitName(idB)) {
		t.Fatal("termb's session ended with terma's password change")
	}
	waitInactive(t, env, UnitName(idA))

	// Closing the browser's connection stops the shell.
	b.c.Close()
	waitInactive(t, env, UnitName(idB))

	// Jailed: only the account's website and the read-only system.
	if b, _ := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"); strings.TrimSpace(string(b)) == "1" {
		profile := "abi <abi/4.0>,\ninclude <tunables/global>\nprofile whacp-bwrap " + ftpmgr.JailBwrap + " flags=(unconfined) {\n  userns,\n}\n"
		os.WriteFile("/etc/apparmor.d/whacp-bwrap", []byte(profile), 0o644)
		if out, err := exec.Command("apparmor_parser", "-r", "/etc/apparmor.d/whacp-bwrap").CombinedOutput(); err != nil {
			t.Fatalf("apparmor_parser: %s", out)
		}
		t.Cleanup(func() {
			exec.Command("apparmor_parser", "-R", "/etc/apparmor.d/whacp-bwrap").Run()
			os.Remove("/etc/apparmor.d/whacp-bwrap")
			os.Remove(ftpmgr.JailBwrap)
		})
	}
	must(t, r, "shell.set", map[string]any{"users": []string{"terma"}, "mode": "jailed"})
	t.Cleanup(func() {
		call(r, "shell.set", map[string]any{"users": []string{"terma"}, "mode": "disabled"})
		os.Remove(ftpmgr.JailShell)
	})
	j, idJ := open("terma", "Terminal-A-Pass-NEW")
	out = run(j, `id -un; pwd; ls `+termSites+`; cat `+homeB+`/private.txt 2>&1; grep -E '^(NoNewPrivs|CapBnd|CapEff)' /proc/self/status; ps -e -o comm= | sort -u | tr '\n' ' '; echo; touch /usr/x 2>&1; echo 1 > /proc/sys/kernel/sysrq 2>&1 && echo SYSCTL-WRITTEN; grep -c . /proc/kmsg 2>&1 | head -1`)
	t.Logf("jailed session:\n%s", out)
	// The jail's own /proc keeps kernel settings and logs out of reach (the
	// unit leaves that to it, see jailCovers).
	if strings.Contains(out, "SYSCTL-WRITTEN") {
		t.Error("the jailed session changed a kernel setting")
	}
	for _, want := range []string{"terma", "terma.test", "NoNewPrivs:\t1", "CapBnd:\t0000000000000000", "CapEff:\t0000000000000000", "Read-only file system", "/proc/kmsg: Permission denied"} {
		if !strings.Contains(out, want) {
			t.Errorf("jailed session lacks %q", want)
		}
	}
	// The other website is neither listed nor readable (cat's own error
	// names the path it was given, which does not exist in the jail).
	if regexp.MustCompile(`(?m)^termb\.test\r?$`).MatchString(out) || !strings.Contains(out, "termb.test/private.txt: No such file or directory") {
		t.Error("the jailed session sees the other website")
	}
	for _, bad := range []string{"SECRET-OF-termb", "systemd", "whacp-agent", "sshd"} {
		if strings.Contains(out, bad) {
			t.Errorf("jailed session sees %q", bad)
		}
	}
	// Disabling shell access ends the session.
	must(t, r, "shell.set", map[string]any{"users": []string{"terma"}, "mode": "disabled"})
	j.until(t, "exit", "")
	waitInactive(t, env, UnitName(idJ))
	if _, err := m.Open(OpenParams{User: "terma"}); err == nil {
		t.Fatal("a terminal was offered with shell access disabled")
	}
	for _, secret := range []string{"Terminal-A-Pass-1", "Terminal-B-Pass-2", "Terminal-A-Pass-NEW"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("a password is in the agent's log")
		}
	}
}

func waitInactive(t *testing.T, env *host.Env, unit string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for env.UnitActive(context.Background(), unit) {
		if time.Now().After(deadline) {
			t.Fatalf("%s is still running", unit)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
