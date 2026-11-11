package dnsmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// installAll puts the programs of every nameserver on the test host.
func (f *fx) installAll(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"usr/sbin/nsd", "usr/sbin/knotd", "usr/sbin/pdns_server"} {
		os.WriteFile(filepath.Join(f.h.Base, bin), nil, 0o755)
	}
	os.MkdirAll(filepath.Join(f.h.Base, "etc/knot"), 0o755)
	os.MkdirAll(filepath.Join(f.h.Base, "etc/nsd/nsd.conf.d"), 0o755)
	os.MkdirAll(filepath.Join(f.h.Base, "etc/powerdns/pdns.d"), 0o755)
	os.WriteFile(filepath.Join(f.h.Base, "etc/knot/knot.conf"), []byte("# package default\nserver:\n    listen: [ 127.0.0.1@53 ]\n"), 0o644)
	// PowerDNS' DNSSEC database exists (creating it is tested on its own).
	os.MkdirAll(filepath.Join(f.h.Base, "var/lib/powerdns"), 0o755)
	os.WriteFile(filepath.Join(f.h.Base, "var/lib/powerdns/nimvor-dnssec.db"), nil, 0o640)
}

// bindWithZone sets BIND up with one zone, as a server has before a switch.
func (f *fx) bindWithZone(t *testing.T) {
	t.Helper()
	if _, err := f.call(t, "dns.configure", map[string]any{"confirm_edit_package_config": true}); err != nil {
		t.Fatal(err)
	}
	f.served = 2026090101
	z := zone()
	z.Secondaries = []string{"198.51.100.2"}
	if _, err := f.call(t, "dns.zone_apply", z); err != nil {
		t.Fatal(err)
	}
}

func TestEveryNameserverListsTheSameZoneFiles(t *testing.T) {
	f := setup(t)
	p := dnsPaths(f.h.Env)
	zones := []zoneInfo{{Name: "a.test", Secondaries: []string{"198.51.100.2", "2001:db8::2"}}, {Name: "b.test", Secondaries: []string{}}}
	signed := map[string]bool{"b.test": true}
	for id, wants := range map[string][]string{
		"bind":     {`zone "a.test" {`, `file "/etc/bind/nimvor/db.a.test";`, "allow-transfer { 198.51.100.2; 2001:db8::2; };", `dnssec-policy "nimvor" {`, "inline-signing yes;", `key-directory "/etc/bind/nimvor/keys";`},
		"nsd":      {"zone:\n\tname: \"a.test\"\n\tzonefile: \"/etc/bind/nimvor/db.a.test\"", "provide-xfr: 198.51.100.2 NOKEY", "notify: 2001:db8::2 NOKEY", "name: \"b.test\""},
		"knot":     {"remote:\n  - id: nimvor-198-51-100-2", "action: transfer", `- domain: "a.test"`, "template: nimvor\n", "notify: [ nimvor-198-51-100-2, nimvor-2001-db8--2 ]", `- domain: "b.test"`, "template: nimvor-signed"},
		"powerdns": {`zone "a.test" {`, "type master;", `file "/etc/bind/nimvor/db.a.test";`},
	} {
		got := driverByID(id).renderList(f.h.Env, p, zones, signed)
		for _, want := range wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s list lacks %q:\n%s", id, want, got)
			}
		}
	}
	// Only BIND and Knot put the signing into the list; the others sign through their own tools or not at all.
	for _, id := range []string{"nsd", "powerdns"} {
		if got := driverByID(id).renderList(f.h.Env, p, zones, signed); strings.Contains(got, "dnssec") || strings.Contains(got, "signed") {
			t.Errorf("%s list mentions signing:\n%s", id, got)
		}
	}
	// Nothing signed: BIND's list is what it always was.
	if got := driverByID("bind").renderList(f.h.Env, p, zones, nil); strings.Contains(got, "dnssec") {
		t.Errorf("unsigned BIND list mentions DNSSEC:\n%s", got)
	}
}

