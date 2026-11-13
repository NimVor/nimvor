//go:build realhost

package intrusion

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

func realEnv(t *testing.T) *host.Env {
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsys := filesystem.Host()
	env := host.New(cfg, fsys, platform.Detect(fsys), execx.NewSystemRunner(time.Minute), jm, log)
	AllowPaths(env)
	return env
}

func as(user string, args ...string) (string, error) {
	out, err := exec.Command("runuser", append([]string{"-u", user, "--"}, args...)...).CombinedOutput()
	return string(out), err
}

// A protected website account is refused a blacklisted program and nothing
// else; taking the protection away (or the program off the list) lets it run
// again. Needs root and the ACL tools (installed when missing).
func TestRealHostIntrusionBlacklist(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("setfacl"); err != nil {
		for _, c := range [][]string{{"apt-get", "install", "-y", "acl"}, {"dnf", "install", "-y", "acl"}} {
			if _, err := exec.LookPath(c[0]); err == nil {
				if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
					t.Fatalf("installing acl: %v\n%s", err, out)
				}
				break
			}
		}
	}
	if _, err := exec.LookPath("setfacl"); err != nil {
		t.Fatalf("setfacl is not installed and could not be installed")
	}
	if out, err := exec.Command("useradd", "-M", "-U", "-s", "/bin/sh", "-u", "1793", "-d", "/home/nimvor/rtintr.test", "rtintr").CombinedOutput(); err != nil && !strings.Contains(string(out), "exists") {
		t.Fatalf("useradd: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("userdel", "rtintr").Run() })
	tru, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	denied, ok := "/usr/local/bin/nimvor-rt-denied", "/usr/local/bin/nimvor-rt-ok"
	for _, p := range []string{denied, ok} {
		if out, err := exec.Command("cp", tru, p).CombinedOutput(); err != nil {
			t.Fatalf("cp: %v %s", err, out)
		}
		os.Chmod(p, 0o755)
		p := p
		t.Cleanup(func() { os.Remove(p) })
	}
	env := realEnv(t)
	ctx := context.Background()
	t.Cleanup(func() {
		Configure(ctx, env, false, nil)
		SetBlacklist(ctx, env, nil)
	})

	for _, p := range []string{denied, ok} {
		if out, err := as("rtintr", p); err != nil {
			t.Fatalf("%s before protection: %v\n%s", p, err, out)
		}
	}
	if err := SetBlacklist(ctx, env, []string{"nimvor-rt-denied"}); err != nil {
		t.Fatal(err)
	}
	if err := Configure(ctx, env, true, map[string]Setting{"rtintr": {Protect: true}}); err != nil {
		t.Fatal(err)
	}
	out, err := as("rtintr", denied)
	if err == nil || !strings.Contains(out, "Permission denied") {
		t.Fatalf("the blacklisted program ran or failed another way: %v\n%s", err, out)
	}
	if out, err := as("rtintr", ok); err != nil {
		t.Fatalf("a program that is not on the blacklist was refused: %v\n%s", err, out)
	}
	// Root and other accounts are not affected.
	if out, err := exec.Command(denied).CombinedOutput(); err != nil {
		t.Fatalf("root was refused: %v\n%s", err, out)
	}

	// A package update replaces the file and drops the entry; Reconcile sets it again.
	os.Remove(denied)
	exec.Command("cp", tru, denied).Run()
	os.Chmod(denied, 0o755)
	if out, err := as("rtintr", denied); err != nil {
		t.Fatalf("a replaced file should be open again until reconciled: %v\n%s", err, out)
	}
	if err := Reconcile(ctx, env); err != nil {
		t.Fatal(err)
	}
	if out, err := as("rtintr", denied); err == nil {
		t.Fatalf("not refused again after Reconcile: %s", out)
	}

	// Off again: runs.
	if err := Configure(ctx, env, true, map[string]Setting{}); err != nil {
		t.Fatal(err)
	}
	if out, err := as("rtintr", denied); err != nil {
		t.Fatalf("still refused after protection was turned off: %v\n%s", err, out)
	}
}
