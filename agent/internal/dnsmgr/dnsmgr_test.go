package dnsmgr

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func zone() Zone {
	return Zone{Name: "Example.COM.", SOA: SOA{PrimaryNS: "ns1.example.com.", Hostmaster: "host.master@example.com"}, Records: []Record{
		{Name: "@", Type: "ns", Value: "ns1.example.com."},
		{Name: "@", Type: "NS", Value: "ns2.other.net."},
		{Name: "@", Type: "A", Value: "203.0.113.10"},
		{Name: "www", Type: "CNAME", Value: "@"},
		{Name: "@", Type: "MX", Value: "mail", Priority: 10},
		{Name: "mail", Type: "AAAA", Value: "2001:db8::1", TTL: 300},
		{Name: "@", Type: "TXT", Value: `v=spf1 a mx ~all "quoted"`},
		{Name: "_sip._tcp", Type: "SRV", Value: "sip.example.com.", Priority: 10, Weight: 5, Port: 5060},
		{Name: "@", Type: "CAA", Value: "issue letsencrypt.org"},
		{Name: "*.dev", Type: "A", Value: "203.0.113.11"},
	}}
}

func TestValidateAndRender(t *testing.T) {
	z := zone()
	if err := z.Validate(); err != nil {
		t.Fatal(err)
	}
	out := z.Render(2026090101)
	for _, want := range []string{
		"$ORIGIN example.com.\n", "@\tIN\tSOA\tns1.example.com. host\\.master.example.com. (",
		"2026090101 ; serial", "@\t\tIN\tNS\tns1.example.com.\n", "www\t\tIN\tCNAME\texample.com.\n",
		"@\t\tIN\tMX\t10 mail\n", "mail\t300\tIN\tAAAA\t2001:db8::1\n", `@		IN	TXT	"v=spf1 a mx ~all \"quoted\""`,
		"_sip._tcp\t\tIN\tSRV\t10 5 5060 sip.example.com.\n", "@\t\tIN\tCAA\t0 issue \"letsencrypt.org\"\n", "*.dev\t\tIN\tA\t203.0.113.11\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("zone lacks %q:\n%s", want, out)
		}
	}
	if FileSerial(out) != 2026090101 {
		t.Fatal("serial not readable back")
	}
	long := Zone{Name: "a.test", SOA: z.SOA, Records: []Record{{Name: "@", Type: "NS", Value: "ns1.a.test."}, {Name: "@", Type: "TXT", Value: strings.Repeat("x", 600)}}}
	if err := long.Validate(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(long.Render(1), `"`+strings.Repeat("x", 255)+`" "`+strings.Repeat("x", 255)+`" "`+strings.Repeat("x", 90)+`"`) {
		t.Fatal("long TXT not split into 255-byte strings")
	}
}

