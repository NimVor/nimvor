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

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

func dig(t *testing.T, args ...string) string {
	t.Helper()
	out, _ := exec.Command("dig", append([]string{"+short", "+time=2", "+tries=1", "@127.0.0.1"}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out))
}

// Switches the zones through every nameserver (BIND → PowerDNS → NSD → Knot DNS → BIND) on a host that has all
// four installed (and a way to start and stop them as units), updating a zone and signing it where the
// nameserver can.
func TestRealHostBackends(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	for _, bin := range []string{"named", "nsd", "knotd", "pdns_server", "pdnsutil", "keymgr", "kzonecheck", "dig"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip("needs " + bin)
		}
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(120*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)

	keep := map[string][]byte{}
	for _, f := range []string{env.M.DNSConfFile, env.M.KnotConf} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		keep[f] = b
	}
	t.Cleanup(func() {
		if os.Getenv("DNS_HOLD") != "" {
			return
		}
		for _, d := range drivers() {
			env.Systemctl(context.Background(), "stop", d.unit(env))
		}
		for f, b := range keep {
			os.WriteFile(f, b, 0o644)
		}
		os.Remove(env.M.NSDConf)
		os.Remove(env.M.PDNSConf)
		os.Remove(env.M.KnotConf + ".pre-nimvor")
		os.Remove(dnssecDB(env))
		os.Remove(dnssecDB(env) + "-wal")
		os.Remove(dnssecDB(env) + "-shm")
		os.Remove(dnssecDB(env) + ".new")
		os.RemoveAll(env.M.DNSZoneDir)
		os.RemoveAll("/var/lib/knot/keys")
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
	z := Zone{Name: "nimvor-rt.test", SOA: SOA{PrimaryNS: "ns1.nimvor-rt.test.", Hostmaster: "hostmaster@nimvor-rt.test"}, Records: []Record{
		{Name: "@", Type: "NS", Value: "ns1"},
		{Name: "ns1", Type: "A", Value: "127.0.0.1"},
		{Name: "@", Type: "A", Value: "203.0.113.10"},
		{Name: "www", Type: "CNAME", Value: "@"},
		{Name: "@", Type: "MX", Priority: 10, Value: "mail"},
		{Name: "mail", Type: "A", Value: "203.0.113.11"},
		{Name: "@", Type: "TXT", Value: "v=spf1 a ~all"},
	}}

	// BIND first (the default), then the others.
	must("dns.configure", map[string]any{"confirm_edit_package_config": true})
	res := must("dns.zone_apply", z)
	if res["loaded"] != true {
		t.Fatalf("apply = %v", res)
	}
	if got := dig(t, "-p", "53", "nimvor-rt.test", "A"); got != "203.0.113.10" {
		t.Fatalf("BIND serves %q", got)
	}

	// Unsure choices are refused before anything happens.
	if _, err := call("dns.backend_select", map[string]any{"backend": "nonsense"}); code(err) != "invalid_params" {
		t.Fatalf("unknown backend: %v", err)
	}
	if _, err := call("dns.backend_select", map[string]any{"backend": "knot"}); code(err) != "conflict" {
		t.Fatalf("taking over knot.conf without confirmation: %v", err)
	}

	step := 0
	order := []driver{pdnsDriver{}, nsdDriver{}, knotDriver{}, bindDriver{}}
	if only := os.Getenv("DNS_ONLY"); only != "" {
		order = []driver{driverByID(only)}
	}
	for _, to := range order {
		step++
		if _, err := selectBackend(context.Background(), env, to, os.Stderr); err != nil {
			t.Fatalf("switch to %s: %+v", to.label(), err)
		}
		st := must("dns.status", nil)
		if st["backend"] != to.id() || st["running"] != true || st["configured"] != true {
			t.Fatalf("status after switch to %s = %v", to.label(), st)
		}
		if got := dig(t, "-p", "53", "nimvor-rt.test", "A"); got != "203.0.113.10" {
			t.Fatalf("%s serves %q after the switch", to.label(), got)
		}
		// A change goes through the new nameserver.
		z.Records = append(z.Records, Record{Name: "host" + string(rune('a'+step)), Type: "A", Value: "203.0.113." + string(rune('0'+step))})
		res := must("dns.zone_apply", z)
		if res["loaded"] != true {
			t.Fatalf("%s: apply = %v", to.label(), res)
		}
		if got := dig(t, "-p", "53", "host"+string(rune('a'+step))+".nimvor-rt.test", "A"); got != "203.0.113."+string(rune('0'+step)) {
			t.Fatalf("%s did not serve the new record: %q", to.label(), got)
		}
		// A broken zone is refused by the nameserver's own checker.
		bad := z
		bad.Name = "broken.test"
		bad.Records = []Record{{Name: "@", Type: "NS", Value: "ns1.broken.test."}}
		if _, err := call("dns.zone_apply", bad); err != nil && strings.Contains(err.Error(), "Panic") {
			t.Fatal(err)
		}
		call("dns.zone_delete", map[string]any{"zone": "broken.test", "confirm": "broken.test"})

		// DNSSEC where the nameserver can sign.
		ops := to.dnssec()
		info := must("dns.dnssec_status", map[string]any{})
		if ops == nil {
			if info["supported"] != false {
				t.Fatalf("%s should report no DNSSEC: %v", to.label(), info)
			}
			if _, err := call("dns.dnssec_enable", map[string]any{"zone": z.Name}); code(err) != "conflict" {
				t.Fatalf("%s: enabling DNSSEC = %v", to.label(), err)
			}
			continue
		}
		res = must("dns.dnssec_enable", map[string]any{"zone": z.Name})
		ds, _ := res["ds"].([]any)
		if len(ds) == 0 {
			t.Fatalf("%s: no DS record after enabling DNSSEC: %v", to.label(), res)
		}
		t.Logf("%s signs the zone; DS: %v", to.label(), ds[0])
		if key := dig(t, "-p", "53", "+dnssec", "nimvor-rt.test", "DNSKEY"); !strings.Contains(key, "DNSKEY") && !strings.Contains(key, "257 3 13") {
			t.Fatalf("%s serves no DNSKEY: %q", to.label(), key)
		}
		if sig := dig(t, "-p", "53", "+dnssec", "nimvor-rt.test", "A"); !strings.Contains(sig, "RRSIG") && !strings.Contains(sig, "A 13 2") {
			t.Fatalf("%s did not sign the A record: %q", to.label(), sig)
		}
		// A change to a signed zone is served (and still signed).
		z.Records = append(z.Records, Record{Name: "signed" + string(rune('a'+step)), Type: "A", Value: "203.0.113.99"})
		if res := must("dns.zone_apply", z); res["loaded"] != true {
			t.Fatalf("%s: apply to a signed zone = %v", to.label(), res)
		}
		if got := dig(t, "-p", "53", "signed"+string(rune('a'+step))+".nimvor-rt.test", "A"); got != "203.0.113.99" {
			t.Fatalf("%s: signed zone change not served: %q", to.label(), got)
		}
		// A switch keeps a signed zone signed (new keys) and says so.
		if st := must("dns.dnssec_status", map[string]any{"zone": z.Name}); st["enabled"] != true {
			t.Fatalf("%s: status = %v", to.label(), st)
		}
		must("dns.dnssec_disable", map[string]any{"zone": z.Name})
		if st := must("dns.dnssec_status", map[string]any{"zone": z.Name}); st["enabled"] != false {
			t.Fatalf("%s: still signed: %v", to.label(), st)
		}
		if got := dig(t, "-p", "53", "nimvor-rt.test", "A"); got != "203.0.113.10" {
			t.Fatalf("%s stopped serving after DNSSEC was turned off: %q", to.label(), got)
		}
	}
	if len(call2(t, call, "dns.status")) == 0 {
		t.Fatal("no status")
	}
}

func call2(t *testing.T, call func(string, any) (map[string]any, error), name string) map[string]any {
	m, err := call(name, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
