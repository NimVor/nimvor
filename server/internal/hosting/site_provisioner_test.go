package hosting

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

func provisionerEnv(t *testing.T) (*SiteProvisioner, *harness) {
	t.Helper()
	db := writeDB(t)
	h := newHarness(t, db)
	cfg := settings.NewRepository(db)
	ctx := context.Background()
	_ = cfg.Set(ctx, "dns_default_ipv4", "192.0.2.1", 0)
	_ = cfg.Set(ctx, "dns_nameservers", "ns1.panel.test ns2.panel.test", 0)
	h.agent.canned = map[string]json.RawMessage{
		"dns.status":  json.RawMessage(`{"installed":true,"configured":true,"running":true}`),
		"mail.status": json.RawMessage(`{"configured":false}`),
	}
	repo := NewDnsRepository(db)
	dns := NewDnsService(db, repo, h.agent, cfg, NewWebsiteRepository(db), nil)
	mail := NewMailService(db, NewMailRepository(db), h.agent, NewWebsiteRepository(db), NewAccountOptionStore(db), NewPlanLimits(db), nil, dns, repo, nil)
	p := NewSiteProvisioner(db, 1, dns, repo, mail, NewMailRepository(db), NewAccountOptionStore(db), cfg,
		func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
			h.audit = append(h.audit, auditRec{action: action, target: target, result: result, details: details})
		})
	return p, h
}

func records(t *testing.T, h *harness, zone string) []string {
	t.Helper()
	rows, err := h.db.Query(`SELECT r.type || ' ' || r.name || ' ' || r.value FROM dns_records r JOIN dns_zones z ON z.id = r.zone_id WHERE z.name = ?`, zone)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestProvisionCreatesZoneWithTheAccountsOptions(t *testing.T) {
	p, h := provisionerEnv(t)
	ctx := context.Background()
	site, _ := NewWebsiteRepository(h.db).FindFull(ctx, 1, 12, nil)
	done := p.Provision(ctx, site)
	if len(done) == 0 || done[0] != "DNS zone" {
		t.Fatalf("done %v", done)
	}
	recs := strings.Join(records(t, h, "solo.example"), "\n")
	for _, want := range []string{"A @ 192.0.2.1", "TXT @ v=spf1", "TXT _dmarc v=DMARC1", "CAA @"} {
		if !strings.Contains(recs, want) {
			t.Errorf("zone lacks %q:\n%s", want, recs)
		}
	}
	var wid int64
	_ = h.db.QueryRow(`SELECT website_id FROM dns_zones WHERE name = 'solo.example'`).Scan(&wid)
	if wid != 12 {
		t.Errorf("zone website %d", wid)
	}
	if len(h.audit) == 0 || h.audit[len(h.audit)-1].action != "website.provision" {
		t.Errorf("audit %+v", h.audit)
	}
	// Run again: the zone exists and is kept as it is.
	if again := p.Provision(ctx, site); len(again) != 0 {
		t.Errorf("second run did %v", again)
	}
}

func TestProvisionHonoursAccountOptions(t *testing.T) {
	p, h := provisionerEnv(t)
	ctx := context.Background()
	site, _ := NewWebsiteRepository(h.db).FindFull(ctx, 1, 12, nil)
	if err := NewAccountOptionStore(h.db).Set(ctx, 4, map[string]any{"dns_registrar_ns": true}); err != nil {
		t.Fatal(err)
	}
	if done := p.Provision(ctx, site); len(done) != 0 {
		t.Fatalf("registrar name servers: %v", done)
	}
	if err := NewAccountOptionStore(h.db).Set(ctx, 4, map[string]any{"dns_registrar_ns": false, "dns_spf": false, "dns_dmarc": true}); err != nil {
		t.Fatal(err)
	}
	if done := p.Provision(ctx, site); len(done) == 0 {
		t.Fatal("no zone")
	}
	recs := strings.Join(records(t, h, "solo.example"), "\n")
	if strings.Contains(recs, "v=spf1") || strings.Contains(recs, "DMARC") {
		t.Errorf("SPF off also turns DMARC off:\n%s", recs)
	}
}

func TestProvisionAddsARecordUnderOwnZoneAndNeverUnderOthers(t *testing.T) {
	p, h := provisionerEnv(t)
	ctx := context.Background()
	exec := func(q string, a ...any) {
		if _, err := h.db.Exec(q, a...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO dns_zones (id,server_id,name,owner_id,primary_ns,hostmaster,created_at,updated_at) VALUES (1,1,'solo.example',4,'ns1.panel.test','h@x','','')`)
	exec(`INSERT INTO dns_zones (id,server_id,name,owner_id,primary_ns,hostmaster,created_at,updated_at) VALUES (2,1,'cust.example',3,'ns1.panel.test','h@x','','')`)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (30,1,4,'blog.solo.example','u_solo','/r','/r/p','php','8.3.1','active','',''),(31,1,4,'x.cust.example','u_solo','/r','/r/p','php','8.3.1','active','','')`)
	repo := NewWebsiteRepository(h.db)
	own, _ := repo.FindFull(ctx, 1, 30, nil)
	if done := p.Provision(ctx, own); len(done) != 1 || !strings.Contains(done[0], "A record blog in solo.example") {
		t.Fatalf("done %v", done)
	}
	other, _ := repo.FindFull(ctx, 1, 31, nil)
	if done := p.Provision(ctx, other); len(done) != 0 {
		t.Fatalf("another account's zone was touched: %v", done)
	}
}

func TestProvisionNeedsHostedDNS(t *testing.T) {
	p, h := provisionerEnv(t)
	ctx := context.Background()
	h.agent.canned["dns.status"] = json.RawMessage(`{"installed":true,"configured":true,"running":false}`)
	site, _ := NewWebsiteRepository(h.db).FindFull(ctx, 1, 12, nil)
	if done := p.Provision(ctx, site); len(done) != 0 {
		t.Fatalf("DNS is not running: %v", done)
	}
}
