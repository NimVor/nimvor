package webprotect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const httpFile = "etc/nginx/sites-enabled/nimvor-00-protect.conf"

func limitReq(extra map[string]any) map[string]any {
	l := map[string]any{"name": "login", "match": "exact", "path": "/wp-login.php", "rate": 5, "unit": "m", "burst": 3, "per_ua": false, "enabled": true}
	for k, v := range extra {
		l[k] = v
	}
	return l
}

func TestTrafficLimitsRenderAndApply(t *testing.T) {
	f := setup(t)
	os.MkdirAll(filepath.Join(f.h.Base, "etc/nginx/sites-enabled"), 0o755)
	res, err := f.call(t, "protect.limits", map[string]any{"domain": "shop.test", "status": 503, "limits": []map[string]any{
		limitReq(nil),
		limitReq(map[string]any{"name": "api", "match": "prefix", "path": "/api/", "rate": 20, "unit": "s", "burst": 0, "per_ua": true}),
		limitReq(map[string]any{"name": "off", "path": "/off", "enabled": false}),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res["limit_status"] != float64(503) || len(res["limits"].([]any)) != 3 {
		t.Fatalf("status = %v", res)
	}
	httpConf := f.h.Read(httpFile)
	idLogin, idAPI := limitID("shop.test", "login"), limitID("shop.test", "api")
	for _, want := range []string{
		`map "$server_name$uri" $nvl_` + idLogin + ` {`,
		`"~^shop\.test/\.well-known/acme-challenge/" "";`,
		`"~^shop\.test/wp-login\.php$" "$binary_remote_addr";`,
		`limit_req_zone $nvl_` + idLogin + ` zone=nvl_` + idLogin + `:5m rate=5r/m;`,
		`"~^shop\.test/api/" "$binary_remote_addr$http_user_agent";`,
		`limit_req_zone $nvl_` + idAPI + ` zone=nvl_` + idAPI + `:5m rate=20r/s;`,
	} {
		if !strings.Contains(httpConf, want) {
			t.Errorf("http file lacks %q:\n%s", want, httpConf)
		}
	}
	if strings.Contains(httpConf, "/off") {
		t.Error("a switched-off rule is in the http file")
	}
	site := f.h.Read("etc/nginx/nimvor-protect/shop.test.conf")
	for _, want := range []string{"limit_req_status 503;", "limit_req zone=nvl_" + idLogin + " burst=3 nodelay;", "limit_req zone=nvl_" + idAPI + " nodelay;"} {
		if !strings.Contains(site, want) {
			t.Errorf("site file lacks %q:\n%s", want, site)
		}
	}
	if strings.Count(site, "limit_req zone=") != 2 {
		t.Errorf("a switched-off rule is switched on:\n%s", site)
	}
	if !strings.Contains(f.h.Read("etc/nginx/sites-available/nimvor-shop.test.conf"), "nimvor-protect/shop.test.con[f]") {
		t.Error("the website's include was not added")
	}

	// All rules off: nothing is left (both files).
	if _, err := f.call(t, "protect.limits", map[string]any{"domain": "shop.test", "limits": []map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if f.h.Exists(httpFile) || f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") {
		t.Fatal("files left with no rules")
	}
}

func TestTrafficLimitsAreValidated(t *testing.T) {
	f := setup(t)
	os.MkdirAll(filepath.Join(f.h.Base, "etc/nginx/sites-enabled"), 0o755)
	bad := []map[string]any{
		limitReq(map[string]any{"name": "Bad Name"}),
		limitReq(map[string]any{"name": ""}),
		limitReq(map[string]any{"match": "regex"}),
		limitReq(map[string]any{"path": "wp-login.php"}),
		limitReq(map[string]any{"path": "/a b"}),
		limitReq(map[string]any{"path": "/../etc"}),
		limitReq(map[string]any{"path": "/x\"; deny all; #"}),
		limitReq(map[string]any{"path": "/a//b"}),
		limitReq(map[string]any{"rate": 0}),
		limitReq(map[string]any{"unit": "s", "rate": 1001}),
		limitReq(map[string]any{"unit": "h"}),
		limitReq(map[string]any{"burst": -1}),
		limitReq(map[string]any{"burst": 1001}),
	}
	for _, l := range bad {
		if _, err := f.call(t, "protect.limits", map[string]any{"domain": "shop.test", "limits": []map[string]any{l}}); code(err) != "invalid_params" {
			t.Errorf("%v: %v", l, err)
		}
	}
	if _, err := f.call(t, "protect.limits", map[string]any{"domain": "shop.test", "status": 200, "limits": []map[string]any{limitReq(nil)}}); code(err) != "invalid_params" {
		t.Errorf("status 200 accepted: %v", err)
	}
	if _, err := f.call(t, "protect.limits", map[string]any{"domain": "shop.test", "limits": []map[string]any{limitReq(nil), limitReq(nil)}}); code(err) != "invalid_params" {
		t.Errorf("duplicate names accepted: %v", err)
	}
	many := make([]map[string]any, 0, 21)
	for i := 0; i < 21; i++ {
		many = append(many, limitReq(map[string]any{"name": "r" + string(rune('a'+i))}))
	}
	if _, err := f.call(t, "protect.limits", map[string]any{"domain": "shop.test", "limits": many}); code(err) != "invalid_params" {
		t.Errorf("21 rules accepted: %v", err)
	}
	if f.h.Exists(httpFile) || f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") {
		t.Fatal("written despite invalid input")
	}
}

func TestTrafficLimitsRolledBackWhenNginxRefuses(t *testing.T) {
	f := setup(t)
	os.MkdirAll(filepath.Join(f.h.Base, "etc/nginx/sites-enabled"), 0o755)
	f.h.Fail["nginx -t"] = "nginx: [emerg] limit_req_zone"
	if _, err := f.call(t, "protect.limits", map[string]any{"domain": "shop.test", "limits": []map[string]any{limitReq(nil)}}); err == nil {
		t.Fatal("accepted")
	}
	if f.h.Exists(httpFile) || f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") || f.h.Read("etc/nginx/sites-available/nimvor-shop.test.conf") != vhost {
		t.Fatal("not rolled back")
	}
	if len(Load(f.h.Env).Sites) != 0 {
		t.Fatal("state kept")
	}
}

// A deleted website takes its rules with it, the http-level file included.
func TestTrafficLimitsGoWithTheWebsite(t *testing.T) {
	f := setup(t)
	os.MkdirAll(filepath.Join(f.h.Base, "etc/nginx/sites-enabled"), 0o755)
	if _, err := f.call(t, "protect.limits", map[string]any{"domain": "shop.test", "limits": []map[string]any{limitReq(nil)}}); err != nil {
		t.Fatal(err)
	}
	if !f.h.Exists(httpFile) {
		t.Fatal("no http file")
	}
	Remove(f.h.Env, "shop.test")
	if f.h.Exists(httpFile) {
		t.Fatal("the http file stayed after the website was forgotten")
	}
}
