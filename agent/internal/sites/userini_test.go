package sites

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// phpSite applies a PHP website on the fixture host and returns the
// manager, the document root on disk and the pool's path.
func phpSite(t *testing.T, domain string, mod func(*Spec)) (*testhost.Host, *Manager, string, string) {
	t.Helper()
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	s := Spec{Domain: domain, User: "u" + strings.ReplaceAll(strings.Split(domain, ".")[0], "-", ""), CreateUser: true, Kind: "php", PHPVersion: "8.3", Enabled: true}
	if mod != nil {
		mod(&s)
	}
	if _, err := m.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	doc := filepath.Join(h.Base, "home/nimvor", domain, "public_html")
	return h, m, doc, filepath.Join(h.Base, "etc/php/8.3/fpm/pool.d/nimvor-"+domain+".conf")
}

func pool(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func scan(m *Manager) { m.scanPHPConfig(context.Background(), &specCache{}) }

func TestWebsiteWithoutCustomFilesHasNoCustomerLayer(t *testing.T) {
	_, m, _, pf := phpSite(t, "plain.test", nil)
	p := pool(t, pf)
	if !strings.Contains(p, "php_value[memory_limit] = 256M\n") || !strings.Contains(p, "php_admin_value[open_basedir]") {
		t.Errorf("pool:\n%s", p)
	}
	cfg, err := m.Describe("plain.test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Status != "active" || cfg.PHPINI[len(cfg.PHPINI)-1].Exists || cfg.UserINI.Exists || !cfg.Managed {
		t.Errorf("%+v", cfg)
	}
	if cfg.CacheTTL != 300 {
		t.Errorf("ttl %d", cfg.CacheTTL)
	}
}

func TestPHPINIIsDetectedAppliedChangedAndRemovedWithoutAnApplyButton(t *testing.T) {
	h, m, doc, pf := phpSite(t, "shop.test", nil)
	ctx := context.Background()
	reloads := func() int {
		n := 0
		for _, c := range h.Runner.Calls {
			if strings.HasPrefix(c, "systemctl reload -- php8.3-fpm.service") {
				n++
			}
		}
		return n
	}
	nginxReloads := func() int {
		n := 0
		for _, c := range h.Runner.Calls {
			if strings.HasPrefix(c, "systemctl reload -- nginx.service") {
				n++
			}
		}
		return n
	}
	r0, n0 := reloads(), nginxReloads()
	scan(m) // nothing changed: nothing is written, nothing is reloaded
	if reloads() != r0 {
		t.Fatal("reloaded without a change")
	}

	// Created.
	php := filepath.Join(doc, "php.ini")
	os.WriteFile(php, []byte("; mine\n[PHP]\nmemory_limit = 512M\nupload_max_filesize = 40M\nmax_execution_time = 120\ndisplay_errors = On\n"), 0o644)
	scan(m)
	p := pool(t, pf)
	for _, want := range []string{"php_value[memory_limit] = 512M\n", "php_value[upload_max_filesize] = 40M\n", "php_value[max_execution_time] = 120\n", "php_flag[display_errors] = on\n"} {
		if !strings.Contains(p, want) {
			t.Errorf("pool lacks %q:\n%s", want, p)
		}
	}
	if strings.Count(p, "[memory_limit]") != 1 {
		t.Errorf("a repeated pool entry would not win in PHP-FPM:\n%s", p)
	}
	if reloads() != r0+1 {
		t.Errorf("PHP-FPM reloads: %d", reloads()-r0)
	}
	if nginxReloads() != n0 {
		t.Error("Nginx was reloaded for a php.ini")
	}
	if !h.Runner.Called("php-fpm8.3 -t") {
		t.Error("the pool was not validated")
	}

	// Scanning again changes nothing.
	calls := len(h.Runner.Calls)
	scan(m)
	if len(h.Runner.Calls) != calls {
		t.Errorf("an unchanged file caused work: %v", h.Runner.Calls[calls:])
	}

	// Modified.
	os.WriteFile(php, []byte("memory_limit = 64M\n"), 0o644)
	os.Chtimes(php, time.Now().Add(time.Minute), time.Now().Add(time.Minute))
	scan(m)
	p = pool(t, pf)
	if !strings.Contains(p, "php_value[memory_limit] = 64M\n") || strings.Contains(p, "upload_max_filesize] = 40M") || !strings.Contains(p, "php_value[upload_max_filesize] = 64M\n") {
		t.Errorf("a removed line must return to NimVor's value:\n%s", p)
	}

	// Renamed away, then deleted: back to NimVor's own values.
	os.Rename(php, filepath.Join(doc, "php.ini.bak"))
	scan(m)
	if p = pool(t, pf); !strings.Contains(p, "php_value[memory_limit] = 256M\n") {
		t.Errorf("a renamed php.ini still applies:\n%s", p)
	}
	_ = ctx
}

func TestCustomerPHPINIRulesAreEnforced(t *testing.T) {
	_, m, doc, pf := phpSite(t, "rules.test", nil)
	os.WriteFile(filepath.Join(doc, "php.ini"), []byte(strings.Join([]string{
		"memory_limit = 1G",
		"open_basedir = /",
		"disable_functions =",
		"extension = evil.so",
		"auto_prepend_file = /etc/passwd",
		"auto_append_file = ../../other.test/public_html/x.php",
		"error_log = /tmp/x",
		"upload_tmp_dir = /tmp",
		"date.timezone = ${HOME}",
		"post_max_size = 8M\nphp_admin_value[open_basedir] = /",
		"session.name = a b",
		"display_errors = maybe",
		"allow_url_include = On",
		"[HOST=evil.test]",
		"memory_limit = 2G",
	}, "\n")), 0o644)
	scan(m)
	p := pool(t, pf)
	for _, bad := range []string{"open_basedir] = /\n", "evil.so", "/etc/passwd", "other.test", "/tmp/x", "HOME", "allow_url_include", "2G", "disable_functions] = \n"} {
		if strings.Contains(p, bad) {
			t.Errorf("pool contains %q:\n%s", bad, p)
		}
	}
	if !strings.Contains(p, "php_value[memory_limit] = 1G") {
		t.Errorf("a valid value before the unsupported section is lost:\n%s", p)
	}
	if !strings.Contains(p, "php_admin_value[open_basedir] = "+"/home/nimvor/rules.test/:/usr/share/php/:/dev/urandom\n") {
		t.Errorf("open_basedir changed:\n%s", p)
	}
	cfg, _ := m.Describe("rules.test")
	states := map[string]string{}
	for _, f := range cfg.PHPINI {
		for _, d := range f.Directives {
			if d.Key != "" {
				states[d.Key+"@"+strings.Split(f.Where, " ")[0]] = d.State
			}
		}
	}
	for k, want := range map[string]string{"open_basedir@document": StateLocked, "extension@document": StateUnsupported, "auto_prepend_file@document": StateInvalid, "date.timezone@document": StateInvalid, "display_errors@document": StateInvalid, "disable_functions@document": StateInvalid, "allow_url_include@document": StateUnsupported, "memory_limit@document": StateApplied} {
		if states[k] != want {
			t.Errorf("%s = %q, want %q (%v)", k, states[k], want, states)
		}
	}
	if cfg.Warnings == 0 {
		t.Error("unsupported directives are not reported")
	}
}

func TestBothFilesAndTheWebsiteFolderHaveAClearOrder(t *testing.T) {
	_, m, doc, pf := phpSite(t, "order.test", func(s *Spec) {
		s.PHP.INI = map[string]string{"date.timezone": "Asia/Karachi", "max_input_vars": "5000"}
	})
	root := filepath.Dir(doc)
	os.WriteFile(filepath.Join(root, "php.ini"), []byte("memory_limit = 300M\nmax_input_vars = 3000\n"), 0o644)
	os.WriteFile(filepath.Join(doc, "php.ini"), []byte("memory_limit = 400M\n"), 0o644)
	os.WriteFile(filepath.Join(doc, ".user.ini"), []byte("memory_limit = 500M\ndate.timezone = UTC\nopen_basedir = /\nfile_uploads = Off\nmy_ext.setting = 1\nupload_max_filesize = 9M\nupload_max_filesize = 11M\n"), 0o644)
	scan(m)
	p := pool(t, pf)
	// The panel's website value yields to the customer's php.ini; the nearer php.ini wins.
	if !strings.Contains(p, "php_value[memory_limit] = 400M\n") || !strings.Contains(p, "php_value[max_input_vars] = 3000\n") || !strings.Contains(p, "php_value[date.timezone] = Asia/Karachi\n") {
		t.Errorf("pool:\n%s", p)
	}
	cfg, _ := m.Describe("order.test")
	eff := map[string]EffectiveSetting{}
	for _, e := range cfg.Effective {
		eff[e.Key] = e
	}
	if e := eff["memory_limit"]; e.Value != "500M" || e.Source != UserININame {
		t.Errorf(".user.ini must win: %+v", e)
	}
	if e := eff["date.timezone"]; e.Value != "UTC" || e.Source != UserININame {
		t.Errorf("%+v", e)
	}
	if e := eff["max_input_vars"]; e.Value != "3000" || e.Source != PHPININame {
		t.Errorf("%+v", e)
	}
	if e := eff["open_basedir"]; e.Source != "nimvor-locked" || strings.Contains(e.Value, "/\n") || e.Value == "/" {
		t.Errorf("a .user.ini changed the confinement: %+v", e)
	}
	if e := eff["upload_max_filesize"]; e.Value != "11M" {
		t.Errorf("the last assignment wins: %+v", e)
	}
	user := map[string]Directive{}
	for _, d := range cfg.UserINI.Directives {
		if d.Line != 0 {
			user[d.Key] = d
		}
	}
	if user["open_basedir"].State != StateLocked || user["file_uploads"].State != StateSystem || user["my_ext.setting"].State != StateUnchecked || user["memory_limit"].State != StateApplied {
		t.Errorf("%+v", user)
	}
	// php.ini lines replaced by a later php.ini are said to be.
	var shadowed bool
	for _, f := range cfg.PHPINI {
		for _, d := range f.Directives {
			shadowed = shadowed || (d.Key == "memory_limit" && d.State == StateOverridden)
		}
	}
	if !shadowed {
		t.Error("the replaced php.ini line is not marked")
	}
}

func TestLockedPlanLimitsCannotBeChangedByCustomerFiles(t *testing.T) {
	_, m, doc, pf := phpSite(t, "locked.test", func(s *Spec) { s.PHP.LockINI = true })
	os.WriteFile(filepath.Join(doc, "php.ini"), []byte("memory_limit = 2G\nmax_input_vars = 4000\n"), 0o644)
	scan(m)
	p := pool(t, pf)
	if !strings.Contains(p, "php_admin_value[memory_limit] = 256M\n") || strings.Contains(p, "2G") || !strings.Contains(p, "php_value[max_input_vars] = 4000\n") {
		t.Errorf("pool:\n%s", p)
	}
}

func TestSymlinksForeignAndHugeFilesAreNotRead(t *testing.T) {
	_, m, doc, pf := phpSite(t, "evil.test", nil)
	secret := filepath.Join(t.TempDir(), "secret.ini")
	os.WriteFile(secret, []byte("memory_limit = 7G\n"), 0o644)
	os.Symlink(secret, filepath.Join(doc, "php.ini"))
	scan(m)
	if p := pool(t, pf); strings.Contains(p, "7G") {
		t.Errorf("a link was followed:\n%s", p)
	}
	cfg, _ := m.Describe("evil.test")
	if f := cfg.PHPINI[len(cfg.PHPINI)-1]; !f.Exists || f.Problem == "" {
		t.Errorf("%+v", f)
	}
	os.Remove(filepath.Join(doc, "php.ini"))
	os.WriteFile(filepath.Join(doc, "php.ini"), []byte("; "+strings.Repeat("x", 70*1024)), 0o644)
	scan(m)
	cfg, _ = m.Describe("evil.test")
	if f := cfg.PHPINI[len(cfg.PHPINI)-1]; f.Problem == "" || !strings.Contains(f.Problem, "larger") {
		t.Errorf("%+v", f)
	}
	// A document root that is a link to another place is not entered.
	os.Remove(filepath.Join(doc, "php.ini"))
	if os.Geteuid() == 0 {
		other := t.TempDir()
		os.WriteFile(filepath.Join(other, "php.ini"), []byte("memory_limit = 6G\n"), 0o644)
		os.Rename(doc, doc+".real")
		os.Symlink(other, doc)
		scan(m)
		if p := pool(t, pf); strings.Contains(p, "6G") {
			t.Errorf("a linked document root was read:\n%s", p)
		}
	}
}

func TestAHandEditedPoolIsLeftAlone(t *testing.T) {
	_, m, doc, pf := phpSite(t, "hand.test", nil)
	b := pool(t, pf)
	os.WriteFile(pf, []byte(b+"; edited by the administrator\n"), 0o644)
	os.WriteFile(filepath.Join(doc, "php.ini"), []byte("memory_limit = 100M\n"), 0o644)
	scan(m)
	if p := pool(t, pf); !strings.Contains(p, "edited by the administrator") || strings.Contains(p, "100M") {
		t.Errorf("a hand-edited pool was replaced:\n%s", p)
	}
	cfg, _ := m.Describe("hand.test")
	if cfg.Status != "error" || cfg.Message == "" {
		t.Errorf("%+v", cfg)
	}
}

func TestInvalidPoolIsNotInstalledAndTheOldOneIsKept(t *testing.T) {
	h, m, doc, pf := phpSite(t, "bad.test", nil)
	h.Fail["php-fpm8.3 -t"] = "ERROR: boom"
	os.WriteFile(filepath.Join(doc, "php.ini"), []byte("memory_limit = 100M\n"), 0o644)
	scan(m)
	if p := pool(t, pf); strings.Contains(p, "100M") {
		t.Errorf("an invalid pool was kept:\n%s", p)
	}
	cfg, _ := m.Describe("bad.test")
	if cfg.Status != "error" {
		t.Errorf("%+v", cfg)
	}
}

func TestWebsitesDoNotShareConfiguration(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	for _, d := range []string{"a.test", "b.test"} {
		if _, err := m.Apply(context.Background(), Spec{Domain: d, User: "u" + d[:1], CreateUser: true, Kind: "php", PHPVersion: "8.3", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(h.Base, "home/nimvor/a.test/public_html/php.ini"), []byte("memory_limit = 111M\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "home/nimvor/b.test/public_html/php.ini"), []byte("memory_limit = 222M\n"), 0o644)
	scan(m)
	a := pool(t, filepath.Join(h.Base, "etc/php/8.3/fpm/pool.d/nimvor-a.test.conf"))
	b := pool(t, filepath.Join(h.Base, "etc/php/8.3/fpm/pool.d/nimvor-b.test.conf"))
	if !strings.Contains(a, "memory_limit] = 111M") || strings.Contains(a, "222M") || !strings.Contains(b, "memory_limit] = 222M") || strings.Contains(b, "111M") {
		t.Errorf("a:\n%s\nb:\n%s", a, b)
	}
}

func TestPHPINIFollowsAChangeOfPHPVersion(t *testing.T) {
	h, m, doc, _ := phpSite(t, "move.test", nil)
	os.WriteFile(filepath.Join(doc, "php.ini"), []byte("memory_limit = 123M\n"), 0o644)
	scan(m)
	os.MkdirAll(filepath.Join(h.Base, "etc/php/8.2/fpm/pool.d"), 0o755)
	s := Spec{Domain: "move.test", User: "umove", Kind: "php", PHPVersion: "8.2", Enabled: true}
	if _, err := m.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if p := pool(t, filepath.Join(h.Base, "etc/php/8.2/fpm/pool.d/nimvor-move.test.conf")); !strings.Contains(p, "memory_limit] = 123M") {
		t.Errorf("the new version's pool lacks the website's php.ini:\n%s", p)
	}
	if _, err := os.Stat(filepath.Join(h.Base, "etc/php/8.3/fpm/pool.d/nimvor-move.test.conf")); err == nil {
		t.Error("the old version's pool was kept")
	}
	// The record follows the website: a later change is applied to the new version's pool.
	os.WriteFile(filepath.Join(doc, "php.ini"), []byte("memory_limit = 77M\n"), 0o644)
	scan(m)
	if p := pool(t, filepath.Join(h.Base, "etc/php/8.2/fpm/pool.d/nimvor-move.test.conf")); !strings.Contains(p, "memory_limit] = 77M") {
		t.Errorf("%s", p)
	}
}

func TestParserHandlesCommentsQuotesAndRefusesTricks(t *testing.T) {
	d := parseCustomerINI([]byte("\xef\xbb\xbf; c\n[PHP]\nmemory_limit = \"128M\" ; note\ndate.timezone='UTC'\nerror_reporting = E_ALL & ~E_NOTICE\n#old = 1\nfoo[bar] = 1\nx = \"unclosed\nnoequals\nv = a$b\nw = a\\b\n[Other]\nlater = 1\n"))
	got := map[string]Directive{}
	for _, x := range d {
		got[x.Key] = x
	}
	if got["memory_limit"].Value != "128M" || got["memory_limit"].State != "" || got["date.timezone"].Value != "UTC" || got["error_reporting"].Value != "E_ALL & ~E_NOTICE" {
		t.Errorf("%+v", got)
	}
	for _, k := range []string{"#old", "foo[bar]", "x", "noequals", "v", "w", "[Other]"} {
		if got[k].State == "" {
			t.Errorf("%q was accepted: %+v", k, got[k])
		}
	}
	if _, ok := got["later"]; ok {
		t.Error("a line below an unsupported section was read")
	}
	if d := parseCustomerINI([]byte("a\x00b")); len(d) != 1 || d[0].State != StateInvalid {
		t.Errorf("%+v", d)
	}
}

func TestNginxNeverServesTheCustomersConfiguration(t *testing.T) {
	h, _, _, _ := phpSite(t, "serve.test", nil)
	conf := h.Read("etc/nginx/sites-available/nimvor-serve.test.conf")
	if !strings.Contains(conf, `location ~* /php\.ini$`) || !strings.Contains(conf, `location ~ /\.(?!well-known)`) {
		t.Errorf("nginx config:\n%s", conf)
	}
}
