//go:build realhost

package egress

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// A website account cannot reach the cloud metadata service (refused at
// once by the table); root and other accounts are not limited, and the
// table survives a restart of its service (as at boot).
func TestRealHostEgressMetadata(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.SitesRoot = "/home/whacp-egress-test"
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(time.Minute), jm, log)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, cfg.StateDir+"/**")
	AllowPaths(env)
	ctx := context.Background()
	if !env.Runner.Available("nft") {
		t.Fatal("nft is not installed")
	}
	const name = "whacpegress"
	if out, err := exec.Command("useradd", "-M", "-d", cfg.SitesRoot+"/egress.test", "-s", "/usr/sbin/nologin", name).CombinedOutput(); err != nil {
		t.Fatalf("useradd: %v %s", err, out)
	}
	// The folder for the rules: removed afterwards when the test created it
	// (an /etc/whacp left behind reads as an unfinished installation).
	_, statErr := os.Stat("/etc/whacp")
	createdDir := os.IsNotExist(statErr)
	t.Cleanup(func() {
		Set(context.Background(), env, Settings{MetadataAllowed: true})
		exec.Command("userdel", name).Run()
		if createdDir {
			os.Remove("/etc/whacp")
		}
	})
	if err := Refresh(ctx, env); err != nil {
		t.Fatal(err)
	}
	connect := func(as string) (string, time.Duration) {
		cmd := "exec 3<>/dev/tcp/169.254.169.254/80"
		var c *exec.Cmd
		if as == "" {
			c = exec.Command("timeout", "3", "bash", "-c", cmd)
		} else {
			c = exec.Command("runuser", "-u", as, "--", "timeout", "3", "bash", "-c", cmd)
		}
		start := time.Now()
		out, _ := c.CombinedOutput()
		return string(out), time.Since(start)
	}
	if out, took := connect(name); !strings.Contains(out, "Connection refused") || took > 2500*time.Millisecond {
		t.Errorf("website account reached the metadata address (%s): %s", took, out)
	}
	if out, _ := connect(""); strings.Contains(out, "Connection refused") {
		t.Errorf("root was refused too: %s", out)
	}
	if b, _ := exec.Command("nft", "list", "table", "inet", "whacp_egress").Output(); !strings.Contains(string(b), "cloud metadata") || strings.Contains(string(b), "counter packets 0 bytes 0 reject with icmp port-unreachable comment \"cloud metadata\"") {
		t.Errorf("table:\n%s", b)
	}
	// As at boot: the service loads the table again.
	exec.Command("nft", "delete", "table", "inet", "whacp_egress").Run()
	if out, err := exec.Command("systemctl", "restart", Unit).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %s", Unit, err, out)
	}
	if out, _ := connect(name); !strings.Contains(out, "Connection refused") {
		t.Errorf("not refused after the service loaded the table again: %s", out)
	}
	if out, _ := exec.Command("systemctl", "is-enabled", Unit).Output(); strings.TrimSpace(string(out)) != "enabled" {
		t.Errorf("%s is not enabled: %s", Unit, out)
	}
}
