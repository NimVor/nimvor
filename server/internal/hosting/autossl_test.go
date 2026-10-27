package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

type fakeLookup struct {
	a  map[string][]string
	ns map[string][]string
}

func (f fakeLookup) A(_ context.Context, n string) []string  { return f.a[n] }
func (f fakeLookup) NS(_ context.Context, n string) []string { return f.ns[n] }

func autoSslEnv(t *testing.T) (*AutoSslService, *harness, *fakeAgent) {
	t.Helper()
	db := writeDB(t)
	h := newHarness(t, db)
	cfg := settings.NewRepository(db)
	ctx := context.Background()
	_ = cfg.Set(ctx, "dns_default_ipv4", "192.0.2.1", 0)
	_ = cfg.Set(ctx, "dns_nameservers", "ns1.panel.test ns2.panel.test", 0)
	h.agent.canned = map[string]json.RawMessage{"ssl.issue": json.RawMessage(`{"cert_file":"/c.pem","key_file":"/k.pem"}`)}
	ssl := NewSslService(h.agent, h.svc, cfg, nil, 1)
	dns := NewDnsService(db, NewDnsRepository(db), h.agent, cfg, NewWebsiteRepository(db), nil)
	svc := NewAutoSslService(db, 1, cfg, h.agent, ssl, NewWebsiteRepository(db), dns, NewSiteOverview(db, h.agent, cfg, nil))
	svc.lookup = fakeLookup{a: map[string][]string{}, ns: map[string][]string{}}
	return svc, h, h.agent
}

func siteState(t *testing.T, s *AutoSslService, id int64) (state string, tries int, note string) {
	t.Helper()
	if err := s.db.QueryRow(`SELECT auto_ssl, auto_ssl_tries, auto_ssl_note FROM websites WHERE id = ?`, id).Scan(&state, &tries, &note); err != nil {
		t.Fatal(err)
	}
	return
}

func TestAutoSslPointsHere(t *testing.T) {
	s, _, _ := autoSslEnv(t)
	ctx := context.Background()
	lk := s.lookup.(fakeLookup)
	lk.a["a.test"] = []string{"192.0.2.1"}
	lk.a["other.test"] = []string{"198.51.100.9"}
	lk.ns["delegated.test"] = []string{"ns1.panel.test", "ns2.panel.test"}
	lk.ns["mixed.test"] = []string{"ns1.panel.test", "ns.elsewhere.test"}
	if !s.PointsHere(ctx, "a.test", "192.0.2.1") || s.PointsHere(ctx, "other.test", "192.0.2.1") {
		t.Error("the A record decides")
	}
	if !s.PointsHere(ctx, "www.delegated.test", "") {
		t.Error("a name under a zone delegated to this panel points here")
	}
	if s.PointsHere(ctx, "mixed.test", "") {
		t.Error("a zone with a foreign name server does not point here")
	}
}

