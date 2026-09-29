package modsec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/capability"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestCheckCustom(t *testing.T) {
	good := "# block a probe\nSecRule ARGS:probe \"@streq yes\" \\\n    \"id:10001,phase:2,deny,status:403,log,msg:'probe'\"\nSecAction \"id:10002,phase:1,pass,nolog\"\nSecRuleRemoveById 942100\n"
	ids, err := CheckCustom(good)
	if err != nil || len(ids) != 2 {
		t.Fatalf("%v %v", ids, err)
	}
	for name, bad := range map[string]string{
		"crs range":     `SecRule ARGS "@rx x" "id:942100,deny"`,
		"no id":         `SecRule ARGS "@rx x" "deny"`,
		"duplicate":     "SecAction \"id:10001,pass\"\nSecAction \"id:10001,pass\"",
		"exec":          `SecRule ARGS "@rx x" "id:10003,pass,exec:/bin/sh"`,
		"include":       `Include /etc/shadow`,
		"engine":        `SecRuleEngine Off`,
		"audit log":     `SecAuditLog /tmp/x`,
		"file operator": `SecRule REMOTE_ADDR "@ipMatchFromFile /etc/passwd" "id:10004,deny"`,
		"inspect file":  `SecRule FILES_TMPNAMES "@inspectFile /bin/x" "id:10005,deny"`,
		"dangling":      "SecAction \"id:10006,pass\" \\",
		"too large":     strings.Repeat("#", MaxCustom+1),
		"remote rules":  `SecRemoteRules key https://x`,
	} {
		if _, err := CheckCustom(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestRenderSite(t *testing.T) {
	if RenderSite(Site{Mode: ModeOff}) != "" {
		t.Fatal("off renders a file")
	}
	on := RenderSite(Site{Mode: ModeOn})
	if !strings.HasSuffix(on, "modsecurity on;\n") || strings.Contains(on, "modsecurity_rules") {
		t.Fatalf("%s", on)
	}
	s := Site{Mode: ModeDetect, Exclude: []string{"crs", "crs"}, Disabled: []int{942100, 941100, 942100}}
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	got := RenderSite(s)
	for _, want := range []string{"SecRuleEngine DetectionOnly", "SecRuleRemoveById 900000-999999", "SecRuleRemoveById 941100 942100"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing:\n%s", want, got)
		}
	}
	for _, bad := range []Site{{Mode: "block"}, {Mode: ModeOn, Exclude: []string{"comodo"}}, {Mode: ModeOn, Disabled: []int{0}}} {
		if bad.Normalize() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if !strings.HasSuffix(SiteInclude(testhost.New(t).Env, "shop.test"), "whacp-modsec/sites/shop.test.con[f];\n") {
		t.Fatal("include glob")
	}
}

type fixture struct {
	h *testhost.Host
	r *actions.Registry
}

func newFixture(t *testing.T) fixture {
	h := testhost.New(t)
	w := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(h.Base, p)), 0o755)
		os.WriteFile(filepath.Join(h.Base, p), []byte(s), 0o644)
	}
	w("etc/nginx/modules-enabled/50-mod-http-modsecurity.conf", "load_module modules/ngx_http_modsecurity_module.so;\n")
	w("usr/lib/nginx/modules/ngx_http_modsecurity_module.so", "\x7fELF")
	os.MkdirAll(filepath.Join(h.Base, "usr/share/nginx"), 0o755)
	os.Symlink("../../lib/nginx/modules", filepath.Join(h.Base, "usr/share/nginx/modules")) // as on Debian
	// Detection by capability: the module file, nginx -V and the active
	// configuration (nginx -T lists what the module files load).
	realNeeded := capability.ReadNeeded
	capability.ReadNeeded = func(string) ([]string, []string, error) { return nil, nil, nil }
	t.Cleanup(func() { capability.ReadNeeded = realNeeded })
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "nginx" && len(args) == 1 && args[0] == "-V" {
			return "nginx version: nginx/1.24.0 (Ubuntu)\nconfigure arguments: --prefix=/usr/share/nginx --modules-path=/usr/lib/nginx/modules --with-compat\n", nil, true
		}
		if id == "nginx" && len(args) == 1 && args[0] == "-T" {
			var b strings.Builder
			for _, f := range []string{"etc/nginx/modules-enabled/50-mod-http-modsecurity.conf"} {
				c, _ := os.ReadFile(filepath.Join(h.Base, f))
				b.Write(c)
			}
			return b.String(), nil, true
		}
		return "", nil, false
	}
	w("usr/share/modsecurity-crs/rules/REQUEST-942-APPLICATION-ATTACK-SQLI.conf", "# rules\n")
	w("etc/modsecurity/crs/crs-setup.conf", "SecAction \\\n  \"id:900990,phase:1,pass,t:none,nolog,\\\n  setvar:tx.crs_setup_version=335\"\n")
	w("etc/modsecurity/crs/REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf", "")
	vhost := "server {\n    listen 80;\n    server_name shop.test;\n    add_header X-Content-Type-Options \"nosniff\" always;\n}\n"
	w("etc/nginx/sites-available/whacp-shop.test.conf", vhost)
	w("etc/nginx/sites-available/whacp-blog.test.conf", strings.ReplaceAll(vhost, "shop", "blog"))
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return fixture{h, r}
}

