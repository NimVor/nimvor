package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

func monitoringEnv(t *testing.T) (*MonitoringService, *fakeAgent, *[]string, *[]string) {
	t.Helper()
	db := writeDB(t)
	h := newHarness(t, db)
	cfg := settings.NewRepository(db)
	h.agent.canned = map[string]json.RawMessage{
		"system.metrics":         json.RawMessage(`{"cpu_percent":12.5,"disks":[{"mount_point":"/","used_percent":95}],"memory":{"used_percent":40},"load":{"load1":0.5},"network":[{"rx_bytes_per_sec":100,"tx_bytes_per_sec":50}]}`),
		"system.info":            json.RawMessage(`{"system":{"cpu":{"cores":2}}}`),
		"services.status":        json.RawMessage(`[{"unit":"nginx.service","active_state":"failed","unit_file_state":"enabled"},{"unit":"cups.service","active_state":"inactive","unit_file_state":"disabled"}]`),
		"discovery.certificates": json.RawMessage(`[{"name":"shop.example","certificate":{"days_remaining":2}}]`),
		"sites.usage":            json.RawMessage(`[]`),
		"sites.bandwidth":        json.RawMessage(`[]`),
	}
	m := NewMonitoringService(db, 1, h.agent, cfg, NewPlanLimits(db))
	var notes, hooks []string
	m.Notify = func(_ context.Context, _, _ sql.NullInt64, _, severity, title, _, key string) {
		notes = append(notes, severity+" "+key+" "+title)
	}
	m.Webhook = func(_, body string) error { hooks = append(hooks, body); return nil }
	_ = cfg.Set(context.Background(), "alert_webhook_url", "https://hooks.test/x", 0)
	m.Now = func() time.Time { return time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC) }
	return m, h.agent, &notes, &hooks
}

func TestMonitoringOpensRefreshesAndResolvesAlerts(t *testing.T) {
	m, ag, notes, hooks := monitoringEnv(t)
	ctx := context.Background()
	log := strings.Join(m.Run(ctx), "\n")
	for _, want := range []string{"Alert: Disk / is 95% full.", "Alert: nginx.service is failed.", "Alert: Certificate shop.example expires in 2 day(s)."} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "cups") {
		t.Error("a disabled unit is not an alert")
	}
	open := m.OpenAlerts(ctx)
	if len(open) != 3 || open[0].Severity != "critical" {
		t.Fatalf("open alerts (critical first): %+v", open)
	}
	if len(*hooks) != 3 || !strings.Contains((*hooks)[0], `"text":"[WhaCP `) {
		t.Errorf("webhook bodies %v", *hooks)
	}
	if len(*notes) != 3 {
		t.Errorf("notifications %v", *notes)
	}
	var n int
	_ = m.db.QueryRow(`SELECT COUNT(*) FROM metric_samples`).Scan(&n)
	if n != 1 {
		t.Errorf("samples %d", n)
	}

	// Still firing: refreshed, nothing new announced.
	m.Now = func() time.Time { return time.Date(2026, 3, 10, 12, 5, 0, 0, time.UTC) }
	if again := m.Run(ctx); len(again) != 0 {
		t.Errorf("repeat announced %v", again)
	}

	// The disk is fine again: resolved.
	ag.canned["system.metrics"] = json.RawMessage(`{"cpu_percent":1,"disks":[{"mount_point":"/","used_percent":40}],"memory":{"used_percent":40},"load":{"load1":0.1}}`)
	m.Now = func() time.Time { return time.Date(2026, 3, 10, 12, 10, 0, 0, time.UTC) }
	if log := strings.Join(m.Run(ctx), "\n"); !strings.Contains(log, "Resolved: Disk / is 95% full.") {
		t.Errorf("not resolved:\n%s", log)
	}
	if recent := m.RecentAlerts(ctx, 10); len(recent) != 1 || recent[0].Rule != "disk" {
		t.Errorf("recent %+v", recent)
	}
}

func TestMonitoringAgentDownKeepsOpenAlerts(t *testing.T) {
	m, ag, _, _ := monitoringEnv(t)
	ctx := context.Background()
	m.Run(ctx)
	ag.failActions = map[string]error{"system.metrics": errors.New("socket gone")}
	m.Now = func() time.Time { return time.Date(2026, 3, 10, 12, 5, 0, 0, time.UTC) }
	log := strings.Join(m.Run(ctx), "\n")
	if !strings.Contains(log, "The WhaCP agent is not reachable") {
		t.Fatalf("log:\n%s", log)
	}
	if strings.Contains(log, "Resolved") {
		t.Errorf("alerts resolved because nothing could be measured:\n%s", log)
	}
	if got := len(m.OpenAlerts(ctx)); got != 4 {
		t.Errorf("open alerts = %d, want the 3 kept plus the agent", got)
	}
}

func TestMonitoringSeriesAveragesIntoBuckets(t *testing.T) {
	m, _, _, _ := monitoringEnv(t)
	ctx := context.Background()
	base := m.Now().Unix()
	for i := 0; i < 10; i++ {
		_, _ = m.db.Exec(`INSERT INTO metric_samples (server_id,taken_at,cpu,mem,disk,load1,rx_bps,tx_bps) VALUES (1,?,?,?,?,?,0,0)`, base-int64(i)*60, float64(i*10), 50, 60, 1)
	}
	pts := m.Series(ctx, 3600, 3)
	if len(pts) == 0 || len(pts) > 4 {
		t.Fatalf("points %d", len(pts))
	}
	var sum float64
	for _, p := range pts {
		sum += p.Disk
	}
	if sum == 0 {
		t.Error("empty series")
	}
}