func TestValidateRejects(t *testing.T) {
	base := zone()
	for name, mut := range map[string]func(*Zone){
		"bad zone":      func(z *Zone) { z.Name = "not a zone" },
		"no apex NS":    func(z *Zone) { z.Records = z.Records[2:] },
		"CNAME + other": func(z *Zone) { z.Records = append(z.Records, Record{Name: "www", Type: "A", Value: "1.2.3.4"}) },
		"apex CNAME":    func(z *Zone) { z.Records = append(z.Records, Record{Name: "@", Type: "CNAME", Value: "x.test."}) },
		"bad A":         func(z *Zone) { z.Records = append(z.Records, Record{Name: "x", Type: "A", Value: "2001:db8::1"}) },
		"bad AAAA":      func(z *Zone) { z.Records = append(z.Records, Record{Name: "x", Type: "AAAA", Value: "1.2.3.4"}) },
		"newline TXT":   func(z *Zone) { z.Records = append(z.Records, Record{Name: "x", Type: "TXT", Value: "a\nb"}) },
		"injection in name": func(z *Zone) {
			z.Records = append(z.Records, Record{Name: "x IN A 1.1.1.1\n", Type: "A", Value: "1.2.3.4"})
		},
		"injection target": func(z *Zone) {
			z.Records = append(z.Records, Record{Name: "x", Type: "CNAME", Value: "a.test.\n@ IN NS evil."})
		},
		"bad type":       func(z *Zone) { z.Records = append(z.Records, Record{Name: "x", Type: "PTR", Value: "a.test."}) },
		"bad CAA":        func(z *Zone) { z.Records = append(z.Records, Record{Name: "@", Type: "CAA", Value: "evil x"}) },
		"low TTL":        func(z *Zone) { z.Records[0].TTL = 5 },
		"bad hostmaster": func(z *Zone) { z.SOA.Hostmaster = "nobody" },
		"bad secondary":  func(z *Zone) { z.Secondaries = []string{"example.net"} },
		"inner wildcard": func(z *Zone) { z.Records = append(z.Records, Record{Name: "a.*", Type: "A", Value: "1.2.3.4"}) },
	} {
		z := base
		z.Records = append([]Record(nil), base.Records...)
		mut(&z)
		if err := z.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNextSerial(t *testing.T) {
	day := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	for old, want := range map[uint32]uint32{0: 2026092501, 2026092501: 2026092502, 2026092599: 2026092600, 2027010101: 2027010102} {
		if got := NextSerial(old, day); got != want {
			t.Errorf("NextSerial(%d) = %d, want %d", old, got, want)
		}
	}
}

func TestParseSOAResponse(t *testing.T) {
	id := [2]byte{1, 2}
	b := []byte{1, 2, 0x84, 0, 0, 1, 0, 1, 0, 0, 0, 0}
	b = append(b, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 6, 0, 1)
	rdata := []byte{0xc0, 12, 0xc0, 12}
	rdata = binary.BigEndian.AppendUint32(rdata, 2026092503)
	rdata = append(rdata, make([]byte, 16)...)
	b = append(b, 0xc0, 12, 0, 6, 0, 1, 0, 0, 0, 60, 0, byte(len(rdata)))
	b = append(b, rdata...)
	if s, err := parseSOAResponse(b, id); err != nil || s != 2026092503 {
		t.Fatalf("serial = %d, %v", s, err)
	}
	b[3] = 3 // NXDOMAIN
	if _, err := parseSOAResponse(b, id); err == nil {
		t.Fatal("error code accepted")
	}
	b[3], b[2] = 0, 0x80 // not authoritative
	if _, err := parseSOAResponse(b, id); err == nil {
		t.Fatal("non-authoritative answer accepted")
	}
}

type fx struct {
	h      *testhost.Host
	r      *actions.Registry
	served uint32
}

func setup(t *testing.T) *fx {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "etc/bind"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "usr/sbin"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/bind/named.conf.local"), []byte("//\n// Do any local configuration here\n//\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "usr/sbin/named"), nil, 0o755)
	f := &fx{h: h, r: actions.NewRegistry(true)}
	Register(f.r, h.Env)
	query = func(ctx context.Context, server, zone string) (uint32, error) { return f.served, nil }
	t.Cleanup(func() { query = querySOA })
	return f
}

func (f *fx) call(t *testing.T, name string, params any) (map[string]any, error) {
	t.Helper()
	a, _ := f.r.Lookup(name)
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

func code(err error) string {
	var ae *actions.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestConfigureAndZones(t *testing.T) {
	f := setup(t)
	if _, err := f.call(t, "dns.zone_apply", zone()); code(err) != "conflict" {
		t.Fatalf("zone before setup: %v", err)
	}
	if _, err := f.call(t, "dns.configure", map[string]any{}); code(err) != "conflict" {
		t.Fatalf("configure without confirmation: %v", err)
	}
	if _, err := f.call(t, "dns.configure", map[string]any{"confirm_edit_package_config": true}); err != nil {
		t.Fatal(err)
	}
	conf := f.h.Read("etc/bind/named.conf.local")
	if !strings.HasPrefix(conf, "//\n// Do any local configuration here") || strings.Count(conf, `include "/etc/bind/whacp/zones.conf";`) != 1 {
		t.Fatalf("named.conf.local = %q", conf)
	}
	// Idempotent: no second include and no confirmation needed.
	if _, err := f.call(t, "dns.configure", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(f.h.Read("etc/bind/named.conf.local"), "include") != 1 {
		t.Fatal("include added twice")
	}

	f.served = 2026090101 // test host date is 2026-09-01
	res, err := f.call(t, "dns.zone_apply", zone())
	if err != nil || res["serial"].(float64) != 2026090101 || res["loaded"] != true {
		t.Fatalf("apply = %v %v", res, err)
	}
	if !strings.Contains(f.h.Read("etc/bind/whacp/db.example.com"), "www\t\tIN\tCNAME\texample.com.") {
		t.Fatal("zone file not written")
	}
	list := f.h.Read("etc/bind/whacp/zones.conf")
	if !strings.Contains(list, `zone "example.com" {`) || !strings.Contains(list, "allow-transfer { none; };") {
		t.Fatalf("zones.conf = %s", list)
	}
	// Second apply: higher serial, secondaries allowed to transfer.
	f.served = 2026090102
	z := zone()
	z.Secondaries = []string{"198.51.100.2"}
	if res, err = f.call(t, "dns.zone_apply", z); err != nil || res["serial"].(float64) != 2026090102 {
		t.Fatalf("second apply = %v %v", res, err)
	}
	if list := f.h.Read("etc/bind/whacp/zones.conf"); !strings.Contains(list, "allow-transfer { 198.51.100.2; };") || !strings.Contains(list, "also-notify { 198.51.100.2; };") {
		t.Fatalf("secondaries: %s", list)
	}
	st, _ := f.call(t, "dns.status", nil)
	if zs := st["zones"].([]any); len(zs) != 1 || zs[0].(map[string]any)["serial"].(float64) != 2026090102 {
		t.Fatalf("status = %v", st)
	}
	// BIND keeps serving the old serial → rolled back.
	before := f.h.Read("etc/bind/whacp/db.example.com")
	f.served = 2026090102
	if _, err := f.call(t, "dns.zone_apply", zone()); code(err) != "failed" {
		t.Fatalf("expected failure, got %v", err)
	}
	if f.h.Read("etc/bind/whacp/db.example.com") != before {
		t.Fatal("zone not restored")
	}
	// A zone the checker rejects is not kept.
	f.h.Fail["named-checkzone"] = "zone example.org/IN: loading from master file failed"
	z2 := zone()
	z2.Name = "example.org"
	if _, err := f.call(t, "dns.zone_apply", z2); err == nil || f.h.Exists("etc/bind/whacp/db.example.org") || strings.Contains(f.h.Read("etc/bind/whacp/zones.conf"), "example.org") {
		t.Fatalf("invalid zone kept: %v", err)
	}
	delete(f.h.Fail, "named-checkzone")
	// Delete.
	if _, err := f.call(t, "dns.zone_delete", map[string]any{"zone": "example.com", "confirm": "nope"}); code(err) != "invalid_params" {
		t.Fatal("delete without confirmation")
	}
	if _, err := f.call(t, "dns.zone_delete", map[string]any{"zone": "example.com", "confirm": "example.com"}); err != nil {
		t.Fatal(err)
	}
	if f.h.Exists("etc/bind/whacp/db.example.com") || strings.Contains(f.h.Read("etc/bind/whacp/zones.conf"), "example.com") {
		t.Fatal("zone not removed")
	}
}

func TestConfigureRollsBackWhenCheckconfFails(t *testing.T) {
	f := setup(t)
	f.h.Fail["named-checkconf"] = "/etc/bind/named.conf.local:5: unknown option"
	if _, err := f.call(t, "dns.configure", map[string]any{"confirm_edit_package_config": true}); err == nil {
		t.Fatal("expected failure")
	}
	if strings.Contains(f.h.Read("etc/bind/named.conf.local"), "include") {
		t.Fatal("include line left behind")
	}
}

// A check that fails without printing anything still says why (reported
// on a real server as "BIND was not set up: named-checkconf:" and nothing).
func TestConfigureSaysWhyWhenCheckconfFailsSilently(t *testing.T) {
	f := setup(t)
	f.h.Fail["named-checkconf"] = ""
	_, err := f.call(t, "dns.configure", map[string]any{"confirm_edit_package_config": true})
	if err == nil || !strings.Contains(err.Error(), "named-checkconf: exit status 1") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenListen(t *testing.T) {
	rhel := "options {\n\tlisten-on port 53 { 127.0.0.1; };\n\tlisten-on-v6 port 53 { ::1; };\n\tallow-query     { localhost; };\n};\n"
	if !localOnly(rhel) {
		t.Fatal("RHEL default not detected")
	}
	got := openListen(rhel)
	if got != "options {\n\tlisten-on port 53 { any; };\n\tlisten-on-v6 port 53 { any; };\n\tallow-query     { localhost; };\n};\n" {
		t.Fatalf("got %q", got)
	}
	if localOnly(got) || localOnly("options {\n\tlisten-on port 53 { 192.0.2.1; };\n};\n") {
		t.Fatal("custom addresses are left alone")
	}
}