func (f fixture) call(t *testing.T, name, params string) (map[string]any, error) {
	t.Helper()
	a, err := f.r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Handler(context.Background(), json.RawMessage(params))
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m, nil
}

func TestConfigureSitesAndRollback(t *testing.T) {
	f := newFixture(t)
	h := f.h
	st, _ := f.call(t, "modsec.status", `{}`)
	if st["module_loaded"] != true || st["crs"].(map[string]any)["version"] != "3.3.5" || st["active"] != false {
		t.Fatalf("%v", st)
	}
	// A website cannot turn it on while it is off server-wide.
	if _, err := f.call(t, "modsec.site", `{"domain":"shop.test","mode":"on"}`); err == nil {
		t.Fatal("site on while off accepted")
	}
	if _, err := f.call(t, "modsec.configure", `{"enabled":true,"crs":{"enabled":true,"paranoia":2,"inbound_threshold":7,"outbound_threshold":4},"custom":{"enabled":true}}`); err != nil {
		t.Fatal(err)
	}
	httpConf := h.Read("etc/nginx/sites-enabled/whacp-00-modsecurity.conf")
	if !strings.Contains(httpConf, "modsecurity off;") || !strings.Contains(httpConf, "modsecurity_rules_file /etc/nginx/whacp-modsec/main.conf;") {
		t.Fatalf("http:\n%s", httpConf)
	}
	main := h.Read("etc/nginx/whacp-modsec/main.conf")
	for _, want := range []string{"SecAuditLog /var/log/nginx/whacp-modsec-audit.json", "SecAuditLogParts ABHZ", "Include /etc/nginx/whacp-modsec/crs.conf"} {
		if !strings.Contains(main, want) {
			t.Errorf("main.conf: %q missing\n%s", want, main)
		}
	}
	if strings.Contains(main, "custom.conf") {
		t.Error("custom rules included before any exist")
	}
	crs := h.Read("etc/nginx/whacp-modsec/crs.conf")
	for _, want := range []string{"Include /etc/modsecurity/crs/crs-setup.conf", "tx.paranoia_level=2", "inbound_anomaly_score_threshold=7", "Include /etc/modsecurity/crs/REQUEST-900-EXCLUSION-RULES-BEFORE-CRS.conf", "Include /usr/share/modsecurity-crs/rules/*.conf"} {
		if !strings.Contains(crs, want) {
			t.Errorf("crs.conf: %q missing\n%s", want, crs)
		}
	}
	// Existing websites got the include line (once).
	vh := h.Read("etc/nginx/sites-available/whacp-shop.test.conf")
	if strings.Count(vh, "include /etc/nginx/whacp-modsec/sites/shop.test.con[f];") != 1 {
		t.Fatalf("vhost:\n%s", vh)
	}

	if _, err := f.call(t, "modsec.site", `{"domain":"shop.test","mode":"detect","exclude":["custom"],"disabled_rules":[942100]}`); err != nil {
		t.Fatal(err)
	}
	site := h.Read("etc/nginx/whacp-modsec/sites/shop.test.conf")
	for _, want := range []string{"modsecurity on;", "SecRuleEngine DetectionOnly", "SecRuleRemoveById 10000-99999", "SecRuleRemoveById 942100"} {
		if !strings.Contains(site, want) {
			t.Errorf("site: %q missing\n%s", want, site)
		}
	}
	if _, err := f.call(t, "modsec.site", `{"domain":"nope.test","mode":"on"}`); err == nil {
		t.Fatal("unknown website accepted")
	}

	// Custom rules: validated, then included.
	if _, err := f.call(t, "modsec.custom_set", `{"rules":"SecRuleEngine Off"}`); err == nil {
		t.Fatal("engine switch accepted as a custom rule")
	}
	if _, err := f.call(t, "modsec.custom_set", `{"rules":"SecRule ARGS:probe \"@streq yes\" \"id:10001,phase:2,deny,status:403\"\n"}`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Read("etc/nginx/whacp-modsec/main.conf"), "Include /etc/nginx/whacp-modsec/custom.conf") {
		t.Fatal("custom rules not included")
	}
	got, _ := f.call(t, "modsec.custom_get", `{}`)
	if !strings.HasPrefix(got["rules"].(string), "SecRule ARGS:probe") {
		t.Fatalf("%v", got)
	}

	// nginx -t refusing the change restores everything.
	before := h.Read("etc/nginx/whacp-modsec/sites/shop.test.conf")
	h.Fail["nginx -t"] = "nginx: [emerg] bad"
	h.FailFrom["nginx -t"] = 2 // the precheck passes, the check after writing fails
	if _, err := f.call(t, "modsec.site", `{"domain":"shop.test","mode":"on"}`); err == nil || !strings.Contains(err.Error(), "restored") && !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("nginx -t failure: %v", err)
	}
	if h.Read("etc/nginx/whacp-modsec/sites/shop.test.conf") != before || Load(h.Env).Sites["shop.test"].Mode != ModeDetect {
		t.Fatal("not rolled back")
	}
	delete(h.Fail, "nginx -t")

	// Off for the website: its file goes; off server-wide: all files go,
	// the settings stay for later.
	if _, err := f.call(t, "modsec.site", `{"domain":"shop.test","mode":"off"}`); err != nil {
		t.Fatal(err)
	}
	if h.Exists("etc/nginx/whacp-modsec/sites/shop.test.conf") {
		t.Fatal("site file left")
	}
	f.call(t, "modsec.site", `{"domain":"blog.test","mode":"on"}`)
	if _, err := f.call(t, "modsec.configure", `{"enabled":false,"crs":{"enabled":true}}`); err != nil {
		t.Fatal(err)
	}
	if h.Exists("etc/nginx/sites-enabled/whacp-00-modsecurity.conf") || h.Exists("etc/nginx/whacp-modsec/sites/blog.test.conf") {
		t.Fatal("files left while off")
	}
	if Load(h.Env).Sites["blog.test"].Mode != ModeOn {
		t.Fatal("site setting lost")
	}
}

