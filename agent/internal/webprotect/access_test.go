package webprotect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rule(kind, action, value string) map[string]any {
	return map[string]any{"kind": kind, "action": action, "value": value}
}

func enabled(f *fixture) {
	os.MkdirAll(filepath.Join(f.h.Base, "etc/nginx/sites-enabled"), 0o755)
	os.WriteFile(filepath.Join(f.h.Base, "etc/nginx/sites-enabled/nimvor-shop.test.conf"), []byte("# vhost\n"), 0o644)
}

func TestAccessListIsRendered(t *testing.T) {
	f := setup(t)
	enabled(f)
	_, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "status": 444, "rules": []map[string]any{
		rule("ip", "deny", "203.0.113.0/24"),
		rule("ip", "allow", "203.0.113.7"),
		rule("ip", "deny", "198.51.100.1-198.51.100.6"),
		rule("ua", "deny", "BadBot/1.0"),
		rule("ua", "allow", "GoodBot"),
		rule("url", "deny", "/wp-admin/"),
		rule("url", "allow", "/wp-admin/admin-ajax.php"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	http := f.h.Read("etc/nginx/sites-enabled/" + LimitFile)
	for _, want := range []string{
		"203.0.113.0/24 1;", "203.0.113.7/32 2;",
		"198.51.100.1/32 1;", "198.51.100.2/31 1;", "198.51.100.4/31 1;", "198.51.100.6/32 1;",
		`"~*BadBot/1[.]0"`,
		`"~^/wp-admin/" 1;`,
		`"~^/\.well-known/acme-challenge/" 2;`,
		`"~2" 0;`,
	} {
		if !strings.Contains(http, want) && !strings.Contains(http, strings.ReplaceAll(want, "[.]", `\.`)) {
			t.Errorf("missing %q in\n%s", want, http)
		}
	}
	// Allow entries come first: the first matching pattern decides.
	if strings.Index(http, "GoodBot") > strings.Index(http, "BadBot") || strings.Index(http, "admin-ajax") > strings.Index(http, `"~^/wp-admin/" 1`) {
		t.Errorf("allow entries must come first:\n%s", http)
	}
	site := f.h.Read("etc/nginx/nimvor-protect/shop.test.conf")
	if !strings.Contains(site, "return 444;") || !strings.Contains(site, "if ($nvblock_") {
		t.Errorf("site file:\n%s", site)
	}
	// Twice is the same (idempotent).
	if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "status": 444, "rules": []map[string]any{
		rule("ip", "deny", "203.0.113.0/24"), rule("ip", "allow", "203.0.113.7"), rule("ip", "deny", "198.51.100.1-198.51.100.6"),
		rule("ua", "deny", "BadBot/1.0"), rule("ua", "allow", "GoodBot"), rule("url", "deny", "/wp-admin/"), rule("url", "allow", "/wp-admin/admin-ajax.php"),
	}}); err != nil {
		t.Fatal(err)
	}
	if f.h.Read("etc/nginx/sites-enabled/"+LimitFile) != http {
		t.Error("changed on a second identical save")
	}
	// Empty list: nothing left.
	if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "rules": []map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if f.h.Exists("etc/nginx/sites-enabled/"+LimitFile) || f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") {
		t.Fatal("files left")
	}
}

func TestAccessListIsValidated(t *testing.T) {
	f := setup(t)
	enabled(f)
	bad := []map[string]any{
		rule("ip", "deny", "999.1.1.1"),
		rule("ip", "deny", "10.0.0.0/33"),
		rule("ip", "deny", "10.0.0.9-10.0.0.1"),
		rule("ip", "deny", "10.0.0.1-::1"),
		rule("ip", "deny", "evil; deny"),
		rule("ip", "deny", "::ffff:1.2.3.4"),
		rule("ip", "block", "1.2.3.4"),
		rule("ua", "deny", ""),
		rule("ua", "deny", "x\"; return 200; #"),
		rule("ua", "deny", "a$b"),
		rule("ua", "deny", "a\\b"),
		rule("ua", "deny", strings.Repeat("a", 101)),
		rule("url", "deny", "no-slash"),
		rule("url", "deny", "/a/../b"),
		rule("url", "deny", "/a b"),
		rule("cookie", "deny", "x"),
		{"kind": "ip", "action": "deny", "value": "1.2.3.4", "note": "a\nb"},
	}
	for _, r := range bad {
		if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "rules": []map[string]any{r}}); code(err) != "invalid_params" {
			t.Errorf("%v accepted: %v", r, err)
		}
	}
	if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "status": 200, "rules": []map[string]any{rule("ip", "deny", "1.2.3.4")}}); code(err) != "invalid_params" {
		t.Errorf("status 200 accepted: %v", err)
	}
	// A range that would need too many networks.
	if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "rules": []map[string]any{rule("ip", "deny", "::1-ffff::fffe")}}); code(err) != "invalid_params" {
		t.Errorf("irregular range accepted: %v", err)
	}
	many := make([]map[string]any, 0, MaxAccess+1)
	for i := 0; i <= MaxAccess; i++ {
		many = append(many, rule("url", "deny", "/p"+strings.Repeat("a", i%5)+"/"+string(rune('a'+i%26))+strings.Repeat("b", i/26)))
	}
	if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "rules": many}); code(err) != "invalid_params" {
		t.Errorf("too many accepted: %v", err)
	}
	if f.h.Exists("etc/nginx/sites-enabled/"+LimitFile) || f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") {
		t.Fatal("written despite invalid input")
	}
}

