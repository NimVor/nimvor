//go:build realhost

package security

import (
	"bytes"
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

func fixEnv(t *testing.T) *host.Env {
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(time.Minute), jm, log)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, fixPaths()...)
	return env
}

// The overview's fixes on a real kernel: the kernel settings take effect
// and the undo puts the earlier values back; hidepid hides root's
// processes from other accounts and is taken back.
func TestRealHostSecurityFixes(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	env := fixEnv(t)
	ctx := context.Background()

	before, _ := readSysctl(env, "kernel.dmesg_restrict")
	writeKernel := func(k, v string) { os.WriteFile("/proc/sys/"+strings.ReplaceAll(k, ".", "/"), []byte(v), 0o644) }
	writeKernel("kernel.dmesg_restrict", "0")
	t.Cleanup(func() { writeKernel("kernel.dmesg_restrict", before) })
	changed, err := FixKernel(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := readSysctl(env, "kernel.dmesg_restrict"); v != "1" || !strings.Contains(strings.Join(changed, ","), "kernel.dmesg_restrict") {
		t.Fatalf("dmesg_restrict = %s after the fix (changed %v)", v, changed)
	}
	if c := sysctlCheck(env); c.Status != "pass" || c.Fix != "undo" {
		t.Errorf("check after the fix: %+v", c)
	}
	if err := UndoKernel(ctx, env); err != nil {
		t.Fatal(err)
	}
	if v, _ := readSysctl(env, "kernel.dmesg_restrict"); v != "0" {
		t.Errorf("dmesg_restrict = %s after the undo", v)
	}
	if _, err := os.Stat(SysctlFile); err == nil {
		t.Error(SysctlFile + " kept after the undo")
	}

	fstab, _ := os.ReadFile("/etc/fstab")
	t.Cleanup(func() {
		HideProcesses(context.Background(), env, false)
		os.WriteFile("/etc/fstab", fstab, 0o644)
	})
	seesPID1 := func() bool {
		return exec.Command("runuser", "-u", "nobody", "--", "test", "-e", "/proc/1/cmdline").Run() == nil
	}
	if !seesPID1() {
		t.Skip("/proc already hides processes here")
	}
	if err := HideProcesses(ctx, env, true); err != nil {
		t.Fatal(err)
	}
	if seesPID1() {
		m, _ := os.ReadFile("/proc/mounts")
		t.Fatalf("nobody still sees PID 1:\n%s", m)
	}
	if b, _ := os.ReadFile("/proc/mounts"); procCheck(string(b)).Status != "pass" {
		t.Errorf("check after the fix: %+v", procCheck(string(b)))
	}
	if b, _ := os.ReadFile("/etc/fstab"); !bytes.Contains(b, []byte("hidepid=2")) {
		t.Errorf("fstab:\n%s", b)
	}
	// Service management (systemd asks polkit) still works for root.
	if out, err := exec.Command("systemctl", "is-system-running").CombinedOutput(); err != nil && !strings.Contains(string(out), "degraded") && !strings.Contains(string(out), "running") {
		t.Errorf("systemctl after hidepid: %v %s", err, out)
	}
	if err := HideProcesses(ctx, env, false); err != nil {
		t.Fatal(err)
	}
	if !seesPID1() {
		t.Error("processes still hidden after the undo")
	}
}

// Automatic security updates: the package is installed when missing and
// the updates are switched on (RHEL: the timer runs with WhaCP's security
// configuration), then off again.
func TestRealHostAutoUpdates(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if os.Getenv("WHACP_NETWORK") == "" {
		t.Skip("installs a package: set WHACP_NETWORK=1")
	}
	env := fixEnv(t)
	ctx := context.Background()
	var log bytes.Buffer
	t.Cleanup(func() { SetAutoUpdates(context.Background(), env, io.Discard, false) })
	if err := SetAutoUpdates(ctx, env, &log, true); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !AutoUpdatesManaged(env) {
		t.Fatal("not managed after turning it on")
	}
	if env.Platform.PackageManager() == "dnf" {
		if !env.UnitActive(ctx, dnfAutoTimer) {
			t.Fatalf("%s is not active", dnfAutoTimer)
		}
		out, _ := exec.Command("systemctl", "cat", dnfAutoUnit).CombinedOutput()
		if !strings.Contains(string(out), dnfAutoConf) {
			t.Fatalf("the service does not use WhaCP's configuration:\n%s", out)
		}
		// The configuration is one dnf-automatic accepts.
		if out, err := exec.Command("dnf-automatic", dnfAutoConf, "--help").CombinedOutput(); err != nil {
			t.Errorf("dnf-automatic --help with the configuration: %v %s", err, out)
		}
	} else if out, err := exec.Command("apt-config", "dump", "APT::Periodic::Unattended-Upgrade").CombinedOutput(); err != nil || !strings.Contains(string(out), `"1"`) {
		t.Fatalf("apt-config: %v %s", err, out)
	}
	if err := SetAutoUpdates(ctx, env, io.Discard, false); err != nil {
		t.Fatal(err)
	}
	if AutoUpdatesManaged(env) || (env.Platform.PackageManager() == "dnf" && env.UnitActive(ctx, dnfAutoTimer)) {
		t.Error("still on after turning it off")
	}
}
