package hosting

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/settings"
)

func setupEnv(t *testing.T) (*ServerSetup, *harness) {
	t.Helper()
	db := writeDB(t)
	h := newHarness(t, db)
	cfg := settings.NewRepository(db)
	s := NewServerSetup(db, 1, h.agent, cfg, NewPlanRepository(db), h.features, NewBackupScheduler(db, 1, func(context.Context, string, string, string, string, map[string]any) {}),
		func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
			h.audit = append(h.audit, auditRec{action: action, target: target, result: result, details: details})
		})
	s.Poll = time.Millisecond
	h.agent.canned = map[string]json.RawMessage{}
	return s, h
}

func TestSetupOptionsAreValidated(t *testing.T) {
	if _, err := ValidateOptions("nope", SetupOptions{}); err == nil {
		t.Error("an unknown profile")
	}
	o, err := ValidateOptions("web", SetupOptions{PHP: []string{"8.4", "8.2"}, Hostname: " Server.Example.COM "})
	if err != nil || o.Hostname != "server.example.com" || strings.Join(o.PHP, ",") != "8.2,8.4" {
		t.Fatalf("%+v %v", o, err)
	}
	for _, bad := range []SetupOptions{{PHP: []string{"abc"}}, {PHP: []string{"8.3", "8.3"}}, {PHP: []string{}}, {Hostname: "no_dots"}} {
		if _, err := ValidateOptions("web", bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if o, _ := ValidateOptions("minimal", SetupOptions{}); strings.Join(o.PHP, ",") != "8.3,8.4" {
		t.Errorf("default PHP %v", o.PHP)
	}
}

func TestSetupStepsPerProfile(t *testing.T) {
	has := func(profile, step string) bool { return hasStr(StepsFor(profile), step) }
	if has("minimal", "mail") || has("minimal", "ftp") || has("minimal", "protection") || !has("minimal", "firewall") {
		t.Error("minimal")
	}
	if !has("web", "ftp") || has("web", "mail") || has("web", "dns") {
		t.Error("web")
	}
	if !has("full", "mail") || !has("full", "dns") {
		t.Error("full")
	}
	if StepsFor("full")[0] != "packages" || StepsFor("full")[len(StepsFor("full"))-1] != "profile" {
		t.Error("order")
	}
}

func TestSetupPlansBackupsAndProfileAreCreatedOnceAndKept(t *testing.T) {
	s, h := setupEnv(t)
	ctx := context.Background()
	o, _ := ValidateOptions("web", SetupOptions{})
	for _, step := range []string{"plans", "backups", "profile"} {
		st, detail := s.call(ctx, "web", o, step)
		if st != "ok" {
			t.Fatalf("%s: %s %s", step, st, detail)
		}
	}
	count := func(q string) (n int) { _ = h.db.QueryRow(q).Scan(&n); return }
	if n := count(`SELECT COUNT(*) FROM feature_lists WHERE name IN ('Hosting','Hosting with SSH','Email only')`); n != 3 {
		t.Errorf("feature lists %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM plans WHERE owner_id IS NULL AND name IN ('Starter','Business','Pro','Email only')`); n != 4 {
		t.Errorf("plans %d", n)
	}
	var disk, cpu, list int
	_ = h.db.QueryRow(`SELECT disk_quota_mb, cpu_percent, feature_list_id FROM plans WHERE name = 'Business'`).Scan(&disk, &cpu, &list)
	if disk != 20480 || cpu != 200 || list == 0 {
		t.Errorf("Business: disk %d cpu %d list %d", disk, cpu, list)
	}
	if n := count(`SELECT COUNT(*) FROM backup_schedules WHERE all_sites = 1`); n != 1 {
		t.Errorf("all-websites schedules %d", n)
	}
	if n := count(`SELECT COUNT(*) FROM backup_schedules WHERE kind = 'server' AND all_sites = 0`); n != 1 {
		t.Errorf("server schedules %d", n)
	}
	if v, _ := s.cfg.String(ctx, "components_expected", ""); v != "web,php,database,firewall,fail2ban,certbot,redis,memcached,ftp" {
		t.Errorf("components_expected %q", v)
	}
	// Again: what exists is left alone.
	for _, step := range []string{"plans", "backups"} {
		if st, _ := s.call(ctx, "web", o, step); st != "skip" {
			t.Errorf("%s again: %s", step, st)
		}
	}
	if n := count(`SELECT COUNT(*) FROM plans WHERE name = 'Starter'`); n != 1 {
		t.Errorf("Starter duplicated: %d", n)
	}
}

func TestSetupPackagesFollowsItsJobInTheWizard(t *testing.T) {
	s, h := setupEnv(t)
	ctx := context.Background()
	h.agent.canned["components.status"] = json.RawMessage(`[{"id":"web","name":"Nginx","installed":false,"steps":[{"id":"installed","status":"todo","items":["nginx"]}]}]`)
	h.agent.canned["packages.install"] = json.RawMessage(`{"job_id":"aaaaaaaaaaaaaaaa"}`)
	res, err := s.RunStep(ctx, "minimal", SetupOptions{}, "packages", "", 0)
	if err != nil || res.Status != "running" || res.JobID != "aaaaaaaaaaaaaaaa" || !strings.Contains(res.Detail, "Install nginx") {
		t.Fatalf("first call: %+v %v", res, err)
	}
	// The job is still going: the step stays running.
	h.agent.canned["jobs.get"] = json.RawMessage(`{"status":"running"}`)
	if res, _ = s.RunStep(ctx, "minimal", SetupOptions{}, "packages", "aaaaaaaaaaaaaaaa", 1); res.Status != "running" {
		t.Fatalf("still running: %+v", res)
	}
	// It failed: reported.
	h.agent.canned["jobs.get"] = json.RawMessage(`{"status":"failed","error":"dpkg broke"}`)
	if res, _ = s.RunStep(ctx, "minimal", SetupOptions{}, "packages", "aaaaaaaaaaaaaaaa", 1); res.Status != "fail" || !strings.Contains(res.Detail, "dpkg broke") {
		t.Fatalf("failed job: %+v", res)
	}
	// It succeeded but the package is still missing: not tried again, reported.
	h.agent.canned["jobs.get"] = json.RawMessage(`{"status":"succeeded"}`)
	if res, _ = s.RunStep(ctx, "minimal", SetupOptions{}, "packages", "aaaaaaaaaaaaaaaa", 1); res.Status != "fail" || !strings.Contains(res.Detail, "Still not installed") {
		t.Fatalf("still missing: %+v", res)
	}
	// It succeeded and nothing is left: done.
	h.agent.canned["components.status"] = json.RawMessage(`[{"id":"web","name":"Nginx","installed":true,"steps":[]}]`)
	if res, _ = s.RunStep(ctx, "minimal", SetupOptions{}, "packages", "aaaaaaaaaaaaaaaa", 1); res.Status != "ok" {
		t.Fatalf("after the job: %+v", res)
	}
	if _, err := s.RunStep(ctx, "minimal", SetupOptions{}, "packages", "not-a-job", 1); err == nil {
		t.Error("a malformed job id was accepted")
	}
	if _, err := s.RunStep(ctx, "minimal", SetupOptions{}, "mail", "", 0); err == nil {
		t.Error("a step of another profile was run")
	}
	// A job that never ends does not loop forever.
	h.agent.canned["components.status"] = json.RawMessage(`[{"id":"web","name":"Nginx","installed":false,"steps":[{"id":"installed","status":"todo","items":["nginx"]}]}]`)
	h.agent.canned["jobs.get"] = json.RawMessage(`{"status":"succeeded"}`)
	s.attempted = nil
	if res, _ = s.RunStep(ctx, "minimal", SetupOptions{}, "packages", "", MaxSetupRounds); res.Status != "fail" || !strings.Contains(res.Detail, "Still not done after 3 jobs") {
		t.Fatalf("round limit: %+v", res)
	}
}

func TestSetupFirewallChangeIsUndoneWhenACriticalPortWouldClose(t *testing.T) {
	s, h := setupEnv(t)
	ctx := context.Background()
	h.agent.canned["firewall.status"] = json.RawMessage(`{"installed":true,"active":true,"required":[{"service":"SSH","port":22,"proto":"tcp","critical":true,"open":false}]}`)
	h.agent.canned["firewall.set_enabled"] = json.RawMessage(`{"changed":true,"pending":{"id":"p1"}}`)
	o, _ := ValidateOptions("minimal", SetupOptions{})
	// Active on the second read only: first status says off.
	h.agent.canned["firewall.status"] = json.RawMessage(`{"installed":true,"active":false}`)
	err := s.keep(ctx, map[string]any{"changed": true, "pending": map[string]any{"id": "p1"}}, "Turning on the firewall")
	if err != nil {
		t.Fatalf("firewall off: nothing is blocked, the change is kept: %v", err)
	}
	h.agent.canned["firewall.status"] = json.RawMessage(`{"installed":true,"active":true,"required":[{"service":"SSH","port":22,"proto":"tcp","critical":true,"open":false}]}`)
	err = s.keep(ctx, map[string]any{"changed": true, "pending": map[string]any{"id": "p2"}}, "Opening the ports")
	if err == nil || !strings.Contains(err.Error(), "undone") || !strings.Contains(err.Error(), "SSH 22/tcp") {
		t.Fatalf("a closed SSH port must undo the change: %v", err)
	}
	if h.agent.callCount("security.undo") != 1 || h.agent.callCount("security.confirm") != 1 {
		t.Errorf("undo %d confirm %d", h.agent.callCount("security.undo"), h.agent.callCount("security.confirm"))
	}
	_ = o
}