func TestAutoSslWaitsThenIssues(t *testing.T) {
	s, h, _ := autoSslEnv(t)
	ctx := context.Background()
	_, _ = s.db.Exec(`UPDATE websites SET enabled = 1, https = 0, auto_ssl = 'pending', auto_ssl_next = NULL, created_at = ? WHERE id = 10`, s.stamp(time.Now()))
	if out := s.Process(ctx, 5); len(out) != 0 {
		t.Fatalf("waiting site reported %v", out)
	}
	if st, _, note := siteState(t, s, 10); st != "pending" || !strings.Contains(note, "Waiting for the domain") {
		t.Fatalf("state %q note %q", st, note)
	}
	var next string
	_ = s.db.QueryRow(`SELECT auto_ssl_next FROM websites WHERE id = 10`).Scan(&next)
	if nt, _ := time.Parse(time.RFC3339, next); time.Until(nt) < 10*time.Minute {
		t.Errorf("rechecked too soon: %s", next)
	}

	s.lookup.(fakeLookup).a["cust.example"] = []string{"192.0.2.1"}
	_, _ = s.db.Exec(`UPDATE websites SET auto_ssl_next = NULL WHERE id = 10`)
	out := s.Process(ctx, 5)
	if len(out) != 1 || !strings.Contains(out[0], "issued for cust.example") {
		t.Fatalf("out %v", out)
	}
	if st, _, note := siteState(t, s, 10); st != "done" || !strings.Contains(note, "Certificate issued automatically") {
		t.Fatalf("state %q note %q", st, note)
	}
	if h.agent.callCount("ssl.issue") != 1 {
		t.Error("ssl.issue was not asked")
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM autossl_log WHERE website_id = 10 AND result = 'issued'`).Scan(&n)
	if n != 1 {
		t.Errorf("issued log lines = %d", n)
	}
}

func TestAutoSslGivesUpAndBacksOff(t *testing.T) {
	s, h, _ := autoSslEnv(t)
	ctx := context.Background()
	old := s.stamp(time.Now().Add(-4 * 24 * time.Hour))
	_, _ = s.db.Exec(`UPDATE websites SET enabled = 1, https = 0, auto_ssl = 'pending', auto_ssl_next = NULL, created_at = ? WHERE id = 10`, old)
	if out := s.Process(ctx, 5); len(out) != 1 || !strings.Contains(out[0], "gave up") {
		t.Fatalf("out %v", out)
	}
	if st, _, _ := siteState(t, s, 10); st != "gave_up" {
		t.Fatalf("state %q", st)
	}

	// Pointing here but refused: retried later, doubling, and finally failed.
	s.lookup.(fakeLookup).a["cust.example"] = []string{"192.0.2.1"}
	h.agent.failActions = map[string]error{"ssl.issue": errors.New("rate limited")}
	_, _ = s.db.Exec(`UPDATE websites SET auto_ssl = 'pending', auto_ssl_tries = 0, auto_ssl_next = NULL WHERE id = 10`)
	for i := 1; i <= AutoSSLMaxTries; i++ {
		out := s.Process(ctx, 5)
		if len(out) != 1 {
			t.Fatalf("attempt %d: %v", i, out)
		}
		st, tries, _ := siteState(t, s, 10)
		if tries != i {
			t.Fatalf("attempt %d: tries %d", i, tries)
		}
		if i < AutoSSLMaxTries && st != "pending" {
			t.Fatalf("attempt %d: state %q", i, st)
		}
		_, _ = s.db.Exec(`UPDATE websites SET auto_ssl_next = NULL WHERE id = 10`)
	}
	if st, _, note := siteState(t, s, 10); st != "failed" || !strings.Contains(note, "refused the certificate 6 times") {
		t.Fatalf("state %q note %q", st, note)
	}
}

func TestAutoSslReplaceReason(t *testing.T) {
	mk := func(f func(h *storeHost)) *storeHost {
		h := &storeHost{Name: "a.test", HTTPS: true, Covered: true}
		h.Certificate = &struct {
			Error         string `json:"error"`
			Expired       bool   `json:"expired"`
			SelfSigned    bool   `json:"self_signed"`
			DaysRemaining int    `json:"days_remaining"`
		}{DaysRemaining: 60}
		f(h)
		return h
	}
	cases := map[string]*storeHost{
		"":                      mk(func(h *storeHost) {}),
		"has expired":           mk(func(h *storeHost) { h.Certificate.Expired = true }),
		"does not cover a.test": mk(func(h *storeHost) { h.Covered = false }),
		"self-signed":           mk(func(h *storeHost) { h.Certificate.SelfSigned = true }),
		"expires in 3 days":     mk(func(h *storeHost) { h.Certificate.DaysRemaining = 3 }),
		"cannot be read":        mk(func(h *storeHost) { h.Certificate.Error = "bad" }),
	}
	for want, h := range cases {
		got := ReplaceReason(h)
		if (want == "") != (got == "") || !strings.Contains(got, want) {
			t.Errorf("want %q, got %q", want, got)
		}
	}
	if ReplaceReason(nil) != "" || ReplaceReason(&storeHost{HTTPS: false}) != "" {
		t.Error("no HTTPS, no replacement")
	}
}

func TestAutoSslQueue(t *testing.T) {
	s, h, _ := autoSslEnv(t)
	ctx := context.Background()
	h.agent.canned["ssl.store"] = json.RawMessage(`{"hosts":[{"name":"solo.example","https":true,"covered":true,"certificate":{"expired":true,"days_remaining":0}}]}`)
	_, _ = s.db.Exec(`UPDATE websites SET enabled = 1, https = 0 WHERE id = 10`)
	_, _ = s.db.Exec(`UPDATE websites SET enabled = 1, https = 1 WHERE id = 12`)
	if n, err := s.Queue(ctx, false); err != nil || n != 1 {
		t.Fatalf("replace-only queue: %d %v", n, err)
	}
	if st, _, note := siteState(t, s, 12); st != "renew" || !strings.Contains(note, "has expired") {
		t.Fatalf("state %q note %q", st, note)
	}
	if st, _, _ := siteState(t, s, 10); st != "" {
		t.Errorf("a site without HTTPS is queued only for 'all', got %q", st)
	}
	if n, _ := s.Queue(ctx, true); n != 1 {
		t.Fatalf("all: %d", n)
	}
	if st, _, _ := siteState(t, s, 10); st != "pending" {
		t.Errorf("state %q", st)
	}
}
