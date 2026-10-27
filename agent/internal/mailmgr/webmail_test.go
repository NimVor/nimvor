package mailmgr

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/ssl"
)

func TestWebmail(t *testing.T) {
	h, r := setup(t)
	modules := "[PHP Modules]\ndom\nintl\nmbstring\nopenssl\npdo_sqlite\n"
	next := h.Respond
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "php8.4" && len(args) == 1 && args[0] == "-m" {
			return "[PHP Modules]\nopenssl\n", nil, true // lacks Roundcube's extensions
		}
		if id == "php8.3" && len(args) == 1 && args[0] == "-m" {
			return modules, nil, true
		}
		return next(id, args)
	}
	old := webmailProbe
	probed := ""
	webmailProbe = func(_ context.Context, host string) error { probed = host; return nil }
	t.Cleanup(func() { webmailProbe = old })
	on := map[string]any{"enabled": true, "hostname": "Webmail.Example.net."}

	if _, err := call(t, r, "mail.webmail_configure", on); code(err) != "conflict" {
		t.Fatalf("before email is set up: %v", err)
	}
	h.Env.Cfg.SimulateCommands = true // no ports to probe on the test host
	if _, err := call(t, r, "mail.configure", map[string]any{"hostname": "mail.example.net", "confirm_edit_package_config": true}); err != nil {
		t.Fatal(err)
	}
	h.Env.Cfg.SimulateCommands = false
	if _, err := call(t, r, "mail.webmail_configure", on); code(err) != "conflict" || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("without Roundcube: %v", err)
	}
	for _, f := range []string{"var/lib/roundcube/public_html/index.php", "etc/roundcube/defaults.inc.php", "usr/share/roundcube/bin/updatedb.sh"} {
		os.MkdirAll(filepath.Join(h.Base, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(h.Base, f), []byte("<?php\n"), 0o644)
	}
	for _, bad := range []map[string]any{{"enabled": true, "hostname": "bad host"}, {"enabled": true, "hostname": "x.test", "php_version": "8.3;id"}} {
		if _, err := call(t, r, "mail.webmail_configure", bad); code(err) != "invalid_params" {
			t.Errorf("%v: %v", bad, err)
		}
	}
	if _, err := call(t, r, "mail.webmail_configure", map[string]any{"enabled": true, "hostname": "example.com"}); code(err) != "conflict" || !strings.Contains(err.Error(), "already answers") {
		t.Fatalf("host name of a website: %v", err)
	}
	if _, err := call(t, r, "mail.webmail_configure", map[string]any{"enabled": true, "hostname": "webmail.example.net", "php_version": "8.4"}); code(err) != "conflict" || !strings.Contains(err.Error(), "intl") {
		t.Fatalf("PHP without extensions: %v", err)
	}

	// Invalid Nginx configuration: nothing stays behind.
	h.Fail["nginx -t"] = "nginx: [emerg] test failure"
	h.FailFrom["nginx -t"] = 2 // the precheck passes
	if _, err := call(t, r, "mail.webmail_configure", on); code(err) != "failed" {
		t.Fatalf("invalid config accepted: %v", err)
	}
	for _, p := range []string{"etc/nginx/sites-available/whacp-webmail.conf", "etc/nginx/sites-enabled/whacp-webmail.conf", "etc/php/8.3/fpm/pool.d/whacp-webmail.conf", "etc/whacp-webmail/webmail.json"} {
		if h.Exists(p) {
			t.Errorf("%s left after a failed change", p)
		}
	}
	delete(h.Fail, "nginx -t")

	res, err := call(t, r, "mail.webmail_configure", on)
	if err != nil {
		t.Fatal(err)
	}
	if res["url"] != "https://webmail.example.net/" || res["php"] != "8.3" || probed != "webmail.example.net" {
		t.Fatalf("result = %v (probed %q)", res, probed)
	}
	pool := h.Read("etc/php/8.3/fpm/pool.d/whacp-webmail.conf")
	for _, want := range []string{"[whacp-webmail]", "user = whacp-webmail", "listen = /run/php/whacp-webmail.sock", "env[ROUNDCUBE_CONFIG_DIR] = /etc/whacp-webmail", "open_basedir] = /var/lib/roundcube/:/usr/share/roundcube/:/etc/roundcube/:/etc/whacp-webmail/:/var/lib/whacp-webmail/", "disable_functions"} {
		if !strings.Contains(pool, want) {
			t.Errorf("pool lacks %q:\n%s", want, pool)
		}
	}
	vhost := h.Read("etc/nginx/sites-available/whacp-webmail.conf")
	for _, want := range []string{"server_name webmail.example.net;", "return 301 https://", "listen 443 ssl http2;", "ssl_certificate /etc/whacp-webmail/tls.crt;", "root /var/lib/roundcube/public_html;", "location = /index.php {", "fastcgi_pass unix:/run/php/whacp-webmail.sock;", "\\.(inc|sql|sh|dist|lock|log|md|txt|php)$ {\n        deny all;", "acme-challenge"} {
		if !strings.Contains(vhost, want) {
			t.Errorf("vhost lacks %q:\n%s", want, vhost)
		}
	}
	if target, _ := os.Readlink(filepath.Join(h.Base, "etc/nginx/sites-enabled/whacp-webmail.conf")); target != "/etc/nginx/sites-available/whacp-webmail.conf" {
		t.Errorf("link = %q", target)
	}
	conf := h.Read("etc/whacp-webmail/config.inc.php")
	m := desKeyRe.FindStringSubmatch(conf)
	for _, want := range []string{"sqlite:////var/lib/whacp-webmail/roundcube.db", "ssl://127.0.0.1:993", "tls://127.0.0.1:587", "$config['smtp_user'] = '%u';"} {
		if !strings.Contains(conf, want) || m == nil {
			t.Errorf("config lacks %q:\n%s", want, conf)
		}
	}
	if !strings.Contains(h.Read("etc/whacp-webmail/defaults.inc.php"), "include '/etc/roundcube/defaults.inc.php';") {
		t.Error("defaults wrapper")
	}
	if h.Read("etc/roundcube/defaults.inc.php") != "<?php\n" {
		t.Error("package file changed")
	}
	if !h.Runner.Called("php8.3 -d error_reporting=E_ALL & ~E_DEPRECATED /usr/share/roundcube/bin/updatedb.sh --package=roundcube --dir=/usr/share/roundcube/SQL") {
		t.Errorf("database not created: %v", h.Runner.Calls)
	}
	st, _ := call(t, r, "mail.status", nil)
	if w := st["webmail"].(map[string]any); w["enabled"] != true || w["hostname"] != "webmail.example.net" || w["installed"] != true {
		t.Errorf("status = %v", w)
	}

	// Again: same key; a website may not take over the name later either.
	if _, err := call(t, r, "mail.webmail_configure", on); err != nil {
		t.Fatal(err)
	}
	if m2 := desKeyRe.FindStringSubmatch(h.Read("etc/whacp-webmail/config.inc.php")); m2 == nil || m2[1] != m[1] {
		t.Error("encryption key changed")
	}

	// The database step failing restores everything.
	h.Fail["php8.3 -d"] = "PHP Fatal error"
	h.FailFrom["php8.3 -d"] = 0
	if _, err := call(t, r, "mail.webmail_configure", map[string]any{"enabled": true, "hostname": "mail2.example.net"}); code(err) != "failed" {
		t.Fatalf("database failure: %v", err)
	}
	if !strings.Contains(h.Read("etc/nginx/sites-available/whacp-webmail.conf"), "server_name webmail.example.net;") {
		t.Error("previous site not restored")
	}
	h.Fail["php8.3 -d"] = "PHP Fatal error:  Cannot redeclare function array_first() in /usr/share/roundcube/program/lib/Roundcube/bootstrap.php"
	if _, err := call(t, r, "mail.webmail_configure", on); err == nil || !strings.Contains(err.Error(), "does not work with PHP 8.3") {
		t.Fatalf("incompatible PHP: %v", err)
	}
	delete(h.Fail, "php8.3 -d")

	// A rename to a host name without a Let's Encrypt certificate
	// regenerates the self-signed certificate for the new name instead of
	// reusing the old one (which would keep presenting the previous name).
	if _, err := call(t, r, "mail.webmail_configure", map[string]any{"enabled": true, "hostname": "newmail.example.net"}); err != nil {
		t.Fatal(err)
	}
	if c := ssl.Inspect(h.Env.Root, "/etc/whacp-webmail/tls.crt", "/etc/whacp-webmail/tls.key", time.Now()); !c.Covers("newmail.example.net") || c.Covers("webmail.example.net") {
		t.Errorf("self-signed webmail cert not regenerated for the new host name (covers %v)", c.DNSNames)
	}

	if _, err := call(t, r, "mail.webmail_configure", map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"etc/nginx/sites-available/whacp-webmail.conf", "etc/nginx/sites-enabled/whacp-webmail.conf", "etc/php/8.3/fpm/pool.d/whacp-webmail.conf"} {
		if h.Exists(p) {
			t.Errorf("%s left after turning off", p)
		}
	}
	if !h.Exists("etc/whacp-webmail/config.inc.php") {
		t.Error("configuration removed when turning off")
	}

	// WhaCP's own host-name challenge site (ACME on port 80) must not count
	// as a website owning the name — webmail can still use it on 443.
	os.WriteFile(filepath.Join(h.Base, "etc/nginx/sites-available/whacp-hostname.conf"),
		[]byte("server {\n    listen 80;\n    server_name hn.example.net;\n    location /.well-known/acme-challenge/ { root /var/www/whacp-acme; }\n    location / { return 404; }\n}\n"), 0o644)
	os.Symlink("/etc/nginx/sites-available/whacp-hostname.conf", filepath.Join(h.Base, "etc/nginx/sites-enabled/whacp-hostname.conf"))
	if _, err := call(t, r, "mail.webmail_configure", map[string]any{"enabled": true, "hostname": "hn.example.net"}); err != nil && strings.Contains(err.Error(), "already answers") {
		t.Fatalf("host-name challenge site wrongly blocked webmail: %v", err)
	}

	// Auto-issue: with no certificate for the name, webmail obtains a Let's
	// Encrypt one (its site serves the ACME challenge) and switches to it.
	prevResp := h.Respond
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "certbot" && len(args) > 0 && args[0] == "certonly" {
			dir := filepath.Join(h.Base, "etc/letsencrypt/live/wm.example.net")
			os.MkdirAll(dir, 0o755)
			c, k, _ := selfSigned("wm.example.net")
			os.WriteFile(filepath.Join(dir, "fullchain.pem"), c, 0o644)
			os.WriteFile(filepath.Join(dir, "privkey.pem"), k, 0o600)
			return "", nil, true
		}
		return prevResp(id, args)
	}
	if _, err := call(t, r, "mail.webmail_configure", map[string]any{"enabled": true, "hostname": "wm.example.net"}); err != nil {
		t.Fatalf("webmail with auto-issue: %v", err)
	}
	if vh := h.Read("etc/nginx/sites-available/whacp-webmail.conf"); !strings.Contains(vh, "ssl_certificate /etc/letsencrypt/live/wm.example.net/fullchain.pem;") {
		t.Errorf("webmail did not switch to the issued Let's Encrypt certificate:\n%s", vh)
	}
}

