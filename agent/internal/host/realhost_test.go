//go:build realhost

package host_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// The PHP-FPM service runs in WhaCP's sandbox: its master and its pools'
// workers cannot gain privileges (NoNewPrivileges) and have no capabilities
// beyond those the master needs. The drop-in stays: the tests after this
// one (webmail, WordPress) serve their PHP from the sandboxed service.
func TestRealHostFPMSandbox(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if exec.Command("systemctl", "is-system-running").Run() != nil {
		if out, _ := exec.Command("systemctl", "is-system-running").Output(); strings.TrimSpace(string(out)) == "offline" {
			t.Skip("no systemd")
		}
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(time.Minute), jm, log)
	ctx := context.Background()
	v := testhost.PHPVersion()
	unit := env.M.FPMUnit(v)
	EnsurePHPSocketDirNow(t, env)
	if _, err := env.EnsureFPMSandbox(ctx, v); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("systemctl", "restart", unit).CombinedOutput(); err != nil {
		j, _ := exec.Command("journalctl", "-u", unit, "-n", "40", "--no-pager").CombinedOutput()
		t.Fatalf("%s does not start in its sandbox: %v %s\n%s", unit, err, out, j)
	}
	if changed, err := env.EnsureFPMSandbox(ctx, v); err != nil || changed {
		t.Errorf("second ensure: changed=%v %v", changed, err)
	}
	show, _ := exec.Command("systemctl", "show", unit, "-p", "NoNewPrivileges", "-p", "ProtectSystem", "-p", "MainPID").Output()
	if !strings.Contains(string(show), "NoNewPrivileges=yes") || !strings.Contains(string(show), "ProtectSystem=full") {
		t.Fatalf("unit properties:\n%s", show)
	}
	var master string
	for _, l := range strings.Split(string(show), "\n") {
		if strings.HasPrefix(l, "MainPID=") {
			master = strings.TrimPrefix(l, "MainPID=")
		}
	}
	// The master and its workers (the distribution's default pool).
	pids := []string{master}
	time.Sleep(time.Second)
	if out, err := exec.Command("pgrep", "-P", master).Output(); err == nil {
		pids = append(pids, strings.Fields(string(out))...)
	}
	if len(pids) < 2 {
		t.Logf("no worker of %s found; checking the master only", unit)
	}
	const capSysAdmin, capNetAdmin, capSysModule = 21, 12, 16
	for _, pid := range pids {
		b, err := os.ReadFile("/proc/" + pid + "/status")
		if err != nil {
			t.Fatalf("pid %s: %v", pid, err)
		}
		st := string(b)
		if !strings.Contains(st, "NoNewPrivs:\t1") {
			t.Errorf("pid %s can gain privileges:\n%s", pid, st)
		}
		for _, l := range strings.Split(st, "\n") {
			if strings.HasPrefix(l, "CapBnd:\t") {
				bnd, _ := strconv.ParseUint(strings.TrimPrefix(l, "CapBnd:\t"), 16, 64)
				for _, c := range []uint{capSysAdmin, capNetAdmin, capSysModule} {
					if bnd&(1<<c) != 0 {
						t.Errorf("pid %s keeps capability %d (%s)", pid, c, l)
					}
				}
			}
		}
	}
}

// EnsurePHPSocketDirNow creates the PHP-FPM socket folder (RHEL with only
// Remi's PHP) as website changes do.
func EnsurePHPSocketDirNow(t *testing.T, env *host.Env) {
	set := env.Changes("test")
	env.EnsurePHPSocketDir(set)
	if _, err := set.Apply(); err != nil {
		t.Fatal(err)
	}
}
