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
	"testing"
	"time"
)

// fakeSystemdRun stands in for systemd-run: like PID 1 it opens the
// StandardInput/Output/Error files named in the properties and runs the
// command after "--" with them. A missing working directory fails before
// the streams are opened, as with systemd.
const fakeSystemdRun = `#!/bin/bash
in=/dev/null; out=; err=
while [ $# -gt 0 ]; do
  case $1 in
    --property=StandardInput=file:*) in=${1#--property=StandardInput=file:} ;;
    --property=StandardOutput=file:*) out=${1#--property=StandardOutput=file:} ;;
    --property=StandardError=file:*) err=${1#--property=StandardError=file:} ;;
    --working-directory=*) cd "${1#--working-directory=}" || exit 200 ;;
    --) shift; break ;;
  esac
  shift
done
args=(); for a in "$@"; do args+=("${a//\$\$/\$}"); done
"${args[@]}" <"$in" >"$out" 2>"$err"
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
	as := Credential{UID: 1000, GID: 1000, Home: t.TempDir()}
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
