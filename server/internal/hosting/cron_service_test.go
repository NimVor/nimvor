package hosting

import (
	"context"
	"testing"
)

func seedCronJobs(t *testing.T, h *accountHarness, websiteID int64) {
	t.Helper()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := h.db.Exec(q, a...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	exec(`INSERT INTO cron_jobs (server_id, website_id, schedule, command, description, email, enabled, created_at, updated_at)
		VALUES (1, ?, '0 * * * *', 'php backup.php', 'Nightly backup', 'ok@example.com', 1, '', '')`, websiteID)
	exec(`INSERT INTO cron_jobs (server_id, website_id, schedule, command, description, email, enabled, created_at, updated_at)
		VALUES (1, ?, '5 * * * *', 'php badmail.php', 'Bad recipient', 'not an address', 1, '', '')`, websiteID)
	exec(`INSERT INTO cron_jobs (server_id, website_id, schedule, command, description, email, enabled, created_at, updated_at)
		VALUES (1, ?, '10 * * * *', 'php off.php', 'Disabled', 'off@example.com', 0, '', '')`, websiteID)
}

func findFull(t *testing.T, h *accountHarness, id int64) *Website {
	t.Helper()
	site, err := h.sites.FindFull(context.Background(), 1, id, nil)
	if err != nil || site == nil {
		t.Fatalf("find site %d: %v", id, err)
	}
	return site
}

func TestCronServiceApply(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	seedCronJobs(t, h, 10)
	site := findFull(t, h, 10)

	if err := h.cron.Apply(ctx, site); err != nil {
		t.Fatal(err)
	}
	p := h.agent.lastParams("cron.apply")
	if p["domain"] != "cust.example" || p["user"] != "u_cust" || p["root"] != "/home/whacp/cust.example" {
		t.Fatalf("params = %v", p)
	}
	jobs, ok := p["jobs"].([]agentCronJob)
	if !ok || len(jobs) != 3 {
		t.Fatalf("jobs = %v", p["jobs"])
	}
	if jobs[0].Email != "ok@example.com" || !jobs[0].Enabled || jobs[0].Schedule != "0 * * * *" {
		t.Fatalf("job 0 = %+v", jobs[0])
	}
	// The bad recipient is dropped, the job is kept.
	if jobs[1].Email != "" || !jobs[1].Enabled {
		t.Fatalf("job 1 = %+v", jobs[1])
	}
	if jobs[2].Enabled {
		t.Fatalf("job 2 = %+v, want disabled kept disabled", jobs[2])
	}
}

func TestCronServiceApplySuspendedAccount(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	seedCronJobs(t, h, 10)
	// Suspend the website's owner.
	if _, err := h.db.Exec(`UPDATE users SET suspended_at = '2026-01-01T00:00:00Z' WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	site := findFull(t, h, 10)
	if !site.AccountSuspended {
		t.Fatal("site should be seen as suspended")
	}
	if err := h.cron.Apply(ctx, site); err != nil {
		t.Fatal(err)
	}
	jobs := h.agent.lastParams("cron.apply")["jobs"].([]agentCronJob)
	for i, j := range jobs {
		if j.Enabled {
			t.Fatalf("job %d enabled for a suspended account: %+v", i, j)
		}
	}
}

func TestCronServiceRemoveFromServer(t *testing.T) {
	h := newAccountHarness(t)
	ctx := context.Background()
	site := findFull(t, h, 10)
	if err := h.cron.RemoveFromServer(ctx, site); err != nil {
		t.Fatal(err)
	}
	p := h.agent.lastParams("cron.apply")
	jobs, ok := p["jobs"].([]agentCronJob)
	if !ok || len(jobs) != 0 {
		t.Fatalf("jobs = %v, want empty", p["jobs"])
	}
}

func TestMailable(t *testing.T) {
	for _, ok := range []string{
		"a@b.co",
		"first.last+x@sub.domain.example",
		"1234@ex-ample.com",
	} {
		if !Mailable(ok) {
			t.Fatalf("%q should be mailable", ok)
		}
	}
	for _, bad := range []string{
		"",
		"no-at-sign",
		"@b.co",
		"a@",
		"a b@b.co",
		"a@b", // no dot in the domain
	} {
		if Mailable(bad) {
			t.Fatalf("%q should not be mailable", bad)
		}
	}
}
