package wordpress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

const fakePhar = "<?php // not really WP-CLI\n"

// fixture: a website with WordPress files, a fake WP-CLI in place, and
// LatestVersion stubbed.
func fixture(t *testing.T) (*testhost.Host, Target) {
	t.Helper()
	h := testhost.New(t)
	old := wpcliSHA512
	wpcliSHA512 = sha512Hex([]byte(fakePhar))
	t.Cleanup(func() { wpcliSHA512 = old })
	oldLatest := LatestVersion
	LatestVersion = func(context.Context) string { return "6.9" }
	t.Cleanup(func() { LatestVersion = oldLatest })
	os.MkdirAll(filepath.Join(h.Base, "usr/local/lib/whacp"), 0o755)
	os.WriteFile(filepath.Join(h.Base, WPCLIPath), []byte(fakePhar), 0o644)
	root := filepath.Join(h.Env.Cfg.SitesRoot, "shop.test")
	os.MkdirAll(filepath.Join(h.Base, root, "public_html"), 0o755)
	os.WriteFile(filepath.Join(h.Base, root, "public_html/wp-settings.php"), []byte("<?php"), 0o644)
	return h, Target{Site: Site{Domain: "shop.test", User: "example", Root: root, PHPVersion: "8.3"}, Path: "public_html"}
}

func infoJSON(v map[string]any) string {
	b, _ := json.Marshal(v)
	return "Notice: noise from a mu-plugin\n" + marker + string(b) + "\n"
}

// WP-CLI runs as the website account, never as root, with the website's
// plugins and themes and any WP-CLI configuration left out.
func TestCommandsRunAsTheAccountWithoutPlugins(t *testing.T) {
	h, target := fixture(t)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "php8.3" && strings.Contains(strings.Join(args, " "), "eval-file -") {
			return infoJSON(map[string]any{"version": "6.8", "siteurl": "https://shop.test", "plugins": []any{map[string]any{"name": "akismet", "title": "Akismet", "update": "5.4"}}}), nil, true
		}
		return "", nil, false
	}
	in, err := info(context.Background(), h.Env, target, false)
	if err != nil {
		t.Fatal(err)
	}
	if in.Version != "6.8" || len(in.Plugins) != 1 || in.Plugins[0].Update != "5.4" {
		t.Fatalf("%+v", in)
	}
	var o execx.Opts
	var call string
	for i, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "php8.3") {
			o, call = h.Runner.Opts[i], c
		}
	}
	dir := filepath.Join(h.Base, target.Root, "public_html")
	for _, want := range []string{WPCLIPath, "--path=" + dir, "--skip-plugins", "--skip-themes", "eval-file -"} {
		if !strings.Contains(call, want) {
			t.Errorf("%q lacks %q", call, want)
		}
	}
	if o.As == nil || o.As.UID != uint32(h.UID) || o.As.UID == 0 {
		t.Fatalf("not run as the account: %+v", o.As)
	}
	if !strings.Contains(strings.Join(o.Env, " "), "WP_CLI_CONFIG_PATH=/dev/null") {
		t.Errorf("env %v", o.Env)
	}
	if !strings.Contains(h.Runner.Stdins[len(h.Runner.Stdins)-1], "wp_update_plugins") {
		t.Error("the overview program did not come from the agent on standard input")
	}
}

// Operations, names and folders are checked before anything runs.
func TestOperationsAreValidated(t *testing.T) {
	h, target := fixture(t)
	bad := []Operation{
		{Target: target, Op: "eval"},
		{Target: target, Op: "shell"},
		{Target: target, Op: "plugin_activate"},
		{Target: target, Op: "plugin_activate", Items: []string{"--require=/tmp/x.php"}},
		{Target: target, Op: "plugin_activate", Items: []string{"a b"}},
		{Target: target, Op: "theme_activate", Items: []string{"a", "b"}},
		{Target: target, Op: "maintenance_on", Items: []string{"x"}},
		{Target: target, Op: "core_auto_update", Value: "always; rm -rf /"},
	}
	for _, t2 := range []Target{
		{Site: Site{Domain: "shop.test", User: "example", Root: "/etc", PHPVersion: "8.3"}},
		{Site: Site{Domain: "shop.test", User: "example", Root: h.Env.Cfg.SitesRoot, PHPVersion: "8.3"}},
		{Site: Site{Domain: "shop.test", User: "root", Root: target.Root, PHPVersion: "8.3"}},
		{Site: Site{Domain: "shop.test", User: "example", Root: target.Root, PHPVersion: "8.3; id"}},
		{Site: target.Site, Path: "logs"},
		{Site: target.Site, Path: "public_html/$(id)"},
	} {
		bad = append(bad, Operation{Target: t2, Op: "maintenance_on"})
	}
	for _, o := range bad {
		if err := o.validate(h.Env); err == nil {
			t.Errorf("accepted %+v", o)
		}
	}
	// ".." cannot climb out: the path is cleaned inside the website.
	o := Operation{Target: Target{Site: target.Site, Path: "../../etc"}, Op: "maintenance_on"}
	if err := o.validate(h.Env); err == nil && o.Path != "etc" {
		t.Errorf("path %q", o.Path)
	}
	good := Operation{Target: target, Op: "plugin_update"}
	if err := good.validate(h.Env); err != nil {
		t.Fatal(err)
	}
	// A root account is refused even if it got through validation.
	if _, err := newRunner(h.Env, Site{Domain: "shop.test", User: "root", Root: target.Root, PHPVersion: "8.3"}); err == nil {
		t.Fatal("ran as root")
	}
}

