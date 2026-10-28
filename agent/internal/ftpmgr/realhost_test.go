//go:build realhost

package ftpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

const rtSites = "/srv/nimvor-rt-sites"

// realEnv prepares a website account (rtftp, home <rtSites>/rt-ftp.test).
func realEnv(t *testing.T) (*host.Env, *actions.Registry) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	os.MkdirAll(rtSites, 0o755)
	os.Chmod(rtSites, 0o755)
	home := filepath.Join(rtSites, "rt-ftp.test")
	removeUser := func() {
		// A finished SFTP session may still be closing.
		exec.Command("pkill", "-KILL", "-u", "rtftp").Run()
		time.Sleep(200 * time.Millisecond)
		exec.Command("userdel", "-f", "rtftp").Run()
	}
	removeUser()
	if out, err := exec.Command("useradd", "--home-dir", home, "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", "--uid", "2890", "rtftp").CombinedOutput(); err != nil {
		t.Fatalf("useradd: %s", out)
	}
	os.MkdirAll(filepath.Join(home, "public_html"), 0o750)
	exec.Command("chown", "-R", "rtftp:rtftp", home).Run()
	os.Chmod(home, 0o710)
	t.Cleanup(func() {
		removeUser()
		os.RemoveAll(rtSites)
	})
	root := filesystem.Host()
	cfg := config.Default()
	cfg.SitesRoot = rtSites
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	return env, r
}

func do(t *testing.T, r *actions.Registry, name string, params any) map[string]any {
	t.Helper()
	a, _ := r.Lookup(name)
	b, _ := json.Marshal(params)
	res, err := a.Handler(context.Background(), b)
	if err != nil {
		t.Fatalf("%s: %+v", name, err)
	}
	out, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m
}

func systemd() bool {
	_, err := os.Stat("/run/systemd/system")
	return err == nil
}

