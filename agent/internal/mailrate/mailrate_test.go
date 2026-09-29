package mailrate

import (
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFindOwner(t *testing.T) {
	table := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:0019 00000000:0000 0A 00000000:00000000 00:00000000 00000000   0        0 1 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0100007F:D431 0100007F:0019 01 00000000:00000000 00:00000000 00000000  1003        0 2 1 0000000000000000 20 4 30 10 -1\n" +
		"   2: 0100007F:0019 0100007F:D431 01 00000000:00000000 00:00000000 00000000   109        0 3 1 0000000000000000 20 4 30 10 -1\n"
	c := netip.MustParseAddrPort("127.0.0.1:54321")
	if uid, ok := findOwner(table, c, netip.MustParseAddrPort("127.0.0.1:25")); !ok || uid != 1003 {
		t.Fatalf("got %d %v", uid, ok)
	}
	if _, ok := findOwner(table, c, netip.MustParseAddrPort("127.0.0.1:587")); ok {
		t.Error("matched a connection to another port")
	}
	v6 := "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid\n" +
		"   0: 00000000000000000000000001000000:C350 00000000000000000000000001000000:0019 01 00000000:00000000 00:00000000 00000000  1004 0 5\n"
	if uid, ok := findOwner(v6, netip.MustParseAddrPort("[::1]:50000"), netip.AddrPort{}); !ok || uid != 1004 {
		t.Fatalf("v6: got %d %v", uid, ok)
	}
}

// The owner of a real connection on this machine is found.
func TestProcOwnerRealSocket(t *testing.T) {
	if _, err := os.Stat("/proc/self/net/tcp"); err != nil {
		t.Skip("no /proc/self/net/tcp")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer l.Close()
	go func() {
		if c, err := l.Accept(); err == nil {
			time.Sleep(time.Second)
			c.Close()
		}
	}()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	uid, ok := ProcOwner("/proc/self/net")(c.LocalAddr().(*net.TCPAddr).AddrPort(), l.Addr().(*net.TCPAddr).AddrPort())
	if !ok || uid != os.Getuid() {
		t.Fatalf("got %d %v, want %d", uid, ok, os.Getuid())
	}
}

func req(port string) map[string]string {
	return map[string]string{"request": "smtpd_access_policy", "protocol_state": "RCPT", "client_address": "127.0.0.1", "client_port": port, "server_address": "127.0.0.1", "server_port": "25"}
}

func TestLimitPerAccountAndHour(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 5, 0, 0, time.UTC)
	owners := map[uint16]int{1000: 1003, 1001: 1004, 1002: 109}
	s := NewServer(&Config{Enabled: true, PerHour: 3, Accounts: map[string]int{"1004": 0}, Names: map[string]string{"1003": "shop"}}, func(c, _ netip.AddrPort) (int, bool) {
		uid, ok := owners[c.Port()]
		return uid, ok
	})
	s.Now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if a := s.Decide(req("1000")); a != "DUNNO" {
			t.Fatalf("recipient %d: %s", i+1, a)
		}
	}
	if a := s.Decide(req("1000")); !strings.HasPrefix(a, "DEFER_IF_PERMIT 4.7.1") {
		t.Fatalf("over the limit: %s", a)
	}
	s.Decide(req("1000"))
	// No limit for 1004, none for system accounts, none for unknown owners
	// or remote clients or signed-in users.
	for i := 0; i < 10; i++ {
		for _, r := range []map[string]string{req("1001"), req("1002"), req("1999")} {
			if a := s.Decide(r); a != "DUNNO" {
				t.Fatalf("%v: %s", r, a)
			}
		}
	}
	remote := req("1000")
	remote["client_address"] = "192.0.2.1"
	sasl := req("1000")
	sasl["sasl_username"] = "a@shop.test"
	if s.Decide(remote) != "DUNNO" || s.Decide(sasl) != "DUNNO" {
		t.Error("limited a remote client or a signed-in user")
	}
	st := s.Snapshot()
	if len(st.Events) != 1 || st.Events[0].Name != "shop" {
		t.Errorf("status: %+v", st)
	}
	for _, a := range st.Accounts {
		if a.UID == 1003 && (!a.Blocked || a.Sent != 3) {
			t.Errorf("shop: %+v", a)
		}
	}
	// State survives a restart within the hour.
	b := s.Marshal()
	s2 := NewServer(&Config{Enabled: true, PerHour: 3}, s.Owner)
	s2.Now = s.Now
	if err := s2.Load(b); err != nil {
		t.Fatal(err)
	}
	if a := s2.Decide(req("1000")); a == "DUNNO" {
		t.Error("restart reset the count")
	}
	// The next hour starts over; so does a reset.
	now = now.Add(time.Hour)
	if a := s.Decide(req("1000")); a != "DUNNO" {
		t.Errorf("next hour: %s", a)
	}
	s2.Reset(1003)
	if a := s2.Decide(req("1000")); a != "DUNNO" {
		t.Errorf("after reset: %s", a)
	}
	// Off: nothing is limited.
	s.SetConfig(&Config{Enabled: false, PerHour: 1})
	for i := 0; i < 5; i++ {
		if s.Decide(req("1000")) != "DUNNO" {
			t.Fatal("limited while off")
		}
	}
}
