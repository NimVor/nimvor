package cronmgr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/cron"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestRenderEscapesPercentAndRoundTrips(t *testing.T) {
	out := Render(SiteJobs{Domain: "a.test", User: "asite", Root: "/home/whacp/a.test", Jobs: []Job{
		{ID: 1, Schedule: "*/5 * * * *", Command: "php artisan schedule:run", Enabled: true},
		{ID: 2, Schedule: "0 3 * * *", Command: "date +%Y-%m-%d", Enabled: true},
		{ID: 3, Schedule: "@daily", Command: "echo off", Enabled: false},
	}})
	if !strings.Contains(out, `date +\%Y-\%m-\%d`) {
		t.Errorf("percent not escaped:\n%s", out)
	}
	jobs := cron.Parse([]byte(out), "/etc/cron.d/x", "")
	if len(jobs) != 2 || jobs[0].User != "asite" || jobs[0].Schedule != "*/5 * * * *" {
		t.Fatalf("parsed = %+v", jobs)
	}
	if !strings.Contains(jobs[0].Command, ">> /home/whacp/a.test/logs/cron-1.log 2>&1") {
		t.Errorf("output redirection missing: %s", jobs[0].Command)
	}
}

func TestApplyValidatesAndWritesOwnedFile(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("cron.apply")
	good := `{"domain":"a.test","user":"example","jobs":[{"id":1,"schedule":"*/5 * * * *","command":"php /x.php","enabled":true}]}`
	if _, err := a.Handler(context.Background(), json.RawMessage(good)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Read("etc/cron.d/whacp-site-a_test"), "*/5 * * * * example cd /home/whacp/a.test") {
		t.Fatalf("file:\n%s", h.Read("etc/cron.d/whacp-site-a_test"))
	}
	for _, bad := range []string{
		`{"domain":"a.test","user":"root","jobs":[{"id":1,"schedule":"* * * * *","command":"id","enabled":true}]}`,
		`{"domain":"a.test","user":"www-data","jobs":[]}`,
		`{"domain":"a.test","user":"example","jobs":[{"id":1,"schedule":"* * * *","command":"id","enabled":true}]}`,
		`{"domain":"a.test","user":"example","jobs":[{"id":1,"schedule":"* * * * *","command":"id\n* * * * * root id","enabled":true}]}`,
		`{"domain":"a.test","user":"example","root":"/etc","jobs":[]}`,
	} {
		if _, err := a.Handler(context.Background(), json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	a.Handler(context.Background(), json.RawMessage(`{"domain":"a.test","user":"example","jobs":[]}`))
	if h.Exists("etc/cron.d/whacp-site-a_test") {
		t.Error("empty schedule should remove the file")
	}
}

func TestRunNowUsesUnprivilegedShell(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("cron.run")
	res, err := a.Handler(context.Background(), json.RawMessage(`{"domain":"a.test","user":"example","id":3,"command":"echo hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	j, _ := h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	if j.Status != "succeeded" {
		t.Fatalf("job %+v", j)
	}
	o := h.Runner.Opts[len(h.Runner.Opts)-1]
	if o.As == nil || o.As.UID < 1000 {
		t.Fatalf("command not run as the site user: %+v", o.As)
	}
}
