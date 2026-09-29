package sites

import (
	"context"
	"errors"
	"os"
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
	if strings.Contains(conf, "autoindex") {
		t.Error("directory listing is off by default")
	}
	s.Autoindex = true
	if conf := RenderNginx(s, Layout{Socket: "/run/php/x.sock", LogDir: "/l", ACMERoot: "/a"}); strings.Count(conf, "    autoindex on;\n") != 1 {
		t.Errorf("autoindex:\n%s", conf)
	}
	s.Kind, s.ProxyPass = "proxy", "http://127.0.0.1:3000"
	if conf := RenderNginx(s, Layout{LogDir: "/l", ACMERoot: "/a"}); strings.Contains(conf, "autoindex") {
		t.Error("autoindex on a proxy")
	}
}

// An account over its monthly bandwidth: every request gets 509, except
// certificate renewals; PHP is not reached.
func TestRenderBandwidthExceeded(t *testing.T) {
	s := spec()
	s.Redirects = []Redirect{{From: "/old", To: "/new", Code: 301}}
	if err := s.Normalize("/home/whacp", "/var/lib/whacp-agent"); err != nil {
		t.Fatal(err)
	}
	s.BandwidthExceeded = true
	conf := RenderNginx(s, Layout{Socket: "/run/php/x.sock", LogDir: "/l", ACMERoot: "/a"})
	for _, want := range []string{"return 509 \"", "Bandwidth limit exceeded", "location ^~ /.well-known/acme-challenge/", "access_log /l/access.log;"} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q:\n%s", want, conf)
		}
	}
	for _, gone := range []string{"fastcgi_pass", "location = /old", "try_files $uri $uri/"} {
		if strings.Contains(conf, gone) {
			t.Errorf("%q still served:\n%s", gone, conf)
		}
	}
	// The page text cannot end the nginx string or expand a variable.
	if strings.Count(bandwidthExceeded, "\"") != 4 || strings.Contains(bandwidthExceeded, "$") {
		t.Errorf("unsafe page text: %s", bandwidthExceeded)
	}
}

// A hosting customer's new website asks for a new account: an existing one
// (another customer's, or a server login) is refused, not reused.
func TestNewUserRefusesExistingAccounts(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	s := spec()
	s.User, s.NewUser = "example", true
	_, err := m.Apply(context.Background(), s)
	var ae *actions.Error
	if !errors.As(err, &ae) || ae.Code != "conflict" || !strings.Contains(ae.Message, "already exists") {
		t.Fatalf("err = %v", err)
	}
	if h.Exists("etc/nginx/sites-available/whacp-new.test.conf") {
		t.Error("website configured for an existing account")
	}
	s.NewUser = false // administrators may attach an existing account
	if _, err := m.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	s = spec()
	s.NewUser = true // a genuinely new name is created
	if res, err := m.Apply(context.Background(), s); err != nil || !res.Created {
		t.Fatalf("%v %+v", err, res)
	}
}

// An existing account with administrator rights (sudo or root-equivalent
// groups, or named in sudoers) never runs a website, whoever asks: its PHP
// could become root. The same account is refused for commands (Credential).
func TestPrivilegedAccountsNeverRunWebsites(t *testing.T) {
	cases := map[string]func(base string){
		"sudo group": func(b string) { appendFile(b, "etc/group", "sudo:x:27:example\n") },
		"docker":     func(b string) { appendFile(b, "etc/group", "docker:x:998:someone,example\n") },
		"sudoers":    func(b string) { appendFile(b, "etc/sudoers", "example ALL=(ALL) NOPASSWD:ALL\n") },
		"user alias": func(b string) {
			appendFile(b, "etc/sudoers", "User_Alias ADMINS = alice, example\nADMINS ALL=(ALL) ALL\n")
		},
		"sudoers.d group": func(b string) {
			appendFile(b, "etc/group", "ops:x:1200:example\n")
			os.MkdirAll(filepath.Join(b, "etc/sudoers.d"), 0o755)
			os.WriteFile(filepath.Join(b, "etc/sudoers.d/90-cloud-init-users"), []byte("%ops ALL=(ALL) NOPASSWD:ALL\n"), 0o440)
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			h := testhost.New(t)
			setup(h.Base)
			m := &Manager{Env: h.Env}
			s := spec()
			s.User = "example" // exists (uid 1500 or the test's own)
			_, err := m.Apply(context.Background(), s)
			if err == nil || !strings.Contains(err.Error(), "administrator rights") {
				t.Fatalf("err = %v", err)
			}
			if h.Exists("etc/nginx/sites-available/whacp-new.test.conf") {
				t.Error("website configured for a privileged account")
			}
			if _, _, err := h.Env.Credential("example"); err == nil || !strings.Contains(err.Error(), "administrator rights") {
				t.Fatalf("Credential: %v", err)
			}
		})
	}
	// A Defaults line or a dotted sudoers.d file names nobody; an ordinary
	// account is fine.
	h := testhost.New(t)
	appendFile(h.Base, "etc/sudoers", "Defaults env_reset\n# example ALL=(ALL) ALL\nCmnd_Alias WEB = /usr/bin/example\nops ALL=(root) /usr/sbin/example restart\n")
	os.MkdirAll(filepath.Join(h.Base, "etc/sudoers.d"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/sudoers.d/README.disabled"), []byte("example ALL=(ALL) ALL\n"), 0o440)
	s := spec()
	s.User = "example"
	if _, err := (&Manager{Env: h.Env}).Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func appendFile(base, rel, text string) {
	p := filepath.Join(base, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, _ := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	f.WriteString(text)
	f.Close()
}

func TestDefaultIndexIsAComingSoonPage(t *testing.T) {
	page := DefaultIndex("shop.test")
	for _, want := range []string{"Coming soon", "<h1 class=\"display-5 fw-bold mb-3 text-break\">shop.test</h1>", `integrity="sha384-QWTKZyjpPEjISv5WaRU9OFeRpok6YctnYmDr5pNlyT2bRjXh0JMhjY6hW+ALEwIH"`} {
		if !strings.Contains(page, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	if strings.Contains(DefaultIndex(`a<b>.test`), "<b>") {
		t.Error("domain not escaped")
	}
	legacy := "<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>shop.test</title></head>\n<body style=\"font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem\">\n<h1>shop.test</h1>\n<p>This website is ready. Upload your files to <code>public_html</code>.</p>\n</body></html>\n"
	if !PlaceholderIndex([]byte(page), "shop.test") || !PlaceholderIndex([]byte(legacy), "shop.test") || PlaceholderIndex([]byte("<h1>my site</h1>"), "shop.test") {
		t.Error("placeholder detection")
	}
}

func TestRenderSecurityHeaders(t *testing.T) {
	s := spec()
	if err := s.Normalize("/home/whacp", "/var/lib/whacp-agent"); err != nil {
		t.Fatal(err)
	}
	l := Layout{Socket: "/run/php/x.sock", LogDir: "/l", ACMERoot: "/a"}
	if strings.Contains(RenderNginx(s, l), "Referrer-Policy") {
		t.Fatal("headers are opt-in")
	}
	s.SecurityHeaders = true
	conf := RenderNginx(s, l)
	for _, want := range []string{"Referrer-Policy \"strict-origin-when-cross-origin\" always", "X-Frame-Options \"SAMEORIGIN\"", "Permissions-Policy \"camera=()"} {
		if !strings.Contains(conf, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(conf, "upgrade-insecure-requests") {
		t.Error("upgrade-insecure-requests only over HTTPS")
	}
}
