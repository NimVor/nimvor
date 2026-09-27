package cronmgr

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

// Runs are framed in the log (start, end with exit code): the rendered
// line, unescaped as cron does, is run by a real shell.
func TestRenderedJobFramesRunsWithExitCode(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	out := Render(SiteJobs{Domain: "a.test", User: "asite", Root: dir, Jobs: []Job{{ID: 7, Schedule: "* * * * *", Command: "echo hello; exit 3", Enabled: true}}})
	if !strings.Contains(out, "MAILTO=\"\"\n* * * * *") {
		t.Errorf("job without email must not be mailed:\n%s", out)
	}
	jobs := cron.Parse([]byte(out), "/etc/cron.d/x", "")
	cmd := strings.ReplaceAll(jobs[0].Command, `\%`, "%")
	if err := exec.Command("sh", "-c", cmd).Run(); err != nil {
		t.Fatalf("%s: %v", cmd, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "logs/cron-7.log"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], RunMarker+" start 20") || lines[1] != "hello" || !strings.HasPrefix(lines[2], RunMarker+" end 20") || !strings.HasSuffix(lines[2], " exit=3") {
		t.Fatalf("log:\n%s", b)
	}
}

func TestEmailedJobsUseMailtoAndTee(t *testing.T) {
	out := Render(SiteJobs{Domain: "a.test", User: "asite", Root: "/home/whacp/a.test", Jobs: []Job{
		{ID: 1, Schedule: "@daily", Command: "php x.php", Enabled: true, Email: "owner@example.test"},
		{ID: 2, Schedule: "@daily", Command: "php y.php", Enabled: true},
	}})
	if !strings.Contains(out, "MAILTO=\"owner@example.test\"\n@daily asite cd") || !strings.Contains(out, "2>&1 | tee -a /home/whacp/a.test/logs/cron-1.log\n") {
		t.Errorf("mailed job:\n%s", out)
	}
	if !strings.Contains(out, "MAILTO=\"\"\n@daily asite cd /home/whacp/a.test && { echo \"### whacp-run start") {
		t.Errorf("the next job must not inherit MAILTO:\n%s", out)
	}
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("cron.apply")
	for _, bad := range []string{`a@b.test\"\n* * * * * root id`, "a b@c.test", "a@b", "a%b@c.test", `"a"@b.test`, "a@b.test\n"} {
		body, _ := json.Marshal(map[string]any{"domain": "a.test", "user": "example", "jobs": []map[string]any{{"id": 1, "schedule": "@daily", "command": "id", "enabled": true, "email": bad}}})
		if _, err := a.Handler(context.Background(), body); err == nil {
			t.Errorf("accepted email %q", bad)
		}
	}
}

// "Run now" appends to the job's log as root: a symlink placed there by the
// website's user must not make it write anywhere else (e.g. /etc/crontab).
func TestRunNowDoesNotFollowLogSymlinksOutOfTheWebsite(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	site := filepath.Join(h.Base, h.Env.Cfg.SitesRoot, "a.test")
	os.MkdirAll(filepath.Join(site, "logs"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, "etc"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/crontab"), []byte("# system crontab\n"), 0o644)
	os.Symlink("../../../../etc/crontab", filepath.Join(site, "logs/cron-3.log"))
	os.WriteFile(filepath.Join(site, "logs/cron-4.log"), nil, 0o644)
	a, _ := r.Lookup("cron.run")
	for _, id := range []string{"3", "4"} {
		res, err := a.Handler(context.Background(), json.RawMessage(`{"domain":"a.test","user":"example","id":`+id+`,"command":"echo '* * * * * root id'"}`))
		if err != nil {
			t.Fatal(err)
		}
		h.Env.Jobs.Wait(context.Background(), res.(map[string]any)["job_id"].(string))
	}
	if got := h.Read("etc/crontab"); got != "# system crontab\n" {
		t.Fatalf("/etc/crontab was written through the symlink:\n%s", got)
	}
	b, _ := os.ReadFile(filepath.Join(site, "logs/cron-4.log"))
	if !strings.Contains(string(b), RunMarker+" start ") || !strings.Contains(string(b), " manual\n") || !strings.Contains(string(b), RunMarker+" end ") {
		t.Errorf("regular log not framed:\n%s", b)
	}

	// cron.log reads the log, confined to the website as well.
	l, _ := r.Lookup("cron.log")
	res, err := l.Handler(context.Background(), json.RawMessage(`{"domain":"a.test","id":4}`))
	if err != nil || !strings.Contains(res.(map[string]any)["content"].(string), RunMarker) {
		t.Fatalf("cron.log: %v %v", res, err)
	}
	if res, err := l.Handler(context.Background(), json.RawMessage(`{"domain":"a.test","id":3}`)); err == nil && strings.Contains(res.(map[string]any)["content"].(string), "system crontab") {
		t.Fatal("cron.log followed the symlink out of the website")
	}
}
