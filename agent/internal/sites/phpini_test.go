package sites

import (
	"strings"
	"testing"
)

func TestWebsitePHPDirectives(t *testing.T) {
	root := "/home/nimvor/shop.test"
	good := map[string]string{
		"display_errors": "1", "error_reporting": "E_ALL & ~E_DEPRECATED", "date.timezone": "Asia/Karachi",
		"post_max_size": "128m", "max_input_vars": "5000", "session.save_path": root + "/tmp/sessions",
		"auto_prepend_file": root + "/public_html/prepend.php", "output_buffering": "4096", "zlib.output_compression": "off",
		"allow_url_fopen": "Off", "disable_functions": "mail, exec", "session.cookie_samesite": "Lax",
	}
	ini, err := NormalizeINI(good, root)
	if err != nil {
		t.Fatal(err)
	}
	if ini["display_errors"] != "On" || ini["post_max_size"] != "128M" || ini["zlib.output_compression"] != "Off" || ini["disable_functions"] != "mail,exec" {
		t.Errorf("normalized: %v", ini)
	}
	for k, v := range map[string]string{
		"open_basedir":          "/",
		"sys_temp_dir":          "/tmp",
		"allow_url_include":     "On",
		"enable_dl":             "On",
		"mysqli.default_pw":     "secret",
		"register_globals":      "On",
		"memory_limit":          "4G",
		"session.save_path":     "/home/nimvor/other.test/tmp",
		"error_log":             root + "/../other.test/log",
		"auto_append_file":      "/etc/passwd",
		"date.timezone":         "UTC\nphp_admin_value[open_basedir] = /",
		"error_reporting":       "E_ALL\"; x",
		"display_errors":        "maybe",
		"session.name":          "a b",
		"disable_functions":     "exec;system",
		"max_input_vars":        "10",
		"default_mimetype":      "text/html\r\nx",
		"session.cookie_domain": "evil test",
	} {
		if _, err := NormalizeINI(map[string]string{k: v}, root); err == nil {
			t.Errorf("accepted %s = %q", k, v)
		}
	}

	s := Spec{Domain: "shop.test", User: "shop", Root: root, PHP: PHPSettings{MemoryLimitMB: 256, UploadMaxMB: 64, MaxExecSeconds: 60, MaxChildren: 5, PM: "ondemand", MaxRequests: 500, IdleTimeoutSec: 10, SlowlogSec: 10, Harden: true, INI: ini}}
	pool := RenderPool(s, Layout{Socket: "/run/php/x.sock", LogDir: root + "/logs", TmpDir: root + "/tmp", WebUser: "www-data"})
	for _, want := range []string{
		"php_admin_value[open_basedir] = " + root + "/:/usr/share/php/:/dev/urandom\n",
		"php_admin_flag[display_errors] = on\n",
		"php_admin_value[post_max_size] = 128M\n",
		"php_admin_value[session.save_path] = " + root + "/tmp/sessions\n",
		"php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec,mail\n",
		"php_admin_flag[allow_url_fopen] = off\n",
		"php_value[error_reporting] = E_ALL & ~E_DEPRECATED\n",
		"php_value[date.timezone] = Asia/Karachi\n",
		"php_value[output_buffering] = 4096\n",
		"php_flag[zlib.output_compression] = off\n",
	} {
		if !strings.Contains(pool, want) {
			t.Errorf("pool lacks %q:\n%s", want, pool)
		}
	}
	if strings.Count(pool, "[post_max_size]") != 1 || strings.Count(pool, "[display_errors]") != 1 {
		t.Errorf("a directive appears twice:\n%s", pool)
	}
	// The server's hardening cannot be removed by the website.
	ini2, _ := NormalizeINI(map[string]string{"disable_functions": ""}, root)
	s.PHP.INI = ini2
	if !strings.Contains(RenderPool(s, Layout{LogDir: root + "/logs", TmpDir: root + "/tmp"}), "disable_functions] = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec\n") {
		t.Error("an empty list removed the hardening")
	}
	if err := (&Spec{Domain: "shop.test", User: "shop", Kind: "php", PHPVersion: "8.3", PHP: PHPSettings{INI: map[string]string{"open_basedir": "/"}}}).Normalize("/home/nimvor", "/var/lib/nimvor"); err == nil {
		t.Error("Normalize accepted open_basedir")
	}
}

func TestPoolSessionDefaults(t *testing.T) {
	root := "/home/nimvor/shop.test"
	s := Spec{Domain: "shop.test", User: "shop", Root: root, PHP: PHPSettings{MemoryLimitMB: 256, UploadMaxMB: 64, MaxExecSeconds: 60, MaxChildren: 5, PM: "ondemand", MaxRequests: 500}}
	l := Layout{LogDir: root + "/logs", TmpDir: root + "/tmp"}
	pool := RenderPool(s, l)
	for _, want := range []string{"php_flag[session.cookie_httponly] = on\n", "php_flag[session.use_strict_mode] = on\n", "php_flag[session.use_only_cookies] = on\n"} {
		if !strings.Contains(pool, want) {
			t.Errorf("pool lacks %q", want)
		}
	}
	if strings.Contains(pool, "cookie_secure") {
		t.Error("cookie_secure set on a website reachable over HTTP")
	}
	s.HTTPS, s.ForceHTTPS = true, true
	if !strings.Contains(RenderPool(s, l), "php_flag[session.cookie_secure] = on\n") {
		t.Error("HTTPS-only website without secure session cookies")
	}
	// The website can still choose otherwise.
	ini, err := NormalizeINI(map[string]string{"session.use_strict_mode": "0"}, root)
	if err != nil {
		t.Fatal(err)
	}
	s.PHP.INI = ini
	if got := RenderPool(s, l); !strings.Contains(got, "php_flag[session.use_strict_mode] = off\n") || strings.Count(got, "use_strict_mode") != 1 {
		t.Errorf("website override not applied:\n%s", got)
	}
}

// PHP's mail() uses NimVor's sendmail, fixed by the administrator.
func TestPoolSendmailPath(t *testing.T) {
	root := "/home/nimvor/shop.test"
	s := Spec{Domain: "shop.test", User: "shop", Root: root, PHP: PHPSettings{MemoryLimitMB: 256, UploadMaxMB: 64, MaxExecSeconds: 60, MaxChildren: 5, PM: "ondemand", MaxRequests: 500}}
	l := Layout{LogDir: root + "/logs", TmpDir: root + "/tmp"}
	if strings.Contains(RenderPool(s, l), "sendmail_path") {
		t.Error("sendmail_path without NimVor's sendmail")
	}
	l.Sendmail = "/usr/local/bin/nimvor-agent sendmail -t -i"
	if !strings.Contains(RenderPool(s, l), "php_admin_value[sendmail_path] = /usr/local/bin/nimvor-agent sendmail -t -i\n") {
		t.Error("PHP mail does not use NimVor's sendmail")
	}
	if _, err := NormalizeINI(map[string]string{"sendmail_path": "/tmp/x"}, root); err == nil {
		t.Error("a website may set sendmail_path")
	}
}
