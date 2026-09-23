//go:build realhost

// Real-host test: run as root on a disposable Ubuntu machine/container with
// nginx and php8.3-fpm installed:
//
//	go test -tags realhost ./internal/sites -run RealHost -v
//
// It writes real files under /etc/nginx, /etc/php and /home/whacp-test and
// runs the real nginx -t, php-fpm8.3 -t, useradd and userdel. systemctl is
// simulated because containers usually lack systemd.
package sites

import (
	"context"
	"errors"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"io"
	"log/slog"
	"os"
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

type hybrid struct {
	real *execx.SystemRunner
	log  []string
}

func (h *hybrid) Available(id string) bool { return id == "systemctl" || h.real.Available(id) }
func (h *hybrid) Run(ctx context.Context, id string, args ...string) ([]byte, error) {
	return h.RunWith(ctx, id, execx.Opts{}, args...)
}
func (h *hybrid) RunWith(ctx context.Context, id string, o execx.Opts, args ...string) ([]byte, error) {
	h.log = append(h.log, id+" "+strings.Join(args, " "))
	if id == "systemctl" {
		if len(args) > 0 && args[0] == "is-active" {
			return []byte("active"), nil
		}
		return nil, nil
	}
	return h.real.RunWith(ctx, id, o, args...)
}

func TestRealHostSiteLifecycle(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.SitesRoot = "/home/whacp-test"
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	r := &hybrid{real: execx.NewSystemRunner(30 * time.Second)}
	env := host.New(cfg, root, platform.Detect(root), r, jm, log)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, cfg.StateDir+"/**")
	m := &Manager{Env: env}
	ctx := context.Background()

	specs := []Spec{
		{Domain: "php.whacp.test", Aliases: []string{"www.php.whacp.test"}, User: "whacptest1", CreateUser: true, Kind: "php", PHPVersion: "8.3", Enabled: true,
			Redirects: []Redirect{{From: "/old", To: "https://php.whacp.test/new", Code: 301}}, PHP: PHPSettings{Harden: true}},
		{Domain: "static.whacp.test", User: "whacptest1", Kind: "static", Enabled: true},
		{Domain: "proxy.whacp.test", User: "whacptest2", CreateUser: true, Kind: "proxy", ProxyPass: "http://127.0.0.1:3000", Enabled: true},
	}
	t.Cleanup(func() {
		for _, s := range specs {
			m.handleDelete(ctx, []byte(`{"domain":"`+s.Domain+`","user":"`+s.User+`","remove_files":true,"remove_user":true}`))
		}
		os.RemoveAll("/home/whacp-test")
	})
	for _, s := range specs {
		if _, err := m.Apply(ctx, s); err != nil {
			var ae *actions.Error
			if errors.As(err, &ae) {
				t.Fatalf("%s: %v\n%s", s.Domain, err, ae.Details)
			}
			t.Fatalf("%s: %v", s.Domain, err)
		}
	}
	if out, err := r.real.Run(ctx, "nginx", "-t"); err != nil {
		t.Fatalf("real nginx -t failed after apply: %s", out)
	}
	if out, err := r.real.Run(ctx, "php-fpm8.3", "-t"); err != nil {
		t.Fatalf("real php-fpm -t failed after apply: %s", out)
	}
	// Invalid configuration is rejected by the real nginx and rolled back.
	bad := specs[0]
	bad.HTTPS, bad.CertFile, bad.KeyFile = true, "/etc/letsencrypt/live/php.whacp.test/fullchain.pem", "/etc/letsencrypt/live/php.whacp.test/privkey.pem"
	if _, err := m.Apply(ctx, bad); err == nil {
		t.Fatal("HTTPS without certificate files must fail")
	}
	b, _ := os.ReadFile("/etc/nginx/sites-available/whacp-php.whacp.test.conf")
	if strings.Contains(string(b), "ssl_certificate") {
		t.Fatal("failed change was not rolled back")
	}
	// Idempotent re-apply.
	if res, err := m.Apply(ctx, specs[1]); err != nil || len(res.Changed) != 0 {
		t.Fatalf("re-apply changed %v (%v)", res, err)
	}
}
