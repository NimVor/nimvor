//go:build realhost

package backup

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

// Restore drill against the real /etc/nginx and /etc/php: a good backup is
// restored, a backup with an invalid Nginx configuration is refused and the
// running configuration is put back. Requires root and nginx.
func TestRealHostServerRestoreDrill(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("nginx"); err != nil {
		t.Skip("needs nginx")
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		t.Skipf("the current nginx configuration is invalid: %s", out)
	}
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.BackupDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	env := host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(30*time.Second), jm, log)
	s := Service{Env: env}
	ctx := context.Background()
	var out bytes.Buffer

	orig, _ := os.ReadFile("/etc/nginx/nginx.conf")
	t.Cleanup(func() {
		os.WriteFile("/etc/nginx/nginx.conf", orig, 0o644)
		os.Remove("/etc/nginx/conf.d/zz-nimvor-drill.conf")
	})
	good, err := s.CreateServer(ctx, true, "drill good", nil, true, &out)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile("/etc/nginx/conf.d/zz-nimvor-drill.conf", []byte("this is not nginx syntax;\n"), 0o644)
	bad, err := s.CreateServer(ctx, true, "drill bad", nil, true, &out)
	if err != nil {
		t.Fatal(err)
	}
	os.Remove("/etc/nginx/conf.d/zz-nimvor-drill.conf")

	// 1. The broken backup is refused and nothing changes.
	_, err = s.RestoreServer(ctx, ServerRestoreRequest{ID: bad.ID, Components: []string{"nginx"}, Confirm: true}, &out)
	if err == nil || !strings.Contains(err.Error(), "put back") {
		t.Fatalf("restore of an invalid config: %v\n%s", err, out.String())
	}
	if _, err := os.Stat("/etc/nginx/conf.d/zz-nimvor-drill.conf"); err == nil {
		t.Fatal("the invalid file was left in place")
	}
	if o, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		t.Fatalf("nginx -t after rollback: %s", o)
	}

	// 2. A hand edit is undone by restoring the good backup (nginx + php).
	os.WriteFile("/etc/nginx/nginx.conf", append(append([]byte{}, orig...), []byte("\n# drill edit\n")...), 0o644)
	res, err := s.RestoreServer(ctx, ServerRestoreRequest{ID: good.ID, Components: []string{"nginx", "php"}, Confirm: true}, &out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	now, _ := os.ReadFile("/etc/nginx/nginx.conf")
	if !bytes.Equal(now, orig) {
		t.Error("nginx.conf was not restored")
	}
	if o, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		t.Fatalf("nginx -t after restore: %s", o)
	}
	if res["safety_backup"] == nil {
		t.Error("no safety backup")
	}
	if m, _ := os.ReadDir("/etc"); m != nil {
		for _, e := range m {
			if strings.Contains(e.Name(), ".nimvor-old-") {
				t.Errorf("leftover /etc/%s", e.Name())
			}
		}
	}
}
