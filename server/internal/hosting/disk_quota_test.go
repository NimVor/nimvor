package hosting

import (
	"context"
	"encoding/json"
	"testing"
)

func TestSplitQuotaSharesWhatIsLeftAndCapsByReseller(t *testing.T) {
	owners := map[string]int64{"a1": 1, "a2": 1, "c1": 2, "adm": 0}
	used := map[string]int64{"a1": 100, "a2": 300, "c1": 50, "adm": 999}
	limits := map[int64]int64{1: 1000, 3: 400} // account 1: 1000 KiB; reseller 3: 400
	parents := map[int64]int64{2: 3}           // c1's owner 2 is a customer of reseller 3
	got := SplitQuota(owners, used, limits, parents)
	// Account 1 has 1000-400 = 600 left over two users: each may grow by 300.
	if got["a1"] != 400 || got["a2"] != 600 {
		t.Errorf("shared account: %v", got)
	}
	// Account 2 has no limit of its own; the reseller's 400-50 = 350 left over one user.
	if got["c1"] != 400 {
		t.Errorf("reseller part: %v", got)
	}
	if got["adm"] != 0 {
		t.Errorf("an unowned user is not limited: %v", got)
	}
	// A full account still gets its use as the limit, never 0 (= unlimited).
	if full := SplitQuota(map[string]int64{"u": 1}, map[string]int64{"u": 0}, map[int64]int64{1: 0 + 1}, nil); full["u"] < 1 {
		t.Errorf("full: %v", full)
	}
	over := SplitQuota(map[string]int64{"u": 1}, map[string]int64{"u": 5000}, map[int64]int64{1: 1000}, nil)
	if over["u"] != 5000 {
		t.Errorf("over the limit keeps its use as the limit: %v", over)
	}
}

func TestDiskQuotaReconcileSetsLimitsFromPlans(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	// solo (user 4) has plan Cust2: give it a 10 MB disk quota; cust (3) none.
	if _, err := db.Exec(`UPDATE plans SET disk_quota_mb = 10 WHERE id = 12`); err != nil {
		t.Fatal(err)
	}
	h.agent.canned = map[string]json.RawMessage{
		"quota.status": json.RawMessage(`{"state":"on"}`),
		"quota.report": json.RawMessage(`{"users":{"u_solo":{"used_kb":2048,"hard_kb":0},"u_cust":{"used_kb":100,"hard_kb":0}}}`),
		"quota.set":    json.RawMessage(`{"failed":{}}`),
	}
	svc := NewDiskQuotaService(db, 1, h.agent, NewPlanLimits(db), nil)
	lines, err := svc.Reconcile(context.Background())
	if err != nil || len(lines) != 1 || lines[0] != "u_solo: disk limit set to 10 MB" {
		t.Fatalf("lines %v err %v", lines, err)
	}
	var hard, used int64
	_ = db.QueryRow(`SELECT hard_kb, used_kb FROM disk_quotas WHERE system_user = 'u_solo'`).Scan(&hard, &used)
	if hard != 10240 || used != 2048 {
		t.Errorf("stored hard %d used %d", hard, used)
	}
	var n int
	_ = db.QueryRow(`SELECT COUNT(*) FROM disk_quotas WHERE system_user = 'u_cust'`).Scan(&n)
	if n != 1 {
		t.Error("the unlimited user's use is still recorded")
	}
	// Quotas off: nothing is asked.
	h.agent.canned["quota.status"] = json.RawMessage(`{"state":"off"}`)
	before := h.agent.callCount("quota.set")
	if lines, _ := svc.Reconcile(context.Background()); len(lines) != 0 || h.agent.callCount("quota.set") != before {
		t.Error("reconciled with quotas off")
	}
}

func TestFfmpegAllowedUsersFollowPlansAndSuspension(t *testing.T) {
	db := writeDB(t)
	h := newHarness(t, db)
	svc := NewFfmpegService(db, 1, h.agent, h.svc, nil)
	ctx := context.Background()
	got, _ := svc.AllowedUsers(ctx)
	if len(got) != 0 {
		t.Fatalf("no plan allows FFmpeg yet: %v", got)
	}
	// cust (3) is a customer of reseller 2; solo (4) is standalone.
	_, _ = db.Exec(`UPDATE plans SET ffmpeg = 1 WHERE id IN (11, 12)`)
	got, _ = svc.AllowedUsers(ctx)
	if len(got) != 1 || got[0] != "u_solo" {
		t.Fatalf("solo only (cust's reseller has no plan allowing it): %v", got)
	}
	_, _ = db.Exec(`UPDATE users SET plan_id = 11 WHERE id = 2`)
	_, _ = db.Exec(`UPDATE plans SET ffmpeg = 1 WHERE id = 11`)
	got, _ = svc.AllowedUsers(ctx)
	if len(got) != 2 {
		t.Fatalf("with the reseller's plan allowing it: %v", got)
	}
	// A system user shared with a website whose owner may not use it is left out.
	_, _ = db.Exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (30,1,1,'adm.example','u_solo','/r','/r/p','php','8.3','active','','')`)
	_, _ = db.Exec(`UPDATE websites SET owner_id = NULL WHERE id = 30`)
	got, _ = svc.AllowedUsers(ctx)
	for _, u := range got {
		if u == "u_solo" {
			t.Errorf("a shared account follows the most restricted owner: %v", got)
		}
	}
	// Suspended accounts may not.
	_, _ = db.Exec(`UPDATE websites SET owner_id = 4 WHERE id = 30`)
	_, _ = db.Exec(`UPDATE users SET suspended_at = '2026-01-01T00:00:00Z' WHERE id = 3`)
	got, _ = svc.AllowedUsers(ctx)
	for _, u := range got {
		if u == "u_cust" {
			t.Errorf("a suspended account is allowed: %v", got)
		}
	}
}
