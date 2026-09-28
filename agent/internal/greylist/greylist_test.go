package greylist

import (
	"context"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type noDNS struct{ txt map[string][]string }

func (d noDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	if v, ok := d.txt[name]; ok {
		return v, nil
	}
	return nil, &net.DNSError{Name: name, IsNotFound: true}
}
func (noDNS) LookupNetIP(_ context.Context, _, h string) ([]netip.Addr, error) {
	return nil, &net.DNSError{Name: h, IsNotFound: true}
}
func (noDNS) LookupMX(_ context.Context, h string) ([]*net.MX, error) {
	return nil, &net.DNSError{Name: h, IsNotFound: true}
}

func req(ip, from, to string) map[string]string {
	return map[string]string{"request": "smtpd_access_policy", "protocol_state": "RCPT", "client_address": ip, "sender": from, "recipient": to, "helo_name": "mx.sender.test"}
}

func TestTripletLifecycle(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cfg := &Config{Enabled: true, Deferral: 5, Resend: 60, Expire: 1440}
	s := NewServer(cfg)
	s.Resolver = noDNS{}
	s.Now = func() time.Time { return now }
	ctx := context.Background()
	r := req("203.0.113.9", "Alice@Sender.test", "bob@here.test")
	if got := s.Decide(ctx, r); !strings.HasPrefix(got, "DEFER_IF_PERMIT 4.7.1 Greylisted, please try again in 5 minute") {
		t.Fatalf("first attempt: %s", got)
	}
	now = now.Add(2 * time.Minute)
	if got := s.Decide(ctx, r); !strings.Contains(got, "in 3 minute") {
		t.Fatalf("early retry: %s", got)
	}
	// Another recipient is its own triplet.
	if got := s.Decide(ctx, req("203.0.113.9", "alice@sender.test", "carol@here.test")); !strings.HasPrefix(got, "DEFER") {
		t.Fatalf("new triplet: %s", got)
	}
	now = now.Add(4 * time.Minute)
	if got := s.Decide(ctx, r); got != "DUNNO" {
		t.Fatalf("retry after deferral: %s", got)
	}
	tr := s.Store.Snapshot()
	var a Triplet
	for _, x := range tr {
		if x.To == "bob@here.test" {
			a = x
		}
	}
	if a.From != "alice@sender.test" || a.Deferred != 2 || a.Accepted != 1 || a.BlockExpire != a.Created+300 || a.MustRetry != a.Created+300+3600 || a.RecordExpire != now.Unix()+1440*60 {
		t.Fatalf("triplet: %+v", a)
	}
	// Remembered: later mail passes at once and refreshes the record.
	now = now.Add(20 * time.Hour)
	if got := s.Decide(ctx, r); got != "DUNNO" {
		t.Fatalf("known triplet: %s", got)
	}
	// Forgotten after the record expires.
	now = now.Add(25 * time.Hour)
	if got := s.Decide(ctx, r); !strings.HasPrefix(got, "DEFER") {
		t.Fatalf("expired triplet: %s", got)
	}
	// A retry after the acceptance window starts again.
	c := req("198.51.100.4", "", "bob@here.test")
	s.Decide(ctx, c)
	now = now.Add(2 * time.Hour)
	if got := s.Decide(ctx, c); !strings.HasPrefix(got, "DEFER") {
		t.Fatalf("late retry: %s", got)
	}
	for _, x := range s.Store.Snapshot() {
		if x.IP == "198.51.100.4" && (x.From != "<>" || x.Deferred != 1 || x.Created != now.Unix()) {
			t.Fatalf("restarted triplet: %+v", x)
		}
	}
}

func TestBypasses(t *testing.T) {
	cfg := &Config{Enabled: true, SPFBypass: true, Trusted: []TrustedHost{{Range: "192.0.2.0/24"}}, Providers: map[string]*ProviderState{
		"google":    {Trust: true, Ranges: []string{"209.85.128.0/17"}},
		"microsoft": {Trust: false, Ranges: []string{"40.92.0.0/15"}},
	}}
	s := NewServer(cfg)
	s.Resolver = noDNS{txt: map[string][]string{"spf.test": {"v=spf1 ip4:198.51.100.0/24 -all"}}}
	ctx := context.Background()
	for _, c := range []struct {
		r    map[string]string
		want string
	}{
		{req("192.0.2.44", "a@x.test", "b@here.test"), "DUNNO"},       // trusted host
		{req("209.85.200.1", "a@gmail.test", "b@here.test"), "DUNNO"}, // trusted provider
		{req("40.92.1.1", "a@outlook.test", "b@here.test"), "DEFER"},  // provider not trusted
		{req("198.51.100.7", "a@spf.test", "b@here.test"), "DUNNO"},   // SPF pass
		{req("203.0.113.7", "a@spf.test", "b@here.test"), "DEFER"},    // SPF fail
		{req("127.0.0.1", "a@x.test", "b@here.test"), "DUNNO"},        // local
		{req("not-an-ip", "a@x.test", "b@here.test"), "DUNNO"},
		{map[string]string{"request": "smtpd_access_policy", "protocol_state": "RCPT", "client_address": "203.0.113.1", "sasl_username": "me@here.test", "recipient": "x@y.test"}, "DUNNO"},
		{map[string]string{"request": "smtpd_access_policy", "protocol_state": "DATA", "client_address": "203.0.113.1", "recipient": "x@y.test"}, "DUNNO"},
	} {
		if got := s.Decide(ctx, c.r); !strings.HasPrefix(got, c.want) {
			t.Errorf("%v: %s", c.r, got)
		}
	}
	s.SetConfig(&Config{Enabled: false})
	if got := s.Decide(ctx, req("203.0.113.50", "a@x.test", "b@here.test")); got != "DUNNO" {
		t.Fatalf("disabled: %s", got)
	}
}

func TestConfigValidateAndProviders(t *testing.T) {
	c := &Config{AutoTrustNew: true, Providers: map[string]*ProviderState{"google": {Trust: false}, "bogus": {Trust: true}}}
	c.Normalize()
	c.Trusted = []TrustedHost{{Range: " 192.0.2.7 ", Comment: " office "}, {Range: "2001:db8::1/48"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Trusted[0].Range != "192.0.2.7" || c.Trusted[0].Comment != "office" || c.Trusted[1].Range != "2001:db8::/48" {
		t.Fatalf("normalized: %+v", c.Trusted)
	}
	if c.Providers["google"].Trust || !c.Providers["microsoft"].Trust || c.Providers["bogus"] != nil {
		t.Fatalf("providers: existing choice kept, new ones trusted, unknown removed: %+v", c.Providers)
	}
	for _, bad := range []Config{
		{Deferral: 0, Resend: 10, Expire: 100},
		{Deferral: 5, Resend: 10, Expire: 10},
		{Deferral: 5, Resend: 10, Expire: 100, Trusted: []TrustedHost{{Range: "evil; rm"}}},
		{Deferral: 5, Resend: 10, Expire: 100, Trusted: []TrustedHost{{Range: "192.0.2.1"}, {Range: "192.0.2.1/32"}}},
		{Deferral: 5, Resend: 10, Expire: 100, Trusted: []TrustedHost{{Range: "192.0.2.1", Comment: "a\nb"}}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestReportAndPersistence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cfg := &Config{Enabled: true}
	cfg.Normalize()
	st := NewStore()
	for i, ip := range []string{"203.0.113.1", "203.0.113.2", "198.51.100.3"} {
		st.Check(cfg, ip, "s@x.test", "r"+string(rune('a'+i))+"@here.test", now.Add(time.Duration(i)*time.Minute))
	}
	st.Check(cfg, "203.0.113.1", "s@x.test", "ra@here.test", now.Add(10*time.Minute)) // passes
	rows, total, counts := Report(st.Snapshot(), ReportQuery{Search: "203.0.113", Sort: "ip", Per: 1, Page: 2}, now.Add(10*time.Minute))
	if total != 2 || len(rows) != 1 || rows[0].IP != "203.0.113.2" || rows[0].Status != "waiting" {
		t.Fatalf("report: %d %+v", total, rows)
	}
	if counts["passed"] != 1 || counts["waiting"] != 2 {
		t.Fatalf("counts: %v", counts)
	}
	rows, _, _ = Report(st.Snapshot(), ReportQuery{Status: "passed"}, now.Add(10*time.Minute))
	if len(rows) != 1 || rows[0].Accepted != 1 {
		t.Fatalf("filter: %+v", rows)
	}
	b := st.Marshal(false)
	st2 := NewStore()
	if err := st2.Load(b, now.Add(10*time.Minute)); err != nil || len(st2.Snapshot()) != 3 {
		t.Fatalf("load: %v %d", err, len(st2.Snapshot()))
	}
	if st.Marshal(false) != nil {
		t.Fatal("unchanged state saved again")
	}
	// Past the resend window the two unpassed ones expire.
	later := now.Add(6 * time.Hour)
	if n := st2.Purge(later); n != 2 {
		t.Fatalf("purged %d", n)
	}
	if n := st2.Delete([]Triplet{{IP: "203.0.113.1", From: "s@x.test", To: "ra@here.test"}}, false); n != 1 {
		t.Fatalf("deleted %d", n)
	}
}

func TestPolicyAndControlSockets(t *testing.T) {
	dir := t.TempDir()
	s := NewServer(&Config{Enabled: true})
	s.Resolver = noDNS{}
	pl, err := net.Listen("unix", filepath.Join(dir, "policy"))
	if err != nil {
		t.Fatal(err)
	}
	cl, _ := net.Listen("unix", filepath.Join(dir, "control"))
	go func() {
		for {
			c, err := pl.Accept()
			if err != nil {
				return
			}
			go s.ServePolicy(context.Background(), c)
		}
	}()
	go func() {
		for {
			c, err := cl.Accept()
			if err != nil {
				return
			}
			go s.ServeControl(c)
		}
	}()
	defer pl.Close()
	defer cl.Close()
	c, err := net.Dial("unix", filepath.Join(dir, "policy"))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	for i := 0; i < 2; i++ { // two requests on one connection
		c.Write([]byte("request=smtpd_access_policy\nprotocol_state=RCPT\nclient_address=203.0.113.5\nsender=a@b.test\nrecipient=c@d.test\n\n"))
		n, _ := c.Read(buf)
		if !strings.HasPrefix(string(buf[:n]), "action=DEFER_IF_PERMIT") || !strings.HasSuffix(string(buf[:n]), "\n\n") {
			t.Fatalf("reply %d: %q", i, buf[:n])
		}
	}
	c.Close()
	var rep struct {
		Total int         `json:"total"`
		Rows  []ReportRow `json:"rows"`
	}
	if err := Control(context.Background(), filepath.Join(dir, "control"), ControlRequest{Op: "report"}, &rep); err != nil || rep.Total != 1 || rep.Rows[0].Deferred != 2 {
		t.Fatalf("report: %v %+v", err, rep)
	}
	if err := Control(context.Background(), filepath.Join(dir, "control"), ControlRequest{Op: "nope"}, nil); err == nil {
		t.Fatal("unknown op accepted")
	}
}
