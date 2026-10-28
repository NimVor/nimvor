package netmgr

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

const ipJSON = `[{"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]},
{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.10","prefixlen":24,"scope":"global"},{"family":"inet","local":"203.0.113.11","prefixlen":24,"scope":"global"},
{"family":"inet6","local":"2001:db8:1::10","prefixlen":64,"scope":"global"},{"family":"inet6","local":"fe80::1","prefixlen":64,"scope":"link"}]}]`

func TestParseAndCheckRange(t *testing.T) {
	addrs, err := ParseAddresses([]byte(ipJSON))
	if err != nil || len(addrs) != 5 || addrs[1] != (Address{Interface: "eth0", Address: "203.0.113.10", Family: 4, PrefixLen: 24, Scope: "global"}) {
		t.Fatalf("%v %+v", err, addrs)
	}
	gws := []netip.Addr{netip.MustParseAddr("2001:db8:2::1")}
	if p, err := CheckRange("2001:db8:5::/64", addrs, gws); err != nil || p.String() != "2001:db8:5::/64" {
		t.Fatalf("%v %v", p, err)
	}
	for r, why := range map[string]string{
		"2001:db8:1::/64":    "on-link",
		"2001:db8:1::/56":    "on-link",
		"2001:db8:2::/64":    "gateway",
		"2001:db8:5::1/64":   "first address",
		"2001:db8::/32":      "/48 to /128",
		"203.0.113.0/24":     "IPv6 range",
		"fe80::/64":          "global",
		"2001:db8:5::/64; x": "IPv6 range",
	} {
		if _, err := CheckRange(r, addrs, gws); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%s: %v (want %q)", r, err, why)
		}
	}
}

func TestIPv6RangesAction(t *testing.T) {
	h := testhost.New(t)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id != "ip" || len(args) < 2 {
			return "", nil, false
		}
		switch strings.Join(args, " ") {
		case "-j addr show":
			return ipJSON, nil, true
		case "-j -4 route show default", "-j -6 route show default":
			return "[]", nil, true
		}
		return "", nil, true
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	call := func(p string) error {
		a, _ := r.Lookup("net.ipv6_ranges")
		_, err := a.Handler(context.Background(), json.RawMessage(p))
		return err
	}
	if err := call(`{"ranges":["2001:db8:5::/64","2001:db8:6::/64"]}`); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("ip -6 route replace local 2001:db8:5::/64 dev lo") || !h.Runner.Called("systemctl enable -- "+Unit) {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	unit, _ := os.ReadFile(filepath.Join(h.Base, h.Env.M.UnitDir, Unit))
	if !strings.Contains(string(unit), "ExecStart=/usr/sbin/ip -6 route replace local 2001:db8:6::/64 dev lo") {
		t.Fatalf("unit:\n%s", unit)
	}
	for _, bad := range []string{`{"ranges":["2001:db8:1::/64"]}`, `{"ranges":["2001:db8:5::/64","2001:db8:5::/80"]}`} {
		if call(bad) == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if st := LoadState(h.Env); len(st.Ranges) != 2 {
		t.Fatalf("refused change altered the state: %v", st)
	}
	// Local: interface addresses and range members.
	for ip, want := range map[string]bool{"203.0.113.11": true, "2001:db8:6::abc": true, "198.51.100.1": false, "2001:db8:9::1": false} {
		if got, _ := Local(context.Background(), h.Env, ip); got != want {
			t.Errorf("Local(%s) = %v", ip, got)
		}
	}
	// Removing all ranges deletes the routes and the unit.
	if err := call(`{"ranges":[]}`); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("ip -6 route del local 2001:db8:5::/64 dev lo") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	if _, err := os.Stat(filepath.Join(h.Base, h.Env.M.UnitDir, Unit)); !os.IsNotExist(err) {
		t.Fatal("unit left behind")
	}
}
