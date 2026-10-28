//go:build realhost

// Real-host test: run as root on a disposable Ubuntu machine/container with
// nginx and php8.3-fpm installed:
//
//	go test -tags realhost ./internal/sites -run RealHost -v
//
// It writes real files under /etc/nginx, /etc/php and /home/nimvor-test and
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

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	"github.com/NimVor/nimvor/agent/internal/netmgr"
	"github.com/NimVor/nimvor/agent/internal/platform"
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
	cfg.SitesRoot = "/home/nimvor-test"
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	r := &hybrid{real: execx.NewSystemRunner(30 * time.Second)}
	env := host.New(cfg, root, platform.Detect(root), r, jm, log)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, cfg.StateDir+"/**")
	m := &Manager{Env: env}
	ctx := context.Background()

	specs := []Spec{
		{Domain: "php.nimvor.test", Aliases: []string{"www.php.nimvor.test"}, User: "nimvortest1", CreateUser: true, Kind: "php", PHPVersion: testhost.PHPVersion(), Enabled: true,
			Redirects: []Redirect{{From: "/old", To: "https://php.nimvor.test/new", Code: 301}}, PHP: PHPSettings{Harden: true}},
		{Domain: "static.nimvor.test", User: "nimvortest1", Kind: "static", Enabled: true},
		{Domain: "proxy.nimvor.test", User: "nimvortest2", CreateUser: true, Kind: "proxy", ProxyPass: "http://127.0.0.1:3000", Enabled: true},
	}
	const live = "/etc/letsencrypt/live/php.nimvor.test"
	t.Cleanup(func() {
		// Delete the sites before their certificate: once it is gone the
		// configuration no longer passes nginx -t and deletion is refused.
		for _, s := range specs {
			if _, err := m.handleDelete(ctx, []byte(`{"domain":"`+s.Domain+`","user":"`+s.User+`","remove_files":true,"remove_user":true}`)); err != nil {
				t.Errorf("cleanup of %s: %v", s.Domain, err)
			}
		}
		os.RemoveAll(live)
		os.RemoveAll("/home/nimvor-test")
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
	bad.HTTPS, bad.CertFile, bad.KeyFile = true, "/etc/letsencrypt/live/php.nimvor.test/fullchain.pem", "/etc/letsencrypt/live/php.nimvor.test/privkey.pem"
	if _, err := m.Apply(ctx, bad); err == nil {
		t.Fatal("HTTPS without certificate files must fail")
	}
	b, _ := os.ReadFile("/etc/nginx/sites-available/nimvor-php.nimvor.test.conf")
	if strings.Contains(string(b), "ssl_certificate") {
		t.Fatal("failed change was not rolled back")
	}
	// With real certificate files the HTTPS configuration passes nginx -t.
	os.MkdirAll(live, 0o755)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "php.nimvor.test"}, DNSNames: []string{"php.nimvor.test"}, NotBefore: time.Now(), NotAfter: time.Now().Add(24 * time.Hour)}
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
	if b, _ := os.ReadFile(m.layout(good).NginxFile); !strings.Contains(string(b), "include /etc/nginx/nimvor-tls.conf;") || strings.Contains(string(b), "ssl_protocols") {
		t.Fatalf("vhost does not use the TLS snippet:\n%s", b)
	}
	if b, err := os.ReadFile("/etc/nginx/nimvor-tls.conf"); err != nil || !strings.Contains(string(b), "ssl_protocols TLSv1.2 TLSv1.3;") {
		t.Fatalf("TLS snippet: %v %s", err, b)
	}

	// Idempotent re-apply.
	if res, err := m.Apply(ctx, specs[1]); err != nil || len(res.Changed) != 0 {
		t.Fatalf("re-apply changed %v (%v)", res, err)
	}

	// Directory listing: off by default (403), on lists the files; served
	// by the real nginx.
	os.MkdirAll("/home/nimvor-test/static.nimvor.test/public_html/files", 0o755)
	os.WriteFile("/home/nimvor-test/static.nimvor.test/public_html/files/report.txt", []byte("x"), 0o644)
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
		req.Host = "static.nimvor.test"
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

