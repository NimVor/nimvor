package migrate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeDNS struct {
	answers map[string][]string // resolver server → addresses ("" = this server's own)
	fail    map[string]bool
	asked   []string
}

func (f *fakeDNS) Lookup(ctx context.Context, server, domain string) ([]string, error) {
	f.asked = append(f.asked, server+"|"+domain)
	if f.fail[server] {
		return nil, errors.New("timeout")
	}
	return f.answers[server], nil
}

func cutRig(t *testing.T, dns *fakeDNS) (*rig, string) {
	r := newRig(t)
	r.plan.Databases = nil
	r.e.Resolver = dns
	m := runDone(t, r)
	return r, m.ID
}

func statusOf(rep *CutoverReport, domain string) string {
	for _, d := range rep.Domains {
		if d.Domain == domain {
			return d.Status
		}
	}
	return ""
}

func TestCutoverSaysWhetherEveryResolverPointsHere(t *testing.T) {
	here := []string{"203.0.113.10"}
	dns := &fakeDNS{answers: map[string][]string{"": here, "1.1.1.1:53": here, "8.8.8.8:53": here, "9.9.9.9:53": here}}
	r, id := cutRig(t, dns)
	rep, err := r.e.CutoverCheck(context.Background(), id, []string{"203.0.113.10", "2001:db8::10"})
	if err != nil {
		t.Fatal(err)
	}
	if statusOf(rep, "example.com") != DNSHere || len(rep.Domains[0].Resolvers) != 4 || !rep.Domains[0].Resolvers[1].PointsHere {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Domains[0].Advice == "" || len(rep.Notes) < 3 || strings.Join(rep.ServerIPs, ",") != "2001:db8::10,203.0.113.10" {
		t.Errorf("advice/notes/ips = %+v", rep)
	}
	// It is kept with the migration.
	if s := r.state(id); s.Cutover == nil || len(s.Cutover.Domains) != 1 {
		t.Errorf("state = %+v", s.Cutover)
	}
}

func TestCutoverShowsAChangeThatIsStillSpreadingAndOneNotMadeYet(t *testing.T) {
	here, old := []string{"203.0.113.10"}, []string{"198.51.100.7"}
	r, id := cutRig(t, &fakeDNS{answers: map[string][]string{"": here, "1.1.1.1:53": here, "8.8.8.8:53": old, "9.9.9.9:53": old}})
	rep, _ := r.e.CutoverCheck(context.Background(), id, []string{"203.0.113.10"})
	if statusOf(rep, "example.com") != DNSMixed || !strings.Contains(rep.Domains[0].Advice, "still spreading") {
		t.Errorf("mixed = %+v", rep.Domains[0])
	}
	r, id = cutRig(t, &fakeDNS{answers: map[string][]string{"": old, "1.1.1.1:53": old, "8.8.8.8:53": old, "9.9.9.9:53": old}})
	rep, _ = r.e.CutoverCheck(context.Background(), id, []string{"203.0.113.10"})
	if statusOf(rep, "example.com") != DNSElsewhere || !strings.Contains(rep.Domains[0].Advice, "does not change DNS") {
		t.Errorf("elsewhere = %+v", rep.Domains[0])
	}
	// An answer with the right address and another one is not "here": it still sends visitors away.
	r, id = cutRig(t, &fakeDNS{answers: map[string][]string{"": {"203.0.113.10", "198.51.100.7"}, "1.1.1.1:53": {"203.0.113.10", "198.51.100.7"}, "8.8.8.8:53": {"203.0.113.10", "198.51.100.7"}, "9.9.9.9:53": {"203.0.113.10", "198.51.100.7"}}})
	rep, _ = r.e.CutoverCheck(context.Background(), id, []string{"203.0.113.10"})
	if statusOf(rep, "example.com") != DNSElsewhere {
		t.Errorf("a name that also points elsewhere was called here: %+v", rep.Domains[0])
	}
}

func TestCutoverCountsOnlyTheResolversThatAnswered(t *testing.T) {
	here := []string{"203.0.113.10"}
	r, id := cutRig(t, &fakeDNS{answers: map[string][]string{"": here, "1.1.1.1:53": here}, fail: map[string]bool{"8.8.8.8:53": true, "9.9.9.9:53": true}})
	rep, _ := r.e.CutoverCheck(context.Background(), id, []string{"203.0.113.10"})
	if statusOf(rep, "example.com") != DNSHere {
		t.Errorf("status = %+v", rep.Domains[0])
	}
	if rep.Domains[0].Resolvers[2].Error != "no answer" {
		t.Errorf("resolver = %+v", rep.Domains[0].Resolvers[2])
	}
	r, id = cutRig(t, &fakeDNS{fail: map[string]bool{"": true, "1.1.1.1:53": true, "8.8.8.8:53": true, "9.9.9.9:53": true}})
	rep, _ = r.e.CutoverCheck(context.Background(), id, []string{"203.0.113.10"})
	if statusOf(rep, "example.com") != DNSNoAnswer {
		t.Errorf("status = %+v", rep.Domains[0])
	}
}

func TestCutoverNeedsAddressesAndARealMigrationAndOnlyReads(t *testing.T) {
	dns := &fakeDNS{}
	r, id := cutRig(t, dns)
	for _, ips := range [][]string{nil, {"not an ip"}, {"999.1.1.1"}} {
		if _, err := r.e.CutoverCheck(context.Background(), id, ips); err == nil {
			t.Errorf("addresses %v accepted", ips)
		}
	}
	if _, err := r.e.CutoverCheck(context.Background(), "0123456789abcdef", []string{"203.0.113.10"}); err == nil {
		t.Error("an unknown migration was checked")
	}
	fresh := newRig(t)
	m := fresh.create()
	if _, err := fresh.e.CutoverCheck(context.Background(), m.ID, []string{"203.0.113.10"}); err == nil {
		t.Error("a migration that never ran was checked")
	}
	rg := newRegistry(r.h)
	a, err := rg.Lookup("migrate.cutover_check")
	if err != nil || a.Mutating {
		t.Fatalf("action = %+v, %v", a, err)
	}
	// Only names of migrated websites are asked about, never anything the caller sends.
	r.e.CutoverCheck(context.Background(), id, []string{"203.0.113.10"})
	for _, q := range dns.asked {
		if !strings.HasSuffix(q, "|example.com") {
			t.Errorf("a name outside the migration was looked up: %s", q)
		}
	}
}