func TestWiringIsRepeatableAndKeepsTheRestOfThePackageFile(t *testing.T) {
	f := setup(t)
	env := f.h.Env
	for _, id := range []string{"bind", "nsd", "knot", "powerdns"} {
		d := driverByID(id)
		p := pathsFor(env, d)
		cur := "# package file\nsetting: 1\n"
		once, wired := d.wire(env, p, cur)
		if wired {
			t.Errorf("%s: an untouched file reads as wired", id)
		}
		twice, wired := d.wire(env, p, once)
		if !wired || twice != once {
			t.Errorf("%s: wiring is not repeatable (wired=%v)", id, wired)
		}
		if !d.wholeFile() && id != "nsd" && id != "powerdns" && !strings.Contains(once, "setting: 1") {
			t.Errorf("%s: the package's own lines are gone:\n%s", id, once)
		}
	}
	// Where the file is the package's own (no drop-in folder), a marked block is kept and replaced, never repeated.
	env.M.NSDDropIn = false
	d := nsdDriver{}
	p := pathsFor(env, d)
	cur := "server:\n  log-only-syslog: yes\n"
	once, _ := d.wire(env, p, cur)
	twice, wired := d.wire(env, p, once)
	if !wired || strings.Count(twice, blockBegin) != 1 || !strings.Contains(twice, "log-only-syslog") {
		t.Fatalf("block wiring:\n%s", twice)
	}
	// PowerDNS: Debian's own drop-in already launches the BIND backend (a second launch is an error);
	// a package file that does not gets the launch line.
	pd := pdnsDriver{}
	pp := pathsFor(env, pd)
	if c, _ := pd.wire(env, pp, ""); strings.Contains(c, "launch") {
		t.Errorf("the drop-in launches the backend a second time:\n%s", c)
	}
	env.M.PDNSDropIn = false
	if c, _ := pd.wire(env, pp, "# default\nlaunch=\n"); !strings.Contains(c, "launch+=bind") {
		t.Errorf("a package file without the BIND backend must launch it:\n%s", c)
	}
	if c, _ := pd.wire(env, pp, "launch=bind\n"); strings.Contains(c, "launch+=bind") {
		t.Errorf("the backend is launched twice:\n%s", c)
	}
}

func TestBackendsAreListedWithWhatTheyOffer(t *testing.T) {
	f := setup(t)
	res, err := f.call(t, "dns.backends", nil)
	if err != nil {
		t.Fatal(err)
	}
	list := res["backends"].([]any)
	if len(list) != 4 || res["active"] != "bind" {
		t.Fatalf("backends = %v", res)
	}
	byID := map[string]map[string]any{}
	for _, b := range list {
		m := b.(map[string]any)
		byID[m["id"].(string)] = m
	}
	if byID["powerdns"]["installed"] != false || byID["bind"]["installed"] != true || byID["bind"]["active"] != true {
		t.Fatalf("installed/active = %v", byID)
	}
	if byID["nsd"]["dnssec"].(map[string]any)["supported"] != false {
		t.Fatalf("NSD cannot sign: %v", byID["nsd"])
	}
	if byID["knot"]["replaces_config"] != true || byID["bind"]["replaces_config"] != false {
		t.Fatalf("replaces_config = %v", byID)
	}
	for id, b := range byID {
		if len(b["advantages"].([]any)) == 0 || len(b["disadvantages"].([]any)) == 0 {
			t.Errorf("%s has no advantages or disadvantages to show", id)
		}
	}
}

