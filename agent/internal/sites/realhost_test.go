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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/netmgr"
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
		{Domain: "php.whacp.test", Aliases: []string{"www.php.whacp.test"}, User: "whacptest1", CreateUser: true, Kind: "php", PHPVersion: testhost.PHPVersion(), Enabled: true,
			Redirects: []Redirect{{From: "/old", To: "https://php.whacp.test/new", Code: 301}}, PHP: PHPSettings{Harden: true}},
		{Domain: "static.whacp.test", User: "whacptest1", Kind: "static", Enabled: true},
		{Domain: "proxy.whacp.test", User: "whacptest2", CreateUser: true, Kind: "proxy", ProxyPass: "http://127.0.0.1:3000", Enabled: true},
	}
	const live = "/etc/letsencrypt/live/php.whacp.test"
	t.Cleanup(func() {
		// Delete the sites before their certificate: once it is gone the
		// configuration no longer passes nginx -t and deletion is refused.
		for _, s := range specs {
			if _, err := m.handleDelete(ctx, []byte(`{"domain":"`+s.Domain+`","user":"`+s.User+`","remove_files":true,"remove_user":true}`)); err != nil {
				t.Errorf("cleanup of %s: %v", s.Domain, err)
			}
		}
		os.RemoveAll(live)
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
	if out, err := r.real.Run(ctx, "php-fpm"+testhost.PHPVersion(), "-t"); err != nil {
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
	// With real certificate files the HTTPS configuration passes nginx -t.
	os.MkdirAll(live, 0o755)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "php.whacp.test"}, DNSNames: []string{"php.whacp.test"}, NotBefore: time.Now(), NotAfter: time.Now().Add(24 * time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	kb, _ := x509.MarshalECPrivateKey(key)
	os.WriteFile(live+"/fullchain.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644)
	os.WriteFile(live+"/privkey.pem", pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600)
	good := bad
	good.ForceHTTPS, good.HSTS = true, true
	if _, err := m.Apply(ctx, good); err != nil {
		var ae *actions.Error
		errors.As(err, &ae)
		t.Fatalf("https apply: %v %+v", err, ae)
	}
	if out, err := r.real.Run(ctx, "nginx", "-t"); err != nil {
		t.Fatalf("real nginx -t failed with HTTPS: %s", out)
	}
	// A website bound to one of the server's addresses; one that is not the
	// server's is refused before anything is written.
	addrs, err := netmgr.Addresses(ctx, env)
	if err != nil {
		t.Fatal(err)
	}
	var v4 string
	for _, a := range addrs {
		if a.Family == 4 && a.Scope == "global" {
			v4 = a.Address
			break
		}
	}
	if v4 == "" {
		t.Fatalf("no global IPv4 address in %+v", addrs)
	}
	bound := specs[1]
	bound.IPv4 = v4
	if _, err := m.Apply(ctx, bound); err != nil {
		t.Fatalf("bound site: %v", err)
	}
	if b, _ := os.ReadFile(m.layout(bound).NginxFile); !strings.Contains(string(b), "listen "+v4+":80;") {
		t.Fatalf("listen line:\n%s", b)
	}
	if out, err := r.real.Run(ctx, "nginx", "-t"); err != nil {
		t.Fatalf("real nginx -t with a bound address: %s", out)
	}
	foreign := bound
	foreign.IPv4 = "198.51.100.77"
	if _, err := m.Apply(ctx, foreign); err == nil || !strings.Contains(err.Error(), "not an address of this server") {
		t.Fatalf("foreign address: %v", err)
	}
	if _, err := m.Apply(ctx, specs[1]); err != nil { // back to every address
		t.Fatal(err)
	}

	// TLS settings come from the server-wide snippet.
	if b, _ := os.ReadFile(m.layout(good).NginxFile); !strings.Contains(string(b), "include /etc/nginx/whacp-tls.conf;") || strings.Contains(string(b), "ssl_protocols") {
		t.Fatalf("vhost does not use the TLS snippet:\n%s", b)
	}
	if b, err := os.ReadFile("/etc/nginx/whacp-tls.conf"); err != nil || !strings.Contains(string(b), "ssl_protocols TLSv1.2 TLSv1.3;") {
		t.Fatalf("TLS snippet: %v %s", err, b)
	}

	// Idempotent re-apply.
	if res, err := m.Apply(ctx, specs[1]); err != nil || len(res.Changed) != 0 {
		t.Fatalf("re-apply changed %v (%v)", res, err)
	}

	// Directory listing: off by default (403), on lists the files; served
	// by the real nginx.
	os.MkdirAll("/home/whacp-test/static.whacp.test/public_html/files", 0o755)
	os.WriteFile("/home/whacp-test/static.whacp.test/public_html/files/report.txt", []byte("x"), 0o644)
	serve := func() {
		if out, err := r.real.Run(ctx, "nginx", "-t"); err != nil {
			t.Fatalf("nginx -t: %s", out)
		}
		if _, err := r.real.Run(ctx, "nginx", "-s", "reload"); err != nil {
			if out, err := r.real.Run(ctx, "nginx"); err != nil {
				t.Fatalf("nginx start: %s", out)
			}
			t.Cleanup(func() { r.real.Run(context.Background(), "nginx", "-s", "quit") })
		}
		time.Sleep(500 * time.Millisecond)
	}
	get := func() (int, string) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1/files/", nil)
		req.Host = "static.whacp.test"
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	serve()
	if code, _ := get(); code != http.StatusForbidden {
		t.Fatalf("listing off: HTTP %d", code)
	}
	listed := specs[1]
	listed.Autoindex = true
	if _, err := m.Apply(ctx, listed); err != nil {
		t.Fatalf("autoindex apply: %v", err)
	}
	serve()
	if code, body := get(); code != http.StatusOK || !strings.Contains(body, "report.txt") {
		t.Fatalf("listing on: HTTP %d\n%s", code, body)
	}
	if _, err := m.Apply(ctx, specs[1]); err != nil {
		t.Fatal(err)
	}
	serve()
	if code, _ := get(); code != http.StatusForbidden {
		t.Fatalf("listing off again: HTTP %d", code)
	}
}
