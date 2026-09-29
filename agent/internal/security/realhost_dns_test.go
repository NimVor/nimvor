//go:build realhost

package security

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Requires BIND's tools: the distribution's configuration is not an open
// resolver, and the check reads it.
func TestRealHostDNSRecursionCheck(t *testing.T) {
	if _, err := exec.LookPath("named-checkconf"); err != nil {
		t.Skip("needs BIND (named-checkconf)")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(30*time.Second), jm, log)
	c, ok := dnsRecursionCheck(context.Background(), env)
	if !ok || c.Status != "pass" {
		t.Fatalf("%+v", c)
	}
}