// Roundcube 1.6.11 declares array_first(), which PHP 8.5 has built in:
// on PHP 8.5 the built-in is turned off for Roundcube only (its pool and
// the database step), and nowhere else.
func TestWebmailOnPHP85(t *testing.T) {
	h, r := setup(t)
	next := h.Respond
	h.Respond = func(id string, args []string) (string, error, bool) {
		if (id == "php8.5" || id == "php8.3") && len(args) == 1 && args[0] == "-m" {
			return "[PHP Modules]\ndom\nintl\nmbstring\nopenssl\npdo_sqlite\n", nil, true
		}
		return next(id, args)
	}
	old := webmailProbe
	webmailProbe = func(context.Context, string) error { return nil }
	t.Cleanup(func() { webmailProbe = old })
	h.Env.Cfg.SimulateCommands = true
	if _, err := call(t, r, "mail.configure", map[string]any{"hostname": "mail.example.net", "confirm_edit_package_config": true}); err != nil {
		t.Fatal(err)
	}
	h.Env.Cfg.SimulateCommands = false
	for _, f := range []string{"var/lib/roundcube/public_html/index.php", "etc/roundcube/defaults.inc.php", "usr/share/roundcube/bin/updatedb.sh"} {
		os.MkdirAll(filepath.Join(h.Base, filepath.Dir(f)), 0o755)
		os.WriteFile(filepath.Join(h.Base, f), []byte("<?php\n"), 0o644)
	}
	os.MkdirAll(filepath.Join(h.Base, "etc/php/8.5/fpm/pool.d"), 0o755)
	bootstrap := filepath.Join(h.Base, "usr/share/roundcube/program/lib/Roundcube/bootstrap.php")
	os.MkdirAll(filepath.Dir(bootstrap), 0o755)
	os.WriteFile(bootstrap, []byte("<?php\n/**\n * Get first element from an array\n */\nfunction array_first($array)\n{\n}\n"), 0o644)

	if _, err := call(t, r, "mail.webmail_configure", map[string]any{"enabled": true, "hostname": "webmail.example.net", "php_version": "8.5"}); err != nil {
		t.Fatal(err)
	}
	if pool := h.Read("etc/php/8.5/fpm/pool.d/whacp-webmail.conf"); !strings.Contains(pool, "php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec,array_first\n") {
		t.Errorf("PHP 8.5 pool:\n%s", pool)
	}
	if !h.Runner.Called("php8.5 -d disable_functions=array_first -d error_reporting=E_ALL & ~E_DEPRECATED /usr/share/roundcube/bin/updatedb.sh --package=roundcube --dir=/usr/share/roundcube/SQL") {
		t.Errorf("database step without the setting: %v", h.Runner.Calls)
	}

	// Older PHP, or a Roundcube that does not declare it: unchanged.
	e := env{h.Env}
	if strings.Contains(e.renderWebmailPool("8.4"), "array_first") || e.shadowsArrayFirst("8.3") {
		t.Error("array_first turned off before PHP 8.5")
	}
	os.WriteFile(bootstrap, []byte("<?php\n// array_first() comes from PHP\n"), 0o644)
	if e.shadowsArrayFirst("8.5") {
		t.Error("array_first turned off although Roundcube does not declare it")
	}
}