// A website cannot serve another website's files through a symbolic link
// (nginx reads both through its group); links within a website still
// work.
func TestRealHostSymlinksAcrossWebsites(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.SitesRoot = "/home/nimvor-test"
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	r := &hybrid{real: execx.NewSystemRunner(30 * time.Second)}
	env := host.New(cfg, root, platform.Detect(root), r, jm, log)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, cfg.StateDir+"/**")
	m := &Manager{Env: env}
	ctx := context.Background()
	victim := Spec{Domain: "victim.nimvor.test", User: "nimvortest3", CreateUser: true, Kind: "static", Enabled: true}
	attacker := Spec{Domain: "attacker.nimvor.test", User: "nimvortest4", CreateUser: true, Kind: "static", Enabled: true}
	t.Cleanup(func() {
		for _, s := range []Spec{victim, attacker} {
			m.handleDelete(ctx, []byte(`{"domain":"`+s.Domain+`","user":"`+s.User+`","remove_files":true,"remove_user":true}`))
		}
		os.RemoveAll("/home/nimvor-test")
	})
	for _, s := range []Spec{victim, attacker} {
		if _, err := m.Apply(ctx, s); err != nil {
			t.Fatalf("%s: %v", s.Domain, err)
		}
	}
	vroot, aroot := "/home/nimvor-test/victim.nimvor.test", "/home/nimvor-test/attacker.nimvor.test"
	// Group as the website folders give it (setgid, the web server's).
	own := func(p, user string) {
		if out, err := exec.Command("chown", "-h", user+":"+env.M.WebUser, p).CombinedOutput(); err != nil {
			t.Fatalf("chown: %s", out)
		}
	}
	os.WriteFile(vroot+"/public_html/secret.txt", []byte("victim-secret"), 0o640)
	own(vroot+"/public_html/secret.txt", "nimvortest3")
	os.WriteFile(aroot+"/public_html/mine.txt", []byte("attacker-own"), 0o644)
	own(aroot+"/public_html/mine.txt", "nimvortest4")
	os.Symlink(vroot+"/public_html/secret.txt", aroot+"/public_html/steal.txt")
	own(aroot+"/public_html/steal.txt", "nimvortest4")
	os.Symlink("mine.txt", aroot+"/public_html/alias.txt")
	own(aroot+"/public_html/alias.txt", "nimvortest4")

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
	get := func(hostName, path string) (int, string) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1"+path, nil)
		req.Host = hostName
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	// nginx can read the victim's file (so the link would leak it)...
	if code, body := get("victim.nimvor.test", "/secret.txt"); code != 200 || body != "victim-secret" {
		t.Fatalf("victim's own file: %d %q", code, body)
	}
	// ...but not through the attacker's link.
	if code, body := get("attacker.nimvor.test", "/steal.txt"); code == http.StatusOK || strings.Contains(body, "victim-secret") {
		t.Fatalf("link to another website: %d %q", code, body)
	}
	if code, body := get("attacker.nimvor.test", "/alias.txt"); code != 200 || body != "attacker-own" {
		t.Fatalf("link within the website: %d %q", code, body)
	}
}

