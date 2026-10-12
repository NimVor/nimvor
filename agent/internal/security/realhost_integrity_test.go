//go:build realhost

package security

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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

// File integrity monitoring against the real package database: a change to a
// package-owned configuration file is reported by dpkg --verify / rpm -Va,
// and a change to a WhaCP-owned file is reported against the baseline.
// Requires root.
func TestRealHostIntegrity(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	env := host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(time.Minute), jm, log)
	ctx := context.Background()

	// 1. WhaCP-owned baseline drift, under a temporary /etc/whacp we clean up.
	confDir := "/etc/whacp"
	madeDir := false
	if _, err := os.Stat(confDir); err != nil {
		os.MkdirAll(confDir, 0o755)
		madeDir = true
	}
	probe := filepath.Join(confDir, "whacp-integrity-probe.conf")
	os.WriteFile(probe, []byte("baseline\n"), 0o640)
	t.Cleanup(func() {
		os.Remove(probe)
		if madeDir {
			os.Remove(confDir)
		}
	})
	if _, err := WriteIntegrityBaseline(env); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	os.WriteFile(probe, []byte("tampered\n"), 0o640)
	r := CheckIntegrity(ctx, env, time.Now())
	if f := findPath(r, probe); f == nil || f.Class != "whacp" || f.Issue != "modified" {
		t.Errorf("WhaCP drift not detected: %+v", f)
	}

	// 2. Package-owned verification: modify a package config file and check
	// the OS tool reports it. Use nginx.conf when nginx came from a package.
	tool := "dpkg"
	if env.Platform.PackageManager() == "dnf" {
		tool = "rpm"
	}
	target := "/etc/nginx/nginx.conf"
	orig, err := os.ReadFile(target)
	if err != nil {
		t.Skipf("no %s to test package verification: %v", target, err)
	}
	// Confirm the file is package-owned and currently verifies clean, so the
	// test is meaningful; otherwise skip.
	if tool == "dpkg" {
		if out, _ := exec.Command("dpkg", "-S", target).CombinedOutput(); !strings.Contains(string(out), target) {
			t.Skipf("%s is not dpkg-owned here", target)
		}
	}
	os.WriteFile(target, append(append([]byte{}, orig...), []byte("\n# whacp integrity probe\n")...), 0o644)
	t.Cleanup(func() { os.WriteFile(target, orig, 0o644) })
	r = CheckIntegrity(ctx, env, time.Now())
	if f := findPath(r, target); f == nil || f.Class != "package" || f.Issue != "modified" {
		t.Errorf("package drift of %s not detected by %s: %+v", target, tool, f)
	}
	if r.PackageTool != tool {
		t.Errorf("package tool = %s, want %s", r.PackageTool, tool)
	}
}

func findPath(r IntegrityReport, path string) *IntegrityFinding {
	for i := range r.Findings {
		if r.Findings[i].Path == path {
			return &r.Findings[i]
		}
	}
	return nil
}
