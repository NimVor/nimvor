package sites

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func spec() Spec {
	return Spec{Domain: "new.test", Aliases: []string{"www.new.test"}, User: "newsite", CreateUser: true, Kind: "php", PHPVersion: "8.3", Enabled: true}
}

func TestApplyCreatesSiteAndIsIdempotent(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	res, err := m.Apply(context.Background(), spec())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.User.Name != "newsite" {
		t.Errorf("user not created: %+v", res)
	}
	conf := h.Read("etc/nginx/sites-available/whacp-new.test.conf")
	for _, want := range []string{"server_name new.test www.new.test;", "root /home/whacp/new.test/public_html;", "fastcgi_pass unix:/run/php/whacp-new.test.sock;", "Managed by WhaCP"} {
		if !strings.Contains(conf, want) {
			t.Errorf("nginx config lacks %q:\n%s", want, conf)
		}
	}
	pool := h.Read("etc/php/8.3/fpm/pool.d/whacp-new.test.conf")
	if !strings.Contains(pool, "user = newsite") || !strings.Contains(pool, "open_basedir] = /home/whacp/new.test/") {
		t.Errorf("pool:\n%s", pool)
	}
	if !h.Exists("etc/nginx/sites-enabled/whacp-new.test.conf") || !h.Exists("home/whacp/new.test/public_html/index.html") {
		t.Error("link or index missing")
	}
	for _, c := range []string{"nginx -t", "php-fpm8.3 -t", "systemctl reload -- php8.3-fpm.service", "systemctl reload -- nginx.service"} {
		if !h.Runner.Called(c) {
			t.Errorf("expected call %q; calls=%v", c, h.Runner.Calls)
		}
	}
	calls := len(h.Runner.Calls)
	res, err = m.Apply(context.Background(), spec())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range h.Runner.Calls[calls:] {
		if strings.HasPrefix(c, "systemctl reload") || strings.HasPrefix(c, "useradd") {
			t.Errorf("second apply must not reload or create users: %s", c)
		}
	}
}

func TestFailedValidationRestoresPreviousConfig(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	if _, err := m.Apply(context.Background(), spec()); err != nil {
		t.Fatal(err)
	}
	before := h.Read("etc/nginx/sites-available/whacp-new.test.conf")
	s := spec()
	s.Aliases = []string{"www.new.test", "shop.new.test"}
	h.Fail["nginx -t"] = "nginx: [emerg] something is wrong"
	h.FailFrom["nginx -t"] = 2 // the pre-check passes, validation of the change fails
	_, err := m.Apply(context.Background(), s)
	var ae *actions.Error
	if !errors.As(err, &ae) || ae.Code != "failed" || !strings.Contains(ae.Details, "emerg") {
		t.Fatalf("err = %v", err)
	}
	if after := h.Read("etc/nginx/sites-available/whacp-new.test.conf"); after != before {
		t.Errorf("configuration not restored:\n%s", after)
	}
}

func TestPrecheckReportsPreexistingBreakage(t *testing.T) {
	h := testhost.New(t)
	h.Fail["nginx -t"] = "emerg: broken by someone else"
	_, err := (&Manager{Env: h.Env}).Apply(context.Background(), spec())
	if err == nil || !strings.Contains(err.Error(), "already invalid") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(h.Read("etc/passwd"), "newsite:") || h.Exists("etc/nginx/sites-available/whacp-new.test.conf") {
		t.Error("nothing may be changed when the existing config is broken")
	}
}

func TestNewSiteRollbackRemovesUser(t *testing.T) {
	h := testhost.New(t)
	h.Fail["nginx -t"] = "emerg"
	h.FailFrom["nginx -t"] = 2
	if _, err := (&Manager{Env: h.Env}).Apply(context.Background(), spec()); err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(h.Read("etc/passwd"), "newsite:") {
		t.Error("created user was not removed after failure")
	}
	if h.Exists("etc/nginx/sites-available/whacp-new.test.conf") || h.Exists("home/whacp/new.test/public_html/index.html") {
		t.Error("files left after failed creation")
	}
}

