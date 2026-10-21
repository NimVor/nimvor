package http

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// This suite drives the worker write path end to end (WorkersController):
// create, edit/update, toggle, restart, delete, the live status on the page,
// and the negative paths (validation re-render, agent failures, plan limits,
// tenant scope, CSRF).

// workerPageToken gets a CSRF token from the workers page and the refreshed
// session cookie (the token is minted into the session while rendering).
func workerPageToken(t *testing.T, h http.Handler, cookie *http.Cookie, path string) (string, *http.Cookie) {
	t.Helper()
	rr := get(t, h, path, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rr.Code, rr.Body.String())
	}
	m := csrfRe.FindStringSubmatch(rr.Body.String())
	if m == nil {
		t.Fatalf("no CSRF token on %s:\n%s", path, rr.Body.String())
	}
	return m[1], refreshCookie(cookie, rr)
}

// createWorker posts a minimal valid worker and fails on a non-303.
func createWorker(t *testing.T, h http.Handler, cookie *http.Cookie, extra url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := workerPageToken(t, h, cookie, "/sites/10/workers")
	values := url.Values{
		"name": {"app"}, "command": {"node app.js"}, "directory": {""},
		"env": {""}, "restart": {"always"}, "memory_mb": {"0"},
		"procs": {"1"}, "runtime": {""}, "enabled": {"1"},
	}
	for k, vs := range extra {
		values[k] = vs
	}
	rr := postForm(h, "/sites/10/workers", values, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("worker create = %d: %s", rr.Code, rr.Body.String())
	}
	return rr
}

func workerIDOf(t *testing.T, db *sql.DB, websiteID int64) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT id FROM workers WHERE website_id = ?`, websiteID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestWorkerCreate(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := workerPageToken(t, h, cookie, "/sites/10/workers")
	rr := postForm(h, "/sites/10/workers", url.Values{
		"_token": {token}, "name": {"app"}, "command": {"node app.js"}, "directory": {""},
		"env": {"PORT=3000"}, "restart": {"always"}, "memory_mb": {"0"},
		"procs": {"2"}, "runtime": {"node:20.1.0"}, "enabled": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/workers" {
		t.Fatalf("create = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	i := lastActionIndex(sa.called(), "workers.apply")
	if i < 0 {
		t.Fatalf("workers.apply not called: %v", sa.called())
	}
	if sa.param(i, "slug") != "bob-app" || sa.param(i, "procs") != float64(2) || sa.param(i, "runtime") != "node:20.1.0" {
		t.Errorf("apply = %v %v %v", sa.param(i, "slug"), sa.param(i, "procs"), sa.param(i, "runtime"))
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workers WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("workers = %d (want 1)", n)
	}
	if got := auditResult(t, db, "worker.create"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10/workers", "Worker created.")
}

func TestWorkerCreateValidation(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	token, cookie := workerPageToken(t, h, cookie, "/sites/10/workers")
	rr := postForm(h, "/sites/10/workers", url.Values{
		"_token": {token}, "name": {""}, "command": {"node bad"}, "directory": {""},
		"env": {""}, "restart": {"always"}, "memory_mb": {"0"},
		"procs": {""}, "runtime": {""}, "enabled": {"1"},
	}, cookie)
	if rr.Code != http.StatusOK {
		t.Fatalf("validation re-render = %d", rr.Code)
	}
	body := rr.Body.String()
	// name and the cleared procs field are rejected; the (valid) command is
	// preserved as old input.
	for _, want := range []string{
		"Please correct the following", "Enter a name",
		"Run 1 to 20 copies", `value="node bad"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("re-render missing %q", want)
		}
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workers WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("rejected form left %d rows", n)
	}
	if lastActionIndex(sa.called(), "workers.apply") >= 0 {
		t.Error("agent called for a rejected form")
	}
}

