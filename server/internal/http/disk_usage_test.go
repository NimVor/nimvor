package http

// This suite drives the website disk-usage pages (DiskUsageController): the
// page (unmeasured, finished scan, the sort, the breadcrumb, a bad ?path=
// being ignored), the scan start (job flash, the running row, the agent
// failure, the already-running refusal) and the page-load refresh (a
// finished job settles, a lost job fails, old scans are pruned). The fake
// agent answers sites.disk_usage and jobs.get.

import (
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
)

// seedScan inserts one disk_usage_scans row and returns its id.
func seedScan(t *testing.T, db *sql.DB, website int64, path, jobID, status, result, errMsg string) int64 {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	finished := sql.NullString{}
	if status != "running" {
		finished = sql.NullString{String: now, Valid: true}
	}
	res, err := db.Exec(`INSERT INTO disk_usage_scans (server_id, website_id, path, job_id, status, result, error, started_at, finished_at)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)`, website, path, jobID, status, result, errMsg, now, finished)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestDiskUsageCleanPath(t *testing.T) {
	cases := []struct{ in, want string; err string }{
		{"", "", ""},
		{"  /app/  ", "app", ""},
		{`app\sub`, "app/sub", ""},
		{"./app", "", "not a folder"},
		{"a/../b", "", "not a folder"},
		{"a/./b", "", "not a folder"},
		{"a/ b", "a/ b", ""},
		{"a\x00b", "", "not a folder"},
		{"/", "", ""},
		{strings.Repeat("x", 1024), strings.Repeat("x", 1024), ""},
		{strings.Repeat("x", 1025), "", "too long"},
	}
	for _, c := range cases {
		got, err := hosting.DiskUsageCleanPath(c.in)
		if c.err == "" {
			if err != nil || got != c.want {
				t.Errorf("CleanPath(%q) = %q, %v; want %q", c.in, got, err, c.want)
			}
		} else if err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("CleanPath(%q) = %q, %v; want error containing %q", c.in, got, err, c.err)
		}
	}
}

func TestDiskUsageShowUnmeasured(t *testing.T) {
	h, sa, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/disk-usage", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Not measured yet", "including the domain's mailboxes", "Measure</button>", "/sites/10/disk-usage/scan"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	for _, a := range sa.called() {
		if a == "sites.disk_usage" {
			t.Errorf("viewing the page started a scan: %v", sa.called())
		}
	}
}

func TestDiskUsageShowBadPathIgnored(t *testing.T) {
	h, _, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/disk-usage?path=../etc", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// The bad path is dropped: the root folder (with the mailboxes note) is
	// shown and no crumb for the escape.
	if !strings.Contains(body, "including the domain's mailboxes") || strings.Contains(body, "etc") {
		t.Errorf("bad ?path= was not ignored: %s", body[:min(200, len(body))])
	}
}