func TestRefusesDomainOwnedByUnmanagedConfig(t *testing.T) {
	h := testhost.New(t)
	s := spec()
	s.Aliases = []string{"shop.example.com"}
	_, err := (&Manager{Env: h.Env}).Apply(context.Background(), s)
	var ae *actions.Error
	if !errors.As(err, &ae) || ae.Code != "conflict" || !strings.Contains(ae.Message, "shop.example.com") {
		t.Fatalf("err = %v", err)
	}
	if h.Read("etc/nginx/sites-available/shop.example.com") == "<missing>" {
		t.Fatal("unmanaged file touched")
	}
}

func TestDisableSwitchVersionAndDelete(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	s := spec()
	m.Apply(context.Background(), s)

	s.PHPVersion = "8.4"
	if _, err := m.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if h.Exists("etc/php/8.3/fpm/pool.d/whacp-new.test.conf") || !h.Exists("etc/php/8.4/fpm/pool.d/whacp-new.test.conf") {
		t.Error("pool not moved to the new PHP version")
	}
	s.Enabled = false
	m.Apply(context.Background(), s)
	if h.Exists("etc/nginx/sites-enabled/whacp-new.test.conf") || h.Exists("etc/php/8.4/fpm/pool.d/whacp-new.test.conf") {
		t.Error("disabled site still enabled")
	}
	if _, err := m.handleDelete(context.Background(), []byte(`{"domain":"new.test","user":"newsite","remove_files":true,"remove_user":true}`)); err != nil {
		t.Fatal(err)
	}
	if h.Exists("etc/nginx/sites-available/whacp-new.test.conf") || h.Exists("home/whacp/new.test") || strings.Contains(h.Read("etc/passwd"), "newsite:") {
		t.Error("delete incomplete")
	}
	// The trash is next to the websites (same filesystem: a rename).
	if m, _ := filepath.Glob(filepath.Join(h.Base, "home/whacp/.whacp-trash/new.test-*")); len(m) != 1 {
		t.Error("files should be in the trash")
	}
}

func TestSpecValidation(t *testing.T) {
	bad := []func(*Spec){
		func(s *Spec) { s.Domain = "bad domain" },
		func(s *Spec) { s.Root = "/etc" },
		func(s *Spec) { s.DocumentRoot = "/home/whacp/other/public" },
		func(s *Spec) { s.Kind = "proxy"; s.ProxyPass = "http://evil.example:80" },
		func(s *Spec) { s.Redirects = []Redirect{{From: "/a;", To: "/b"}} },
		func(s *Spec) { s.Redirects = []Redirect{{From: "/a", To: "https://x/$1 ;"}} },
		func(s *Spec) { s.HTTPS = true; s.CertFile = "/etc/shadow"; s.KeyFile = "/etc/shadow" },
		func(s *Spec) { s.PHP.MemoryLimitMB = 99999 },
		func(s *Spec) { s.User = "root" },
	}
	for i, mut := range bad {
		s := spec()
		mut(&s)
		if err := s.Normalize("/home/whacp", "/var/lib/whacp-agent"); err == nil {
			t.Errorf("case %d accepted: %+v", i, s)
		}
	}
	s := spec()
	s.HTTPS, s.ForceHTTPS = true, true
	s.CertFile, s.KeyFile = "/etc/letsencrypt/live/new.test/fullchain.pem", "/etc/letsencrypt/live/new.test/privkey.pem"
	if err := s.Normalize("/home/whacp", "/var/lib/whacp-agent"); err != nil {
		t.Fatal(err)
	}
	conf := RenderNginx(s, Layout{Socket: "/run/php/x.sock", LogDir: "/l", ACMERoot: "/a"})
	if !strings.Contains(conf, "return 301 https://$host$request_uri;") || !strings.Contains(conf, "listen 443 ssl http2;") {
		t.Errorf("https render:\n%s", conf)
	}
}
