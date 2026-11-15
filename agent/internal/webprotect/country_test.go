package webprotect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRangePrefixes(t *testing.T) {
	cases := []struct{ from, to, want string }{
		{"10.0.0.0", "10.0.0.255", "10.0.0.0/24"},
		{"10.0.0.1", "10.0.0.6", "10.0.0.1/32,10.0.0.2/31,10.0.0.4/31,10.0.0.6/32"},
		{"1.0.0.0", "1.0.0.0", "1.0.0.0/32"},
		{"0.0.0.0", "255.255.255.255", "0.0.0.0/0"},
		{"255.255.255.254", "255.255.255.255", "255.255.255.254/31"},
		{"2001:db8::", "2001:db8::ffff", "2001:db8::/112"},
	}
	for _, c := range cases {
		got, err := RangePrefixes(c.from, c.to)
		if err != nil || strings.Join(got, ",") != c.want {
			t.Errorf("%s-%s = %v %v, want %s", c.from, c.to, got, err, c.want)
		}
	}
	for _, bad := range [][2]string{{"10.0.0.9", "10.0.0.1"}, {"10.0.0.1", "::1"}, {"x", "y"}} {
		if _, err := RangePrefixes(bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func ranges(m map[string][2]string) map[string][]map[string]string {
	out := map[string][]map[string]string{}
	for code, r := range m {
		out[code] = []map[string]string{{"from": r[0], "to": r[1]}}
	}
	return out
}

func TestCountryRestrictionIsRendered(t *testing.T) {
	f := setup(t)
	enabled(f)
	if _, err := f.call(t, "protect.countries", map[string]any{"domain": "shop.test", "mode": "deny", "codes": []string{"AA", "BB"}, "status": 404,
		"ranges": ranges(map[string][2]string{"AA": {"203.0.113.0", "203.0.113.255"}, "BB": {"198.51.100.0", "198.51.100.127"}})}); err != nil {
		t.Fatal(err)
	}
	http := f.h.Read("etc/nginx/sites-enabled/" + LimitFile)
	for _, want := range []string{"geo $nvcc_", "default 0;", "198.51.100.0/25 1;", "203.0.113.0/24 1;", "$nvcc_"} {
		if !strings.Contains(http, want) {
			t.Errorf("missing %q in\n%s", want, http)
		}
	}
	if !strings.Contains(http, `"~^/\.well-known/acme-challenge/" 2;`) {
		t.Errorf("certificates are not exempt:\n%s", http)
	}
	if site := f.h.Read("etc/nginx/nimvor-protect/shop.test.conf"); !strings.Contains(site, "return 404;") {
		t.Errorf("site file:\n%s", site)
	}
	// Allow only: everything else is refused except private addresses.
	if _, err := f.call(t, "protect.countries", map[string]any{"domain": "shop.test", "mode": "allow_only", "codes": []string{"AA"},
		"ranges": ranges(map[string][2]string{"AA": {"203.0.113.0", "203.0.113.255"}})}); err != nil {
		t.Fatal(err)
	}
	http = f.h.Read("etc/nginx/sites-enabled/" + LimitFile)
	for _, want := range []string{"default 1;", "203.0.113.0/24 0;", "127.0.0.0/8 0;", "192.168.0.0/16 0;", "::1/128 0;"} {
		if !strings.Contains(http, want) {
			t.Errorf("allow_only: missing %q in\n%s", want, http)
		}
	}
	if strings.Contains(http, "198.51.100.0/25") {
		t.Errorf("an unused country's networks are kept:\n%s", http)
	}
	st := Load(f.h.Env)
	if len(st.Geo) != 1 || len(st.Geo["AA"]) != 1 {
		t.Errorf("state geo: %v", st.Geo)
	}
	// Status reports it.
	res, _ := f.call(t, "protect.status", map[string]any{"domain": "shop.test"})
	if c, _ := res["countries"].(map[string]any); c["mode"] != "allow_only" {
		t.Errorf("status: %v", res)
	}
	// Removing it leaves nothing.
	if _, err := f.call(t, "protect.countries", map[string]any{"domain": "shop.test", "mode": ""}); err != nil {
		t.Fatal(err)
	}
	if f.h.Exists("etc/nginx/sites-enabled/"+LimitFile) || f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") || len(Load(f.h.Env).Geo) != 0 {
		t.Fatal("files or state left")
	}
}

func TestCountrySetsAreSharedBetweenWebsites(t *testing.T) {
	f := setup(t)
	enabled(f)
	os.WriteFile(filepath.Join(f.h.Base, "etc/nginx/sites-available/nimvor-blog.test.conf"), []byte(strings.ReplaceAll(vhost, "shop.test", "blog.test")), 0o644)
	os.WriteFile(filepath.Join(f.h.Base, "etc/nginx/sites-enabled/nimvor-blog.test.conf"), []byte("# vhost\n"), 0o644)
	for _, d := range []string{"shop.test", "blog.test"} {
		if _, err := f.call(t, "protect.countries", map[string]any{"domain": d, "mode": "deny", "codes": []string{"AA"}, "ranges": ranges(map[string][2]string{"AA": {"203.0.113.0", "203.0.113.255"}})}); err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(f.h.Read("etc/nginx/sites-enabled/"+LimitFile), "geo $nvcc_"); n != 1 {
		t.Errorf("%d geo blocks for one country set", n)
	}
}

func TestCountryRestrictionIsValidated(t *testing.T) {
	f := setup(t)
	enabled(f)
	ok := ranges(map[string][2]string{"AA": {"203.0.113.0", "203.0.113.255"}})
	bad := []map[string]any{
		{"mode": "block", "codes": []string{"AA"}, "ranges": ok},
		{"mode": "deny", "codes": []string{}, "ranges": ok},
		{"mode": "deny", "codes": []string{"aa"}, "ranges": ok},
		{"mode": "deny", "codes": []string{"AAA"}, "ranges": ok},
		{"mode": "deny", "codes": []string{"AA", "AA"}, "ranges": ok},
		{"mode": "deny", "codes": []string{"AA"}, "ranges": nil},
		{"mode": "deny", "codes": []string{"AA"}, "ranges": map[string]any{"AA": []map[string]string{{"from": "x", "to": "y"}}}},
		{"mode": "deny", "codes": []string{"AA"}, "ranges": ok, "status": 200},
	}
	for _, in := range bad {
		in["domain"] = "shop.test"
		if _, err := f.call(t, "protect.countries", in); code(err) != "invalid_params" {
			t.Errorf("%v: %v", in, err)
		}
	}
	if f.h.Exists("etc/nginx/sites-enabled/"+LimitFile) || f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") {
		t.Fatal("written despite invalid input")
	}
}

func TestCountryRestrictionRolledBackWhenNginxRefuses(t *testing.T) {
	f := setup(t)
	enabled(f)
	f.h.Fail["nginx -t"] = "nginx: [emerg] geo"
	if _, err := f.call(t, "protect.countries", map[string]any{"domain": "shop.test", "mode": "deny", "codes": []string{"AA"}, "ranges": ranges(map[string][2]string{"AA": {"203.0.113.0", "203.0.113.255"}})}); err == nil {
		t.Fatal("accepted")
	}
	if f.h.Exists("etc/nginx/sites-enabled/"+LimitFile) || f.h.Read("etc/nginx/sites-available/nimvor-shop.test.conf") != vhost || len(Load(f.h.Env).Geo) != 0 {
		t.Fatal("not rolled back")
	}
}
