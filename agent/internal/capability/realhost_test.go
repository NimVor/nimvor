//go:build realhost

package capability

import (
	"context"
	"io"
	"log/slog"
	"os"
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
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	return host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
}

// What this machine has, as detected: every state must come with its
// evidence (a module path for installed/functional, a reason otherwise),
// and a module the configuration loads must resolve its libraries.
func TestRealHostCapabilities(t *testing.T) {
	e := realEnv(t)
	if !e.Runner.Available("nginx") {
		t.Skip("no nginx")
	}
	for id, s := range Modules(context.Background(), e, true) {
		t.Logf("%s: %s (package %q) %s %+v %+v", id, s.State, s.Package, s.Reason, s.Modules, s.Libraries)
		switch s.State {
		case Functional, Installed:
			if len(s.Modules) == 0 || (s.Modules[0].Path == "" && !s.Modules[0].Static) {
				t.Errorf("%s: %s without a module", id, s.State)
			}
		case Missing:
			if s.Reason == "" {
				t.Errorf("%s: missing without a reason", id)
			}
		}
		for _, l := range s.Libraries {
			if s.State == Functional && l.Path == "" {
				t.Errorf("%s works but %s does not resolve", id, l.Name)
			}
		}
	}
}