func TestChoosingANameserverAsksFirst(t *testing.T) {
	f := setup(t)
	f.installAll(t)
	f.bindWithZone(t)
	if _, err := f.call(t, "dns.backend_select", map[string]any{"backend": "nonsense"}); code(err) != "invalid_params" {
		t.Fatalf("unknown backend: %v", err)
	}
	if res, err := f.call(t, "dns.backend_select", map[string]any{"backend": "bind"}); err != nil || res["changed"] != false {
		t.Fatalf("choosing what is in use: %v %v", res, err)
	}
	// Knot's configuration is taken over: it must be confirmed.
	_, err := f.call(t, "dns.backend_select", map[string]any{"backend": "knot"})
	if code(err) != "conflict" || !strings.Contains(err.Error(), "knot.conf.pre-nimvor") {
		t.Fatalf("knot without confirmation: %v", err)
	}
	if f.h.Exists("etc/bind/nimvor/.backend") {
		t.Fatal("a refused choice was recorded")
	}
	// Signed zones: the keys cannot move.
	os.WriteFile(filepath.Join(f.h.Base, "etc/bind/nimvor/dnssec-zones"), []byte("example.com\n"), 0o640)
	_, err = f.call(t, "dns.backend_select", map[string]any{"backend": "nsd", "confirm_edit_package_config": true})
	if code(err) != "conflict" || !strings.Contains(err.Error(), "become unsigned") {
		t.Fatalf("NSD with signed zones: %v", err)
	}
	_, err = f.call(t, "dns.backend_select", map[string]any{"backend": "powerdns", "confirm_edit_package_config": true})
	if code(err) != "conflict" || !strings.Contains(err.Error(), "DS record") {
		t.Fatalf("PowerDNS with signed zones: %v", err)
	}
}