func TestWorkerUpdate(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	createWorker(t, h, cookie, nil)
	id := workerIDOf(t, db, 10)
	editURL := fmt.Sprintf("/sites/10/workers?edit=%d", id)

	// The edit form is pre-filled and carries the worker id.
	token, cookie := workerPageToken(t, h, cookie, editURL)
	body := get(t, h, editURL, cookie).Body.String()
	if !strings.Contains(body, fmt.Sprintf(`name="worker_id" value="%d"`, id)) {
		t.Errorf("edit form missing worker_id:\n%s", body)
	}
	if !strings.Contains(body, `value="node app.js"`) {
		t.Error("edit form not pre-filled")
	}
	if !strings.Contains(body, "Save and restart") {
		t.Error("edit form missing the save label")
	}

	before := lastActionIndex(sa.called(), "workers.apply")
	rr := postForm(h, "/sites/10/workers", url.Values{
		"_token": {token}, "worker_id": {strconv.FormatInt(id, 10)}, "name": {"app"},
		"command": {"node new.js"}, "directory": {""}, "env": {""},
		"restart": {"always"}, "memory_mb": {"256"}, "procs": {"1"},
		"runtime": {""}, "enabled": {"1"},
	}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/workers" {
		t.Fatalf("update = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	if i := lastActionIndex(sa.called(), "workers.apply"); i <= before {
		t.Errorf("workers.apply not re-issued: %v", sa.called())
	}
	var cmd string
	var mem int
	if err := db.QueryRow(`SELECT command, memory_mb FROM workers WHERE id = ?`, id).Scan(&cmd, &mem); err != nil {
		t.Fatal(err)
	}
	if cmd != "node new.js" || mem != 256 {
		t.Errorf("row = %q %d", cmd, mem)
	}
	if got := auditResult(t, db, "worker.update"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10/workers", "Worker updated and restarted.")
}

func TestWorkerToggle(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	createWorker(t, h, cookie, nil)
	id := workerIDOf(t, db, 10)

	token, cookie := workerPageToken(t, h, cookie, "/sites/10/workers")
	rr := postForm(h, fmt.Sprintf("/sites/10/workers/%d/toggle", id), url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle off = %d", rr.Code)
	}
	i := lastActionIndex(sa.called(), "workers.apply")
	if i < 0 || sa.param(i, "enabled") != false {
		t.Fatalf("toggle apply = %v", sa.param(i, "enabled"))
	}
	var on int
	if err := db.QueryRow(`SELECT enabled FROM workers WHERE id = ?`, id).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 0 {
		t.Errorf("enabled = %d (want 0)", on)
	}
	if got := auditResult(t, db, "worker.stop"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10/workers", "Worker stopped.")

	// Back on.
	token, cookie = workerPageToken(t, h, cookie, "/sites/10/workers")
	rr = postForm(h, fmt.Sprintf("/sites/10/workers/%d/toggle", id), url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("toggle on = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10/workers", "Worker started.")
}

func TestWorkerRestart(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	createWorker(t, h, cookie, nil)
	id := workerIDOf(t, db, 10)
	token, cookie := workerPageToken(t, h, cookie, "/sites/10/workers")
	rr := postForm(h, fmt.Sprintf("/sites/10/workers/%d/restart", id), url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("restart = %d", rr.Code)
	}
	i := lastActionIndex(sa.called(), "workers.restart")
	if i < 0 || sa.param(i, "slug") != "bob-app" {
		t.Fatalf("workers.restart = %v", sa.param(i, "slug"))
	}
	if got := auditResult(t, db, "worker.restart"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10/workers", "Worker restarted.")
}

func TestWorkerDelete(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	createWorker(t, h, cookie, nil)
	id := workerIDOf(t, db, 10)
	token, cookie := workerPageToken(t, h, cookie, "/sites/10/workers")
	rr := postForm(h, fmt.Sprintf("/sites/10/workers/%d/delete", id), url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("delete = %d", rr.Code)
	}
	i := lastActionIndex(sa.called(), "workers.remove")
	if i < 0 || sa.param(i, "slug") != "bob-app" {
		t.Fatalf("workers.remove = %v", sa.param(i, "slug"))
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workers WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("rows left = %d", n)
	}
	if got := auditResult(t, db, "worker.delete"); got != "success" {
		t.Errorf("audit = %q", got)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10/workers", "Worker removed.")
}

func TestWorkerPageShowsStatus(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	createWorker(t, h, cookie, url.Values{"procs": {"2"}})
	cookie = refreshCookie(cookie, get(t, h, "/sites/10/workers", cookie))
	body := get(t, h, "/sites/10/workers", cookie).Body.String()
	// The canned agent status (2/2 copies up, 12 MB) is shown for bob-app.
	for _, want := range []string{
		"whacp-worker-bob-app.service", "active", "2/2 up", "12.0 MB",
		"Restart", "Edit", "Stop",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("workers page missing %q", want)
		}
	}
}

func TestWorkerPageEmpty(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	body := get(t, h, "/sites/10/workers", cookie).Body.String()
	if !strings.Contains(body, "No workers yet") {
		t.Errorf("empty state missing:\n%s", body)
	}
	if !strings.Contains(body, "Add worker") {
		t.Error("create form missing")
	}
}

func TestWorkerMissingSite(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	ck := refreshCookie(cookie, get(t, h, "/websites", cookie))
	rr := postForm(h, "/sites/999/workers", url.Values{"_token": {"x"}, "name": {"a"}, "command": {"node"}}, ck)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("store missing = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(ck, rr), "/websites", "Website not found.")
}

func TestWorkerMissingWorker(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	token, cookie := workerPageToken(t, h, cookie, "/sites/10/workers")
	rr := postForm(h, "/sites/10/workers/999/toggle", url.Values{"_token": {token}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/sites/10/workers" {
		t.Fatalf("toggle missing = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10/workers", "Worker not found.")
}

func TestWorkerAgentFailureFlashesAgentMessage(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	sa.failActions = map[string]string{"workers.apply": "the agent refused"}
	rr := createWorker(t, h, cookie, nil)
	// The row must not survive a failed apply.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workers WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("failed create left %d rows", n)
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10/workers", "the agent refused")
}

func TestWorkerPlanLimit(t *testing.T) {
	h, cookie, sa, db := sitesEnv(t, false, false)
	if _, err := db.Exec(`UPDATE plans SET max_workers = 1 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	createWorker(t, h, cookie, nil)
	rr := createWorker(t, h, cookie, url.Values{"name": {"second"}})
	flashAt(t, h, refreshCookie(cookie, rr), "/sites/10/workers", "allows at most 1 workers")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM workers WHERE website_id = 10`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("workers = %d (want 1)", n)
	}
	if i := lastActionIndex(sa.called(), "workers.apply"); i < 0 {
		t.Fatal("no apply at all")
	}
}

func TestWorkerTenantScope(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, true, false) // alice owns site 20
	if rr := get(t, h, "/sites/20/workers", cookie); rr.Code != http.StatusNotFound {
		t.Errorf("another customer's workers page = %d (want 404)", rr.Code)
	}
	token, cookie := workerPageToken(t, h, cookie, "/sites/10/workers")
	rr := postForm(h, "/sites/20/workers", url.Values{"_token": {token}, "name": {"a"}, "command": {"node"}}, cookie)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("store on foreign site = %d loc=%q", rr.Code, rr.Header().Get("Location"))
	}
	flashAt(t, h, refreshCookie(cookie, rr), "/websites", "Website not found.")
}

func TestWorkersRequireCSRF(t *testing.T) {
	h, cookie, _, _ := sitesEnv(t, false, false)
	paths := []string{
		"/sites/10/workers",
		"/sites/10/workers/1/toggle",
		"/sites/10/workers/1/restart",
		"/sites/10/workers/1/delete",
	}
	for _, path := range paths {
		if rr := postForm(h, path, url.Values{"name": {"x"}}, cookie); rr.Code != http.StatusBadRequest {
			t.Errorf("POST %s without CSRF = %d (want 400)", path, rr.Code)
		}
	}
}
