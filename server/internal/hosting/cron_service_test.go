package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
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

// cronTestSite loads website 10 of writeDB's seed (owner: the plan-11
// customer) without tenant scoping.
func cronTestSite(t *testing.T, db *sql.DB) *Website {
	t.Helper()
	site, err := NewWebsiteRepository(db).FindFull(context.Background(), 1, 10, nil)
	if err != nil || site == nil {
		t.Fatalf("site 10: %v", err)
	}
	return site
}

func TestCronValidSchedule(t *testing.T) {
	cases := map[string]bool{
		"@hourly":          true,
		"@daily":           true,
		"@reboot":          true,
		"*/5 * * * *":      true,
		"30 2 * * 1":       true,
		"0 0 1 1 0":        true,
		"5-10/2 * * * *":   true,
		"1,15,30 * * * *":  true,
		"* * * * 7":        true,
		"* * * * 8":        false,
		"60 * * * *":       false,
		"5 24 * * *":       false,
		"0 0 0 * *":        false,
		"0 0 1 13 *":       false,
		"a * * * *":        false,
		"* * * *":          false,
		"* * * * * *":      false,
		"*/15 0-6 * * mon": false,
	}
	for s, want := range cases {
		if got := ValidSchedule(s); got != want {
			t.Errorf("ValidSchedule(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestCronParseRuns(t *testing.T) {
	log := "loose line one\n" +
		"### whacp-run start 2026-10-21T03:00:00Z\n" +
		"out a\n" +
		"out b\n" +
		"### whacp-run end 2026-10-21T03:00:02Z exit=0\n" +
		"### whacp-run start 2026-10-22T03:00:00Z\n" +
		"### whacp-run end 2026-10-22T03:00:01Z exit=3\n" +
		"### whacp-run start 2026-10-22T08:15:30Z manual\n" +
		"manual output"
	runs := ParseRuns(log, 200)
	if len(runs) != 4 {
		t.Fatalf("runs = %d, want 4: %+v", len(runs), runs)
	}
	// Newest first.
	if !runs[0].Manual || *runs[0].Start != "2026-10-22T08:15:30Z" || runs[0].End != nil || runs[0].Exit != nil || runs[0].Output != "manual output" {
		t.Errorf("newest run = %+v", runs[0])
	}
	if !runs[1].Manual || runs[1].Start == nil || runs[1].End == nil || runs[1].Exit == nil || *runs[1].Exit != 3 || runs[1].Output != "" {
		t.Errorf("failed run = %+v", runs[1])
	}
	if runs[2].Manual || runs[2].Exit == nil || *runs[2].Exit != 0 || runs[2].Output != "out a\nout b" {
		t.Errorf("successful run = %+v", runs[2])
	}
	if runs[3].Start != nil || runs[3].Output != "loose line one" {
		t.Errorf("loose run = %+v", runs[3])
	}
	// The cap keeps the newest runs.
	if all := ParseRuns(log, 2); len(all) != 2 || !all[0].Manual {
		t.Errorf("capped runs = %+v", all)
	}
}

// cronHarness builds a CronService on writeDB's seed with the recording
// fake agent.
func cronHarness(t *testing.T) (*CronService, *fakeAgent, *sql.DB, *[]auditRec) {
	t.Helper()
	db := writeDB(t)
	fa := &fakeAgent{canned: map[string]json.RawMessage{
		"cron.apply": json.RawMessage(`{}`),
		"cron.run":   json.RawMessage(`{"job_id":"0123456789c0a001"}`),
	}}
	recs := []auditRec{}
	audit := func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		recs = append(recs, auditRec{action: action, target: target, result: result, details: details})
	}
	return NewCronService(db, fa, NewPlanLimits(db), audit), fa, db, &recs
}

func TestCronRun(t *testing.T) {
	svc, fa, db, recs := cronHarness(t)
	ctx := context.Background()
	site := cronTestSite(t, db)
	if _, err := svc.jobs.Insert(ctx, site, "0 3 * * *", "echo run-me", "", "", "2026-10-22T00:00:00Z", true); err != nil {
		t.Fatal(err)
	}
	job := &CronJob{ID: 1, Command: "echo run-me"}
	jobID, err := svc.Run(ctx, site, job)
	if err != nil {
		t.Fatal(err)
	}
	if jobID != "0123456789c0a001" {
		t.Errorf("job id = %q", jobID)
	}
	p := fa.lastParams("cron.run")
	if p["domain"] != "cust.example" || p["user"] != "u_cust" || p["id"] != 1 || p["command"] != "echo run-me" {
		t.Errorf("cron.run params = %v", p)
	}
	var last string
	if err := db.QueryRow(`SELECT last_run_job FROM cron_jobs WHERE id=1`).Scan(&last); err != nil || last != "0123456789c0a001" {
		t.Errorf("last_run_job = %q: %v", last, err)
	}
	found := false
	for _, r := range *recs {
		if r.action == "cron.run" && r.result == "success" {
			found = true
		}
	}
	if !found {
		t.Errorf("cron.run audit missing: %+v", *recs)
	}
}

func TestCronRunSuspended(t *testing.T) {
	svc, fa, db, _ := cronHarness(t)
	ctx := context.Background()
	if _, err := db.Exec(`UPDATE users SET suspended_at='2026-10-01T00:00:00Z' WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	site := cronTestSite(t, db)
	_, err := svc.Run(ctx, site, &CronJob{ID: 1, Command: "echo no"})
	var ue *UserError
	if !errors.As(err, &ue) || ue.What != "The account is suspended; its cron jobs do not run." {
		t.Fatalf("err = %v, want the suspension UserError", err)
	}
	if fa.callCount("cron.run") != 0 {
		t.Error("cron.run called for a suspended account")
	}
}

func TestCronSaveRevertsOnApplyFailure(t *testing.T) {
	svc, fa, db, recs := cronHarness(t)
	ctx := context.Background()
	site := cronTestSite(t, db)
	if _, err := svc.jobs.Insert(ctx, site, "0 3 * * *", "echo keep", "", "", "2026-10-22T00:00:00Z", true); err != nil {
		t.Fatal(err)
	}
	fa.failActions = map[string]error{"cron.apply": errors.New("boom")}
	err := svc.Save(ctx, site, 0, map[string]string{
		"schedule": "0 * * * *", "command": "echo doomed", "enabled": "1",
	}, "c@x")
	if err == nil {
		t.Fatal("save with a failing apply must error")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cron_jobs WHERE website_id=10`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("jobs after failed save = %d, want 1 (reverted)", n)
	}
	var command string
	if err := db.QueryRow(`SELECT command FROM cron_jobs WHERE website_id=10`).Scan(&command); err != nil || command != "echo keep" {
		t.Errorf("surviving job = %q: %v", command, err)
	}
	failed := false
	for _, r := range *recs {
		if r.action == "cron.create" && r.result == "failure" {
			failed = r.details["error"] != ""
		}
	}
	if !failed {
		t.Errorf("cron.create failure audit missing: %+v", *recs)
	}
}

func TestCronValidate(t *testing.T) {
	svc, _, _, _ := cronHarness(t)
	// An invalid schedule + an empty command → both field errors.
	_, err := svc.Validate(map[string]string{"schedule": "custom", "schedule_custom": "99 * * * *", "command": ""})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatal("invalid input must fail validation")
	}
	if ve.Fields["schedule"] == "" || ve.Fields["command"] == "" {
		t.Errorf("field errors = %v", ve.Fields)
	}
	// A 201-rune description is over the limit.
	_, err = svc.Validate(map[string]string{"schedule": "* * * * *", "command": "x", "description": strings.Repeat("a", 201)})
	if !errors.As(err, &ve) || ve.Fields["description"] == "" {
		t.Errorf("long description must be rejected: %v", err)
	}
	// Control characters in the command are rejected.
	_, err = svc.Validate(map[string]string{"schedule": "* * * * *", "command": "echo\necho"})
	if !errors.As(err, &ve) || ve.Fields["command"] == "" {
		t.Errorf("multiline command must be rejected: %v", err)
	}
	// A clean input is normalised (the "custom" sentinel, whitespace, the
	// checkbox flags).
	out, err := svc.Validate(map[string]string{
		"schedule": "custom", "schedule_custom": "  30 2 * * 1  ",
		"command": "  echo hi  ", "description": " d ", "enabled": "1",
	})
	if err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}
	if out.Schedule != "30 2 * * 1" || out.Command != "echo hi" || out.Description != "d" || !out.Enabled || out.EmailOutput {
		t.Errorf("normalised = %+v", out)
	}
}
