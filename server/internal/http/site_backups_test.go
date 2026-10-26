package http

// Tests for the per-website backups area (site_backups.go +
// hosting/backup_service.go + hosting/backup_scheduler.go): the tab page,
// starting a backup, restoring parts, deleting, streaming a part, the
// best-effort refresh of running rows, the hidesAutoBackups rule and the
// permission split (backups.view vs backups.manage).

import (
	"database/sql"
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

	"encoding/base64"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
)

const (
	backupTestJob     = "0123456789ba0001"
	backupTestRestore = "0123456789ba0002"
)

// backupManifest is a stored manifest with a files part and two database
// parts (as the agent's backup.create job would finish with).
const backupManifest = `{"id":"20261020T120000Z-a1b2c3","version":1,"kind":"site","domain":"bob.example","encrypted":true,"local":true,"size":11,` +
	`"files":[{"name":"files.tar.gz","kind":"files","size":6},{"name":"db-u_bob_shop.sql.gz","kind":"database","database":"u_bob_shop","size":5}]}`

// backupWEnv seeds an administrator, a customer (bob, backups.manage, owns
// website 10 with one linked database) and a read-only viewer (carol,
// backups.view only, owns website 20 without databases).
func backupWEnv(t *testing.T) (http.Handler, *sitesAgent, *sql.DB) {
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
		(1,'websites.view','v','web'),(2,'websites.manage','m','web'),(3,'backups.view','bv','web'),(4,'backups.manage','bm','web')`)
	exec(`INSERT INTO role_permissions (role_id,permission_id) VALUES (1,1),(1,2),(1,3),(1,4),(2,1),(2,2),(2,3),(2,4),(5,1),(5,3)`)
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
	exec(`INSERT INTO databases (server_id,website_id,owner_id,name,created_at) VALUES (1,10,2,'u_bob_shop','2026-01-01T00:00:00Z')`)

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

// backupPost fetches the site's backups page for a fresh CSRF token, then
// POSTs the form to path.
func backupPost(t *testing.T, h http.Handler, cookie *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	token, cookie := siteFormToken(t, h, cookie, "/sites/10/backups")
	form.Set("_token", token)
	return postForm(h, path, form, cookie)
}

// backupSeed inserts one backups row for website 10 (bob.example) and
// returns its id.
func backupSeed(t *testing.T, db *sql.DB, status, manifest string, scheduleID, jobID any) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO backups (server_id, website_id, schedule_id, kind, domain, backup_id, label, manifest, size, encrypted, local, status, error, job_id, user_id, created_at)
		VALUES (1, 10, ?, 'site', 'bob.example', '20261020T120000Z-a1b2c3', 'seeded', ?, 11, 1, 1, ?, '', ?, 2, '2026-10-20T12:00:00Z')`,
		scheduleID, manifest, status, jobID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func backupStatus(t *testing.T, db *sql.DB, id int64) (string, string, string) {
	t.Helper()
	var status, errMsg, manifest string
	if err := db.QueryRow(`SELECT status, error, manifest FROM backups WHERE id = ?`, id).Scan(&status, &errMsg, &manifest); err != nil {
		t.Fatalf("backup %d gone: %v", id, err)
	}
	return status, errMsg, manifest
}

func TestSiteBackupsPageEmpty(t *testing.T) {
	h, _, _ := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")

	rr := get(t, h, "/sites/10/backups", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/10/backups = %d", rr.Code)
	}
	body := rr.Body.String()
	for _, want := range []string{"No backups yet", "No automatic backups scheduled", "Back up now", "u_bob_shop", "No schedule."} {
		if !pageHas(body, want) {
			t.Errorf("page missing %q", want)
		}
	}

	// Carol (viewer) sees her own site's page.
	carol := loginAsUser(t, h, "carol")
	if rr := get(t, h, "/sites/20/backups", carol); rr.Code != http.StatusOK {
		t.Fatalf("GET /sites/20/backups (carol) = %d", rr.Code)
	}
	// …but not bob's website (scoped lookup redirects to /websites).
	rr = get(t, h, "/sites/10/backups", carol)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/websites" {
		t.Fatalf("GET /sites/10/backups (carol) = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	// The viewer gets no "Back up now" and no actions column.
	rr = get(t, h, "/sites/20/backups", carol)
	if pageHas(rr.Body.String(), "Back up now") {
		t.Error("viewer page offers Back up now")
	}
}

func TestSiteBackupsCreate(t *testing.T) {
	h, sa, db := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")

	// A checked box makes the browser submit the hidden 0 and the 1.
	rr := backupPost(t, h, bob, "/sites/10/backups", url.Values{
		"files":     {"0", "1"},
		"databases": {"0", "1"},
		"encrypt":   {"0", "1"},
		"label":     {"pre-launch"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST create = %d", rr.Code)
	}
	// The agent started the job with the site's parts.
	idx := -1
	for i, c := range sa.called() {
		if c == "backup.create" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("backup.create not called (calls %v)", sa.called())
	}
	sa.mu.Lock()
	create := sa.params[idx]
	sa.mu.Unlock()
	if create["domain"] != "bob.example" || create["user"] != "u_bob" || create["root"] != "/home/whacp/bob.example" {
		t.Errorf("create params = %v", create)
	}
	if create["files"] != true || create["encrypt"] != true || create["label"] != "pre-launch" {
		t.Errorf("create flags = %v", create)
	}
	dbs, _ := create["databases"].([]any)
	if len(dbs) != 1 || dbs[0] != "u_bob_shop" {
		t.Errorf("create databases = %v", create["databases"])
	}
	if _, ok := create["remote"]; ok {
		t.Error("customer backup got a remote storage")
	}

	// The row is stored as running with the job, and the job is tracked.
	var status, jobID, kind, domain, label string
	var userID sql.NullInt64
	if err := db.QueryRow(`SELECT status, job_id, kind, domain, label, user_id FROM backups WHERE website_id = 10`).Scan(&status, &jobID, &kind, &domain, &label, &userID); err != nil {
		t.Fatal(err)
	}
	if status != "running" || jobID != backupTestJob || kind != "site" || domain != "bob.example" || label != "pre-launch" || !userID.Valid || userID.Int64 != 2 {
		t.Errorf("row = %s %s %s %s %s user=%v", status, jobID, kind, domain, label, userID)
	}
	var tracked string
	if err := db.QueryRow(`SELECT kind FROM agent_jobs WHERE job_id = ?`, backupTestJob).Scan(&tracked); err != nil {
		t.Fatalf("job not tracked: %v", err)
	}
	if tracked != "Backup bob.example" {
		t.Errorf("tracked kind = %q", tracked)
	}

	// The page now shows the running row with a job link (the job is still
	// in flight, so the page's refresh keeps the row running).
	sa.setJob(backupTestJob, `{"success":true,"data":{"status":"running","result":null,"log":""}}`)
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	body := rr.Body.String()
	if !pageHas(body, "Running") || !pageHas(body, backupTestJob) || !pageHas(body, "pre-launch") {
		t.Errorf("page missing running row: %s", body)
	}
}

func TestSiteBackupsCreateNothingToBackUp(t *testing.T) {
	h, sa, db := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")

	rr := backupPost(t, h, bob, "/sites/10/backups", url.Values{
		"files":     {"0"},
		"databases": {"0"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST create = %d", rr.Code)
	}
	// No agent call, no row; the error is flashed on the next GET.
	if calls := sa.called(); len(calls) != 0 {
		t.Errorf("agent calls = %v", calls)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM backups`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("backups rows = %d", n)
	}
	rr = get(t, h, "/sites/10/backups", bob)
	if !pageHas(rr.Body.String(), "Nothing to back up.") {
		t.Error("missing 'Nothing to back up.' flash")
	}
}

func TestSiteBackupsRestore(t *testing.T) {
	h, sa, db := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")
	id := backupSeed(t, db, "completed", backupManifest, nil, "")
	restore := fmt.Sprintf("/sites/10/backups/%d/restore", id)

	// Wrong confirmation text.
	rr := backupPost(t, h, bob, restore, url.Values{
		"restore_files":  {"1"},
		"confirm_domain": {"wrong.example"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST restore = %d", rr.Code)
	}
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	if !pageHas(rr.Body.String(), "Type the domain name to confirm the restore.") {
		t.Error("missing confirm error")
	}

	// A database the backup does not contain.
	rr = backupPost(t, h, bob, restore, url.Values{
		"restore_files":     {"0"},
		"restore_databases": {"nope"},
		"confirm_domain":    {"bob.example"},
	})
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	if !pageHas(rr.Body.String(), "The backup does not contain database nope.") {
		t.Error("missing missing-database error")
	}

	// Nothing selected.
	rr = backupPost(t, h, bob, restore, url.Values{
		"confirm_domain": {"bob.example"},
	})
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	if !pageHas(rr.Body.String(), "Choose what to restore.") {
		t.Error("missing choose-what error")
	}

	// The real restore: files plus one database.
	rr = backupPost(t, h, bob, restore, url.Values{
		"restore_files":     {"1"},
		"restore_databases": {"u_bob_shop"},
		"confirm_domain":    {"bob.example"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST restore = %d", rr.Code)
	}
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	if !pageHas(rr.Body.String(), "Restore started. A safety backup of the current files is taken first.") {
		t.Error("missing restore flash")
	}
	calls := sa.called()
	last := calls[len(calls)-1]
	if last != "backup.restore" {
		t.Fatalf("last agent call = %v (calls %v)", last, calls)
	}
	sa.mu.Lock()
	p := sa.params[len(sa.params)-1]
	sa.mu.Unlock()
	if p["id"] != "20261020T120000Z-a1b2c3" || p["files"] != true || p["safety_backup"] != true || p["domain"] != "bob.example" {
		t.Errorf("restore params = %v", p)
	}
	dm, _ := p["databases"].(map[string]any)
	if len(dm) != 1 || dm["u_bob_shop"] != "u_bob_shop" {
		t.Errorf("restore databases = %v", p["databases"])
	}
	var tracked string
	if err := db.QueryRow(`SELECT kind FROM agent_jobs WHERE job_id = ?`, backupTestRestore).Scan(&tracked); err != nil {
		t.Fatalf("restore job not tracked: %v", err)
	}
	if tracked != "Restore bob.example" {
		t.Errorf("tracked kind = %q", tracked)
	}
}

func TestSiteBackupsRestoreRunning(t *testing.T) {
	h, sa, db := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")
	backupSeed(t, db, "running", backupManifest, nil, backupTestJob)
	sa.setJob(backupTestJob, `{"success":true,"data":{"status":"running","result":null,"log":""}}`)

	rr := backupPost(t, h, bob, "/sites/10/backups/1/restore", url.Values{
		"restore_files":  {"1"},
		"confirm_domain": {"bob.example"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST restore = %d", rr.Code)
	}
	// No backup action reached the agent (the page refreshes poll jobs.get).
	for _, c := range sa.called() {
		if strings.HasPrefix(c, "backup.") {
			t.Fatalf("agent call %q for a running backup", c)
		}
	}
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	if !pageHas(rr.Body.String(), "Only completed website backups can be restored.") {
		t.Error("missing running-restore error")
	}
}

func TestSiteBackupsDelete(t *testing.T) {
	h, sa, db := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")
	done := backupSeed(t, db, "completed", backupManifest, nil, "")
	running := backupSeed(t, db, "running", backupManifest, nil, backupTestJob)
	sa.setJob(backupTestJob, `{"success":true,"data":{"status":"running","result":null,"log":""}}`)

	// The running one cannot be deleted.
	rr := backupPost(t, h, bob, fmt.Sprintf("/sites/10/backups/%d/delete", running), url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST delete = %d", rr.Code)
	}
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	if !pageHas(rr.Body.String(), "This backup is still running.") {
		t.Error("missing still-running error")
	}

	// The completed one goes: agent copy first, then the row.
	rr = backupPost(t, h, bob, fmt.Sprintf("/sites/10/backups/%d/delete", done), url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST delete = %d", rr.Code)
	}
	calls := sa.called()
	last := calls[len(calls)-1]
	if last != "backup.delete" {
		t.Fatalf("last agent call = %v", last)
	}
	sa.mu.Lock()
	p := sa.params[len(sa.params)-1]
	sa.mu.Unlock()
	if p["domain"] != "bob.example" || p["id"] != "20261020T120000Z-a1b2c3" {
		t.Errorf("delete params = %v", p)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM backups WHERE id = ?`, done).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("row not deleted")
	}
	rr = get(t, h, "/sites/10/backups", bob)
	if !pageHas(rr.Body.String(), "Backup deleted.") {
		t.Error("missing delete flash")
	}
}

func TestSiteBackupsDeleteNotFound(t *testing.T) {
	h, _, _ := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")
	// A website-20 (carol's) backup is invisible to bob's site-10 route.
	rr := backupPost(t, h, bob, "/sites/10/backups/99/delete", url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST delete = %d", rr.Code)
	}
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	if !pageHas(rr.Body.String(), "Backup not found.") {
		t.Error("missing not-found flash")
	}
}

func TestSiteBackupsDownload(t *testing.T) {
	h, _, db := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")
	backupSeed(t, db, "completed", backupManifest, nil, "")

	// Fetch the page once (CSRF + cookie refresh are not needed for GET,
	// but the row must be seeded before the request).
	req := httptest.NewRequest("GET", "/sites/10/backups/1/download?file=files.tar.gz", nil)
	req.AddCookie(bob)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET download = %d: %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rr.Header().Get("Content-Disposition"); cd != `attachment; filename="bob.example-20261020T120000Z-a1b2c3-files.tar.gz"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if cl := rr.Header().Get("Content-Length"); cl != "11" {
		t.Errorf("Content-Length = %q", cl)
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" || rr.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("security headers = %v %v", rr.Header().Get("X-Content-Type-Options"), rr.Header().Get("Cache-Control"))
	}
	if rr.Body.String() != "helloworld" {
		t.Errorf("body = %q", rr.Body.String())
	}

	// A part name the backup does not have is a plain 404.
	req = httptest.NewRequest("GET", "/sites/10/backups/1/download?file=nope.tar.gz", nil)
	req.AddCookie(bob)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound || rr.Body.String() != "Not found" {
		t.Errorf("GET download (bad file) = %d %q", rr.Code, rr.Body.String())
	}

	// A remote-only backup (local = 0) cannot be streamed.
	if _, err := db.Exec(`UPDATE backups SET local = 0`); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest("GET", "/sites/10/backups/1/download?file=files.tar.gz", nil)
	req.AddCookie(bob)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("GET download (remote only) = %d", rr.Code)
	}

	// The route is backups.manage: carol (view only) gets a 403.
	carol := loginAsUser(t, h, "carol")
	req = httptest.NewRequest("GET", "/sites/20/backups/1/download?file=x", nil)
	req.AddCookie(carol)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("GET download (carol) = %d", rr.Code)
	}
}

func TestSiteBackupsRefresh(t *testing.T) {
	h, sa, db := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")

	// One row whose job succeeded, one whose job is lost (agent restart).
	ok := backupSeed(t, db, "running", "{}", nil, "feedfacefeedface")
	lost := backupSeed(t, db, "running", "{}", nil, "deadbeefdeadbeef")
	sa.setJob("feedfacefeedface", `{"success":true,"data":{"status":"succeeded","result":`+backupManifest+`,"log":""}}`)

	// The page triggers the refresh (best effort).
	rr := get(t, h, "/sites/10/backups", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	status, _, manifest := backupStatus(t, db, ok)
	if status != "completed" {
		t.Fatalf("refreshed status = %q", status)
	}
	if !strings.Contains(manifest, "files.tar.gz") || !strings.Contains(manifest, "u_bob_shop") {
		t.Errorf("stored manifest = %s", manifest)
	}
	var errMsg string
	status, errMsg, _ = backupStatus(t, db, lost)
	if status != "failed" || errMsg != "The job was lost (agent restarted)." {
		t.Errorf("lost job = %q %q", status, errMsg)
	}
	// The page shows the completed row with its parts.
	body := rr.Body.String()
	if !pageHas(body, "Completed") || !pageHas(body, "u_bob_shop") || !pageHas(body, "The job was lost (agent restarted).") {
		t.Errorf("page after refresh: %s", body)
	}
	// A failed job is also reflected.
	var failedStatus string
	if err := db.QueryRow(`SELECT status FROM backups WHERE id = ?`, lost).Scan(&failedStatus); err != nil {
		t.Fatal(err)
	}
	if failedStatus != "failed" {
		t.Errorf("lost row status = %q", failedStatus)
	}
}

func TestSiteBackupsHideScheduled(t *testing.T) {
	h, sa, db := backupWEnv(t)
	bob := loginAsUser(t, h, "bob")
	manual := backupSeed(t, db, "completed", backupManifest, nil, "")
	auto := backupSeed(t, db, "completed", backupManifest, 7, "")
	backupSeed(t, db, "completed", backupManifest, 7, "")

	// Bob (own scope, auto_backups off) sees the manual backup only.
	rr := get(t, h, "/sites/10/backups", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET = %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Count(body, "seeded") != 1 {
		t.Errorf("scheduled backups visible to the customer (expect 1 seeded row, page: %s)", body)
	}

	// …and cannot restore or delete a scheduled one.
	rr = backupPost(t, h, bob, "/sites/10/backups/2/restore", url.Values{
		"restore_files":  {"1"},
		"confirm_domain": {"bob.example"},
	})
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	if !pageHas(rr.Body.String(), "Backup not found.") {
		t.Error("scheduled restore not hidden")
	}
	rr = backupPost(t, h, bob, "/sites/10/backups/2/delete", url.Values{})
	rr = get(t, h, "/sites/10/backups", bob)
	bob = refreshCookie(bob, rr)
	if !pageHas(rr.Body.String(), "Backup not found.") {
		t.Error("scheduled delete not hidden")
	}
	// The manual backup stays deletable.
	rr = backupPost(t, h, bob, "/sites/10/backups/1/delete", url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST delete = %d", rr.Code)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM backups WHERE id = ?`, auto).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("scheduled row touched by a hidden delete")
	}
	_ = manual
	_ = sa
}

func TestSiteBackupsPermissions(t *testing.T) {
	h, sa, _ := backupWEnv(t)
	carol := loginAsUser(t, h, "carol")

	// backups.view: the page is fine, writes are 403.
	rr := get(t, h, "/sites/20/backups", carol)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET (carol) = %d", rr.Code)
	}
	token, carol := siteFormToken(t, h, carol, "/sites/20/backups")
	form := url.Values{"_token": {token}, "files": {"1"}}
	rr = postForm(h, "/sites/20/backups", form, carol)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST create (carol) = %d", rr.Code)
	}
	if calls := sa.called(); len(calls) != 0 {
		t.Errorf("agent calls = %v", calls)
	}
	// The download route is backups.manage as well.
	req := httptest.NewRequest("GET", "/sites/20/backups/1/download?file=x", nil)
	req.AddCookie(carol)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("GET download (carol) = %d", rr.Code)
	}
}