// Applies a website with customer security headers and cache rules through
// the real Manager (real nginx -t), reloads the running nginx when there is
// one, and checks the headers a real request receives: each header once,
// static assets cached, signed-in and sensitive requests never cached publicly.
func TestRealHostWebPolicy(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.SitesRoot = "/home/nimvor-test"
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	r := &hybrid{real: execx.NewSystemRunner(30 * time.Second)}
	env := host.New(cfg, root, platform.Detect(root), r, jm, log)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, cfg.StateDir+"/**")
	m := &Manager{Env: env}
	ctx := context.Background()

	s := Spec{Domain: "policy.nimvor.test", User: "nimvorpolicy", CreateUser: true, Kind: "static", Enabled: true,
		Headers: &HeaderPolicy{Clickjacking: "deny", CSP: "default-src 'self'", CSPReportOnly: true, Referrer: "no-referrer", Permissions: "restrictive"},
		Cache:   &CachePolicy{StaticTTL: 3600, DynamicMode: "public", DynamicTTL: 60, BypassPaths: []string{"/members"}}}
	t.Cleanup(func() {
		for _, d := range []string{"proxy-policy.nimvor.test", s.Domain} {
			if _, err := m.handleDelete(ctx, []byte(`{"domain":"`+d+`","user":"`+s.User+`","remove_files":true,"remove_user":true}`)); err != nil && !strings.Contains(err.Error(), "not found") {
				t.Errorf("cleanup of %s: %v", d, err)
			}
		}
		os.RemoveAll("/home/nimvor-test")
	})
	if _, err := m.Apply(ctx, s); err != nil {
		var ae *actions.Error
		if errors.As(err, &ae) {
			t.Fatalf("apply: %v\n%s", err, ae.Details)
		}
		t.Fatal(err)
	}
	if out, err := r.real.Run(ctx, "nginx", "-t"); err != nil {
		t.Fatalf("real nginx -t failed with the policy applied: %s", out)
	}
	docroot := "/home/nimvor-test/policy.nimvor.test/public_html"
	for dir, files := range map[string]map[string]string{"": {"index.html": "<p>home</p>", "app.css": "body{}"}, "members": {"index.html": "<p>members</p>"}, "admin": {"index.html": "<p>admin</p>"}} {
		os.MkdirAll(docroot+"/"+dir, 0o755)
		for f, body := range files {
			os.WriteFile(docroot+"/"+dir+"/"+f, []byte(body), 0o644)
		}
	}
	if out, err := exec.Command("nginx", "-s", "reload").CombinedOutput(); err != nil {
		t.Logf("nginx is not running here (%s); the generated configuration passed nginx -t but no request was made", strings.TrimSpace(string(out)))
		return
	}
	time.Sleep(time.Second)
	get := func(path, cookie string) http.Header {
		req, _ := http.NewRequest("GET", "http://127.0.0.1"+path, nil)
		req.Host = "policy.nimvor.test"
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("GET %s: %d", path, res.StatusCode)
		}
		return res.Header
	}
	h := get("/app.css", "")
	if cc := h.Values("Cache-Control"); len(cc) != 1 || cc[0] != "max-age=3600" || h.Get("Expires") == "" {
		t.Errorf("static asset: Cache-Control %v Expires %q", cc, h.Get("Expires"))
	}
	for _, name := range []string{"X-Frame-Options", "Content-Security-Policy", "Content-Security-Policy-Report-Only", "Referrer-Policy", "Permissions-Policy", "X-Content-Type-Options"} {
		if v := h.Values(name); len(v) != 1 {
			t.Errorf("%s sent %d times: %v", name, len(v), v)
		}
	}
	if v := h.Get("Content-Security-Policy"); v != "frame-ancestors 'none'" {
		t.Errorf("enforced CSP: %q", v)
	}
	if cc := get("/index.html", "").Values("Cache-Control"); len(cc) != 1 || cc[0] != "public, max-age=60" {
		t.Errorf("anonymous page: %v", cc)
	}
	for name, got := range map[string][]string{
		"session cookie": get("/index.html", "PHPSESSID=abc").Values("Cache-Control"),
		"sensitive path": get("/admin/index.html", "").Values("Cache-Control"),
		"custom bypass":  get("/members/index.html", "").Values("Cache-Control"),
		"logged-in (wp)": get("/index.html", "wordpress_logged_in_x=1").Values("Cache-Control"),
	} {
		if len(got) != 1 || got[0] != "private, no-store" {
			t.Errorf("%s must never be cached publicly: %v", name, got)
		}
	}

	// A proxied website: what the application sends back decides too.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sets-cookie":
			http.SetCookie(w, &http.Cookie{Name: "tracker", Value: "1"})
		case "/own-cache":
			w.Header().Set("Cache-Control", "max-age=5")
		}
		io.WriteString(w, "ok")
	}))
	defer backend.Close()
	px := Spec{Domain: "proxy-policy.nimvor.test", User: "nimvorpolicy", Kind: "proxy", ProxyPass: backend.URL, Enabled: true,
		Cache: &CachePolicy{DynamicMode: "public", DynamicTTL: 60}}
	if _, err := m.Apply(ctx, px); err != nil {
		t.Fatalf("apply proxy site: %v", err)
	}
	if out, err := exec.Command("nginx", "-s", "reload").CombinedOutput(); err != nil {
		t.Fatalf("reload: %s", out)
	}
	time.Sleep(time.Second)
	pget := func(path, cookie, auth string) []string {
		req, _ := http.NewRequest("GET", "http://127.0.0.1"+path, nil)
		req.Host = "proxy-policy.nimvor.test"
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		res.Body.Close()
		return res.Header.Values("Cache-Control")
	}
	if cc := pget("/plain", "", ""); len(cc) != 1 || cc[0] != "public, max-age=60" {
		t.Errorf("anonymous dynamic page: %v", cc)
	}
	for name, got := range map[string][]string{
		"response sets a cookie": pget("/sets-cookie", "", ""),
		"Authorization header":   pget("/plain", "", "Bearer abc"),
		"session cookie":         pget("/plain", "sessionid=1", ""),
		"sensitive path":         pget("/wp-admin/x", "", ""),
	} {
		if len(got) != 1 || got[0] != "private, no-store" {
			t.Errorf("%s must never be cached publicly: %v", name, got)
		}
	}
	if cc := pget("/own-cache", "", ""); len(cc) != 1 || cc[0] != "max-age=5" {
		t.Errorf("the application's own Cache-Control must be left alone and not doubled: %v", cc)
	}
	if req, _ := http.NewRequest("POST", "http://127.0.0.1/plain", nil); req != nil {
		req.Host = "proxy-policy.nimvor.test"
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
			if cc := res.Header.Values("Cache-Control"); len(cc) != 1 || cc[0] != "private, no-store" {
				t.Errorf("POST responses must never be cached: %v", cc)
			}
		}
	}
}