// Updates run as a job with --all when nothing is named; quick changes run
// directly with exactly the named items.
func TestManageBuildsWPCLICommands(t *testing.T) {
	h, target := fixture(t)
	res, job, err := manage(context.Background(), h.Env, Operation{Target: target, Op: "plugin_update"})
	if err != nil || res != nil || job == nil {
		t.Fatalf("%v %v", res, err)
	}
	if _, err := job(context.Background(), &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if last := h.Runner.Calls[len(h.Runner.Calls)-1]; !strings.HasSuffix(last, "plugin update --all") {
		t.Fatalf("%q", last)
	}
	_, job, _ = manage(context.Background(), h.Env, Operation{Target: target, Op: "core_update"})
	job(context.Background(), &strings.Builder{})
	if c := strings.Join(h.Runner.Calls[len(h.Runner.Calls)-2:], "|"); !strings.Contains(c, "core update|") || !strings.HasSuffix(c, "core update-db") {
		t.Fatalf("%q", c)
	}
	if _, job, err := manage(context.Background(), h.Env, Operation{Target: target, Op: "plugin_deactivate", Items: []string{"hello-dolly", "akismet"}}); err != nil || job != nil {
		t.Fatal(err)
	}
	if last := h.Runner.Calls[len(h.Runner.Calls)-1]; !strings.HasSuffix(last, "plugin deactivate hello-dolly akismet") {
		t.Fatalf("%q", last)
	}
	manage(context.Background(), h.Env, Operation{Target: target, Op: "core_auto_update", Value: "off"})
	if last := h.Runner.Calls[len(h.Runner.Calls)-1]; !strings.HasSuffix(last, "config set WP_AUTO_UPDATE_CORE --type=constant false --raw") {
		t.Fatalf("%q", last)
	}
	// Without WordPress in the folder nothing runs.
	os.Remove(filepath.Join(h.Base, target.Root, "public_html/wp-settings.php"))
	if _, _, err := manage(context.Background(), h.Env, Operation{Target: target, Op: "maintenance_on"}); err == nil {
		t.Fatal("managed a folder without WordPress")
	}
}

// What a website's WordPress reports is bounded: unknown settings,
// odd names and control characters are dropped.
func TestInfoIsSanitized(t *testing.T) {
	i := Info{
		Title:      "Shop\x1b[31m",
		Plugins:    []Extension{{Name: "ok-plugin", Title: strings.Repeat("x", 500)}, {Name: "../../etc", Title: "bad"}, {Name: "--require=x", Title: "bad"}},
		Constants:  map[string]any{"WP_DEBUG": true, "DB_PASSWORD": "secret", "WP_AUTO_UPDATE_CORE": "'minor'", "DISABLE_WP_CRON": 5.0},
		ConfigMode: "rwx",
	}
	i.sanitize()
	if i.Title != "Shop[31m" || len(i.Plugins) != 1 || len(i.Plugins[0].Title) != 200 {
		t.Fatalf("%+v", i)
	}
	if _, ok := i.Constants["DB_PASSWORD"]; ok || i.Constants["WP_DEBUG"] != true || i.Constants["WP_AUTO_UPDATE_CORE"] != "'minor'" {
		t.Fatalf("%v", i.Constants)
	}
	if _, ok := i.Constants["DISABLE_WP_CRON"]; ok || i.ConfigMode != "" {
		t.Fatalf("%v %q", i.Constants, i.ConfigMode)
	}
}

// Installations reported outside the website's folder are ignored; the
// scan program only reads files and never asks for passwords.
func TestScanStaysInsideTheWebsite(t *testing.T) {
	h, target := fixture(t)
	root := filepath.Join(h.Base, target.Root)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id != "php8.3" {
			return "", nil, false
		}
		b, _ := json.Marshal(map[string]any{"installs": []any{
			map[string]any{"dir": root + "/public_html", "version": "6.8", "db_name": "shop_wp"},
			map[string]any{"dir": "/var/www/other", "version": "6.1"},
			map[string]any{"dir": root + "/../x.test/public_html", "version": "6.1"},
		}})
		return marker + string(b) + "\n", nil, true
	}
	found, err := scanSite(context.Background(), h.Env, target.Site)
	if err != nil || len(found) != 1 || found[0].Path != "public_html" || found[0].DBName != "shop_wp" {
		t.Fatalf("%+v %v", found, err)
	}
	script := h.Runner.Stdins[len(h.Runner.Stdins)-1]
	if strings.Contains(script, "DB_PASSWORD") || strings.Contains(script, "wp-load") || regexp.MustCompile(`\b(require|include)(_once)?\s*[('"$]`).MatchString(script) {
		t.Fatal("the scan must read files only, never load WordPress or read the password")
	}
}

