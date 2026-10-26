package http

// This suite drives GET /jobs/{id} — the JSON endpoint the job viewer polls:
// per-job visibility (own / reseller-customer / website-scoped / all-scope),
// the streamed log reply, the status update, and settle-on-finish.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// getJSON is like get for a JSON client (Accept: application/json), so
// requireAuth answers 401 instead of redirecting to /login.
func getJSON(t *testing.T, h http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Accept", "application/json")
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	adoptCookie(cookie, rr)
	return rr
}

// seedJobs seeds the agent_jobs rows the suite polls (plus dave, a customer
// of bob, for the reseller-visibility cases).
func seedJobs(t *testing.T, db *sql.DB) {
	t.Helper()
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id,username,email,display_name,password_hash,role_id,is_active,password_changed_at,created_at,updated_at,plan_id,parent_id)
		VALUES (5,'dave','e@x','Dave',?,2,1,'2026-01-01T00:00:00Z','','',1,2)`, loginHash)
	job := func(jobID, kind, targetType, targetID string, userID any, settled any) {
		exec(`INSERT INTO agent_jobs (server_id,job_id,kind,target_type,target_id,user_id,status,settled_at,created_at)
			VALUES (1,?,?,?,?,?,'queued',?,'2026-01-01T00:00:00Z')`, jobID, kind, targetType, targetID, userID, settled)
	}
	job("deadbeefcafe0003", "Dump a_main", "database", "100", 2, nil)
	job("deadbeefcafe0004", "Dump b_main", "database", "101", 3, nil)
	job("deadbeefcafe0005", "Dump a_main", "database", "100", 5, nil)
	job("deadbeefcafe0006", "Restore bob.example", "website", "10", nil, nil)
	job("deadbeefcafe0007", "Dump a_main", "database", "100", 2, "2026-01-01T00:00:00Z")
}

func TestJobShowStreamsLog(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	seedJobs(t, db)
	bob := loginAsUser(t, h, "bob")

	rr := getJSON(t, h, "/jobs/deadbeefcafe0003?offset=17", bob)
	if rr.Code != http.StatusOK {
		t.Fatalf("jobs = %d: %s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	for k, want := range map[string]any{"success": true, "kind": "Dump a_main", "log": "Dump finished.\n", "offset": float64(42), "done": true} {
		if body[k] != want {
			t.Errorf("body[%q] = %v, want %v", k, body[k], want)
		}
	}
	if job, _ := body["job"].(map[string]any); job == nil || job["status"] != "succeeded" {
		t.Errorf("body.job = %v", body["job"])
	}
	// The agent got the job id and the offset the viewer asked for.
	if got := agentParam(t, sa, "jobs.log", "id"); got != "deadbeefcafe0003" {
		t.Fatalf("jobs.log id = %v", got)
	}
	if got := agentParam(t, sa, "jobs.log", "offset"); got != float64(17) {
		t.Fatalf("jobs.log offset = %v", got)
	}
	// The polled status is stored, and the finished job is settled.
	var status string
	var settled any
	if err := db.QueryRow(`SELECT status, settled_at FROM agent_jobs WHERE job_id = 'deadbeefcafe0003'`).Scan(&status, &settled); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || settled == nil {
		t.Errorf("agent_jobs = %q / settled %v", status, settled)
	}
}

func TestJobShowSettlesOnce(t *testing.T) {
	h, _, db := dbUsersEnv(t, "")
	seedJobs(t, db)
	bob := loginAsUser(t, h, "bob")

	// A fresh job is settled by the first poll…
	if rr := getJSON(t, h, "/jobs/deadbeefcafe0003", bob); rr.Code != http.StatusOK {
		t.Fatalf("jobs = %d: %s", rr.Code, rr.Body.String())
	}
	var fresh any
	if err := db.QueryRow(`SELECT settled_at FROM agent_jobs WHERE job_id = 'deadbeefcafe0003'`).Scan(&fresh); err != nil || fresh == nil {
		t.Fatalf("fresh job not settled: %v %v", fresh, err)
	}
	// …and a job settled earlier keeps its timestamp (no re-settle).
	if rr := getJSON(t, h, "/jobs/deadbeefcafe0007", bob); rr.Code != http.StatusOK {
		t.Fatalf("jobs = %d: %s", rr.Code, rr.Body.String())
	}
	var kept string
	if err := db.QueryRow(`SELECT settled_at FROM agent_jobs WHERE job_id = 'deadbeefcafe0007'`).Scan(&kept); err != nil || kept != "2026-01-01T00:00:00Z" {
		t.Errorf("settled_at = %q %v, want the seeded value", kept, err)
	}
}

func TestJobShowUnknown(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	seedJobs(t, db)
	bob := loginAsUser(t, h, "bob")

	for _, path := range []string{"/jobs/not-a-job", "/jobs/aaaaaaaaaaaaaaaa"} {
		rr := getJSON(t, h, path, bob)
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s = %d, want 404", path, rr.Code)
		}
		var body map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		errObj, _ := body["error"].(map[string]any)
		if errObj["message"] != "Job not found." {
			t.Errorf("%s error = %v", path, body["error"])
		}
	}
	if len(sa.called()) != 0 {
		t.Fatalf("agent called for unknown jobs: %v", sa.called())
	}
}

func TestJobShowForeignInvisible(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	seedJobs(t, db)
	bob := loginAsUser(t, h, "bob")

	rr := getJSON(t, h, "/jobs/deadbeefcafe0004", bob) // alice's job
	if rr.Code != http.StatusNotFound {
		t.Fatalf("foreign job = %d, want 404", rr.Code)
	}
	if len(sa.called()) != 0 {
		t.Fatalf("agent called for an invisible job: %v", sa.called())
	}
}

func TestJobShowAllScopeSeesEveryJob(t *testing.T) {
	h, _, db := dbUsersEnv(t, "")
	seedJobs(t, db)
	admin := loginAsUser(t, h, "admin")

	rr := getJSON(t, h, "/jobs/deadbeefcafe0004", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("admin on a customer job = %d, want 200", rr.Code)
	}
}

func TestJobShowResellerSeesCustomerJobs(t *testing.T) {
	h, _, db := dbUsersEnv(t, "")
	seedJobs(t, db)
	bob := loginAsUser(t, h, "bob")
	alice := loginAsUser(t, h, "alice")

	if rr := getJSON(t, h, "/jobs/deadbeefcafe0005", bob); rr.Code != http.StatusOK {
		t.Fatalf("reseller on customer job = %d, want 200", rr.Code)
	}
	if rr := getJSON(t, h, "/jobs/deadbeefcafe0005", alice); rr.Code != http.StatusNotFound {
		t.Fatalf("unrelated user on customer job = %d, want 404", rr.Code)
	}
}

func TestJobShowWebsiteTargetVisibleToSiteOwner(t *testing.T) {
	h, _, db := dbUsersEnv(t, "")
	seedJobs(t, db)
	bob := loginAsUser(t, h, "bob")
	alice := loginAsUser(t, h, "alice")

	// A job on a website is visible to whoever may see that website (e.g. a
	// restore an administrator ran on it), even when it is not theirs.
	if rr := getJSON(t, h, "/jobs/deadbeefcafe0006", bob); rr.Code != http.StatusOK {
		t.Fatalf("site owner on website job = %d, want 200", rr.Code)
	}
	if rr := getJSON(t, h, "/jobs/deadbeefcafe0006", alice); rr.Code != http.StatusNotFound {
		t.Fatalf("other user on website job = %d, want 404", rr.Code)
	}
}

func TestJobShowAgentError(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	seedJobs(t, db)
	bob := loginAsUser(t, h, "bob")
	sa.failActions = map[string]string{"jobs.log": "the job is gone"}

	rr := getJSON(t, h, "/jobs/deadbeefcafe0003", bob)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("agent-down jobs = %d, want 503", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	errObj, _ := body["error"].(map[string]any)
	if body["success"] != false || errObj["message"] != "the job is gone" {
		t.Errorf("error body = %v", body)
	}
}

func TestJobShowRequiresAuth(t *testing.T) {
	h, _, _ := dbUsersEnv(t, "")
	if rr := getJSON(t, h, "/jobs/deadbeefcafe0003", nil); rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated jobs = %d, want 401", rr.Code)
	}
}