func TestSiteBackupsAdminStorage(t *testing.T) {
	h, sa, db := backupWEnv(t)
	// Seed a storage (active) with a sealed config. The env's panel key is
	// the zero key, so seal with the same one.
	var key [32]byte
	c := crypto.NewWithKey(key)
	enc, err := c.Encrypt([]byte(`{"type":"sftp","host":"backups.example","username":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO backup_storages (id,name,kind,config_enc,active,test_ok,test_error,created_at,tested_at)
		VALUES (1,'Nightly','sftp',?,1,1,'','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, enc); err != nil {
		t.Fatal(err)
	}

	admin := loginAsUser(t, h, "admin")
	// The admin's page offers the storage.
	rr := get(t, h, "/sites/10/backups", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET (admin) = %d", rr.Code)
	}
	if !pageHas(rr.Body.String(), "Nightly (SFTP server)") {
		t.Error("admin page missing the storage option")
	}
	// …and can pick it: the agent call carries the remote config.
	token, admin := siteFormToken(t, h, admin, "/sites/10/backups")
	rr = postForm(h, "/sites/10/backups", url.Values{
		"_token": {token}, "files": {"1"}, "storage_id": {"1"}, "keep_local": {"0"},
	}, admin)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST create = %d", rr.Code)
	}
	sa.mu.Lock()
	p := sa.params[len(sa.params)-1]
	sa.mu.Unlock()
	remote, _ := p["remote"].(map[string]any)
	if remote == nil || remote["host"] != "backups.example" || remote["type"] != "sftp" {
		t.Errorf("remote = %v", p["remote"])
	}
	if p["keep_local"] != false {
		t.Errorf("keep_local = %v", p["keep_local"])
	}
	// A customer never sees the select, even with storage_id submitted.
	bob := loginAsUser(t, h, "bob")
	rr = get(t, h, "/sites/10/backups", bob)
	if pageHas(rr.Body.String(), "Nightly") {
		t.Error("customer page shows the storage select")
	}
}