func TestAddressRanges(t *testing.T) {
	cases := map[string]string{
		"10.0.0.0-10.0.0.255":       "10.0.0.0/24",
		"10.0.0.1-10.0.0.1":         "10.0.0.1/32",
		"10.0.0.1-10.0.0.2":         "10.0.0.1/32,10.0.0.2/32",
		"192.168.0.0/16":            "192.168.0.0/16",
		"192.168.5.9/16":            "192.168.0.0/16",
		"2001:db8::-2001:db8::ffff": "2001:db8::/112",
		"::1":                       "::1/128",
	}
	for in, want := range cases {
		ps, err := ParseAddresses(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		var got []string
		for _, p := range ps {
			got = append(got, p.String())
		}
		if strings.Join(got, ",") != want {
			t.Errorf("%s = %v, want %s", in, got, want)
		}
	}
}

func TestAccessListRolledBackWhenNginxRefuses(t *testing.T) {
	f := setup(t)
	enabled(f)
	f.h.Fail["nginx -t"] = "nginx: [emerg] geo"
	if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "rules": []map[string]any{rule("ip", "deny", "1.2.3.4")}}); err == nil {
		t.Fatal("accepted")
	}
	if f.h.Exists("etc/nginx/sites-enabled/"+LimitFile) || f.h.Read("etc/nginx/sites-available/nimvor-shop.test.conf") != vhost || len(Load(f.h.Env).Sites) != 0 {
		t.Fatal("not rolled back")
	}
}

func TestServerWideListReachesEveryWebsite(t *testing.T) {
	f := setup(t)
	enabled(f)
	// A second website.
	os.WriteFile(filepath.Join(f.h.Base, "etc/nginx/sites-available/nimvor-blog.test.conf"), []byte(strings.ReplaceAll(vhost, "shop.test", "blog.test")), 0o644)
	os.WriteFile(filepath.Join(f.h.Base, "etc/nginx/sites-enabled/nimvor-blog.test.conf"), []byte("# vhost\n"), 0o644)
	res, err := f.call(t, "protect.access_global", map[string]any{"rules": []map[string]any{rule("ip", "deny", "192.0.2.0/24"), rule("ua", "deny", "scrapy")}})
	if err != nil {
		t.Fatal(err)
	}
	if sk, _ := res["skipped"].([]any); len(sk) != 0 {
		t.Fatalf("skipped %v", sk)
	}
	for _, d := range []string{"shop.test", "blog.test"} {
		site := f.h.Read("etc/nginx/nimvor-protect/" + d + ".conf")
		if !strings.Contains(site, "if ($nvblock_global)") {
			t.Errorf("%s:\n%s", d, site)
		}
		if !strings.Contains(f.h.Read("etc/nginx/sites-available/nimvor-"+d+".conf"), "nimvor-protect/"+d+".con[f]") {
			t.Errorf("%s has no include", d)
		}
	}
	// A website with its own list also honours the server-wide one, and its
	// allow entries override it.
	if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "rules": []map[string]any{rule("ip", "allow", "192.0.2.9")}}); err != nil {
		t.Fatal(err)
	}
	http := f.h.Read("etc/nginx/sites-enabled/" + LimitFile)
	if !strings.Contains(http, "$nvip_global$nvua_global$nvurl_global$nvip_") {
		t.Errorf("combined verdict:\n%s", http)
	}
	st, _ := f.call(t, "protect.access_global_status", map[string]any{})
	if l, _ := st["access"].([]any); len(l) != 2 {
		t.Errorf("status %v", st)
	}
	// Emptying the list clears every file.
	if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "rules": []map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(t, "protect.access_global", map[string]any{"rules": []map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"etc/nginx/sites-enabled/" + LimitFile, "etc/nginx/nimvor-protect/shop.test.conf", "etc/nginx/nimvor-protect/blog.test.conf"} {
		if f.h.Exists(p) {
			t.Errorf("%s left", p)
		}
	}
}

// An allow-listed address is never counted by a traffic limit.
func TestAllowedAddressesAreNotLimited(t *testing.T) {
	f := setup(t)
	enabled(f)
	if _, err := f.call(t, "protect.limits", map[string]any{"domain": "shop.test", "limits": []map[string]any{limitReq(nil)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(t, "protect.access", map[string]any{"domain": "shop.test", "rules": []map[string]any{rule("ip", "allow", "198.51.100.0/24")}}); err != nil {
		t.Fatal(err)
	}
	http := f.h.Read("etc/nginx/sites-enabled/" + LimitFile)
	if !strings.Contains(http, `"$nvallow_`) || !strings.Contains(http, `"~^1" ""`) || !strings.Contains(http, `"~^0shop[.]test`) && !strings.Contains(http, `"~^0shop\.test`) {
		t.Errorf("allowed addresses are still limited:\n%s", http)
	}
}
