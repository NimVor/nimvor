package sites

import (
	"strings"
	"testing"
)

func TestSiteEnvReachesThePoolAndNothingElse(t *testing.T) {
	s := Spec{Domain: "shop.test", User: "shop", CreateUser: true, Kind: "php", PHPVersion: "8.3", Enabled: true,
		Env: map[string]string{"STRIPE_KEY": "sk_live_a$b${c}\"d\\e", "APP_ENV": "production"}}
	if err := s.Normalize(t.TempDir(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	pool := RenderPool(s, Layout{Socket: "/run/x.sock", LogDir: "/var/log/x", TmpDir: "/tmp/x"})
	for _, want := range []string{"env[APP_ENV] = 'production'\n", "env[STRIPE_KEY] = 'sk_live_a$b${c}\"d\\e'\n"} {
		if !strings.Contains(pool, want) {
			t.Errorf("pool lacks %q:\n%s", want, pool)
		}
	}
	if strings.Index(pool, "env[APP_ENV]") > strings.Index(pool, "env[STRIPE_KEY]") {
		t.Error("variables are not sorted")
	}
	if strings.Contains(RenderNginx(s, Layout{Socket: "/run/x.sock", LogDir: "/var/log/x"}), "sk_live") {
		t.Error("a value leaked into the Nginx file")
	}
	s.Env = nil
	if strings.Contains(RenderPool(s, Layout{Socket: "/run/x.sock", LogDir: "/var/log/x", TmpDir: "/tmp/x"}), "env[") {
		t.Error("a website without variables renders none")
	}
}

func TestSiteEnvRefusesUnsafeNamesAndValues(t *testing.T) {
	big := map[string]string{}
	for i := 0; i < 101; i++ {
		big["V"+strings.Repeat("A", 1)+string(rune('A'+i%26))+strings.Repeat("B", i/26)] = "x"
	}
	cases := map[string]map[string]string{
		"lowercase":    {"api_key": "x"},
		"digit first":  {"1KEY": "x"},
		"path":         {"PATH": "/tmp"},
		"preload":      {"LD_PRELOAD": "/tmp/x.so"},
		"php ini scan": {"PHPRC": "/tmp"},
		"nimvor":       {"NIMVOR_TOKEN": "x"},
		"server var":   {"HTTP_HOST": "evil"},
		"quote":        {"KEY": "it's"},
		"newline":      {"KEY": "a\nb"},
		"nul":          {"KEY": "a\x00b"},
		"too long":     {"KEY": strings.Repeat("x", 4097)},
		"too many":     big,
	}
	for name, env := range cases {
		s := Spec{Domain: "shop.test", User: "shop", CreateUser: true, Kind: "php", PHPVersion: "8.3", Enabled: true, Env: env}
		if err := s.Normalize(t.TempDir(), t.TempDir()); err == nil {
			t.Errorf("%s: expected a refusal", name)
		}
	}
}
