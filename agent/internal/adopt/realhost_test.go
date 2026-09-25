//go:build realhost

package adopt

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
	"github.com/mchkhawarshahzad/whacp/agent/internal/sites"
)

// systemctl is simulated (containers lack systemd); everything else is real.
type hybrid struct{ real *execx.SystemRunner }

func (h hybrid) Available(id string) bool { return id == "systemctl" || h.real.Available(id) }
func (h hybrid) Run(ctx context.Context, id string, args ...string) ([]byte, error) {
	return h.RunWith(ctx, id, execx.Opts{}, args...)
}
func (h hybrid) RunWith(ctx context.Context, id string, o execx.Opts, args ...string) ([]byte, error) {
	if id == "systemctl" {
		if len(args) > 0 && args[0] == "is-active" {
			return []byte("active"), nil
		}
		return nil, nil
	}
	return h.real.RunWith(ctx, id, o, args...)
}

// Imports a hand-written Nginx site into WhaCP with the real nginx -t,
// php-fpm8.3 -t and useradd, then undoes it.
func TestRealHostImport(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	// Debian: sites-available plus a link; RHEL: a file in conf.d.
	avail, link := "/etc/nginx/sites-available/imp.whacp.test", "/etc/nginx/sites-enabled/imp.whacp.test"
	if _, err := os.Stat("/etc/nginx/sites-enabled"); err != nil {
		avail, link = "/etc/nginx/conf.d/imp.whacp.test.conf", ""
	}
	os.MkdirAll("/var/www/imp.whacp.test/public", 0o755)
	os.WriteFile("/var/www/imp.whacp.test/public/index.php", []byte("<?php echo 'imported';"), 0o644)
	os.WriteFile(avail, []byte("server {\n    listen 80;\n    server_name imp.whacp.test;\n    root /var/www/imp.whacp.test/public;\n    index index.php;\n}\n"), 0o644)
	if link != "" {
		os.Symlink(avail, link)
	}
	cfg := config.Default()
	cfg.SitesRoot = "/home/whacp-imp"
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	root := filesystem.Host()
	env := host.New(cfg, root, platform.Detect(root), hybrid{execx.NewSystemRunner(30 * time.Second)}, jm, log)
	s := Service{Env: env, Sites: &sites.Manager{Env: env}}
	req := Request{Spec: sites.Spec{Domain: "imp.whacp.test", User: "whacpimp", Kind: "php", PHPVersion: "8.3", Enabled: true}, Source: "/var/www/imp.whacp.test/public"}
	t.Cleanup(func() {
		if link != "" {
			os.Remove(link)
		}
		os.Remove(avail)
		os.RemoveAll("/var/www/imp.whacp.test")
		os.Remove(filepath.Join(env.M.NginxEnabled, "whacp-imp.whacp.test.conf"))
		os.Remove(filepath.Join(env.M.NginxAvailable, "whacp-imp.whacp.test.conf"))
		os.Remove(filepath.Join(env.M.PoolDir("8.3"), "whacp-imp.whacp.test.conf"))
		exec.Command("userdel", "whacpimp").Run()
		os.RemoveAll("/home/whacp-imp")
	})
	ctx := context.Background()
	p, err := s.Plan(ctx, req)
	if err != nil || len(p.Blockers) != 0 {
		t.Fatalf("plan: %v %v", err, p.Blockers)
	}
	if _, err := s.Run(ctx, req, io.Discard); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		t.Fatalf("real nginx -t after import: %s", out)
	}
	if _, err := os.Lstat(link); link != "" && err == nil {
		t.Fatal("old site still enabled")
	}
	if _, err := os.Lstat(avail); link == "" && err == nil {
		t.Fatal("old site still enabled")
	}
	st, _ := exec.Command("stat", "-c", "%U", "/home/whacp-imp/imp.whacp.test/public_html/index.php").Output()
	if strings.TrimSpace(string(st)) != "whacpimp" {
		t.Fatalf("copied file owned by %q", st)
	}
	if err := s.Undo(ctx, req.Spec); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		t.Fatalf("real nginx -t after undo: %s", out)
	}
	if link == "" {
		if _, err := os.Stat(avail); err != nil {
			t.Fatalf("old site not re-enabled: %v", err)
		}
	} else if target, err := os.Readlink(link); err != nil || target != avail {
		t.Fatalf("old site not re-enabled: %q %v", target, err)
	}
}