func TestSwitchingToNSDServesTheSameZoneFiles(t *testing.T) {
	f := setup(t)
	f.installAll(t)
	f.bindWithZone(t)
	var log bytes.Buffer
	res, err := selectBackend(context.Background(), f.h.Env, nsdDriver{}, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res.(map[string]any)["backend"] != "nsd" || f.h.Read("etc/bind/nimvor/.backend") != "nsd\n" {
		t.Fatalf("res = %v, backend file %q", res, f.h.Read("etc/bind/nimvor/.backend"))
	}
	if got := f.h.Read("etc/nsd/nsd.conf.d/nimvor.conf"); !strings.Contains(got, `include: "/etc/bind/nimvor/zones-nsd.conf"`) {
		t.Fatalf("nsd drop-in = %q", got)
	}
	if got := f.h.Read("etc/bind/nimvor/zones-nsd.conf"); !strings.Contains(got, "name: \"example.com\"") || !strings.Contains(got, "provide-xfr: 198.51.100.2 NOKEY") {
		t.Fatalf("nsd zone list = %q", got)
	}
	if !strings.Contains(f.h.Read("etc/bind/nimvor/db.example.com"), "www\t\tIN\tCNAME") {
		t.Fatal("the zone file changed")
	}
	calls := strings.Join(f.h.Runner.Calls, "\n")
	stop := strings.Index(calls, "systemctl stop -- named.service")
	start := strings.Index(calls, "systemctl start -- nsd.service")
	if stop < 0 || start < 0 || stop > start || !strings.Contains(calls, "nsd-checkconf /etc/nsd/nsd.conf") {
		t.Fatalf("calls:\n%s", calls)
	}
	if st, _ := f.call(t, "dns.status", nil); st["backend"] != "nsd" {
		t.Fatalf("status = %v", st)
	}
	// The next change goes through NSD: its own checker, and a reload (the list did not change).
	f.h.Runner.Calls = nil
	z := zone()
	z.Secondaries = []string{"198.51.100.2"}
	z.Records = append(z.Records, Record{Name: "new", Type: "A", Value: "203.0.113.50"})
	f.served = 2026090102
	if _, err := f.call(t, "dns.zone_apply", z); err != nil {
		t.Fatal(err)
	}
	calls = strings.Join(f.h.Runner.Calls, "\n")
	if !strings.Contains(calls, "nsd-checkzone example.com") || !strings.Contains(calls, "systemctl reload -- nsd.service") || strings.Contains(calls, "restart") || strings.Contains(calls, "named-checkzone") {
		t.Fatalf("calls after the switch:\n%s", calls)
	}
	// A new zone changes the list: NSD restarts.
	f.h.Runner.Calls = nil
	z2 := zone()
	z2.Name = "example.org"
	f.served = 2026090101
	if _, err := f.call(t, "dns.zone_apply", z2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(f.h.Runner.Calls, "\n"), "systemctl restart -- nsd.service") {
		t.Fatalf("a new zone needs a restart:\n%s", strings.Join(f.h.Runner.Calls, "\n"))
	}
}

func TestSwitchingToKnotKeepsTheOriginalConfiguration(t *testing.T) {
	f := setup(t)
	f.installAll(t)
	f.bindWithZone(t)
	if _, err := selectBackend(context.Background(), f.h.Env, knotDriver{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := f.h.Read("etc/knot/knot.conf.pre-nimvor"); !strings.Contains(got, "# package default") {
		t.Fatalf("original = %q", got)
	}
	conf := f.h.Read("etc/knot/knot.conf")
	for _, want := range []string{"Managed by NimVor", "journal-content: none", `include: "/etc/bind/nimvor/zones-knot.conf"`, "id: nimvor-signed", `kasp-db: "/var/lib/knot/keys"`} {
		if !strings.Contains(conf, want) {
			t.Errorf("knot.conf lacks %q:\n%s", want, conf)
		}
	}
	if !strings.Contains(strings.Join(f.h.Runner.Calls, "\n"), "knotc -c /etc/knot/knot.conf conf-check") {
		t.Fatalf("calls:\n%s", strings.Join(f.h.Runner.Calls, "\n"))
	}
	// Switching away and back must not take the first original for NimVor's own file.
	if _, err := selectBackend(context.Background(), f.h.Env, bindDriver{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := selectBackend(context.Background(), f.h.Env, knotDriver{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if got := f.h.Read("etc/knot/knot.conf.pre-nimvor"); !strings.Contains(got, "# package default") {
		t.Fatalf("original lost: %q", got)
	}
}

func TestSwitchingBackPutsTheOldNameserverBackWhenTheNewOneFails(t *testing.T) {
	f := setup(t)
	f.installAll(t)
	f.bindWithZone(t)
	// The new configuration is refused by its own checker: nothing was stopped, nothing is left behind.
	f.h.Fail["nsd-checkconf"] = "nsd.conf: unknown option"
	if _, err := selectBackend(context.Background(), f.h.Env, nsdDriver{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected a failure")
	}
	if f.h.Exists("etc/nsd/nsd.conf.d/nimvor.conf") || f.h.Exists("etc/bind/nimvor/.backend") || strings.Contains(strings.Join(f.h.Runner.Calls, "\n"), "stop -- named.service") {
		t.Fatalf("a refused configuration changed the server; calls:\n%s", strings.Join(f.h.Runner.Calls, "\n"))
	}
	if st, _ := f.call(t, "dns.status", nil); st["backend"] != "bind" {
		t.Fatalf("status = %v", st)
	}
	// The new daemon does not start: BIND is started again, the files are restored.
	delete(f.h.Fail, "nsd-checkconf")
	f.h.Fail["systemctl start -- nsd.service"] = "Job for nsd.service failed"
	var log bytes.Buffer
	_, err := selectBackend(context.Background(), f.h.Env, nsdDriver{}, &log)
	if code(err) != "failed" || !strings.Contains(err.Error(), "BIND is running again") {
		t.Fatalf("err = %v", err)
	}
	if f.h.Exists("etc/nsd/nsd.conf.d/nimvor.conf") || f.h.Exists("etc/bind/nimvor/.backend") {
		t.Fatal("files left after the failed switch")
	}
	calls := strings.Join(f.h.Runner.Calls, "\n")
	if !strings.Contains(calls, "systemctl start -- named.service") {
		t.Fatalf("BIND was not started again:\n%s", calls)
	}
	// And the zone is not served by the new one: the check after the start fails → back as well.
	delete(f.h.Fail, "systemctl start -- nsd.service")
	f.served = 1 // NSD would serve an older serial
	if _, err := selectBackend(context.Background(), f.h.Env, nsdDriver{}, &log); code(err) != "failed" {
		t.Fatalf("a nameserver that does not serve the zones was accepted: %v", err)
	}
	if f.h.Exists("etc/bind/nimvor/.backend") {
		t.Fatal("the choice was recorded")
	}
}

func TestPowerDNSKeepsItsSettingsInADropInAndTheSecondariesAsMetadata(t *testing.T) {
	f := setup(t)
	f.installAll(t)
	f.bindWithZone(t)
	if _, err := selectBackend(context.Background(), f.h.Env, pdnsDriver{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	conf := f.h.Read("etc/powerdns/pdns.d/nimvor.conf")
	for _, want := range []string{"bind-config=/etc/bind/nimvor/zones-pdns.conf", "bind-dnssec-db=/var/lib/powerdns/nimvor-dnssec.db", "local-address=0.0.0.0"} {
		if !strings.Contains(conf, want) {
			t.Errorf("pdns drop-in lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "launch") {
		t.Fatalf("the drop-in launches the backend a second time:\n%s", conf)
	}
	f.h.Runner.Calls = nil
	z := zone()
	z.Secondaries = []string{"198.51.100.2", "198.51.100.3"}
	f.served = 2026090102
	if _, err := f.call(t, "dns.zone_apply", z); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(f.h.Runner.Calls, "\n")
	// Secondaries are metadata for PowerDNS: the list is unchanged, so only the zone is read again.
	if strings.Contains(calls, "pdns_control reload") {
		t.Errorf("the zone list did not change but PowerDNS reloaded its configuration:\n%s", calls)
	}
	for _, want := range []string{"pdnsutil check-zone example.com", "pdns_control bind-reload-now example.com", "pdnsutil set-meta example.com ALLOW-AXFR-FROM 198.51.100.2 198.51.100.3", "pdnsutil set-meta example.com ALSO-NOTIFY 198.51.100.2 198.51.100.3"} {
		if !strings.Contains(calls, want) {
			t.Errorf("calls lack %q:\n%s", want, calls)
		}
	}
	// pdnsutil reports problems with exit status 0 too.
	f.h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "pdnsutil" && len(args) > 0 && args[0] == "check-zone" {
			return "[Error] CNAME 'www.example.com' found with other records\nChecked 11 records of 'example.com', 1 errors, 0 warnings.", nil, true
		}
		return "", nil, false
	}
	f.served = 2026090103
	if _, err := f.call(t, "dns.zone_apply", z); err == nil {
		t.Fatal("a zone pdnsutil reports errors for was kept")
	}
}

func TestSigningWithBINDKnotAndPowerDNSAndNotWithNSD(t *testing.T) {
	f := setup(t)
	f.installAll(t)
	f.bindWithZone(t)
	version := "BIND 9.18.1-1ubuntu1 (Extended Support Version)"
	f.h.Respond = func(id string, args []string) (string, error, bool) {
		switch {
		case id == "named" && len(args) > 0 && args[0] == "-v":
			return version, nil, true
		case id == "dnssec-dsfromkey":
			return "example.com. IN DS 19358 13 2 8BDD904E37605DECAC7CB96EE137BAC2B534430CD347FCACFD938E3D8419CFD1\n", nil, true
		case id == "keymgr":
			return "example.com. DS 26738 13 2 52f628c9085a4eeaa1e9a094bbe0d5d54cc4aa7f0da0ce4063a6b782f5d2db17\nexample.com. DS 26738 13 4 06a4e4167a142fdea1280d61760aedd8cd868a5799ebbb5663d3537482e9f7cc3e9035cbe3693e1f3b8f49cbd786d277\n", nil, true
		case id == "pdnsutil" && len(args) > 0 && args[0] == "export-zone-ds":
			return "example.com. IN DS 1470 13 2 7f872c12d3a7f96f61ef737ebac3e06d66ba89f01be715a7f70edb9259494506 ; ( SHA256 digest )\n", nil, true
		}
		return "", nil, false
	}
	// BIND: the zone list carries the policy; the key file makes the DS.
	os.MkdirAll(filepath.Join(f.h.Base, "etc/bind/nimvor/keys"), 0o770)
	os.WriteFile(filepath.Join(f.h.Base, "etc/bind/nimvor/keys/Kexample.com.+013+19358.key"), []byte("example.com. 3600 IN DNSKEY 257 3 13 AAAA\n"), 0o640)
	res, err := f.call(t, "dns.dnssec_enable", map[string]any{"zone": "Example.COM."})
	if err != nil {
		t.Fatal(err)
	}
	ds := res["ds"].([]any)
	if len(ds) != 1 || ds[0].(map[string]any)["key_tag"].(float64) != 19358 || ds[0].(map[string]any)["digest_type"].(float64) != 2 {
		t.Fatalf("BIND DS = %v", res)
	}
	if got := f.h.Read("etc/bind/nimvor/zones.conf"); !strings.Contains(got, "dnssec-policy \"nimvor\";") || !strings.Contains(got, "inline-signing yes;") {
		t.Fatalf("zones.conf = %s", got)
	}
	if got := f.h.Read("etc/bind/nimvor/dnssec-zones"); !strings.Contains(got, "example.com") {
		t.Fatalf("signed zones = %q", got)
	}
	// Enabling again changes nothing; status lists the zone with its DS.
	if res, _ := f.call(t, "dns.dnssec_enable", map[string]any{"zone": "example.com"}); res["changed"] != false {
		t.Fatalf("second enable = %v", res)
	}
	st, _ := f.call(t, "dns.dnssec_status", map[string]any{"zone": "example.com"})
	if st["enabled"] != true || len(st["ds"].([]any)) != 1 || st["backend"] != "bind" {
		t.Fatalf("status = %v", st)
	}
	// A zone applied while signed is verified with the signed serial (signing raises it).
	f.served = 2026090199
	if _, err := f.call(t, "dns.zone_apply", zone()); err != nil {
		t.Fatalf("a signed zone's higher served serial was refused: %v", err)
	}
	// Old BIND cannot sign by policy.
	version = "BIND 9.11.4-P2-RedHat"
	if _, err := f.call(t, "dns.dnssec_enable", map[string]any{"zone": "other.test"}); code(err) != "conflict" || !strings.Contains(err.Error(), "9.16") {
		t.Fatalf("old BIND: %v", err)
	}
	// Turn it off again.
	version = "BIND 9.18.1-1ubuntu1"
	if _, err := f.call(t, "dns.dnssec_disable", map[string]any{"zone": "example.com"}); err != nil {
		t.Fatal(err)
	}
	if got := f.h.Read("etc/bind/nimvor/zones.conf"); strings.Contains(got, "dnssec") {
		t.Fatalf("zones.conf still signs: %s", got)
	}

	// Knot and PowerDNS sign too; NSD does not.
	for id, want := range map[string]string{"knot": "26738", "powerdns": "1470"} {
		if _, err := selectBackend(context.Background(), f.h.Env, driverByID(id), &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
		res, err := f.call(t, "dns.dnssec_enable", map[string]any{"zone": "example.com"})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if ds := res["ds"].([]any); len(ds) == 0 || !strings.Contains(string(mustJSON(ds[0])), want) {
			t.Fatalf("%s DS = %v", id, res["ds"])
		}
		if _, err := f.call(t, "dns.dnssec_disable", map[string]any{"zone": "example.com"}); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	calls := strings.Join(f.h.Runner.Calls, "\n")
	for _, want := range []string{"pdnsutil secure-zone example.com", "pdnsutil rectify-zone example.com", "pdnsutil disable-dnssec example.com", "keymgr -c /etc/knot/knot.conf example.com ds"} {
		if !strings.Contains(calls, want) {
			t.Errorf("calls lack %q", want)
		}
	}
	if _, err := selectBackend(context.Background(), f.h.Env, nsdDriver{}, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.call(t, "dns.dnssec_enable", map[string]any{"zone": "example.com"}); code(err) != "conflict" || !strings.Contains(err.Error(), "does not sign") {
		t.Fatalf("NSD: %v", err)
	}
	if st, _ := f.call(t, "dns.dnssec_status", nil); st["supported"] != false {
		t.Fatalf("NSD status = %v", st)
	}
}

func TestSwitchingKeepsSignedZonesSignedWithNewKeysOrUnsignedForNSD(t *testing.T) {
	f := setup(t)
	f.installAll(t)
	f.bindWithZone(t)
	f.h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "pdnsutil" && len(args) > 0 && args[0] == "export-zone-ds" {
			return "example.com. IN DS 1470 13 2 7f87\n", nil, true
		}
		return "", nil, false
	}
	os.WriteFile(filepath.Join(f.h.Base, "etc/bind/nimvor/dnssec-zones"), []byte("example.com\n"), 0o640)
	res, err := f.call(t, "dns.backend_select", map[string]any{"backend": "powerdns", "confirm_edit_package_config": true, "confirm_dnssec_reset": true})
	if err != nil {
		t.Fatal(err)
	}
	f.h.Env.Jobs.Drain()
	_ = res
	calls := strings.Join(f.h.Runner.Calls, "\n")
	if !strings.Contains(calls, "pdnsutil secure-zone example.com") {
		t.Fatalf("the signed zone was not signed on PowerDNS:\n%s", calls)
	}
	if got := f.h.Read("etc/bind/nimvor/dnssec-zones"); !strings.Contains(got, "example.com") {
		t.Fatalf("signed zones = %q", got)
	}
	// To NSD: the zones become unsigned and the list says so.
	var log bytes.Buffer
	out, err := selectBackend(context.Background(), f.h.Env, nsdDriver{}, &log)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.h.Read("etc/bind/nimvor/dnssec-zones"); strings.Contains(got, "example.com") {
		t.Fatalf("NSD still lists signed zones: %q", got)
	}
	if notes := out.(map[string]any)["dnssec_notes"].([]string); len(notes) != 1 || !strings.Contains(notes[0], "unsigned") {
		t.Fatalf("notes = %v", notes)
	}
}

func TestParseDS(t *testing.T) {
	got := parseDS("example.com. IN DS 19358 13 2 8BDD904E\nexample.com. 3600 IN DS 19358 13 4 AABB\nexample.com. DS 26738 13 2 52f6\nexample.com. IN DS 19358 13 2 8bdd904e\nnot a record\n")
	if len(got) != 3 {
		t.Fatalf("parsed %v", got)
	}
	if got[0].KeyTag != 19358 || got[0].Digest != "8bdd904e" || got[0].String() != "19358 13 2 8bdd904e" {
		t.Fatalf("first = %+v", got[0])
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// PowerDNS' database is made by pdnsutil (which refuses an existing file and runs as root), so the agent has
// it write a scratch file and places it with the daemon's ownership; a scratch file left by an earlier attempt
// does not get in the way.
func TestPowerDNSDatabaseIsCreatedOnce(t *testing.T) {
	f := setup(t)
	os.MkdirAll(filepath.Join(f.h.Base, "var/lib/powerdns"), 0o755)
	os.WriteFile(filepath.Join(f.h.Base, "var/lib/powerdns/nimvor-dnssec.db.new"), []byte("stale"), 0o644)
	f.h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "pdnsutil" && len(args) == 2 && args[0] == "create-bind-db" {
			if _, err := os.Stat(args[1]); err == nil {
				return "Error: SQLite database already exists", errors.New("exit status 1"), true
			}
			os.WriteFile(args[1], []byte("SQLite format 3"), 0o644)
			return "", nil, true
		}
		return "", nil, false
	}
	d := pdnsDriver{}
	if err := d.prepare(context.Background(), f.h.Env, pathsFor(f.h.Env, d)); err != nil {
		t.Fatal(err)
	}
	if f.h.Read("var/lib/powerdns/nimvor-dnssec.db") != "SQLite format 3" || f.h.Exists("var/lib/powerdns/nimvor-dnssec.db.new") {
		t.Fatalf("db = %q, scratch left = %v", f.h.Read("var/lib/powerdns/nimvor-dnssec.db"), f.h.Exists("var/lib/powerdns/nimvor-dnssec.db.new"))
	}
	n := len(f.h.Runner.Calls)
	if err := d.prepare(context.Background(), f.h.Env, pathsFor(f.h.Env, d)); err != nil || len(f.h.Runner.Calls) != n {
		t.Fatalf("an existing database was created again: %v", err)
	}
}

// Ubuntu runs systemd-resolved's stub on 127.0.0.53:53: a nameserver that binds every address on port 53 then
// fails to start. NimVor binds the machine's own addresses instead (BIND binds one at a time and carries on).
func TestNameserversAvoidTheResolverStub(t *testing.T) {
	f := setup(t)
	env := f.h.Env
	interfaceAddrs = func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("127.0.0.1"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("127.0.0.53"), Mask: net.CIDRMask(8, 32)},
			&net.IPNet{IP: net.ParseIP("203.0.113.10"), Mask: net.CIDRMask(24, 32)},
			&net.IPNet{IP: net.ParseIP("2001:db8::10"), Mask: net.CIDRMask(64, 128)},
			&net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)},
		}, nil
	}
	t.Cleanup(func() { interfaceAddrs = net.InterfaceAddrs })
	if got := listenAddresses(env); got != nil {
		t.Fatalf("no stub, yet addresses = %v", got)
	}
	os.MkdirAll(filepath.Join(f.h.Base, "run/systemd/resolve"), 0o755)
	os.WriteFile(filepath.Join(f.h.Base, "run/systemd/resolve/stub-resolv.conf"), []byte("nameserver 127.0.0.53\n"), 0o644)
	// This machine has no IPv6 kernel support (the fixture has no /proc/net/if_inet6): IPv4 only.
	if got := strings.Join(listenAddresses(env), ","); got != "127.0.0.1,203.0.113.10" {
		t.Fatalf("addresses = %s", got)
	}
	os.MkdirAll(filepath.Join(f.h.Base, "proc/net"), 0o755)
	os.WriteFile(filepath.Join(f.h.Base, "proc/net/if_inet6"), []byte("x"), 0o644)
	if got := strings.Join(listenAddresses(env), ","); got != "127.0.0.1,2001:db8::10,203.0.113.10" {
		t.Fatalf("addresses with IPv6 = %s", got)
	}
	nsd, _ := nsdDriver{}.wire(env, pathsFor(env, nsdDriver{}), "")
	knot, _ := knotDriver{}.wire(env, pathsFor(env, knotDriver{}), "")
	pdns, _ := pdnsDriver{}.wire(env, pathsFor(env, pdnsDriver{}), "")
	for name, want := range map[string][]string{
		"nsd":      {"ip-address: 127.0.0.1\n", "ip-address: 203.0.113.10\n", "ip-address: 2001:db8::10\n"},
		"knot":     {`listen: [ "127.0.0.1@53", "2001:db8::10@53", "203.0.113.10@53" ]`},
		"powerdns": {"local-address=127.0.0.1, 2001:db8::10, 203.0.113.10\n"},
	} {
		got := map[string]string{"nsd": nsd, "knot": knot, "powerdns": pdns}[name]
		for _, w := range want {
			if !strings.Contains(got, w) {
				t.Errorf("%s lacks %q:\n%s", name, w, got)
			}
		}
		if strings.Contains(got, "127.0.0.53") || strings.Contains(got, "fe80") || strings.Contains(got, "0.0.0.0") {
			t.Errorf("%s binds an address it must not:\n%s", name, got)
		}
	}
	// A change of the machine's addresses does not make the nameserver look unconfigured.
	if _, wired := (nsdDriver{}).wire(env, pathsFor(env, nsdDriver{}), nsd+"# older addresses\n"); !wired {
		t.Fatal("wiring is judged by what NimVor wrote, not by every address in it")
	}
	// BIND is left alone: it binds one address at a time.
	if c, _ := (bindDriver{}).wire(env, pathsFor(env, bindDriver{}), "options {};\n"); strings.Contains(c, "203.0.113.10") {
		t.Fatalf("BIND wiring lists addresses:\n%s", c)
	}
}
