//go:build realhost

package execx

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Commands run as a website account (backups, restores, cron "run now",
// deploy hooks, app installers) are started by PID 1 in a confined
// transient unit: the right account, no capabilities, no privilege gain,
// the system read-only, arguments and binary streams passed unchanged, exit
// status and timeouts honoured. Regression test for F-16.
func TestRealHostCommandsAsAccount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	r := NewSystemRunner(30 * time.Second)
	if !r.transient() {
		t.Fatal("systemd-run is not used: commands as other accounts would switch credentials inside the agent")
	}
	name := "whacprt" + strconv.Itoa(os.Getpid()%100000)
	home := "/home/" + name
	if out, err := exec.Command("useradd", "-m", "-d", home, "-s", "/usr/sbin/nologin", name).CombinedOutput(); err != nil {
		t.Fatalf("useradd: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("userdel", "-r", name).Run() })
	u, _ := user.Lookup(name)
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	as := &Credential{UID: uint32(uid), GID: uint32(gid), Home: home}
	ctx := context.Background()
	sh := func(script string, o Opts) (string, error) {
		o.As = as
		if o.Dir == "" {
			o.Dir = home
		}
		out, err := r.RunWith(ctx, "sh", o, "-c", script)
		return string(out), err
	}

	// Account, confinement.
	out, err := sh("id -u; id -g; grep -E '^(CapEff|CapBnd|NoNewPrivs)' /proc/self/status; pwd", Opts{})
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, want := range []string{u.Uid + "\n" + u.Gid + "\n", "CapEff:\t0000000000000000", "CapBnd:\t0000000000000000", "NoNewPrivs:\t1", home} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if out, err := sh("touch /etc/whacp-escape-test", Opts{}); err == nil {
		os.Remove("/etc/whacp-escape-test")
		t.Errorf("the account wrote to /etc: %s", out)
	}
	if _, err := os.Stat(filepath.Join("/etc", "whacp-escape-test")); err == nil {
		t.Error("/etc/whacp-escape-test exists")
	}

	// Arguments and environment reach the program unchanged: $, %, quotes,
	// several lines.
	script := "printf '%s|' \"$WHACP_T\" '$HOME' '100%' \"a\\$b\"\necho\necho line2"
	out, err = sh(script, Opts{Env: []string{"WHACP_T=v$1%n"}})
	if err != nil || out != "v$1%n|$HOME|100%|a$b|\nline2\n" {
		t.Errorf("arguments changed: %v %q", err, out)
	}

	// Binary standard input and output (backup and restore stream tar).
	os.WriteFile(filepath.Join(home, "f.bin"), []byte{0, 1, 2, 255, '\n', 0}, 0o644)
	exec.Command("chown", name+":", filepath.Join(home, "f.bin")).Run()
	var archive bytes.Buffer
	if _, err := r.RunWith(ctx, "tar", Opts{As: as, Dir: home, Stdout: &archive}, "-C", home, "-cf", "-", "f.bin"); err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(bytes.NewReader(archive.Bytes()))
	if h, err := tr.Next(); err != nil || h.Name != "f.bin" {
		t.Fatalf("archive: %v", err)
	}
	restore := filepath.Join(home, "restore")
	os.Mkdir(restore, 0o755)
	exec.Command("chown", name+":", restore).Run()
	if out, err := r.RunWith(ctx, "tar", Opts{As: as, Dir: restore, Stdin: bytes.NewReader(archive.Bytes())}, "-C", restore, "--no-same-owner", "-xf", "-"); err != nil {
		t.Fatalf("extract: %v %s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(restore, "f.bin")); !bytes.Equal(b, []byte{0, 1, 2, 255, '\n', 0}) {
		t.Errorf("round trip changed the data: %v", b)
	}
	if fi, err := os.Stat(filepath.Join(restore, "f.bin")); err == nil {
		if o, _ := user.LookupId(strconv.Itoa(int(fi.Sys().(*syscall.Stat_t).Uid))); o != nil && o.Username != name {
			t.Errorf("restored file owned by %s", o.Username)
		}
	}

	// Exit status.
	_, err = sh("exit 3", Opts{})
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Errorf("exit status: %v", err)
	}

	// A timeout stops the command and its unit.
	start := time.Now()
	_, err = sh("sleep 60", Opts{Timeout: 2 * time.Second})
	if err == nil || time.Since(start) > 20*time.Second {
		t.Errorf("timeout not enforced: %v after %s", err, time.Since(start))
	}
	time.Sleep(time.Second)
	if out, _ := exec.Command("pgrep", "-u", name, "sleep").Output(); len(out) > 0 {
		t.Errorf("the command survived its timeout: %s", out)
	}
}
