package sysmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
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