func TestDiskUsageScanStartsJob(t *testing.T) {
	h, sa, db := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/disk-usage")
	rr := postForm(h, "/sites/10/disk-usage/scan", url.Values{"_token": {token}, "path": {"app"}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("scan = %d: %s", rr.Code, rr.Body.String())
	}
	if loc := rr.Header().Get("Location"); loc != "/sites/10/disk-usage?path=app" {
		t.Errorf("Location = %q", loc)
	}
	// The job is still running, so the page-load refresh keeps the row.
	sa.setJob("feedfacefeedface", `{"success":true,"data":{"id":"feedfacefeedface","status":"running"}}`)
	bob = refreshCookie(bob, rr)
	flashAt(t, h, bob, "/sites/10/disk-usage?path=app", "Measuring the folders.")
	flashAt(t, h, bob, "/sites/10/disk-usage?path=app", "feedfacefeedface")
	if got := agentParam(t, sa, "sites.disk_usage", "domain"); got != "bob.example" {
		t.Errorf("domain = %v", got)
	}
	if got := agentParam(t, sa, "sites.disk_usage", "path"); got != "app" {
		t.Errorf("path = %v", got)
	}
	var status, jobID string
	if err := db.QueryRow(`SELECT status, job_id FROM disk_usage_scans WHERE website_id = 10 AND path = 'app'`).Scan(&status, &jobID); err != nil {
		t.Fatalf("running row: %v", err)
	}
	if status != "running" || jobID != "feedfacefeedface" {
		t.Errorf("row = %s, %s", status, jobID)
	}
}

func TestDiskUsageScanAgentFailure(t *testing.T) {
	h, sa, db := filesEnv(t)
	sa.failActions = map[string]string{"sites.disk_usage": "The disk is full."}
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/disk-usage")
	rr := postForm(h, "/sites/10/disk-usage/scan", url.Values{"_token": {token}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("scan = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(bob, rr), "/sites/10/disk-usage", "The disk is full.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM disk_usage_scans`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows = %d, %v (want 0)", n, err)
	}
}

func TestDiskUsageShowFinishedScan(t *testing.T) {
	h, _, db := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	result := `{"entries":[{"name":"public_html","kind":"dir","bytes":10485760,"files":42},` +
		`{"name":"app","kind":"dir","bytes":2097152,"files":10},` +
		`{"name":"mail","kind":"mail","bytes":1048576,"files":3},` +
		`{"name":"files","kind":"files","bytes":2048,"files":9}],` +
		`"bytes":13631488,"partial":false,"truncated":0}`
	seedScan(t, db, 10, "", "jobdone", "done", result, "")
	rr := get(t, h, "/sites/10/disk-usage", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{"Measured", "Total", "13.0 MB", "77%", "Email (mailboxes)", "Files in this folder", "Measure again", `path=public_html`} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// The default sort is by size (descending): public_html before app.
	if i, j := strings.Index(body, "path=public_html"); i < 0 || i > strings.Index(body, "path=app\"") {
		t.Errorf("default sort is not by size")
	}
	// ?sort=name sorts by folder name (ascending).
	if rr2 := get(t, h, "/sites/10/disk-usage?sort=name", bob); rr2.Code != http.StatusOK {
		t.Fatalf("GET ?sort=name = %d", rr2.Code)
	}
	b2 := rr2.Body.String()
	if i, j := strings.Index(b2, "path=app\""); i < 0 || i > strings.Index(b2, "path=public_html") {
		t.Errorf("name sort is not ascending")
	}
	// ?sort=files sorts by file count (descending): public_html (42) first.
	if rr3 := get(t, h, "/sites/10/disk-usage?sort=files", bob); rr3.Code != http.StatusOK {
		t.Fatalf("GET ?sort=files = %d", rr3.Code)
	}
	if i, j := strings.Index(rr3.Body.String(), "path=public_html"); i < 0 || i > strings.Index(rr3.Body.String(), "path=app\"") {
		t.Errorf("files sort is not descending")
	}
}

func TestDiskUsageRefreshSettlesSucceeded(t *testing.T) {
	h, sa, db := filesEnv(t)
	sa.setJob("jobok", `{"success":true,"data":{"id":"jobok","status":"succeeded","result":{"entries":[{"name":"app","kind":"dir","bytes":2048,"files":2}],"bytes":2048,"partial":false,"truncated":0}}}`)
	seedScan(t, db, 10, "", "jobok", "running", `{}`, "")
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/disk-usage", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d: %s", rr.Code, rr.Body.String())
	}
	var status, result string
	if err := db.QueryRow(`SELECT status, result FROM disk_usage_scans WHERE job_id = 'jobok'`).Scan(&status, &result); err != nil {
		t.Fatal(err)
	}
	if status != "done" || !strings.Contains(result, `"app"`) {
		t.Errorf("settled = %s, %s", status, result)
	}
	body := rr.Body.String()
	for _, want := range []string{`path=app"`, "Total", "Measured"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}

func TestDiskUsageRefreshSettlesNotFound(t *testing.T) {
	h, sa, db := filesEnv(t)
	seedScan(t, db, 10, "", "gone", "running", `{}`, "")
	bob := loginAsUser(t, h, "bob")
	rr := get(t, h, "/sites/10/disk-usage", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	var status, errMsg string
	if err := db.QueryRow(`SELECT status, error FROM disk_usage_scans WHERE job_id = 'gone'`).Scan(&status, &errMsg); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(errMsg, "interrupted") {
		t.Errorf("settled = %s, %s", status, errMsg)
	}
	if !strings.Contains(rr.Body.String(), "The measurement failed:") {
		t.Errorf("page missing the failure alert")
	}
}

func TestDiskUsageScanAlreadyRunning(t *testing.T) {
	h, sa, db := filesEnv(t)
	// The job is still running, so the page-load refresh keeps the row.
	sa.setJob("busy", `{"success":true,"data":{"id":"busy","status":"running"}}`)
	seedScan(t, db, 10, "app", "busy", "running", `{}`, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/sites/10/disk-usage?path=app")
	rr := postForm(h, "/sites/10/disk-usage/scan", url.Values{"_token": {token}, "path": {"app"}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("scan = %d", rr.Code)
	}
	flashAt(t, h, refreshCookie(bob, rr), "/sites/10/disk-usage?path=app", "This folder is being measured already.")
	for _, a := range sa.called() {
		if a == "sites.disk_usage" {
			t.Errorf("a second scan was started: %v", sa.called())
		}
	}
}

func TestDiskUsageForeignAndForbidden(t *testing.T) {
	h, _, db := filesEnv(t)
	alice := loginAsUser(t, h, "alice")
	carol := loginAsUser(t, h, "carol")
	// Alice (a customer without this site) gets the 404…
	if rr := get(t, h, "/sites/10/disk-usage", alice); rr.Code != http.StatusNotFound {
		t.Errorf("alice GET = %d, want 404", rr.Code)
	}
	// …carol (no files.manage) the 403.
	if rr := get(t, h, "/sites/10/disk-usage", carol); rr.Code != http.StatusForbidden {
		t.Errorf("carol GET = %d, want 403", rr.Code)
	}
	// A foreign scan redirects to the website list with the error.
	token, alice := siteFormToken(t, h, alice, "/websites")
	rr := postForm(h, "/sites/10/disk-usage/scan", url.Values{"_token": {token}}, alice)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("scan = %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/websites" {
		t.Errorf("Location = %q", loc)
	}
	flashAt(t, h, refreshCookie(alice, rr), "/websites", "Website not found.")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM disk_usage_scans`).Scan(&n); err != nil || n != 0 {
		t.Errorf("rows = %d, %v (want 0)", n, err)
	}
}

func TestDiskUsageScanBadCSRF(t *testing.T) {
	h, _, _ := filesEnv(t)
	bob := loginAsUser(t, h, "bob")
	rr := postForm(h, "/sites/10/disk-usage/scan", url.Values{"path": {"app"}}, bob)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("scan without token = %d, want 400", rr.Code)
	}
}

func TestDiskUsagePrunesOldScans(t *testing.T) {
	h, sa, db := filesEnv(t)
	// Seven finished scans of the folder plus one still running.
	for i := 0; i < 7; i++ {
		seedScan(t, db, 10, "", "old"+string(rune('a'+i)), "done", `{"entries":[],"bytes":0,"partial":false,"truncated":0}`, "")
	}
	sa.setJob("busy", `{"success":true,"data":{"id":"busy","status":"running"}}`)
	seedScan(t, db, 10, "", "busy", "running", `{}`, "")
	bob := loginAsUser(t, h, "bob")
	if rr := get(t, h, "/sites/10/disk-usage", bob); rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	var done, running int
	if err := db.QueryRow(`SELECT COUNT(*) FROM disk_usage_scans WHERE website_id = 10 AND status = 'done'`).Scan(&done); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM disk_usage_scans WHERE website_id = 10 AND status = 'running'`).Scan(&running); err != nil {
		t.Fatal(err)
	}
	if done != 5 || running != 1 {
		t.Errorf("done = %d (want 5), running = %d (want 1)", done, running)
	}
}
