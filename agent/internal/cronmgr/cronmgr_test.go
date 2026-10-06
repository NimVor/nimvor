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

// Root's lines only start the launchers; the commands, "%" included (cron
// would turn it into a newline in a line), are in the scripts.
func TestRenderKeepsCommandsInScripts(t *testing.T) {
	out, scripts := render(SiteJobs{Domain: "a.test", User: "asite", Root: "/home/whacp/a.test", Jobs: []Job{
		{ID: 1, Schedule: "*/5 * * * *", Command: "php artisan schedule:run", Enabled: true},
		{ID: 2, Schedule: "0 3 * * *", Command: "date +%Y-%m-%d", Enabled: true},
		{ID: 3, Schedule: "@daily", Command: "echo off", Enabled: false},
	}}, "")
	jobs := cron.Parse([]byte(out), "/etc/cron.d/x", "")
	if len(jobs) != 2 || jobs[0].User != "root" || jobs[0].Schedule != "*/5 * * * *" || jobs[0].Command != "/bin/sh /etc/whacp-cron/whacp-site-a_test/1.run" || strings.Contains(out, "%") {
		t.Fatalf("parsed = %+v\n%s", jobs, out)
	}
	if !strings.Contains(scripts["/etc/whacp-cron/whacp-site-a_test/2.sh"], "( date +%Y-%m-%d )") || !strings.Contains(scripts["/etc/whacp-cron/whacp-site-a_test/1.sh"], ">> /home/whacp/a.test/logs/cron-1.log 2>&1") {
		t.Errorf("scripts: %v", scripts)
	}
	if _, ok := scripts["/etc/whacp-cron/whacp-site-a_test/3.run"]; !ok {
		t.Error("a disabled job keeps its launcher (enabling it changes only the line)")
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
	if !strings.Contains(h.Read("etc/cron.d/whacp-site-a_test"), "*/5 * * * * root /bin/sh /etc/whacp-cron/whacp-site-a_test/1.run\n") {
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

// Runs are framed in the log (start, end with exit code): the job's
// script is run by a real shell.
func TestRenderedJobFramesRunsWithExitCode(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)
	out, scripts := render(SiteJobs{Domain: "a.test", User: "asite", Root: dir, Jobs: []Job{{ID: 7, Schedule: "* * * * *", Command: "echo hello 100%; exit 3", Enabled: true}}}, "")
	if !strings.Contains(out, "MAILTO=\"\"\n* * * * * root /bin/sh /etc/whacp-cron/whacp-site-a_test/7.run\n") {
		t.Errorf("job without email must not be mailed:\n%s", out)
	}
	if err := exec.Command("sh", "-c", scripts["/etc/whacp-cron/whacp-site-a_test/7.sh"]).Run(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "logs/cron-7.log"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], RunMarker+" start 20") || lines[1] != "hello 100%" || !strings.HasPrefix(lines[2], RunMarker+" end 20") || !strings.HasSuffix(lines[2], " exit=3") {
		t.Fatalf("log:\n%s", b)
	}
}

// Every job runs as the account in the sandbox of commands run as the
// account (not only those of accounts with resource limits), and only a
// job that mails its output passes it to cron.
func TestEmailedJobsUseMailtoAndTee(t *testing.T) {
	out, scripts := render(SiteJobs{Domain: "a.test", User: "asite", Root: "/home/whacp/a.test", Jobs: []Job{
		{ID: 1, Schedule: "@daily", Command: "php x.php", Enabled: true, Email: "owner@example.test"},
		{ID: 2, Schedule: "@daily", Command: "php y.php", Enabled: true},
	}}, "")
	if !strings.Contains(out, "MAILTO=\"owner@example.test\"\n@daily root /bin/sh /etc/whacp-cron/whacp-site-a_test/1.run\n") || !strings.Contains(scripts["/etc/whacp-cron/whacp-site-a_test/1.sh"], "2>&1 | tee -a /home/whacp/a.test/logs/cron-1.log\n") {
		t.Errorf("mailed job:\n%s", out)
	}
	if !strings.Contains(out, "MAILTO=\"\"\n@daily root /bin/sh /etc/whacp-cron/whacp-site-a_test/2.run\n") {
		t.Errorf("the next job must not inherit MAILTO:\n%s", out)
	}
	if strings.Contains(out, "php ") {
		t.Errorf("command in root's line:\n%s", out)
	}
	l1, l2 := scripts["/etc/whacp-cron/whacp-site-a_test/1.run"], scripts["/etc/whacp-cron/whacp-site-a_test/2.run"]
	if !strings.Contains(l1, " --pipe ") || strings.Contains(l2, "--pipe") || strings.Contains(l2, "--slice") {
		t.Errorf("launchers:\n%s\n%s", l1, l2)
	}
	for _, p := range []string{"--uid=asite --gid=asite", "--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet= ", "--property=ProtectSystem=full", "-- /bin/sh /etc/whacp-cron/whacp-site-a_test/2.sh\n"} {
		if !strings.Contains(l2, p) {
			t.Errorf("launcher lacks %q:\n%s", p, l2)
		}
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

func TestLimitedAccountJobsRunInSlice(t *testing.T) {
	s := SiteJobs{Domain: "shop.test", User: "shop", Root: "/home/whacp/shop.test", Jobs: []Job{{ID: 4, Schedule: "*/5 * * * *", Command: "php artisan schedule:run 'x%y'", Enabled: true}}}
	out, scripts := render(s, "whacp-acct2.slice")
	if !strings.Contains(out, "*/5 * * * * root /bin/sh /etc/whacp-cron/whacp-site-shop_test/4.run\n") || strings.Contains(out, "artisan") {
		t.Fatalf("cron file (the command must not be in root's line):\n%s", out)
	}
	launcher := scripts["/etc/whacp-cron/whacp-site-shop_test/4.run"]
	if !strings.HasPrefix(strings.SplitN(launcher, "\n", 2)[1], "exec /usr/bin/systemd-run --quiet --collect --wait --uid=shop --gid=shop --slice=whacp-acct2.slice --working-directory=/home/whacp/shop.test --property=NoNewPrivileges=yes") {
		t.Fatalf("launcher:\n%s", launcher)
	}
	body := scripts["/etc/whacp-cron/whacp-site-shop_test/4.sh"]
	if !strings.Contains(body, "( php artisan schedule:run 'x%y' )") || !strings.Contains(body, ">> /home/whacp/shop.test/logs/cron-4.log 2>&1") {
		t.Fatalf("script:\n%s", body)
	}
}

// Debian's cron ignores lines over 1000 characters: the longest website
// name, account name and schedule still fit.
func TestCronLinesStayShort(t *testing.T) {
	d := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 57) + ".com"
	sched := "0,5,10,15,20,25,30,35,40,45,50,55 0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21,22,23 1,2,3,4,5,6,7,8,9,10 1,2,3,4,5,6,7,8,9,10,11,12 0,1,2,3,4,5,6"
	out, _ := render(SiteJobs{Domain: d, User: strings.Repeat("u", 31), Root: "/home/whacp/" + d, Jobs: []Job{{ID: 999999999, Schedule: sched, Command: "x", Email: "a@b.test", Enabled: false}}}, "whacp-acct999999999.slice")
	for _, l := range strings.Split(out, "\n") {
		if len(l) > 999 {
			t.Errorf("%d characters: %s", len(l), l)
		}
	}
}

// The launcher is root's alone; the script is readable by the account
// (it runs it) and neither is writable by it.
func TestApplyWritesLauncherForRootOnly(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("cron.apply")
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"domain":"a.test","user":"example","root":"/home/whacp/a.test","jobs":[{"id":1,"schedule":"@daily","command":"id","enabled":true}]}`)); err != nil {
		t.Fatal(err)
	}
	for f, mode := range map[string]os.FileMode{"etc/whacp-cron/whacp-site-a_test/1.run": 0o700, "etc/whacp-cron/whacp-site-a_test/1.sh": 0o640} {
		st, err := os.Stat(filepath.Join(h.Base, f))
		if err != nil || st.Mode().Perm() != mode {
			t.Errorf("%s: %v %v", f, st, err)
		}
	}
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"domain":"a.test","user":"example","root":"/home/whacp/a.test","jobs":[]}`)); err != nil {
		t.Fatal(err)
	}
	if h.Exists("etc/whacp-cron/whacp-site-a_test/1.run") || h.Exists("etc/whacp-cron/whacp-site-a_test/1.sh") {
		t.Error("launcher or script left behind")
	}
}
