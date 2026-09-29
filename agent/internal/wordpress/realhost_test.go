//go:build realhost

package wordpress

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/apps"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

// oldWordPress is installed in CI (network) so that a core update can be
// tested; WHACP_WP_TARBALL (with a .sha1 next to it) replaces it offline.
const oldWordPress = "https://wordpress.org/wordpress-6.5.5.tar.gz"

// Requires root, PHP with mysqli, a local MariaDB, GitHub (WP-CLI) and
// either WHACP_WP_TARBALL or WHACP_NETWORK=1 (WordPress.org).
func TestRealHostWordPressToolkit(t *testing.T) {
	tarball := os.Getenv("WHACP_WP_TARBALL")
	network := os.Getenv("WHACP_NETWORK") == "1"
	if os.Geteuid() != 0 || (tarball == "" && !network) {
		t.Skip("needs root and WHACP_WP_TARBALL or WHACP_NETWORK=1")
	}
	src := apps.Sources{WordPressURL: oldWordPress, Client: &http.Client{Timeout: 5 * time.Minute}}
	if tarball != "" {
		srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(tarball))))
		defer srv.Close()
		src = apps.Sources{WordPressURL: srv.URL + "/" + filepath.Base(tarball), Client: srv.Client()}
	}
	hadPhar := pharReady(&host.Env{Root: filesystem.Host()})

	sitesRoot, _ := os.MkdirTemp("/tmp", "whacp-wpt-")
	os.Chmod(sitesRoot, 0o711)
	t.Cleanup(func() { os.RemoveAll(sitesRoot) })
	for _, u := range []string{"1781:whacpwt", "1782:whacpwt2"} {
		id, name, _ := strings.Cut(u, ":")
		exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", "-u", id, name).Run()
		t.Cleanup(func() { exec.Command("userdel", name).Run() })
	}
	root := filepath.Join(sitesRoot, "wpt.test")
	os.MkdirAll(filepath.Join(root, "public_html"), 0o755)
	os.MkdirAll(filepath.Join(root, "tmp"), 0o755)
	exec.Command("chown", "-R", "whacpwt:whacpwt", root).Run()
	os.Chmod(root, 0o710)
	const dbPass = "wpt-Db-Secret-77"
	sql := "CREATE DATABASE IF NOT EXISTS whacp_wpt; CREATE USER IF NOT EXISTS 'whacp_wpt'@'localhost' IDENTIFIED BY '" + dbPass + "'; GRANT ALL ON whacp_wpt.* TO 'whacp_wpt'@'localhost';"
	if out, err := exec.Command("mysql", "-e", sql).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	t.Cleanup(func() {
		exec.Command("mysql", "-e", "DROP DATABASE IF EXISTS whacp_wpt; DROP USER IF EXISTS 'whacp_wpt'@'localhost'").Run()
	})

	cfg := config.Default()
	cfg.SitesRoot = sitesRoot
	cfg.StateDir = t.TempDir()
	logBuf := &strings.Builder{}
	log := slog.New(slog.NewTextHandler(logBuf, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	env := host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(10*time.Minute), jm, log)
	t.Cleanup(func() {
		if !hadPhar {
			os.Remove(WPCLIPath)
		}
	})
	cred, _, err := env.Credential("whacpwt")
	if err != nil {
		t.Fatal(err)
	}
	php := testhost.PHPVersion()
	req := apps.Request{App: "wordpress", Domain: "wpt.test", User: "whacpwt", Root: root, Path: "public_html", PHPVersion: php,
		DB: apps.DB{Name: "whacp_wpt", User: "whacp_wpt", Password: dbPass, Host: "localhost"}, URL: "https://wpt.test",
		Title: "Toolkit test", AdminUser: "owner", AdminEmail: "owner@wpt.test", AdminPassword: "a-long-admin-password-1"}
	var buf strings.Builder
	if _, err := apps.Install(context.Background(), env, src, req, cred, &buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	ctx := context.Background()
	site := Site{Domain: "wpt.test", User: "whacpwt", Root: root, PHPVersion: php}

	// Scan: found without loading WordPress, no password reported.
	found, err := scanSite(ctx, env, site)
	if err != nil || len(found) != 1 || found[0].Path != "public_html" || found[0].Version == "" || found[0].DBName != "whacp_wpt" || !found[0].FileEditDisabled {
		t.Fatalf("scan: %+v %v", found, err)
	}
	if b, _ := json.Marshal(found); strings.Contains(string(b), dbPass) {
		t.Fatal("the scan reported the database password")
	}
	target := Target{Site: site, Path: "public_html"}

	// Overview through WP-CLI as the account.
	in, err := info(ctx, env, target, false)
	if err != nil {
		t.Fatal(err)
	}
	if in.Version != found[0].Version || in.SiteURL != "https://wpt.test" || in.DB.Name != "whacp_wpt" || len(in.Admins) != 1 || in.Admins[0].Login != "owner" || in.DB.Tables == 0 {
		t.Fatalf("info: %+v", in)
	}
	if b, _ := json.Marshal(in); strings.Contains(string(b), dbPass) {
		t.Fatal("the overview reported the database password")
	}
	if len(in.Plugins) == 0 || len(in.Themes) == 0 {
		t.Fatalf("plugins %v themes %v", in.Plugins, in.Themes)
	}
	st, _ := os.Stat(WPCLIPath)
	if st == nil || st.Mode().Perm() != 0o644 || !pharReady(env) {
		t.Fatalf("WP-CLI copy: %v", st)
	}

	do := func(op string, items []string, value string) map[string]any {
		t.Helper()
		o := Operation{Target: target, Op: op, Items: items, Value: value}
		if err := o.validate(env); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		res, job, err := manage(ctx, env, o)
		if err != nil {
			t.Fatalf("%s: %v", op, err)
		}
		if job != nil {
			var jl strings.Builder
			if _, err := job(ctx, &jl); err != nil {
				t.Fatalf("%s job: %v\n%s", op, err, jl.String())
			}
		}
		return res
	}
	plugin := in.Plugins[0].Name
	do("plugin_activate", []string{plugin}, "")
	if in2, _ := info(ctx, env, target, false); !in2.Plugins[0].Active {
		t.Fatalf("%s not active: %+v", plugin, in2.Plugins)
	}
	do("plugin_deactivate", []string{plugin}, "")
	do("plugin_auto_on", []string{plugin}, "")
	do("maintenance_on", nil, "")
	if _, err := os.Stat(filepath.Join(root, "public_html/.maintenance")); err != nil {
		t.Fatal("maintenance mode not on")
	}
	do("maintenance_off", nil, "")
	do("core_auto_update", nil, "minor")
	do("search_engines_off", nil, "")
	cfgFile := filepath.Join(root, "public_html/wp-config.php")
	os.Chmod(cfgFile, 0o644)
	do("protect_config", nil, "")
	in3, err := info(ctx, env, target, false)
	if err != nil {
		t.Fatal(err)
	}
	if in3.Plugins[0].Active || !in3.Plugins[0].AutoUpdate || in3.Maintenance || in3.Constants["WP_AUTO_UPDATE_CORE"] != "'minor'" || in3.SearchEngines || in3.ConfigMode != "0600" {
		t.Fatalf("after changes: %+v", in3)
	}
	if b, _ := exec.Command("stat", "-c", "%U %a", cfgFile).Output(); strings.TrimSpace(string(b)) != "whacpwt 600" {
		t.Fatalf("wp-config.php: %s", b)
	}

	// One-time sign-in file: the account's, private, gone after one use.
	res, err := login(ctx, env, target, in.Admins[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "public_html", res["file"].(string))
	if b, _ := exec.Command("stat", "-c", "%U %a", file).Output(); strings.TrimSpace(string(b)) != "whacpwt 600" {
		t.Fatalf("sign-in file: %s", b)
	}
	phpBin := "php" + php
	run := exec.Command(phpBin, file)
	run.Env = []string{"REQUEST_METHOD=GET", "HTTP_HOST=wpt.test"}
	run.Dir = filepath.Dir(file)
	out, _ := run.CombinedOutput()
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("the sign-in file is still there after use: %s", out)
	}
	// An expired one only deletes itself.
	env.Now = func() time.Time { return time.Now().Add(-10 * time.Minute) }
	res, _ = login(ctx, env, target, in.Admins[0].ID)
	env.Now = func() time.Time { return time.Now().UTC() }
	file = filepath.Join(root, "public_html", res["file"].(string))
	run = exec.Command(phpBin, file)
	run.Env = []string{"REQUEST_METHOD=GET"}
	if out, _ := run.CombinedOutput(); strings.Contains(string(out), "wp-admin") {
		t.Fatalf("an expired link signed in: %s", out)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatal("expired sign-in file not deleted")
	}
	if _, err := login(ctx, env, target, 999); err == nil {
		t.Fatal("a sign-in link for a user who is not an administrator")
	}

	// Another account cannot read or manage this website.
	other := Target{Site: Site{Domain: "wpt.test", User: "whacpwt2", Root: root, PHPVersion: php}, Path: "public_html"}
	if _, err := info(ctx, env, other, false); err == nil {
		t.Fatal("another account read the installation")
	}
	if f, err := scanSite(ctx, env, other.Site); err == nil && len(f) > 0 {
		t.Fatalf("another account found the installation: %+v", f)
	}

	// Unknown operations and names that could be options are refused.
	for _, o := range []Operation{{Target: target, Op: "eval"}, {Target: target, Op: "plugin_activate", Items: []string{"--require=/tmp/x.php"}}, {Target: target, Op: "plugin_activate"}} {
		if o.validate(env) == nil {
			t.Errorf("accepted %+v", o)
		}
	}

	// Debugging: exactly the chosen settings; the debug log is read back.
	do("debug", []string{"WP_DEBUG", "WP_DEBUG_LOG"}, "")
	if c := wpEval(t, root, "echo json_encode([WP_DEBUG, WP_DEBUG_LOG, WP_DEBUG_DISPLAY, SCRIPT_DEBUG]);"); c != "[true,true,false,false]" {
		t.Fatalf("debug constants: %s", c)
	}
	logFile := filepath.Join(root, "public_html/wp-content/debug.log")
	os.WriteFile(logFile, []byte("PHP Notice: one\nPHP Warning: two\n"), 0o644)
	exec.Command("chown", "whacpwt:whacpwt", logFile).Run()
	if r := do("debug_log", nil, ""); r["exists"] != true || r["path"] != "public_html/wp-content/debug.log" || !strings.Contains(fmt.Sprint(r["lines"]), "PHP Warning: two") {
		t.Fatalf("debug log: %v", r)
	}
	do("debug", nil, "")
	if c := wpEval(t, root, "echo json_encode([WP_DEBUG, WP_DEBUG_LOG]);"); c != "[false,false]" {
		t.Fatalf("debug off: %s", c)
	}

	// wp-cron.php taken over: DISABLE_WP_CRON and a command that runs the
	// due tasks as the account.
	r := do("cron_takeover_on", nil, "")
	cmd, _ := r["command"].(string)
	if !strings.Contains(cmd, " --path="+filepath.Join(root, "public_html")+" cron event run --due-now") || wpEval(t, root, "var_export(DISABLE_WP_CRON);") != "true" {
		t.Fatalf("takeover: %v", r)
	}
	if out, err := exec.Command("runuser", "-u", "whacpwt", "--", "sh", "-c", cmd).CombinedOutput(); err != nil {
		t.Fatalf("the cron command fails: %v %s", err, out)
	}
	do("cron_takeover_off", nil, "")
	if wpEval(t, root, "var_export(DISABLE_WP_CRON);") != "false" {
		t.Fatal("takeover not off")
	}

	// Passwords with quotes, backslashes and dollars: only on standard input.
	const adminPass = `new 'admin' "pass" \$x\`
	secret := func(op string, items []string, pw string) (map[string]any, error) {
		o := Operation{Target: target, Op: op, Items: items, Password: pw}
		if err := o.validate(env); err != nil {
			return nil, err
		}
		res, _, err := manage(ctx, env, o)
		return res, err
	}
	if _, err := secret("admin_password", []string{fmt.Sprint(in.Admins[0].ID)}, adminPass); err != nil {
		t.Fatal(err)
	}
	if ok := wpEvalEnv(t, root, `echo wp_check_password(getenv("P"), get_user_by("login", "owner")->user_pass) ? "yes" : "no";`, "P="+adminPass); ok != "yes" {
		t.Fatalf("admin password not changed: %s", ok)
	}
	if _, err := secret("admin_password", []string{"999"}, "long-enough-1"); err == nil {
		t.Fatal("password set for a user who is not an administrator")
	}
	const newDB = `db'Pa"ss\w0rd$1`
	if _, err := secret("db_password", nil, newDB); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mysql", "-uwhacp_wpt", "-p"+newDB, "-e", "SELECT 1").CombinedOutput(); err != nil {
		t.Fatalf("the new database password does not work: %s", out)
	}
	if err := exec.Command("mysql", "-uwhacp_wpt", "-p"+dbPass, "-e", "SELECT 1").Run(); err == nil {
		t.Fatal("the old database password still works")
	}
	if _, err := info(ctx, env, target, false); err != nil {
		t.Fatalf("WordPress cannot reach its database after the change: %v", err)
	}
	for _, bad := range []string{"short", "two\nlines-long"} {
		if _, err := secret("db_password", nil, bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}

	// Hardening: apply, then roll back to exactly what was there.
	os.Chmod(cfgFile, 0o644)
	sec := func(op string, m ...string) map[string]any {
		t.Helper()
		res, err := security(ctx, env, SecurityRequest{Target: target, URLPath: "/", Op: op, Measures: m})
		if err != nil {
			t.Fatalf("%s %v: %v", op, m, err)
		}
		return res
	}
	state := func(res map[string]any) map[string]bool {
		out := map[string]bool{}
		for _, m := range res["measures"].([]map[string]any) {
			out[m["id"].(string)] = m["applied"].(bool)
		}
		return out
	}
	before := state(sec("status"))
	if before["disable_pingbacks"] || before["config_permissions"] || !before["disable_file_edit"] {
		t.Fatalf("status before: %v", before)
	}
	oldKey := wpEval(t, root, "echo AUTH_KEY;")
	after := state(sec("apply", "disable_pingbacks", "disable_script_concat", "config_permissions", "security_keys", "debug_off"))
	for _, m := range []string{"disable_pingbacks", "disable_script_concat", "config_permissions", "debug_off"} {
		if !after[m] {
			t.Errorf("%s not applied", m)
		}
	}
	if wpEval(t, root, "echo AUTH_KEY;") == oldKey {
		t.Error("security keys not changed")
	}
	sec("rollback", "disable_pingbacks", "disable_script_concat", "config_permissions", "debug_off")
	if b, _ := exec.Command("stat", "-c", "%a", cfgFile).Output(); strings.TrimSpace(string(b)) != "644" {
		t.Fatalf("mode not restored: %s", b)
	}
	if got := wpEval(t, root, `echo get_option("default_ping_status"), defined("CONCATENATE_SCRIPTS") ? "defined" : "none";`); got != "opennone" {
		t.Fatalf("after rollback: %s", got)
	}
	if _, err := security(ctx, env, SecurityRequest{Target: target, URLPath: "/", Op: "rollback", Measures: []string{"security_keys"}}); err == nil {
		t.Fatal("security keys rolled back")
	}
	if b, _ := os.ReadFile(filepath.Join(cfg.StateDir, "wordpress-security.json")); strings.Contains(string(b), "AUTH_KEY") {
		t.Fatal("state holds keys")
	}

	if network {
		// Updates from WordPress.org.
		in4, err := info(ctx, env, target, true)
		if err != nil {
			t.Fatal(err)
		}
		if in4.Core.Latest == "" || in4.Core.CheckedAt == 0 {
			t.Fatalf("update check: %+v", in4.Core)
		}
		do("plugin_update", nil, "")
		if in4.Core.Latest != in4.Version {
			do("core_update", nil, "")
			if in5, _ := info(ctx, env, target, false); in5.Version != in4.Core.Latest {
				t.Fatalf("core update: %s, want %s", in5.Version, in4.Core.Latest)
			}
		}
		if r := do("verify_core", nil, ""); r["ok"] != true {
			t.Fatalf("verify: %v", r)
		}
		os.WriteFile(filepath.Join(root, "public_html/wp-includes/version.php"), []byte("<?php $wp_version = '1.0';\n"), 0o644)
		if r := do("verify_core", nil, ""); r["ok"] != false || !strings.Contains(strings.Join(anyStrings(r["files"]), " "), "wp-includes/version.php") {
			t.Fatalf("verify after a change: %v", r)
		}
	}
	if strings.Contains(logBuf.String(), dbPass) || strings.Contains(logBuf.String(), newDB) || strings.Contains(logBuf.String(), "admin\" \"pass") {
		t.Fatal("the database password is in the agent log")
	}
	_ = io.Discard
}

func anyStrings(v any) []string {
	s, _ := v.([]string)
	return s
}

// wpEval runs PHP inside the installation as root (checks only).
func wpEval(t *testing.T, root, code string) string { return wpEvalEnv(t, root, code) }

func wpEvalEnv(t *testing.T, root, code string, env ...string) string {
	t.Helper()
	c := exec.Command("php"+testhost.PHPVersion(), WPCLIPath, "--allow-root", "--skip-plugins", "--skip-themes", "--path="+filepath.Join(root, "public_html"), "eval", code)
	c.Env = append(os.Environ(), env...)
	out, err := c.Output()
	if err != nil {
		t.Fatalf("eval %s: %v", code, err)
	}
	return strings.TrimSpace(string(out))
}