func TestConfigureNeedsTheModule(t *testing.T) {
	f := newFixture(t)
	os.Remove(filepath.Join(f.h.Base, "etc/nginx/modules-enabled/50-mod-http-modsecurity.conf"))
	// The module file alone: installed, not loaded.
	if _, err := f.call(t, "modsec.configure", `{"enabled":true,"crs":{"enabled":true}}`); err == nil || !strings.Contains(err.Error(), "installed but not usable") || !strings.Contains(err.Error(), "does not load it") {
		t.Fatalf("%v", err)
	}
	os.Remove(filepath.Join(f.h.Base, "usr/lib/nginx/modules/ngx_http_modsecurity_module.so"))
	if _, err := f.call(t, "modsec.configure", `{"enabled":true,"crs":{"enabled":true}}`); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("%v", err)
	}
	for _, bad := range []string{`{"enabled":false,"crs":{"paranoia":5}}`, `{"enabled":false,"crs":{"inbound_threshold":1}}`} {
		if _, err := f.call(t, "modsec.configure", bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestReadEventsAndTrim(t *testing.T) {
	ev, err := ReadEvents("testdata/audit.json", nil, 10)
	if err != nil || len(ev) != 2 {
		t.Fatalf("%v %v", ev, err)
	}
	// Newest first.
	if ev[0].Host != "blog.test" || ev[0].Engine != "DetectionOnly" || ev[0].Status != 200 {
		t.Fatalf("%+v", ev[0])
	}
	if ev[1].Host != "shop.test" || ev[1].Status != 403 || ev[1].Method != "GET" || !strings.Contains(ev[1].URI, "id=1") {
		t.Fatalf("%+v", ev[1])
	}
	ids := []string{}
	for _, r := range ev[1].Rules {
		ids = append(ids, r.ID)
	}
	if !strings.Contains(strings.Join(ids, ","), "942100") {
		t.Fatalf("rules %v", ids)
	}
	only, _ := ReadEvents("testdata/audit.json", map[string]bool{"shop.test": true}, 10)
	if len(only) != 1 || only[0].Host != "shop.test" {
		t.Fatalf("%+v", only)
	}
	b, _ := json.Marshal(ev)
	if strings.Contains(string(b), "Cookie") || strings.Contains(string(b), "REDACTED") {
		t.Fatal("request headers leak into events")
	}

	p := filepath.Join(t.TempDir(), "a.json")
	var big strings.Builder
	for i := 0; i < 2000; i++ {
		big.WriteString(strings.Repeat("x", 99) + "\n")
	}
	os.WriteFile(p, []byte(big.String()), 0o600)
	n, err := Trim(p, 100_000, 10_000)
	fi, _ := os.Stat(p)
	if err != nil || n == 0 || fi.Size() > 10_000 || fi.Size() < 9_000 {
		t.Fatalf("trim: %d %v size %d", n, err, fi.Size())
	}
	if n, _ := Trim(p, 100_000, 10_000); n != 0 {
		t.Fatal("trimmed below the limit")
	}
}
