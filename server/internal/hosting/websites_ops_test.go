package hosting

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// This suite covers the advanced website operations (restartPhp, reapply,
// directory listing, permissions, setRuntime), the runtime selection, and the
// WorkerService write path — validation, save/apply/rollback, slugs, plan
// limits and runtime changes — against the real schema with a fake agent.

// insertOpsSite seeds a website row (plus its primary domain) for the ops
// tests: owner 1 (admin, no plan limits) unless overridden.
func insertOpsSite(t *testing.T, db *sql.DB, id, owner int64, kind, phpVersion, appRuntime, proxyPass string) {
	t.Helper()
	dom := "ops" + strconv.FormatInt(id, 10) + ".example"
	if _, err := db.Exec(`INSERT INTO websites (id, server_id, owner_id, domain, system_user, root, document_root, kind, php_version, app_runtime, proxy_pass, status, created_at, updated_at)
		VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', '', '')`,
		id, owner, dom, "u_"+dom, "/home/whacp/"+dom, "/home/whacp/"+dom+"/public_html", kind, phpVersion, appRuntime, proxyPass); err != nil {
		t.Fatalf("seed website %d: %v", id, err)
	}
	if _, err := db.Exec(`INSERT INTO domains (server_id, website_id, name, is_primary) VALUES (1, ?, ?, 1)`, id, dom); err != nil {
		t.Fatalf("seed domain %s: %v", dom, err)
	}
}

func findSite(t *testing.T, h *harness, id int64) *Website {
	t.Helper()
	site, err := h.svc.sites.FindFull(context.Background(), 1, id, nil)
	if err != nil || site == nil {
		t.Fatalf("find site %d: %v", id, err)
	}
	return site
}

func lastAuditAction(t *testing.T, h *harness, action string) auditRec {
	t.Helper()
	for i := len(h.audit) - 1; i >= 0; i-- {
		if h.audit[i].action == action {
			return h.audit[i]
		}
	}
	t.Fatalf("audit %q not found; log=%v", action, h.audit)
	return auditRec{}
}

// --- WebsiteService ops ------------------------------------------------------

func TestWebsiteRestartPhp(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10) // cust.example, php 8.3.1
	if err := h.svc.RestartPhp(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	if h.agent.callCount("discovery.php") != 1 {
		t.Fatalf("discovery not consulted: %v", h.agent.calls)
	}
	p := h.agent.lastParams("services.control")
	// The fake discovery reports fpm_binary only, so the default unit name
	// (php<version>-fpm.service) is used.
	if p["unit"] != "php8.3.1-fpm.service" || p["op"] != "reload" {
		t.Fatalf("services.control params = %v", p)
	}
	if a := lastAuditAction(t, h, "website.restart_php"); a.result != "success" || a.details["domain"] != "cust.example" {
		t.Fatalf("audit = %+v", a)
	}
}

