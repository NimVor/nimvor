//go:build realhost

package dnsmgr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Requires BIND running (named.service, or named started by hand) with
// rndc access, as on a fresh bind9 installation.
func TestRealHostDNS(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if exec.Command("rndc", "status").Run() != nil {
		t.Skip("needs a running BIND reachable with rndc")
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	p := dnsPaths(env)
	orig, err := os.ReadFile(p.conf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.WriteFile(p.conf, orig, 0o644)
		os.RemoveAll(p.dir)
		exec.Command("rndc", "reconfig").Run()
	})
	call := func(name string, params any) (map[string]any, error) {
		a, _ := r.Lookup(name)
		b, _ := json.Marshal(params)
		res, err := a.Handler(context.Background(), b)
		if err != nil {
			return nil, err
		}
		out, _ := json.Marshal(res)
		var m map[string]any
		json.Unmarshal(out, &m)
		return m, nil
	}
	must := func(name string, params any) map[string]any {
		t.Helper()
		m, err := call(name, params)
		if err != nil {
			t.Fatalf("%s: %+v", name, err)
		}
		return m
	}
	must("dns.configure", map[string]any{"confirm_edit_package_config": true})
	z := Zone{Name: "whacp-rt.test", SOA: SOA{PrimaryNS: "ns1.whacp-rt.test.", Hostmaster: "hostmaster@whacp-rt.test"}, Records: []Record{
		{Name: "@", Type: "NS", Value: "ns1"},
		{Name: "ns1", Type: "A", Value: "127.0.0.1"},
		{Name: "@", Type: "A", Value: "203.0.113.10"},
		{Name: "www", Type: "CNAME", Value: "@"},
		{Name: "@", Type: "TXT", Value: "v=spf1 a ~all"},
	}}
	res := must("dns.zone_apply", z)
	if res["loaded"] != true {
		t.Fatalf("apply = %v", res)
	}
	serial := uint32(res["serial"].(float64))
	got, err := querySOA(context.Background(), "127.0.0.1:53", "whacp-rt.test")
	if err != nil || got != serial {
		t.Fatalf("served serial = %d, %v; want %d", got, err, serial)
	}
	// Change and reload: the serial goes up and BIND serves it.
	z.Records = append(z.Records, Record{Name: "api", Type: "A", Value: "203.0.113.20"})
	res = must("dns.zone_apply", z)
	if s2 := uint32(res["serial"].(float64)); s2 <= serial {
		t.Fatalf("serial did not increase: %d → %d", serial, s2)
	}
	if out, _ := exec.Command("named-checkzone", "whacp-rt.test", p.zoneFile("whacp-rt.test")).CombinedOutput(); !strings.Contains(string(out), "OK") {
		t.Fatalf("zone file: %s", out)
	}
	// A zone that is valid for WhaCP but conflicts in BIND (defined twice)
	// is refused and nothing is left behind.
	if _, err := call("dns.zone_apply", Zone{Name: "localhost", SOA: z.SOA, Records: []Record{{Name: "@", Type: "NS", Value: "ns1.whacp-rt.test."}}}); err == nil {
		t.Fatal("zone already defined by the distribution accepted")
	}
	if _, err := os.Stat(p.zoneFile("localhost")); err == nil {
		t.Fatal("rejected zone file left behind")
	}
	must("dns.zone_delete", map[string]any{"zone": "whacp-rt.test", "confirm": "whacp-rt.test"})
	time.Sleep(300 * time.Millisecond)
	if _, err := querySOA(context.Background(), "127.0.0.1:53", "whacp-rt.test"); err == nil {
		t.Fatal("deleted zone still served")
	}
}
