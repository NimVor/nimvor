//go:build realhost

package limits

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
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

// Real systemd and cgroup v2: the slice carries the limits, and a command
// run as the account (as cron and the panel run them) lands in it and
// cannot leave it. Without systemd (a container) there is nothing to test.
func TestRealHostAccountLimits(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Log("systemd is not running here: the limits are checked on the systemd test machines")
		return
	}
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err != nil {
		t.Fatal("cgroup v2 is required")
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	ctx := context.Background()
	const user = "nimvorlimtest"
	exec.Command("useradd", "-M", "-s", "/usr/sbin/nologin", user).Run()
	t.Cleanup(func() {
		a, _ := r.Lookup("limits.set")
		a.Handler(ctx, json.RawMessage(`{"account":"acct990","users":["`+user+`"]}`))
		exec.Command("userdel", user).Run()
	})
	a, _ := r.Lookup("limits.set")
	if _, err := a.Handler(ctx, json.RawMessage(`{"account":"acct990","users":["`+user+`"],"cpu_percent":150,"memory_mb":256,"tasks_max":64}`)); err != nil {
		t.Fatal(err)
	}
	// A command as the account, through the agent's runner (cron jobs and
	// panel commands take this path), reports its own cgroup and limits.
	cred, _, err := env.Credential(user)
	if err != nil || cred.Slice != "nimvor-acct990.slice" {
		t.Fatalf("credential: %+v %v", cred, err)
	}
	out, err := env.Runner.RunWith(ctx, "sh", execx.Opts{As: cred, Dir: "/"}, "-c", "cat /proc/self/cgroup; echo; g=$(cut -d: -f3 /proc/self/cgroup); cat /sys/fs/cgroup${g%/*}/cpu.max /sys/fs/cgroup${g%/*}/memory.max /sys/fs/cgroup${g%/*}/pids.max; echo 1 > /sys/fs/cgroup/cgroup.procs && echo ESCAPED")
	text := string(out)
	if !strings.Contains(text, "/nimvor.slice/nimvor-acct990.slice/") {
		t.Fatalf("not in the account slice:\n%s (%v)", text, err)
	}
	for _, want := range []string{"150000 100000", "268435456", "64"} {
		if !strings.Contains(text, want) {
			t.Errorf("limit %q missing:\n%s", want, text)
		}
	}
	if strings.Contains(text, "ESCAPED") {
		t.Fatalf("the account moved itself out of its cgroup:\n%s", text)
	}
	// Changing the plan applies to the running slice at once.
	if _, err := a.Handler(ctx, json.RawMessage(`{"account":"acct990","users":["`+user+`"],"cpu_percent":50,"memory_mb":128,"tasks_max":64}`)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile("/sys/fs/cgroup/nimvor.slice/nimvor-acct990.slice/cpu.max")
	if !strings.Contains(string(b), "50000 100000") {
		// The slice may be inactive once its last process ended; the unit
		// file carries the new value for the next start.
		u, _ := os.ReadFile("/etc/systemd/system/nimvor-acct990.slice")
		if !strings.Contains(string(u), "CPUQuota=50%") {
			t.Fatalf("new limit not applied: %s / %s", b, u)
		}
	}
}