func TestWebsiteRestartPhpUsesDiscoveredUnit(t *testing.T) {
	h := newHarness(t, writeDB(t))
	h.agent.canned = map[string]json.RawMessage{
		"discovery.php": json.RawMessage(`{"versions":[
			{"version":"8.2","fpm_service":"php82-php-fpm.service"},
			{"version":"8.3.1","fpm_service":"php83-php-fpm.service"}]}`),
	}
	site := findSite(t, h, 10)
	if err := h.svc.RestartPhp(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	if p := h.agent.lastParams("services.control"); p["unit"] != "php83-php-fpm.service" {
		t.Fatalf("unit = %v (want the per-distribution name from discovery)", p["unit"])
	}
}

func TestWebsiteRestartPhpDiscoveryFailureIsHard(t *testing.T) {
	h := newHarness(t, writeDB(t))
	h.agent.failActions = map[string]error{"discovery.php": errors.New("agent down")}
	site := findSite(t, h, 10)
	if err := h.svc.RestartPhp(context.Background(), site); err == nil {
		t.Fatal("a failed discovery must surface as an error")
	}
	if h.agent.callCount("services.control") != 0 {
		t.Error("services.control must not run without a usable discovery")
	}
}

func TestWebsiteRestartPhpNoPhp(t *testing.T) {
	h := newHarness(t, writeDB(t))
	insertOpsSite(t, h.db, 42, 1, "static", "", "", "")
	site := findSite(t, h, 42)
	var ue *UserError
	if err := h.svc.RestartPhp(context.Background(), site); !errors.As(err, &ue) {
		t.Fatalf("err = %v (want UserError)", err)
	}
	if ue.What != "This website does not use PHP." {
		t.Fatalf("message = %q", ue.What)
	}
}

func TestWebsiteReapply(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	before := h.agent.callCount("sites.apply")
	if err := h.svc.Reapply(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	if h.agent.callCount("sites.apply") != before+1 {
		t.Fatalf("sites.apply not called once: %v", h.agent.calls)
	}
	if a := lastAuditAction(t, h, "website.reapply"); a.result != "success" || a.details["domain"] != "cust.example" {
		t.Fatalf("audit = %+v", a)
	}
}

func TestWebsiteDirectoryListing(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	if err := h.svc.SetDirectoryListing(context.Background(), site, true); err != nil {
		t.Fatal(err)
	}
	var on int
	if err := h.db.QueryRow(`SELECT directory_listing FROM websites WHERE id = 10`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 1 {
		t.Errorf("directory_listing = %d, want 1", on)
	}
	if a := lastAuditAction(t, h, "website.listing_on"); a.result != "success" {
		t.Fatalf("audit = %+v", a)
	}
	if err := h.svc.SetDirectoryListing(context.Background(), site, false); err != nil {
		t.Fatal(err)
	}
	if a := lastAuditAction(t, h, "website.listing_off"); a.result != "success" {
		t.Fatalf("audit = %+v", a)
	}
	// A proxy site serves its own pages: the toggle is refused.
	insertOpsSite(t, h.db, 43, 1, "proxy", "", "", "http://127.0.0.1:3000")
	proxy := findSite(t, h, 43)
	var ue *UserError
	if err := h.svc.SetDirectoryListing(context.Background(), proxy, true); !errors.As(err, &ue) {
		t.Fatalf("proxy listing = %v (want UserError)", err)
	}
}

func TestWebsitePermissions(t *testing.T) {
	h := newHarness(t, writeDB(t))
	h.agent.canned = map[string]json.RawMessage{
		"sites.permissions": json.RawMessage(`{"checked":120,"fixed":3,"skipped_count":2,"errors":[]}`),
	}
	site := findSite(t, h, 10)

	// Dry run: the report comes back, nothing is audited.
	res, err := h.svc.Permissions(context.Background(), site, false)
	if err != nil {
		t.Fatal(err)
	}
	if res["checked"] != float64(120) || res["fixed"] != float64(3) || res["skipped_count"] != float64(2) {
		t.Fatalf("result = %v", res)
	}
	if p := h.agent.lastParams("sites.permissions"); p["dry_run"] != true || p["user"] != "u_cust" {
		t.Fatalf("params = %v", p)
	}

	// Repair: dry_run off, and the fixed count lands in the audit.
	res, err = h.svc.Permissions(context.Background(), site, true)
	if err != nil {
		t.Fatal(err)
	}
	if p := h.agent.lastParams("sites.permissions"); p["dry_run"] != false || p["document_root"] != "/home/whacp/cust.example/public_html" {
		t.Fatalf("repair params = %v", p)
	}
	if a := lastAuditAction(t, h, "website.permissions_repair"); a.details["fixed"] != 3 {
		t.Fatalf("audit = %+v", a)
	}
	_ = res
}

func TestWebsiteSetRuntime(t *testing.T) {
	h := newHarness(t, writeDB(t))
	insertOpsSite(t, h.db, 47, 1, "proxy", "", "node:20.1.0", "http://127.0.0.1:3000")
	site := findSite(t, h, 47)

	// The runtime must match the site's app type (nodejs here).
	ve, _ := errAsValidation(h.svc.SetRuntime(context.Background(), site, "python:3.12"))
	if ve == nil || !strings.Contains(ve.Fields["app_runtime"], "Node.js") {
		t.Fatalf("cross-kind runtime accepted or wrong message: %v", ve)
	}
	if err := h.svc.SetRuntime(context.Background(), site, "node:22.0.0"); err != nil {
		t.Fatal(err)
	}
	var rt string
	if err := h.db.QueryRow(`SELECT app_runtime FROM websites WHERE id = 47`).Scan(&rt); err != nil {
		t.Fatal(err)
	}
	if rt != "node:22.0.0" {
		t.Errorf("app_runtime = %q", rt)
	}
	if a := lastAuditAction(t, h, "website.runtime"); a.details["runtime"] != "node:22.0.0" {
		t.Fatalf("audit = %+v", a)
	}

	// A plain PHP site cannot change runtimes.
	php := findSite(t, h, 10)
	var ue *UserError
	if err := h.svc.SetRuntime(context.Background(), php, "node:20.1.0"); !errors.As(err, &ue) {
		t.Fatalf("php setRuntime = %v (want UserError)", err)
	}
}

// --- RuntimeService ------------------------------------------------------------

type cannedAgent struct{ resp json.RawMessage }

func (c cannedAgent) Call(ctx context.Context, action string, params any, timeout ...time.Duration) (json.RawMessage, error) {
	if action == "runtime.status" {
		return c.resp, nil
	}
	return json.RawMessage(`{}`), nil
}

func TestRuntimeSelectable(t *testing.T) {
	agent := cannedAgent{json.RawMessage(`{"arch":"x86_64",
		"node":[{"kind":"node","version":"20.1.0","bin":"/usr/bin/node","binary":"/usr/bin/node"},
		        {"kind":"node","version":"22.0.0","bin":"/usr/bin/node","binary":"/usr/bin/node"}],
		"python":[],
		"system":[{"kind":"python","version":"3.12","bin":"/usr/bin/python3","binary":"/usr/bin/python3"},
		          {"kind":"python","version":"3.11","bin":"/usr/bin/python3","binary":"/usr/bin/python3"}]}`)}
	sel := NewRuntimeService(agent).Selectable(context.Background())
	want := []string{"node:20.1.0", "node:22.0.0", "python:3.12", "python:3.11"}
	if strings.Join(sel, ",") != strings.Join(want, ",") {
		t.Fatalf("selectable = %v, want %v", sel, want)
	}
	// A failed status yields no versions, not an error (PHP behavior).
	if got := NewRuntimeService(failingCannedAgent{}).Selectable(context.Background()); len(got) != 0 {
		t.Fatalf("failed status = %v (want empty)", got)
	}
}

type failingCannedAgent struct{}

func (failingCannedAgent) Call(ctx context.Context, action string, params any, timeout ...time.Duration) (json.RawMessage, error) {
	return nil, errors.New("agent down")
}

func TestWorkerStatus(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	ws := newWorkerService(t, h, nil)
	w := seedWorker(t, h, site, nil)

	h.agent.canned = map[string]json.RawMessage{
		"workers.status": json.RawMessage(`[{"unit":"whacp-worker-cust-app.service","active_state":"active","sub_state":"running","procs_total":2,"procs_running":1,"memory_bytes":10485760}]`),
	}
	st := ws.Status(context.Background(), []WorkerFull{w})
	if st == nil {
		t.Fatal("no status rows")
	}
	row, ok := st[w.Unit()]
	if !ok {
		t.Fatalf("status not keyed by unit: %v", st)
	}
	if row.ActiveState != "active" || row.ProcsTotal != 2 || row.ProcsRunning != 1 || row.MemoryBytes == nil || *row.MemoryBytes != 10485760 {
		t.Fatalf("status = %+v", row)
	}
	if p := h.agent.lastParams("workers.status"); p["slugs"] == nil {
		t.Fatal("slugs not sent")
	}

	// An unreachable agent yields an empty status (the page says unknown).
	h.agent.canned = nil
	h.agent.failActions = map[string]error{"workers.status": errors.New("agent down")}
	if st := ws.Status(context.Background(), []WorkerFull{w}); st != nil {
		t.Fatalf("agent-down status = %v (want nil)", st)
	}
	// No workers: no agent call at all.
	if st := ws.Status(context.Background(), nil); st != nil {
		t.Fatalf("empty status = %v (want nil)", st)
	}
}

// --- WorkerService -------------------------------------------------------------

func newWorkerService(t *testing.T, h *harness, log *[]auditRec) *WorkerService {
	t.Helper()
	var fn Auditor
	if log != nil {
		fn = func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
			*log = append(*log, auditRec{action: action, target: target, result: result, details: details})
		}
	}
	return NewWorkerService(h.db, h.agent, NewPlanLimits(h.db), fn, 1)
}

func TestWorkerValidate(t *testing.T) {
	// Valid input: the PHP defaults (restart always, 1 proc, enabled).
	// Absent procs (nil) takes the PHP ?? 1 default.
	w, err := validateWorker(WorkerInput{Name: "app", Command: "node app.js", Restart: "", MemoryMB: 0, EnvText: "PORT=3000"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Restart != "always" || w.Procs != 1 || !w.Enabled || w.Env["PORT"] != "3000" {
		t.Fatalf("defaults = %+v", w)
	}
	// "system" normalizes to the empty runtime.
	w, err = validateWorker(WorkerInput{Name: "app", Command: "node app.js", Runtime: "system"})
	if err != nil || w.Runtime != "" {
		t.Fatalf("system runtime: %+v err=%v", w, err)
	}
	bad := []WorkerInput{
		{Name: "", Command: "node"},
		{Name: strings.Repeat("a", 101), Command: "node"},
		{Name: "a", Command: "node\nsecond-line"},
		{Name: "a", Command: "node", Directory: "../etc"},
		{Name: "a", Command: "node", Directory: "a/b/../c"},
		{Name: "a", Command: "node", Restart: "sometimes"},
		{Name: "a", Command: "node", Runtime: "ruby:3.2"},
		{Name: "a", Command: "node", MemoryMB: -1},
		{Name: "a", Command: "node", MemoryMB: 65537},
		{Name: "a", Command: "node", Procs: ProcsPtr(21)},
		{Name: "a", Command: "node", Procs: ProcsPtr(0)},
		{Name: "a", Command: "node", EnvText: "port=1"},
		{Name: "a", Command: "node", EnvText: "NO_EQUALS"},
		{Name: "a", Command: "node", EnvText: "9KEY=1"},
	}
	for i, in := range bad {
		if _, err := validateWorker(in); err == nil {
			t.Fatalf("bad input %d accepted: %+v", i, in)
		}
	}
	// More than 50 env vars.
	lines := make([]string, 0, 51)
	for i := 0; i < 51; i++ {
		lines = append(lines, "K"+strconv.Itoa(i)+"="+strconv.Itoa(i))
	}
	if _, err := validateWorker(WorkerInput{Name: "a", Command: "node", EnvText: strings.Join(lines, "\n")}); err == nil {
		t.Fatal("51 env vars accepted")
	}
	// Comments and blank lines are ignored.
	w, err = validateWorker(WorkerInput{Name: "a", Command: "node", EnvText: "\n# comment\nA=1\n"})
	if err != nil || len(w.Env) != 1 || w.Env["A"] != "1" {
		t.Fatalf("env comments: %+v err=%v", w, err)
	}
}

func TestWorkerSaveApplies(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10) // cust.example → slug prefix "cust"
	var log []auditRec
	ws := newWorkerService(t, h, &log)

	if err := ws.Save(context.Background(), site, nil, WorkerInput{
		Name: "app", Command: "node app.js", Runtime: "node:20.1.0", EnvText: "PORT=3000", Procs: ProcsPtr(2),
	}); err != nil {
		t.Fatal(err)
	}
	p := h.agent.lastParams("workers.apply")
	if p == nil {
		t.Fatal("workers.apply not called")
	}
	if p["slug"] != "cust-app" || p["command"] != "node app.js" || p["procs"] != 2 ||
		p["enabled"] != true || p["runtime"] != "node:20.1.0" || p["user"] != "u_cust" {
		t.Fatalf("apply params = %v", p)
	}
	if env, _ := p["env"].(map[string]string); env["PORT"] != "3000" {
		t.Fatalf("env = %v", p["env"])
	}
	var name, runtime string
	var procs int
	if err := h.db.QueryRow(`SELECT name, runtime, procs FROM workers WHERE website_id = 10`).Scan(&name, &runtime, &procs); err != nil {
		t.Fatal(err)
	}
	if name != "app" || runtime != "node:20.1.0" || procs != 2 {
		t.Fatalf("row = %s %s %d", name, runtime, procs)
	}
	if a := lastAuditActionIn(t, log, "worker.create"); a.result != "success" {
		t.Fatalf("audit = %+v", a)
	}

	// A second worker with the same name gets a -2 slug.
	if err := ws.Save(context.Background(), site, nil, WorkerInput{Name: "app", Command: "node app.js"}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE slug = 'cust-app-2'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("second slug missing (n=%d err=%v)", n, err)
	}
}

func TestWorkerSaveAgentFailureRollsBack(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	h.agent.failActions = map[string]error{"workers.apply": errors.New("agent down")}
	var log []auditRec
	ws := newWorkerService(t, h, &log)

	if err := ws.Save(context.Background(), site, nil, WorkerInput{Name: "app", Command: "node app.js"}); err == nil {
		t.Fatal("save should fail with a failing agent")
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE website_id = 10`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed create left %d worker rows (want 0)", n)
	}
	if a := lastAuditActionIn(t, log, "worker.create"); a.result != "failure" {
		t.Fatalf("audit = %+v", a)
	}
}

func TestWorkerUpdateRollsBackToPrevious(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	ws := newWorkerService(t, h, nil)
	if err := ws.Save(context.Background(), site, nil, WorkerInput{Name: "app", Command: "node app.js"}); err != nil {
		t.Fatal(err)
	}
	workers, err := ws.ForSite(context.Background(), site.ID)
	if err != nil || len(workers) != 1 {
		t.Fatalf("workers = %v err=%v", workers, err)
	}
	// Now the agent fails: the update must restore the previous row.
	h.agent.failActions = map[string]error{"workers.apply": errors.New("agent down")}
	var log []auditRec
	ws = newWorkerService(t, h, &log)
	if err := ws.Save(context.Background(), site, &workers[0].ID, WorkerInput{Name: "app", Command: "node changed.js", MemoryMB: 512}); err == nil {
		t.Fatal("update should fail with a failing agent")
	}
	var cmd string
	var mem int
	if err := h.db.QueryRow(`SELECT command, memory_mb FROM workers WHERE id = ?`, workers[0].ID).Scan(&cmd, &mem); err != nil {
		t.Fatal(err)
	}
	if cmd != "node app.js" || mem != 0 {
		t.Fatalf("row not restored: cmd=%q mem=%d", cmd, mem)
	}
	if a := lastAuditActionIn(t, log, "worker.update"); a.result != "failure" {
		t.Fatalf("audit = %+v", a)
	}
}

func TestWorkerPlanLimit(t *testing.T) {
	h := newHarness(t, writeDB(t))
	if _, err := h.db.Exec(`UPDATE plans SET max_workers = 1 WHERE id = 11`); err != nil {
		t.Fatal(err)
	}
	site := findSite(t, h, 10) // owner 3 (cust), plan 11
	ws := newWorkerService(t, h, nil)
	if err := ws.Save(context.Background(), site, nil, WorkerInput{Name: "app", Command: "node app.js"}); err != nil {
		t.Fatal(err)
	}
	var ue *UserError
	err := ws.Save(context.Background(), site, nil, WorkerInput{Name: "second", Command: "node app.js"})
	if !errors.As(err, &ue) {
		t.Fatalf("second worker = %v (want plan limit)", err)
	}
	if !strings.Contains(ue.What, "allows at most 1 workers") {
		t.Fatalf("limit message = %q", ue.What)
	}
	// The agent saw exactly one apply (the successful create).
	if h.agent.callCount("workers.apply") != 1 {
		t.Fatalf("workers.apply = %d (want 1)", h.agent.callCount("workers.apply"))
	}
}

func TestWorkerChangeRuntime(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	ws := newWorkerService(t, h, nil)
	if err := ws.Save(context.Background(), site, nil, WorkerInput{
		Name: "app", Command: "node app.js", Runtime: "node:20.1.0", EnvText: "PORT=3000",
	}); err != nil {
		t.Fatal(err)
	}
	workers, _ := ws.ForSite(context.Background(), site.ID)
	before := h.agent.callCount("workers.apply")
	var log []auditRec
	if err := ws.ChangeRuntime(context.Background(), site, workers[0], "node:22.0.0"); err != nil {
		t.Fatal(err)
	}
	got, err := ws.Find(context.Background(), site.ID, workers[0].ID)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Runtime != "node:22.0.0" || got.Env["PORT"] != "3000" || got.Command != "node app.js" {
		t.Fatalf("worker after change = %+v", got)
	}
	if h.agent.callCount("workers.apply") != before+1 {
		t.Fatal("change must re-apply the worker")
	}
	if a := lastAuditActionIn(t, log, "worker.update"); a.result != "success" {
		t.Fatalf("audit = %+v", a)
	}
}

// --- worker run operations (setEnabled / restart / delete) -------------------

// seedWorker saves one "app" worker and returns it.
func seedWorker(t *testing.T, h *harness, site *Website, log *[]auditRec) WorkerFull {
	t.Helper()
	ws := newWorkerService(t, h, log)
	if err := ws.Save(context.Background(), site, nil, WorkerInput{Name: "app", Command: "node app.js"}); err != nil {
		t.Fatal(err)
	}
	workers, err := ws.ForSite(context.Background(), site.ID)
	if err != nil || len(workers) != 1 {
		t.Fatalf("workers = %v err=%v", workers, err)
	}
	return workers[0]
}

func TestWorkerSetEnabled(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	var log []auditRec
	w := seedWorker(t, h, site, &log)

	if err := newWorkerService(t, h, &log).SetEnabled(context.Background(), site, w, false); err != nil {
		t.Fatal(err)
	}
	// The new state was applied on the server, and the row followed.
	if p := h.agent.lastParams("workers.apply"); p["enabled"] != false {
		t.Fatalf("apply enabled = %v (want false)", p["enabled"])
	}
	var on int
	if err := h.db.QueryRow(`SELECT enabled FROM workers WHERE id = ?`, w.ID).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 0 {
		t.Errorf("row enabled = %d, want 0", on)
	}
	if a := lastAuditActionIn(t, log, "worker.stop"); a.result != "success" {
		t.Fatalf("audit = %+v", a)
	}
}

func TestWorkerSetEnabledAgentFailureKeepsRow(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	var log []auditRec
	w := seedWorker(t, h, site, &log)
	h.agent.failActions = map[string]error{"workers.apply": errors.New("agent down")}

	if err := newWorkerService(t, h, &log).SetEnabled(context.Background(), site, w, false); err == nil {
		t.Fatal("setEnabled must fail with a failing agent")
	}
	var on int
	if err := h.db.QueryRow(`SELECT enabled FROM workers WHERE id = ?`, w.ID).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 1 {
		t.Errorf("row enabled = %d (want 1 — unchanged)", on)
	}
	for _, a := range log {
		if a.action == "worker.stop" {
			t.Fatalf("a failed toggle was audited: %+v", a)
		}
	}
}

func TestWorkerRestart(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	var log []auditRec
	w := seedWorker(t, h, site, &log)

	if err := newWorkerService(t, h, &log).Restart(context.Background(), site, w); err != nil {
		t.Fatal(err)
	}
	if p := h.agent.lastParams("workers.restart"); p["slug"] != "cust-app" {
		t.Fatalf("restart slug = %v", p["slug"])
	}
	if a := lastAuditActionIn(t, log, "worker.restart"); a.result != "success" {
		t.Fatalf("audit = %+v", a)
	}
}

func TestWorkerRestartSuspended(t *testing.T) {
	h := newHarness(t, writeDB(t))
	if _, err := h.db.Exec(`UPDATE users SET suspended_at = '2026-01-01' WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	site := findSite(t, h, 10) // owner 3 is suspended now
	w := seedWorker(t, h, site, nil)

	var ue *UserError
	if err := newWorkerService(t, h, nil).Restart(context.Background(), site, w); !errors.As(err, &ue) {
		t.Fatalf("restart = %v (want UserError)", err)
	}
	if ue.What != "The account is suspended; its workers are stopped." {
		t.Fatalf("message = %q", ue.What)
	}
	if h.agent.callCount("workers.restart") != 0 {
		t.Error("workers.restart must not run for a suspended account")
	}
}

func TestWorkerDelete(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	var log []auditRec
	w := seedWorker(t, h, site, &log)

	if err := newWorkerService(t, h, &log).Delete(context.Background(), site, w); err != nil {
		t.Fatal(err)
	}
	if p := h.agent.lastParams("workers.remove"); p["slug"] != "cust-app" {
		t.Fatalf("remove slug = %v", p["slug"])
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("rows left = %d", n)
	}
	if a := lastAuditActionIn(t, log, "worker.delete"); a.result != "success" {
		t.Fatalf("audit = %+v", a)
	}
}

func TestWorkerDeleteAgentFailureKeepsRow(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	w := seedWorker(t, h, site, nil)
	h.agent.failActions = map[string]error{"workers.remove": errors.New("agent down")}

	if err := newWorkerService(t, h, nil).Delete(context.Background(), site, w); err == nil {
		t.Fatal("delete must fail with a failing agent")
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rows = %d (want 1 — the unit still exists on the server)", n)
	}
}

func TestWebsiteDeleteRemovesWorkers(t *testing.T) {
	h := newHarness(t, writeDB(t))
	site := findSite(t, h, 10)
	ws := newWorkerService(t, h, nil)
	seedWorker(t, h, site, nil)
	if err := ws.Save(context.Background(), site, nil, WorkerInput{Name: "queue", Command: "php artisan queue:work"}); err != nil {
		t.Fatal(err)
	}

	if _, err := h.svc.Delete(context.Background(), site, true, false); err != nil {
		t.Fatal(err)
	}
	removed, teardown := 0, -1
	for i, c := range h.agent.calls {
		switch c.action {
		case "workers.remove":
			removed++
			if i > teardown && teardown >= 0 {
				t.Fatal("a worker was removed after the website teardown")
			}
		case "sites.delete":
			teardown = i
		}
	}
	if removed != 2 {
		t.Fatalf("workers.remove = %d (want 2)", removed)
	}
	if teardown < 0 {
		t.Fatal("sites.delete not called")
	}
	var n int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM websites WHERE id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("website row not deleted")
	}
}

// --- worker slug edge cases ----------------------------------------------------

func TestWorkerSlugNormalization(t *testing.T) {
	h := newHarness(t, writeDB(t))
	insertOpsSite(t, h.db, 48, 1, "proxy", "", "", "http://127.0.0.1:3000")
	site := findSite(t, h, 48) // ops48.example
	ws := newWorkerService(t, h, nil)
	if err := ws.Save(context.Background(), site, nil, WorkerInput{Name: "My Queue Worker", Command: "php artisan queue:work"}); err != nil {
		t.Fatal(err)
	}
	var slug string
	if err := h.db.QueryRow(`SELECT slug FROM workers WHERE website_id = 48`).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	if slug != "ops48-my-queue-worker" {
		t.Fatalf("slug = %q (want ops48-my-queue-worker)", slug)
	}
}

func lastAuditActionIn(t *testing.T, log []auditRec, action string) auditRec {
	t.Helper()
	for i := len(log) - 1; i >= 0; i-- {
		if log[i].action == action {
			return log[i]
		}
	}
	t.Fatalf("audit %q not found; log=%v", action, log)
	return auditRec{}
}
