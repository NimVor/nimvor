//go:build realhost

package security

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

// The scan on a real machine: the package lists are read, it finishes in
// reasonable time, and the distribution's own set-user-ID programs (su,
// passwd, sudo, ...) are not reported.
func TestRealHostScan(t *testing.T) {
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(120*time.Second), jm, log)
	owned, err := OwnedFiles(context.Background(), env)
	if err != nil || len(owned) < 1000 {
		t.Fatalf("package files: %d %v", len(owned), err)
	}
	r := Scan(context.Background(), env, owned, time.Now())
	t.Logf("%d entries in %.1fs, truncated=%v", r.Entries, r.Seconds, r.Truncated)
	for _, f := range r.Setuid {
		t.Logf("setuid: %s (%s)", f.Path, f.Detail)
		for _, p := range []string{"/usr/bin/su", "/usr/bin/passwd", "/usr/bin/sudo", "/usr/bin/mount"} {
			if f.Path == p {
				t.Errorf("a package's program reported: %s", p)
			}
		}
	}
	for _, f := range append(append(r.Caps, r.Writable...), r.Persist...) {
		t.Logf("%s: %s", f.Path, f.Detail)
	}
	if r.Seconds > 600 {
		t.Errorf("too slow: %.0fs", r.Seconds)
	}
}
