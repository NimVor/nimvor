package execx

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeSystemdRun stands in for systemd-run: it enters the working
// directory and runs the command after "--" (the pipe wrapper) with no
// standard streams of its own. A missing working directory fails before the
// command starts, as with systemd.
const fakeSystemdRun = `#!/bin/bash
while [ $# -gt 0 ]; do
  case $1 in
    --working-directory=*) cd "${1#--working-directory=}" || exit 200 ;;
    --) shift; break ;;
  esac
  shift
done
args=(); for a in "$@"; do args+=("${a//\$\$/\$}"); done
exec "${args[@]}" </dev/null >/dev/null 2>/dev/null
`

func fakeRunner(t *testing.T) *SystemRunner {
	t.Helper()
	d := t.TempDir()
	p := filepath.Join(d, "systemd-run")
	os.WriteFile(p, []byte(fakeSystemdRun), 0o755)
	old := RunDir
	RunDir = filepath.Join(d, "run")
	t.Cleanup(func() { RunDir = old })
	r := NewSystemRunner(10 * time.Second)
	r.paths["systemd-run"] = p
	return r
}

func TestTransientStreams(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	r := fakeRunner(t)
	sh := r.paths["sh"]
	as := Credential{UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Home: t.TempDir()}
	ctx := context.Background()

	// Binary data in and out, stderr captured, exit status kept.
	data := make([]byte, 3<<20)
	rand.Read(data)
	var got bytes.Buffer
	out, err := r.runTransient(ctx, sh, as.Home, nil, Opts{As: &as, Stdin: bytes.NewReader(data), Stdout: &got}, time.Minute, []string{"-c", "cat; echo to-stderr >&2; exit 3"})
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("exit status: %v %s", err, out)
	}
	if !bytes.Equal(got.Bytes(), data) {
		t.Fatalf("stdin/stdout changed %d bytes of data (got %d)", len(data), got.Len())
	}
	if !strings.Contains(string(out), "to-stderr") {
		t.Errorf("stderr not captured: %q", out)
	}

	// Combined output without stdin.
	out, err = r.runTransient(ctx, sh, as.Home, nil, Opts{As: &as}, time.Minute, []string{"-c", "echo out; echo err >&2"})
	if err != nil || !strings.Contains(string(out), "out\n") || !strings.Contains(string(out), "err\n") {
		t.Errorf("combined output: %v %q", err, out)
	}

	// A unit that fails before opening its streams does not hang the agent.
	// (like systemd's 200/CHDIR when the working directory is missing).
	done := make(chan error, 1)
	go func() {
		_, err := r.runTransient(ctx, sh, "/nonexistent-whacp", nil, Opts{As: &as, Stdin: strings.NewReader("x")}, time.Minute, []string{"-c", "true"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.As(err, &ee) || ee.ExitCode() != 200 {
			t.Errorf("failed start: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hung waiting for a unit that never started")
	}
	// Nothing left behind in the runtime directory.
	if left, _ := os.ReadDir(RunDir); len(left) != 0 {
		t.Errorf("left in %s: %v", RunDir, left)
	}
}

// The agent (root) never follows a link planted in place of a pipe, nor
// opens anything that is not a pipe.
func TestOpenPipeRefusesLinksAndFiles(t *testing.T) {
	d := t.TempDir()
	secret := filepath.Join(d, "secret")
	os.WriteFile(secret, []byte("x"), 0o600)
	os.Symlink(secret, filepath.Join(d, "link"))
	fifo := filepath.Join(d, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(d, "link"), secret} {
		if f, err := openPipe(p, os.O_RDONLY|syscall.O_NONBLOCK); err == nil {
			f.Close()
			t.Errorf("opened %s", p)
		}
	}
	f, err := openPipe(fifo, os.O_RDONLY|syscall.O_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// Each sendmail on the machine is covered once, whatever links lead to it
// (Debian: /usr/lib/sendmail -> ../sbin/sendmail; RHEL: alternatives).
func TestSendmailBinds(t *testing.T) {
	d := t.TempDir()
	real := filepath.Join(d, "sendmail.postfix")
	os.WriteFile(real, []byte("x"), 0o755)
	os.Symlink(real, filepath.Join(d, "sbin-sendmail"))
	os.Symlink(filepath.Join(d, "sbin-sendmail"), filepath.Join(d, "lib-sendmail"))
	oldPaths, oldShim := sendmailPaths, SendmailShim
	t.Cleanup(func() { sendmailPaths, SendmailShim = oldPaths, oldShim })
	sendmailPaths = []string{filepath.Join(d, "sbin-sendmail"), filepath.Join(d, "lib-sendmail"), filepath.Join(d, "missing")}

	SendmailShim = ""
	if b := sendmailBinds(); b != nil {
		t.Errorf("binds without a shim: %v", b)
	}
	SendmailShim = "/usr/local/bin/whacp-agent"
	if b := sendmailBinds(); len(b) != 1 || b[0] != "/usr/local/bin/whacp-agent:"+real {
		t.Errorf("binds = %v", b)
	}
}
