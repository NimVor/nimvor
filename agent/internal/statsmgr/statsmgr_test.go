package statsmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func call(t *testing.T, r *actions.Registry, name, params string) (any, error) {
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a.Handler(context.Background(), json.RawMessage(params))
}

func TestConfigAndCronRunAsTheWebsite(t *testing.T) {
	s := Site{Domain: "shop.test", Aliases: []string{"www.shop.test"}, User: "shop", Root: "/home/nimvor/shop.test"}
	c := Config(s)
	for _, want := range []string{`LogFile="/home/nimvor/shop.test/logs/access.log"`, "LogFormat=1", `SiteDomain="shop.test"`, `HostAliases="shop.test www.shop.test localhost 127.0.0.1"`, `DirData="/home/nimvor/shop.test/.awstats"`, "AllowToUpdateStatsFromBrowser=0"} {
		if !strings.Contains(c, want) {
			t.Errorf("config lacks %s:\n%s", want, c)
		}
	}
	line := CronLine(s, "/usr/lib/cgi-bin/awstats.pl")
	if !strings.Contains(line, " */6 * * * shop /usr/bin/perl /usr/lib/cgi-bin/awstats.pl -config=nimvor-shop.test -update") {
		t.Errorf("cron line: %s", line)
	}
}

func TestEnableUpdateAndConfinedData(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	site := `"domain":"a.test","user":"example","root":"/home/nimvor/a.test"`
	os.MkdirAll(filepath.Join(h.Base, "home/nimvor/a.test/logs"), 0o755)
	if _, err := call(t, r, "stats.enable", `{`+site+`,"enabled":true}`); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("without AWStats: %v", err)
	}
	os.MkdirAll(filepath.Join(h.Base, "usr/lib/cgi-bin"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "etc/awstats"), 0o755) // created by the package
	os.WriteFile(filepath.Join(h.Base, "usr/lib/cgi-bin/awstats.pl"), []byte("#!/usr/bin/perl\n"), 0o755)
	for _, bad := range []string{`{"domain":"a.test","aliases":["x\" LogFile=\"/etc/shadow"],"user":"example","enabled":true}`, `{"domain":"a.test","user":"root","enabled":true}`, `{"domain":"a.test","user":"example","root":"/etc","enabled":true}`} {
		if _, err := call(t, r, "stats.enable", bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := call(t, r, "stats.enable", `{`+site+`,"enabled":true}`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Read("etc/awstats/awstats.nimvor-a.test.conf"), `SiteDomain="a.test"`) || !strings.Contains(h.Read("etc/cron.d/nimvor-awstats-a_test"), " example /usr/bin/perl ") {
		t.Fatalf("files:\n%s\n%s", h.Read("etc/awstats/awstats.nimvor-a.test.conf"), h.Read("etc/cron.d/nimvor-awstats-a_test"))
	}
	owner, _ := h.Env.LookupUser("example")
	info, err := os.Stat(filepath.Join(h.Base, "home/nimvor/a.test/.awstats"))
	if err != nil || !info.IsDir() {
		t.Fatal("data folder not created")
	}
	if os.Geteuid() == 0 && int(info.Sys().(*syscall.Stat_t).Uid) != owner.UID {
		t.Errorf("data folder owned by %d, want the website's account", info.Sys().(*syscall.Stat_t).Uid)
	}

	res, err := call(t, r, "stats.update", `{`+site+`}`)
	if err != nil {
		t.Fatal(err)
	}
	h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	o := h.Runner.Opts[len(h.Runner.Opts)-1]
	if o.As == nil || int(o.As.UID) != owner.UID || owner.UID < 1000 {
		t.Fatalf("AWStats must run as the website's account: %+v", o.As)
	}

	// Data: the month's file, and nothing reached through a symlink.
	data := filepath.Join(h.Base, "home/nimvor/a.test/.awstats")
	os.WriteFile(filepath.Join(data, "awstats092026.nimvor-a.test.txt"), []byte("BEGIN_GENERAL 1\nTotalVisits 42\nEND_GENERAL\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "etc/secret"), []byte("root-only"), 0o600)
	os.Symlink("../../../../etc/secret", filepath.Join(data, "awstats082026.nimvor-a.test.txt"))
	res, err = call(t, r, "stats.data", `{`+site+`,"month":"202609"}`)
	if err != nil || !strings.Contains(res.(map[string]any)["content"].(string), "TotalVisits 42") {
		t.Fatalf("data: %v %v", res, err)
	}
	if res, err := call(t, r, "stats.data", `{`+site+`,"month":"202608"}`); err == nil && strings.Contains(res.(map[string]any)["content"].(string), "root-only") {
		t.Fatal("stats.data followed a symlink out of the website")
	}
	if _, err := call(t, r, "stats.data", `{`+site+`,"month":"2026-09"}`); err == nil {
		t.Error("bad month accepted")
	}
	st, _ := call(t, r, "stats.status", `{`+site+`}`)
	m := st.(map[string]any)
	if m["enabled"] != true || m["installed"] != true || len(m["months"].([]string)) < 1 || m["months"].([]string)[0] != "202609" {
		t.Fatalf("status: %+v", m)
	}

	if _, err := call(t, r, "stats.enable", `{`+site+`,"enabled":false}`); err != nil {
		t.Fatal(err)
	}
	if h.Exists("etc/awstats/awstats.nimvor-a.test.conf") || h.Exists("etc/cron.d/nimvor-awstats-a_test") {
		t.Error("turning off must remove the configuration and the schedule")
	}
	if _, err := os.Stat(filepath.Join(data, "awstats092026.nimvor-a.test.txt")); err != nil {
		t.Error("turning off must keep the data")
	}
}