// The scan action handles many websites at once; one failing website is
// reported without hiding the others' results.
func TestScanActionReportsEachWebsite(t *testing.T) {
	h, target := fixture(t)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id != "php8.3" {
			return "", nil, false
		}
		return "", nil, true
	}
	var sites []Site
	for _, d := range []string{"a.test", "b.test", "c.test", "d.test", "e.test", "f.test"} {
		root := filepath.Join(h.Env.Cfg.SitesRoot, d)
		os.MkdirAll(filepath.Join(h.Base, root), 0o755)
		sites = append(sites, Site{Domain: d, User: "example", Root: root, PHPVersion: "8.3"})
	}
	sites = append(sites, target.Site)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		for _, e := range o.Env {
			if strings.HasSuffix(e, "/c.test") {
				return "", os.ErrPermission
			}
			if v, ok := strings.CutPrefix(e, "WHACP_ROOT="); ok {
				b, _ := json.Marshal(map[string]any{"installs": []any{map[string]any{"dir": v + "/public_html", "version": "6.8"}}})
				return marker + string(b) + "\n", nil
			}
		}
		return "", nil
	}
	h.Respond = nil
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	p, _ := json.Marshal(map[string]any{"sites": sites})
	act, err := r.Lookup("wordpress.scan")
	if err != nil {
		t.Fatal(err)
	}
	out, err := act.Handler(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var res struct {
		Installs []Installation      `json:"installs"`
		Errors   []map[string]string `json:"errors"`
		Latest   string              `json:"latest"`
	}
	json.Unmarshal(b, &res)
	if len(res.Installs) != 6 || len(res.Errors) != 1 || res.Errors[0]["domain"] != "c.test" || res.Latest != "6.9" {
		t.Fatalf("%s", b)
	}
}

// WP-CLI is installed only when the download has the pinned checksum.
func TestWPCLIDownloadIsVerified(t *testing.T) {
	h, _ := fixture(t)
	os.Remove(filepath.Join(h.Base, WPCLIPath))
	var hits atomic.Int32
	body := "tampered"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	src := Source{URL: srv.URL, Client: srv.Client()}
	if err := ensurePhar(context.Background(), h.Env, src); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered file accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.Base, WPCLIPath)); err == nil {
		t.Fatal("a tampered WP-CLI was written")
	}
	body = fakePhar
	if err := ensurePhar(context.Background(), h.Env, src); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(filepath.Join(h.Base, WPCLIPath))
	if st.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v", st.Mode())
	}
	ensurePhar(context.Background(), h.Env, src)
	if hits.Load() != 2 {
		t.Fatalf("downloaded %d times", hits.Load())
	}
	// A copy changed on disk is not used.
	os.WriteFile(filepath.Join(h.Base, WPCLIPath), []byte("<?php evil();"), 0o644)
	if pharReady(h.Env) {
		t.Fatal("a changed WP-CLI copy was trusted")
	}
}

// The sign-in file is written as the account, private, named by 128 random
// bits, and only for an administrator.
func TestLoginFile(t *testing.T) {
	h, target := fixture(t)
	roles := "administrator"
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "php8.3" && strings.Contains(strings.Join(args, " "), "user get") {
			return roles + "\n", nil, true
		}
		return "", nil, false
	}
	res, err := login(context.Background(), h.Env, target, 7)
	if err != nil {
		t.Fatal(err)
	}
	name := res["file"].(string)
	if len(name) != len("whacp-login-")+32+len(".php") || !strings.HasPrefix(name, "whacp-login-") {
		t.Fatalf("%q", name)
	}
	var o execx.Opts
	var call string
	for i, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "sh ") {
			o, call = h.Runner.Opts[i], c
		}
	}
	if o.As == nil || o.As.UID == 0 || !strings.Contains(call, "umask 077 && cat > "+name) {
		t.Fatalf("%q %+v", call, o.As)
	}
	code := h.Runner.Stdins[len(h.Runner.Stdins)-1]
	for _, want := range []string{"@unlink(__FILE__)", "get_user_by('id', 7)", "manage_options", "wp_set_auth_cookie"} {
		if !strings.Contains(code, want) {
			t.Errorf("sign-in file lacks %q", want)
		}
	}
	roles = "subscriber"
	if _, err := login(context.Background(), h.Env, target, 8); err == nil {
		t.Fatal("a sign-in link for a subscriber")
	}
}
