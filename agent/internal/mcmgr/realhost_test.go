//go:build realhost

package mcmgr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Requires root, systemd and the memcached package. Changes the memory
// limit for real, checks it through the stats protocol, then restores the
// original file.
func TestRealHostMemcached(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if err := exec.Command("systemctl", "is-active", "--quiet", "memcached").Run(); err != nil {
		t.Skip("needs a running memcached.service under systemd")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(30*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	orig, _ := os.ReadFile(env.M.MemcachedConfig)
	t.Cleanup(func() {
		os.WriteFile(env.M.MemcachedConfig, orig, 0o644)
		exec.Command("systemctl", "restart", "memcached").Run()
	})
	do := func(name, params string) map[string]any {
		t.Helper()
		a, _ := r.Lookup(name)
		res, err := a.Handler(context.Background(), json.RawMessage(params))
		if err != nil {
			t.Fatalf("%s: %+v", name, err)
		}
		return res.(map[string]any)
	}
	do("memcached.configure", `{"memory_mb":96,"max_conns":512,"listen":["127.0.0.1"],"confirm_edit_package_config":true}`)
	st := do("memcached.status", `{}`)
	if st["error"] != nil {
		t.Fatalf("status: %v", st["error"])
	}
	if got := st["runtime"].(map[string]string)["maxbytes"]; got != "100663296" {
		t.Errorf("maxbytes = %s, want 96 MiB", got)
	}
	if got := st["runtime"].(map[string]string)["maxconns"]; got != "512" {
		t.Errorf("maxconns = %s", got)
	}
	do("memcached.flush", `{"confirm":"flush"}`)
}