// RHEL: no package creates the pools' socket folder for Remi's PHP
// versions; webmail (like a website) adds it and its boot-time entry, else
// PHP-FPM cannot bind the webmail socket (Nginx: 502).
func TestEnsurePHPSocketDir(t *testing.T) {
	h, _ := setup(t)
	// (The test host is Ubuntu-shaped: its socket folder, RHEL's entry.)
	dir := h.Env.M.PHPSocketDir
	h.Env.M.PHPSocketTmpfiles = "/etc/tmpfiles.d/whacp-php-fpm.conf"
	os.MkdirAll(filepath.Join(h.Base, "etc/tmpfiles.d"), 0o755) // always there on a real system
	// Missing, as on the RHEL server that showed the bug: WhaCP creates it.
	os.RemoveAll(filepath.Join(h.Base, dir))
	set := h.Env.Changes("socket-dir")
	h.Env.EnsurePHPSocketDir(set)
	if _, err := set.Commit(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(h.Base, dir)); err != nil || !fi.IsDir() {
		t.Fatalf("socket folder: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(h.Base, "etc/tmpfiles.d/whacp-php-fpm.conf"))
	if !strings.Contains(string(b), "d "+dir+" 0755 root root -") {
		t.Fatalf("tmpfiles entry: %q", b)
	}
}

// Roundcube must be able to set error_reporting itself.
func TestWebmailPoolLeavesErrorReporting(t *testing.T) {
	h, _ := setup(t)
	if pool := (env{h.Env}).renderWebmailPool("8.3"); strings.Contains(pool, "error_reporting") {
		t.Fatalf("the pool fixes error_reporting:\n%s", pool)
	}
}
