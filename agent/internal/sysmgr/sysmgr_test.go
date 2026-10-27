package sysmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/capability"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestParseUpgrades(t *testing.T) {
	out := `Reading package lists...
Inst libssl3 [3.0.2-0ubuntu1.14] (3.0.2-0ubuntu1.15 Ubuntu:22.04/jammy-updates, Ubuntu:22.04/jammy-security [amd64])
Inst curl [7.81.0-1ubuntu1.15] (7.81.0-1ubuntu1.16 Ubuntu:22.04/jammy-updates [amd64])
Conf libssl3 (3.0.2-0ubuntu1.15 Ubuntu:22.04/jammy-updates, Ubuntu:22.04/jammy-security [amd64])
`
	ups := ParseUpgrades(out)
	if len(ups) != 2 || ups[0].Name != "libssl3" || !ups[0].Security || ups[1].Security || ups[1].New != "7.81.0-1ubuntu1.16" {
		t.Fatalf("ups = %+v", ups)
	}
}

func call(t *testing.T, r *actions.Registry, name, params string) (any, error) {
	t.Helper()
	a, err := r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	return a.Handler(context.Background(), json.RawMessage(params))
}

func TestServerSettingsAndValidation(t *testing.T) {
	h := testhost.New(t)
	os.WriteFile(filepath.Join(h.Base, "etc/hostname"), []byte("web1\n"), 0o644)
	os.MkdirAll(filepath.Join(h.Base, "usr/share/zoneinfo/Europe"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "usr/share/zoneinfo/Europe/Berlin"), []byte("TZif"), 0o644)
	h.Runner.Outputs = map[string]string{"timedatectl show": "Timezone=Etc/UTC\nNTP=yes\nNTPSynchronized=yes\n"}
	r := actions.NewRegistry(true)
	Register(r, h.Env)

	res, _ := call(t, r, "server.settings", `{}`)
	s := res.(Settings)
	if s.Hostname != "web1" || s.Timezone != "Etc/UTC" || !s.Synchronized {
		t.Fatalf("settings = %+v", s)
	}
	for _, bad := range []string{`{"hostname":"-bad"}`, `{"hostname":"a b"}`, `{"hostname":"x;reboot"}`} {
		if _, err := call(t, r, "server.set_hostname", bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := call(t, r, "server.set_hostname", `{"hostname":"Web2.Example.com"}`); err != nil || !h.Runner.Called("hostnamectl set-hostname -- web2.example.com") {
		t.Fatalf("set hostname: %v %v", err, h.Runner.Calls)
	}
	for _, bad := range []string{`{"timezone":"Mars/Base"}`, `{"timezone":"../../etc/passwd"}`} {
		if _, err := call(t, r, "server.set_timezone", bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err := call(t, r, "server.set_timezone", `{"timezone":"Europe/Berlin"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := call(t, r, "server.reboot", `{"confirm":"web2","minutes":5}`); err == nil {
		t.Error("reboot confirmed with the wrong host name")
	}
	if _, err := call(t, r, "server.reboot", `{"confirm":"web1","minutes":5}`); err != nil || !h.Runner.Called("shutdown -r +5 Restart requested from WhaCP") {
		t.Fatalf("reboot: %v %v", err, h.Runner.Calls)
	}
	if _, err := call(t, r, "packages.install", `{"name":"netcat-traditional"}`); err == nil {
		t.Error("non-allowlisted package accepted")
	}
}

func TestPackagesList(t *testing.T) {
	h := testhost.New(t)
	h.Runner.Outputs = map[string]string{}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	res, err := call(t, r, "packages.list", `{}`)
	if err != nil || len(res.([]Package)) != len(Components) {
		t.Fatalf("list: %v %v", err, res)
	}
}

// A Brotli module compiled by hand and loaded, and an ffmpeg installed
// without the package: shown as installed (not from the package), and
// installing their packages is skipped. The ModSecurity package is
// installed but its module is not loaded: installed, not functional.
func TestPackagesByCapability(t *testing.T) {
	h := testhost.New(t)
	w := func(p string) {
		os.MkdirAll(filepath.Join(h.Base, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(h.Base, p), []byte("\x7fELF"), 0o644)
	}
	w("usr/local/nginx/modules/ngx_http_brotli_filter_module.so")
	w("usr/lib/nginx/modules/ngx_http_modsecurity_module.so")
	realNeeded := capability.ReadNeeded
	capability.ReadNeeded = func(string) ([]string, []string, error) { return nil, nil, nil }
	t.Cleanup(func() { capability.ReadNeeded = realNeeded })
	h.Respond = func(id string, args []string) (string, error, bool) {
		switch {
		case id == "nginx" && args[0] == "-V":
			return "nginx version: nginx/1.24.0\nconfigure arguments: --modules-path=/usr/lib/nginx/modules --with-compat\n", nil, true
		case id == "nginx" && args[0] == "-T":
			return "load_module /usr/local/nginx/modules/ngx_http_brotli_filter_module.so;\n", nil, true
		case id == "dpkg-query" && args[0] == "-W":
			return "libnginx-mod-http-modsecurity\tii \t1.0.3-1\n", nil, true
		case id == "dpkg-query":
			return "", fmt.Errorf("not found"), true
		}
		return "", nil, false
	}
	h.Runner.Unavailable["composer"] = true
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	res, err := call(t, r, "packages.list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Package{}
	for _, p := range res.([]Package) {
		by[p.Name] = p
	}
	if b := by["libnginx-mod-brotli"]; !b.Installed || b.State != capability.Functional || b.Source != "other" {
		t.Errorf("brotli: %+v", b)
	}
	if m := by["libnginx-mod-http-modsecurity"]; !m.Installed || m.State != capability.Installed || m.Source != "package" || !strings.Contains(m.Reason, "does not load") {
		t.Errorf("modsecurity: %+v", m)
	}
	if c := by["composer"]; c.Installed || c.State != capability.Missing {
		t.Errorf("composer: %+v", c)
	}
	if f := by["ffmpeg"]; !f.Installed || f.Source != "other" {
		t.Errorf("ffmpeg: %+v", f)
	}
	res2, err := call(t, r, "packages.install", `{"names":["libnginx-mod-brotli","ffmpeg","zip"]}`)
	if err != nil {
		t.Fatal(err)
	}
	h.Env.Jobs.Wait(context.Background(), res2.(map[string]any)["job_id"].(string))
	for _, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "apt-get install") && (strings.Contains(c, "brotli") || strings.Contains(c, "ffmpeg") || !strings.HasSuffix(c, " zip")) {
			t.Errorf("installed what already works: %s", c)
		}
	}
}

// Components the repositories do not offer (Brotli and the ModSecurity
// module on Ubuntu 22.04) are listed as unavailable, not as installable.
func TestPackagesNotInTheRepositories(t *testing.T) {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "var/lib/apt/lists"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "var/lib/apt/lists/archive.ubuntu.com_ubuntu_dists_noble_main_binary-amd64_Packages"), []byte("Package: x\n"), 0o644)
	h.Respond = func(id string, args []string) (string, error, bool) {
		switch {
		case id == "dpkg-query":
			return "", fmt.Errorf("not found"), true
		case id == "apt-cache" && args[0] == "policy":
			// The ModSecurity module has no candidate; the Brotli filter
			// (Debian's name for the Brotli module) is not listed at all,
			// as apt-cache does for names no repository has.
			var b strings.Builder
			for _, p := range args[2:] {
				switch p {
				case "libnginx-mod-http-brotli-filter":
					continue
				case "libnginx-mod-http-modsecurity":
					fmt.Fprintf(&b, "%s:\n  Installed: (none)\n  Candidate: (none)\n", p)
				default:
					fmt.Fprintf(&b, "%s:\n  Installed: (none)\n  Candidate: 1.0-1\n", p)
				}
			}
			return b.String(), nil, true
		}
		return "", nil, false
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	res, err := call(t, r, "packages.list", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.([]Package) {
		if p.Name == "libnginx-mod-brotli" && p.Package != "libnginx-mod-http-brotli-filter" {
			t.Errorf("brotli package name on Debian/Ubuntu: %q", p.Package)
		}
		want := p.Name == "libnginx-mod-brotli" || p.Name == "libnginx-mod-http-modsecurity"
		if p.Unavailable != want {
			t.Errorf("%s: unavailable %v, want %v", p.Name, p.Unavailable, want)
		}
	}
}