func TestRealHostSFTP(t *testing.T) {
	env, r := realEnv(t)
	if _, err := exec.LookPath("sftp"); err != nil {
		t.Skip("needs the OpenSSH client")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	t.Cleanup(func() {
		do(t, r, "sftp.configure", map[string]any{"user": "rtftp", "enabled": false})
		os.Remove(env.M.SFTPDropIn)
		reloadSSH()
	})
	do(t, r, "sftp.configure", map[string]any{"user": "rtftp", "enabled": true, "keys": []string{string(pub)}})
	reloadSSH()
	if fi, _ := os.Stat(rtSites); fi.Mode().Perm() != 0o711 {
		t.Fatalf("sites root mode = %v", fi.Mode().Perm())
	}
	os.WriteFile(filepath.Join(dir, "up.txt"), []byte("hello"), 0o644)
	batch := filepath.Join(dir, "batch")
	os.WriteFile(batch, []byte("pwd\nput "+filepath.Join(dir, "up.txt")+" public_html/up.txt\n-ls /\n-cd /etc\n-get /etc/passwd "+filepath.Join(dir, "stolen")+"\n"), 0o644)
	sshOpts := []string{"-i", key, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes"}
	var out []byte
	var err error
	for i := 0; i < 10; i++ { // sshd may take a moment to reload
		out, err = exec.Command("sftp", append(sshOpts, "-b", batch, "rtftp@127.0.0.1")...).CombinedOutput()
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatalf("sftp: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "Remote working directory: /rt-ftp.test") {
		t.Fatalf("not started in the website folder:\n%s", s)
	}
	if _, err := os.Stat(filepath.Join(dir, "stolen")); err == nil {
		t.Fatal("read /etc/passwd through SFTP")
	}
	info, err := os.Stat(filepath.Join(rtSites, "rt-ftp.test/public_html/up.txt"))
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("uploaded file: %v %v\n%s", info, err, s)
	}
	// No shell.
	out, _ = exec.Command("ssh", append(sshOpts, "rtftp@127.0.0.1", "id")...).CombinedOutput()
	if strings.Contains(string(out), "uid=") {
		t.Fatalf("got a shell: %s", out)
	}
	// Other users are unaffected.
	eff, _ := exec.Command("sshd", "-T", "-C", "user=root,host=localhost,addr=127.0.0.1").CombinedOutput()
	if strings.Contains(string(eff), "chrootdirectory "+rtSites) {
		t.Fatal("root is confined too")
	}
	// Turning it off locks the user out again.
	do(t, r, "sftp.configure", map[string]any{"user": "rtftp", "enabled": false})
	reloadSSH()
	time.Sleep(time.Second)
	if out, err := exec.Command("sftp", append(sshOpts, "-b", batch, "rtftp@127.0.0.1")...).CombinedOutput(); err == nil {
		t.Fatalf("still reachable after disabling:\n%s", out)
	}
}

// reloadSSH makes an sshd that is not managed by systemd (containers)
// re-read its configuration; under systemd the action reloads it.
func reloadSSH() {
	if !systemd() {
		exec.Command("pkill", "-HUP", "-x", "sshd").Run()
		time.Sleep(500 * time.Millisecond)
	}
}

func TestRealHostFTP(t *testing.T) {
	env, r := realEnv(t)
	if !systemd() {
		t.Skip("needs systemd")
	}
	if _, err := os.Stat("/usr/sbin/pure-ftpd"); err != nil {
		t.Skip("needs pure-ftpd")
	}
	var wasActive []string
	for _, u := range otherFTPUnits {
		if env.UnitActive(context.Background(), u) {
			wasActive = append(wasActive, u)
		}
	}
	p := ftpPaths(env)
	t.Cleanup(func() {
		exec.Command("systemctl", "disable", "--now", Unit).Run()
		os.RemoveAll(p.dir)
		os.Remove(p.unit)
		exec.Command("systemctl", "daemon-reload").Run()
		for _, u := range wasActive {
			exec.Command("systemctl", "enable", "--now", u).Run()
		}
	})
	res := do(t, r, "ftp.configure", map[string]any{"confirm_replace_service": true})
	t.Logf("configured: %v", res["notes"])
	do(t, r, "ftp.account_save", map[string]any{"name": "dev@rt-ftp.test", "user": "rtftp", "dir": "public_html", "password": "Ftp-test-password-1"})

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644)
	curl := func(args ...string) (string, error) {
		out, err := exec.Command("curl", append([]string{"--silent", "--show-error", "--max-time", "20"}, args...)...).CombinedOutput()
		return string(out), err
	}
	creds := "dev@rt-ftp.test:Ftp-test-password-1"
	if out, err := curl("--ssl-reqd", "--insecure", "-u", creds, "-T", filepath.Join(dir, "a.txt"), "ftp://127.0.0.1/"); err != nil {
		t.Fatalf("upload: %v %s", err, out)
	}
	info, err := os.Stat(filepath.Join(rtSites, "rt-ftp.test/public_html/a.txt"))
	if err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 2890 || info.Mode().Perm() != 0o640 {
		t.Fatalf("uploaded file owner/mode: %+v %v", info.Sys(), info.Mode())
	}
	out, err := curl("--ssl-reqd", "--insecure", "-u", creds, "--list-only", "ftp://127.0.0.1/")
	if err != nil || !strings.Contains(out, "a.txt") {
		t.Fatalf("list: %v %s", err, out)
	}
	// Chrooted: the parent listing is the same folder.
	if out, _ := curl("--ssl-reqd", "--insecure", "-u", creds, "--list-only", "ftp://127.0.0.1/../"); strings.Contains(out, "public_html") {
		t.Fatalf("escaped the chroot: %s", out)
	}
	// TLS is required and wrong passwords are refused.
	if _, err := curl("-u", creds, "--list-only", "ftp://127.0.0.1/"); err == nil {
		t.Fatal("plain FTP login accepted")
	}
	if _, err := curl("--ssl-reqd", "--insecure", "-u", "dev@rt-ftp.test:wrong-password", "--list-only", "ftp://127.0.0.1/"); err == nil {
		t.Fatal("wrong password accepted")
	}
	do(t, r, "ftp.account_delete", map[string]any{"name": "dev@rt-ftp.test"})
	if _, err := curl("--ssl-reqd", "--insecure", "-u", creds, "--list-only", "ftp://127.0.0.1/"); err == nil {
		t.Fatal("deleted account can still log in")
	}
	do(t, r, "ftp.disable", map[string]any{})
}

// Account suspension: the expired account cannot log in with its key;
// resuming lets it in again.
func TestRealHostSuspendedAccountCannotLogIn(t *testing.T) {
	env, r := realEnv(t)
	if _, err := exec.LookPath("sftp"); err != nil {
		t.Skip("needs the OpenSSH client")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	t.Cleanup(func() {
		do(t, r, "sftp.configure", map[string]any{"user": "rtftp", "enabled": false})
		os.Remove(env.M.SFTPDropIn)
		reloadSSH()
	})
	do(t, r, "sftp.configure", map[string]any{"user": "rtftp", "enabled": true, "keys": []string{string(pub)}})
	reloadSSH()
	batch := filepath.Join(dir, "batch")
	os.WriteFile(batch, []byte("pwd\n"), 0o644)
	login := func() ([]byte, error) {
		return exec.Command("sftp", "-i", key, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-b", batch, "rtftp@127.0.0.1").CombinedOutput()
	}
	var out []byte
	var err error
	for i := 0; i < 10; i++ {
		if out, err = login(); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatalf("before suspension: %v\n%s", err, out)
	}
	do(t, r, "account.suspend", map[string]any{"user": "rtftp", "suspended": true})
	if out, err := login(); err == nil {
		t.Fatalf("a suspended account logged in:\n%s", out)
	}
	do(t, r, "account.suspend", map[string]any{"user": "rtftp", "suspended": false})
	for i := 0; i < 10; i++ {
		if out, err = login(); err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		t.Fatalf("after resuming: %v\n%s\n%s", err, out, sshDiagnosis(env.M.SFTPKeysDir))
	}
}

// Shell modes over a real sshd: a jailed session sees only its website and
// the read-only system; SFTP runs in the jail too; a normal shell is a
// plain login; disabled refuses commands.
func TestRealHostShellModes(t *testing.T) {
	env, r := realEnv(t)
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("needs bubblewrap")
	}
	if b, _ := os.ReadFile("/proc/sys/kernel/apparmor_restrict_unprivileged_userns"); strings.TrimSpace(string(b)) == "1" {
		// What the installer does on Ubuntu 23.10 and later.
		profile := "abi <abi/4.0>,\ninclude <tunables/global>\nprofile nimvor-bwrap " + JailBwrap + " flags=(unconfined) {\n  userns,\n}\n"
		os.WriteFile("/etc/apparmor.d/nimvor-bwrap", []byte(profile), 0o644)
		if out, err := exec.Command("apparmor_parser", "-r", "/etc/apparmor.d/nimvor-bwrap").CombinedOutput(); err != nil {
			t.Fatalf("apparmor_parser: %s", out)
		}
		t.Cleanup(func() {
			exec.Command("apparmor_parser", "-R", "/etc/apparmor.d/nimvor-bwrap").Run()
			os.Remove("/etc/apparmor.d/nimvor-bwrap")
			os.Remove(JailBwrap)
		})
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %s", out)
	}
	pub, _ := os.ReadFile(key + ".pub")
	// Another website on the same server, readable by everyone.
	other := filepath.Join(rtSites, "other.test")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "secret.txt"), []byte("other website"), 0o644)
	t.Cleanup(func() {
		do(t, r, "shell.set", map[string]any{"users": []string{"rtftp"}, "mode": "disabled"})
		do(t, r, "sftp.configure", map[string]any{"user": "rtftp", "enabled": false})
		os.Remove(env.M.SFTPDropIn)
		os.Remove(JailShell)
		reloadSSH()
	})
	do(t, r, "sftp.configure", map[string]any{"user": "rtftp", "enabled": true, "keys": []string{string(pub)}})
	reloadSSH()
	sshOpts := []string{"-i", key, "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes"}
	run := func(cmd string) (string, error) {
		var out []byte
		var err error
		// Right after a configuration change sshd may refuse the first
		// connections (Ubuntu 24.04, socket activation): a few tries, then
		// sshd's view of the account if it still refuses.
		for i := 0; i < 8; i++ {
			out, err = exec.Command("ssh", append(sshOpts, "rtftp@127.0.0.1", cmd)...).CombinedOutput()
			if err == nil || !(strings.Contains(string(out), "Connection refused") || strings.Contains(string(out), "Permission denied (publickey)")) {
				break
			}
			time.Sleep(time.Second)
		}
		if strings.Contains(string(out), "Permission denied (publickey)") {
			out = append(out, "\n"+sshDiagnosis(env.M.SFTPKeysDir)...)
		}
		return string(out), err
	}

	do(t, r, "shell.set", map[string]any{"users": []string{"rtftp"}, "mode": "jailed"})
	reloadSSH()
	out, err := run(`id -un; pwd; ls ` + rtSites + `; cat ` + filepath.Join(other, "secret.txt") + `; ls /etc; grep -c . /etc/passwd; echo hi > public_html/from-jail.txt && echo WROTE; touch /usr/x 2>&1; ps -e -o comm= | sort -u | tr '\n' ' '; echo; grep NoNewPrivs /proc/self/status`)
	if err != nil && !strings.Contains(out, "rtftp") {
		t.Fatalf("jailed session: %v\n%s\n%s", err, out, sshDiagnosis(env.M.SFTPKeysDir))
	}
	if strings.Contains(out, "Permission denied") {
		t.Fatalf("jailed session refused:\n%s\n%s", out, sshDiagnosis(env.M.SFTPKeysDir))
	}
	t.Logf("jailed session:\n%s", out)
	home := filepath.Join(rtSites, "rt-ftp.test")
	for _, want := range []string{"rtftp\n" + home + "\n", "WROTE", "NoNewPrivs:\t1"} {
		if !strings.Contains(out, want) {
			t.Errorf("jailed session lacks %q", want)
		}
	}
	for _, bad := range []string{"other.test\n", "other website", "shadow", "nginx", "nimvor-agent", "sshd"} {
		if strings.Contains(out, bad) {
			t.Errorf("jailed session sees %q", bad)
		}
	}
	if !strings.Contains(out, "Read-only file system") {
		t.Error("/usr is writable in the jail")
	}
	if b, err := os.ReadFile(filepath.Join(home, "public_html/from-jail.txt")); err != nil || string(b) != "hi\n" {
		t.Errorf("file written in the jail: %q %v", b, err)
	}
	// SFTP runs inside the jail as well.
	batch := filepath.Join(dir, "batch")
	os.WriteFile(batch, []byte("pwd\n-ls "+rtSites+"\n-get "+filepath.Join(other, "secret.txt")+" "+filepath.Join(dir, "stolen")+"\n"), 0o644)
	sout, err := exec.Command("sftp", append(sshOpts, "-b", batch, "rtftp@127.0.0.1")...).CombinedOutput()
	if err != nil {
		t.Fatalf("sftp in the jail: %v\n%s", err, sout)
	}
	if _, err := os.Stat(filepath.Join(dir, "stolen")); err == nil || strings.Contains(string(sout), "other.test  ") || strings.Contains(string(sout), "other.test\n") {
		t.Fatalf("SFTP reached another website:\n%s", sout)
	}

	do(t, r, "shell.set", map[string]any{"users": []string{"rtftp"}, "mode": "normal"})
	reloadSSH()
	if out, err := run("echo $0; id -un"); err != nil || !strings.Contains(out, "rtftp") {
		t.Fatalf("normal shell: %v\n%s", err, out)
	}

	do(t, r, "shell.set", map[string]any{"users": []string{"rtftp"}, "mode": "disabled"})
	reloadSSH()
	if out, _ := run("id -un"); strings.Contains(out, "rtftp\n") {
		t.Fatalf("a command ran with the shell disabled:\n%s", out)
	}
}

// sshDiagnosis: why sshd refused the test account (its log, the effective
// settings for it, the account and its key file).
func sshDiagnosis(keysDir string) string {
	var b strings.Builder
	for _, c := range [][]string{
		{"journalctl", "--no-pager", "-n", "30", "-t", "sshd", "-t", "sshd-session", "--since", "-5min"},
		{"sh", "-c", "sshd -T -C user=rtftp,host=localhost,addr=127.0.0.1 | grep -iE '^(authorizedkeysfile|forcecommand|chrootdirectory|pubkeyauthentication|allowusers|allowgroups|denyusers|denygroups|pubkeyacceptedalgorithms) '"},
		{"getent", "passwd", "rtftp"},
		{"chage", "-l", "rtftp"},
		{"id", "rtftp"},
		{"ls", "-ld", keysDir, filepath.Join(keysDir, "rtftp"), JailShell},
	} {
		out, _ := exec.Command(c[0], c[1:]...).CombinedOutput()
		fmt.Fprintf(&b, "$ %s\n%s\n", strings.Join(c, " "), strings.TrimSpace(string(out)))
	}
	return b.String()
}
