package http

// This suite drives the per-website cron write area (CronController + the
// write side of CronService): adding/editing jobs (schedule validation, the
// email-output address, the 200-job cap), pausing/enabling, deleting,
// running now (agent job + tracker), the job's log (parsed runs, the agent
// error path), the apply-failure revert, and the role split. The agent is
// the recording fake (cron.* fixtures in startSitesAgent).

import (
	"database/sql"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

// cronWEnv seeds an administrator (all permissions), a customer (bob,
// cron.view + cron.manage + websites.view, owns website 10 with two jobs)
// and a viewer (carol, read-only, owns website 20).
func cronWEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO roles (id,name,label,scope,created_at) VALUES
		(1,'admin','Administrator','all',''),(2,'customer','Customer','own',''),(5,'viewer','Viewer','own','')`)
	exec(`INSERT INTO permissions (id,name,label,grp) VALUES
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'cron.view','cv','cron'),(4,'cron.manage','cm','cron')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(1,4),(2,1),(2,3),(2,4),(5,1),(5,3)`)
	exec(`INSERT INTO plans (id,name,max_websites,max_subdomains,max_addon_domains,max_parked_domains,created_at,updated_at)
		VALUES (1,'Basic',5,3,5,10,'','')`)
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id)
		VALUES (1,'admin','a@x','Admin',?,1,1,'2026-01-01T00:00:00Z','','',NULL),
		(2,'bob','b@x','Bob',?,2,1,'2026-01-01T00:00:00Z','','',1),
		(3,'carol','c@x','Carol',?,5,1,'2026-01-01T00:00:00Z','','',NULL)`,
		loginHash, loginHash, loginHash)
	exec(`INSERT INTO websites (id,server_id,owner_id,domain,system_user,root,document_root,kind,php_version,status,created_at,updated_at)
		VALUES (10,1,2,'bob.example','u_bob','/home/whacp/bob.example','/home/whacp/bob.example/public_html','php','8.3.1','active','',''),
		(20,1,3,'carol.example','u_carol','/home/whacp/carol.example','/home/whacp/carol.example/public_html','php','8.3.1','active','','')`)
	exec(`INSERT INTO domains (server_id,website_id,name,is_primary) VALUES (1,10,'bob.example',1),(1,20,'carol.example',1)`)
	exec(`INSERT INTO cron_jobs (id,server_id,website_id,schedule,command,description,enabled,email,created_at,updated_at) VALUES
		(1,1,10,'0 3 * * *','/usr/bin/php backup.php','Nightly backup',1,'b@x','',''),
		(2,1,10,'*/5 * * * *','/usr/bin/php cache:clear','',0,'','','')`)

	kf := filepath.Join(t.TempDir(), "panel.key")
	var key [32]byte
	if err := os.WriteFile(kf, []byte(base64.StdEncoding.EncodeToString(key[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sa, socket, tokenFile := startSitesAgent(t, false)
	cfg := config.Config{Production: true, KeyFile: kf, AgentSocket: socket, AgentTokenFile: tokenFile}
	s, err := New(cfg, db, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s.Handler(), sa, db
}

// cronWPost fetches the cron page of website 10 for a fresh CSRF token,
// then POSTs the form to path.
func cronWPost(t *testing.T, h http.Handler, path string, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/cron")
	form.Set("_token", token)
	return postForm(h, path, form, cookie)
}

// cronApplyJobs returns the jobs array of the last cron.apply call.
func cronApplyJobs(t *testing.T, sa *sitesAgent) []map[string]any {
	t.Helper()
	sa.mu.Lock()
	defer sa.mu.Unlock()
	for i := len(sa.actions) - 1; i >= 0; i-- {
		if sa.actions[i] == "cron.apply" {
			jobs, _ := sa.params[i]["jobs"].([]any)
			out := make([]map[string]any, 0, len(jobs))
			for _, j := range jobs {
				out = append(out, j.(map[string]any))
			}
			return out
		}
	}
	t.Fatalf("cron.apply was never called (actions: %v)", sa.actions)
	return nil
}

func TestSiteCronPage(t *testing.T) {
	h, _, _ := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/cron", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/10/cron = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	for _, want := range []string{
		"<title>Cron · bob.example · WhaCP</title>",
		"/usr/bin/php backup.php",
		"Nightly backup",
		"Every day at 03:00",
		"Every 5 minutes",
		"Add cron job",
		`name="schedule_custom"`,
		`name="email_output"`,
		"Email the output to <strong>b@x</strong>",
		"/sites/10/cron/1/log",
		"/sites/10/cron/1/run",
		"/sites/10/cron?edit=1",
		"/sites/10/cron/1/toggle",
		"/sites/10/cron/1/delete",
		"Jobs run as <code>u_bob</code> from <code>/home/whacp/bob.example</code>",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestSiteCronPageReadOnly(t *testing.T) {
	h, _, _ := cronWEnv(t)
	carol := loginAsUser(t, h, "carol")
	rr := get(t, h, "/sites/20/cron", carol)
	if rr.Code != http.StatusOK {
		t.Fatalf("/sites/20/cron = %d: %s", rr.Code, rr.Body.String())
	}
	page := rr.Body.String()
	if !strings.Contains(page, "No cron jobs yet") {
		t.Error("viewer page missing the empty state")
	}
	for _, banned := range []string{"Add cron job", `name="command"`, "/cron/", "data-wcp-confirm"} {
		if strings.Contains(page, banned) {
			t.Errorf("read-only page shows %q", banned)
		}
	}
}

func TestSiteCronAdd(t *testing.T) {
	h, sa, db := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := cronWPost(t, h, "/sites/10/cron", bob, url.Values{
		"schedule":        {"custom"},
		"schedule_custom": {"30 2 * * 1"},
		"command":         {"php artisan queue:work"},
		"description":     {"Queue worker"},
		"enabled":         {"1"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("add = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, rr.Result().Cookies()[0], "/sites/10/cron", "Cron job added. It runs as u_bob.")
	var schedule, command, description, email string
	var enabled, id int
	if err := db.QueryRow(`SELECT id, schedule, command, description, email, enabled FROM cron_jobs WHERE website_id=10 ORDER BY id DESC LIMIT 1`).
		Scan(&id, &schedule, &command, &description, &email, &enabled); err != nil {
		t.Fatal(err)
	}
	if schedule != "30 2 * * 1" || command != "php artisan queue:work" || description != "Queue worker" || email != "" || enabled != 1 {
		t.Errorf("row = %q %q %q %q %d", schedule, command, description, email, enabled)
	}
	// The complete schedule was pushed, the new job included.
	jobs := cronApplyJobs(t, sa)
	found := false
	for _, j := range jobs {
		if j["command"] == "php artisan queue:work" && j["schedule"] == "30 2 * * 1" && j["id"] == float64(id) {
			found = true
		}
	}
	if !found {
		t.Errorf("cron.apply payload missing the new job: %v", jobs)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='cron.create' AND result='success'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("cron.create audit = %d: %v", n, err)
	}
}

func TestSiteCronAddPreset(t *testing.T) {
	h, _, db := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := cronWPost(t, h, "/sites/10/cron", bob, url.Values{
		"schedule": {"0 * * * *"},
		"command":  {"true"},
		"enabled":  {"1"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("add = %d: %s", rr.Code, rr.Body.String())
	}
	var schedule string
	if err := db.QueryRow(`SELECT schedule FROM cron_jobs WHERE command='true'`).Scan(&schedule); err != nil {
		t.Fatal(err)
	}
	if schedule != "0 * * * *" {
		t.Errorf("schedule = %q", schedule)
	}
}

func TestSiteCronAddValidation(t *testing.T) {
	h, sa, db := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := cronWPost(t, h, "/sites/10/cron", bob, url.Values{
		"schedule":        {"99 99 * * *"},
		"schedule_custom": {"99 99 * * *"},
		"command":         {""},
		"description":     {"x"},
		"enabled":         {"1"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("rejected form re-render = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"Use five fields (minute hour day month weekday)",
		"Enter the command on a single line",
		`name="description" value="x"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("re-render missing %q", want)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cron_jobs WHERE website_id=10`).Scan(&n); err != nil || n != 2 {
		t.Errorf("job count after rejection = %d, want 2", n)
	}
	for _, a := range sa.called() {
		if a == "cron.apply" {
			t.Errorf("agent apply called on a rejected form: %v", sa.called())
		}
	}
}

func TestSiteCronEdit(t *testing.T) {
	h, sa, db := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	// The ?edit= page pre-fills the form.
	rr := get(t, h, "/sites/10/cron?edit=1", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("edit page = %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, `name="job_id" value="1"`) || !strings.Contains(body, `value="/usr/bin/php backup.php"`) {
		t.Errorf("edit form not pre-filled:\n%s", body)
	}
	token, cookie := siteFormToken(t, h, bob, "/sites/10/cron?edit=1")
	rr = postForm(h, "/sites/10/cron", url.Values{
		"_token":       {token},
		"job_id":       {"1"},
		"schedule":     {"0 3 * * *"},
		"command":      {"/usr/bin/php backup.php --full"},
		"description":  {"Nightly backup (full)"},
		"enabled":      {"1"},
		"email_output": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, rr.Result().Cookies()[0], "/sites/10/cron", "Cron job updated.")
	var command, email string
	if err := db.QueryRow(`SELECT command, email FROM cron_jobs WHERE id=1`).Scan(&command, &email); err != nil {
		t.Fatal(err)
	}
	if command != "/usr/bin/php backup.php --full" {
		t.Errorf("command = %q", command)
	}
	if email != "b@x" {
		t.Errorf("email (kept on update) = %q, want b@x", email)
	}
	jobs := cronApplyJobs(t, sa)
	if len(jobs) != 2 {
		t.Fatalf("cron.apply jobs = %v", jobs)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='cron.update' AND result='success'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("cron.update audit = %d: %v", n, err)
	}
}

func TestSiteCronEmailFlow(t *testing.T) {
	h, _, db := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	// New job with email output: the saving account's address is stored.
	rr := cronWPost(t, h, "/sites/10/cron", bob, url.Values{
		"schedule":     {"0 * * * *"},
		"command":      {"echo mailed"},
		"enabled":      {"1"},
		"email_output": {"1"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("add = %d: %s", rr.Code, rr.Body.String())
	}
	var id int
	var email string
	if err := db.QueryRow(`SELECT id, email FROM cron_jobs WHERE command='echo mailed'`).Scan(&id, &email); err != nil {
		t.Fatal(err)
	}
	if email != "b@x" {
		t.Errorf("new job email = %q, want b@x", email)
	}
	// Turning the switch off clears the recipient.
	token, cookie := siteFormToken(t, h, bob, "/sites/10/cron")
	rr = postForm(h, "/sites/10/cron", url.Values{
		"_token":   {token},
		"job_id":   {fmt.Sprint(id)},
		"schedule": {"0 * * * *"},
		"command":  {"echo mailed"},
		"enabled":  {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("update = %d: %s", rr.Code, rr.Body.String())
	}
	if err := db.QueryRow(`SELECT email FROM cron_jobs WHERE id=?`, id).Scan(&email); err != nil {
		t.Fatal(err)
	}
	if email != "" {
		t.Errorf("email after unchecking = %q, want empty", email)
	}
}

func TestSiteCronToggle(t *testing.T) {
	h, sa, db := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := cronWPost(t, h, "/sites/10/cron/1/toggle", bob, url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("pause = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, rr.Result().Cookies()[0], "/sites/10/cron", "Cron job paused.")
	var enabled int
	if err := db.QueryRow(`SELECT enabled FROM cron_jobs WHERE id=1`).Scan(&enabled); err != nil || enabled != 0 {
		t.Errorf("enabled after pause = %d: %v", enabled, err)
	}
	jobs := cronApplyJobs(t, sa)
	if len(jobs) != 2 || jobs[0]["enabled"] != false {
		t.Errorf("cron.apply payload = %v", jobs)
	}
	rr = cronWPost(t, h, "/sites/10/cron/1/toggle", bob, url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("enable = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, rr.Result().Cookies()[0], "/sites/10/cron", "Cron job enabled.")
	if err := db.QueryRow(`SELECT enabled FROM cron_jobs WHERE id=1`).Scan(&enabled); err != nil || enabled != 1 {
		t.Errorf("enabled after enable = %d: %v", enabled, err)
	}
}

func TestSiteCronDelete(t *testing.T) {
	h, sa, db := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := cronWPost(t, h, "/sites/10/cron/2/delete", bob, url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, rr.Result().Cookies()[0], "/sites/10/cron", "Cron job deleted.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cron_jobs WHERE id=2`).Scan(&n); err != nil || n != 0 {
		t.Errorf("job 2 count = %d, want 0", n)
	}
	jobs := cronApplyJobs(t, sa)
	if len(jobs) != 1 || jobs[0]["id"] != float64(1) {
		t.Errorf("cron.apply payload after delete = %v", jobs)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='cron.delete' AND result='success'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("cron.delete audit = %d: %v", n, err)
	}
}

func TestSiteCronRun(t *testing.T) {
	h, sa, db := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := cronWPost(t, h, "/sites/10/cron/1/run", bob, url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("run = %d: %s", rr.Code, rr.Body.String())
	}
	cookie := rr.Result().Cookies()[0]
	rr = get(t, h, "/sites/10/cron", cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("cron page after run = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "Command started.") || !strings.Contains(body, "0123456789c0a001") {
		t.Errorf("page after run missing the flash or the job banner:\n%s", body)
	}
	p := agentParam(t, sa, "cron.run", "command")
	if p != "/usr/bin/php backup.php" {
		t.Errorf("cron.run command = %v", p)
	}
	if p := agentParam(t, sa, "cron.run", "user"); p != "u_bob" {
		t.Errorf("cron.run user = %v", p)
	}
	var last string
	if err := db.QueryRow(`SELECT last_run_job FROM cron_jobs WHERE id=1`).Scan(&last); err != nil || last != "0123456789c0a001" {
		t.Errorf("last_run_job = %q: %v", last, err)
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM agent_jobs WHERE job_id='0123456789c0a001'`).Scan(&kind); err != nil {
		t.Fatalf("agent_jobs row: %v", err)
	}
	if kind != "Cron: /usr/bin/php backup.php" {
		t.Errorf("tracked kind = %q", kind)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='cron.run' AND result='success'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("cron.run audit = %d: %v", n, err)
	}
}

func TestSiteCronLog(t *testing.T) {
	h, _, _ := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/cron/1/log", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("log = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"logs/cron-1.log",
		"early loose output line",
		"earlier output",
		"2026-10-21 03:00:00 UTC",
		"Success",
		"Failed (exit 1)",
		"Running or stopped",
		"Manual",
		"Scheduled",
		"manual output",
		"first run line",
		`/sites/10/cron/1/run`,
		"All cron jobs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("log page missing %q", want)
		}
	}
	// Newest first: the still-running manual run is the first row.
	if strings.Index(body, "Running or stopped") > strings.Index(body, "Success") {
		t.Error("runs not newest first")
	}
}

func TestSiteCronLogAgentError(t *testing.T) {
	h, sa, _ := cronWEnv(t)
	sa.failActions = map[string]string{"cron.log": "the log could not be opened"}
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/cron/1/log", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("log = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "the log could not be opened") || !strings.Contains(body, "No runs recorded yet") {
		t.Errorf("agent-error log page wrong:\n%s", body)
	}
}

func TestSiteCronLogNotFound(t *testing.T) {
	h, _, _ := cronWEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/cron/99/log", bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("log of a missing job = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, rr.Result().Cookies()[0], "/sites/10/cron", "Cron job not found.")
}

func TestSiteCronActionsForbidden(t *testing.T) {
	h, _, _ := cronWEnv(t)
	carol := loginAsUser(t, h, "carol")
	// The page is visible (cron.view); the write routes are not.
	if rr := get(t, h, "/sites/20/cron", carol); rr.Code != http.StatusOK {
		t.Fatalf("viewer page = %d", rr.Code)
	}
	token, cookie := siteFormToken(t, h, carol, "/websites")
	for _, path := range []string{"/sites/20/cron", "/sites/20/cron/1/toggle", "/sites/20/cron/1/run", "/sites/20/cron/1/delete"} {
		rr := postForm(h, path, url.Values{"_token": {token}}, cookie)
		if rr.Code != http.StatusForbidden {
			t.Errorf("POST %s = %d, want 403", path, rr.Code)
		}
	}
}

func TestSiteCronForeignSite(t *testing.T) {
	h, _, _ := cronWEnv(t)
	carol := loginAsUser(t, h, "carol")
	rr := get(t, h, "/sites/10/cron", carol)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("viewer on a foreign site = %d, want 404", rr.Code)
	}
	if strings.Contains(rr.Body.String(), "backup.php") {
		t.Fatal("TENANT LEAK: viewer saw another site's cron job")
	}
	bob := loginAsUser(t, h, "bob")
	if rr := get(t, h, "/sites/20/cron", bob); rr.Code != http.StatusNotFound {
		t.Errorf("owner on a foreign site = %d, want 404", rr.Code)
	}
}

func TestSiteCronApplyFailsReverts(t *testing.T) {
	h, sa, db := cronWEnv(t)
	sa.failActions = map[string]string{"cron.apply": "the agent refused"}
	bob := loginAsUser(t, h, "bob")
	rr := cronWPost(t, h, "/sites/10/cron", bob, url.Values{
		"schedule": {"0 * * * *"},
		"command":  {"echo doomed"},
		"enabled":  {"1"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("add = %d: %s", rr.Code, rr.Body.String())
	}
	flashAt(t, h, rr.Result().Cookies()[0], "/sites/10/cron", "the agent refused")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cron_jobs WHERE website_id=10`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("job count after failed apply = %d, want 2 (reverted)", n)
	}
	var command string
	err := db.QueryRow(`SELECT command FROM cron_jobs WHERE command='echo doomed'`).Scan(&command)
	if err != sql.ErrNoRows {
		t.Errorf("doomed job still stored: %v", err)
	}
	// The pre-existing rows came back intact.
	if err := db.QueryRow(`SELECT email FROM cron_jobs WHERE id=1`).Scan(&command); err != nil || command != "b@x" {
		t.Errorf("reverted row 1 email = %q: %v", command, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_logs WHERE action='cron.create' AND result='failure'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("cron.create failure audit = %d: %v", n, err)
	}
}
