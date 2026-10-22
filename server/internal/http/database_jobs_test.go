package http

// This suite drives the job-based database routes: POST /databases/{id}/dump
// and POST /database-users/../import — the agent job start, the agent_jobs
// tracking row (who started what), the job flash, and the import path
// validation.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestDatabaseDumpStartsJob(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/databases")
	rr := postForm(h, "/databases/100/dump", url.Values{"_token": {token}}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("dump = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	page := get(t, h, "/databases", bob).Body.String()
	for _, want := range []string{
		"Dump started. The file is written to the server backup directory.",
		"deadbeefcafe0001",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("dump flash missing %q:\\n%s", want, page)
		}
	}
	// The agent saw the dump of the right database.
	if got := agentParam(t, sa, "db.dump", "name"); got != "a_main" {
		t.Fatalf("db.dump name = %v", got)
	}
	// The job is tracked: kind, target, and the starter.
	var kind, targetType, targetID string
	var userID *int64
	err := db.QueryRow(`SELECT kind, target_type, target_id, user_id FROM agent_jobs WHERE job_id = 'deadbeefcafe0001'`).Scan(&kind, &targetType, &targetID, &userID)
	if err != nil || kind != "Dump a_main" || targetType != "database" || targetID != "100" || userID == nil || *userID != 2 {
		t.Fatalf("agent_jobs row = %q %q %q %v %v", kind, targetType, targetID, userID, err)
	}
	// Audited with the output file.
	var target string
	var details string
	if err := db.QueryRow(`SELECT target_id, details FROM audit_logs WHERE action = 'database.dump'`).Scan(&target, &details); err != nil || target != "a_main" || !strings.Contains(details, "a_main-20260101T000000Z.sql.gz") {
		t.Fatalf("dump audit = %q %q %v", target, details, err)
	}
}

func TestDatabaseDumpForeignNotFound(t *testing.T) {
	h, sa, _ := dbUsersEnv(t, "")
	alice := loginAsUser(t, h, "alice")
	token, alice := siteFormToken(t, h, alice, "/databases")
	rr := postForm(h, "/databases/100/dump", url.Values{"_token": {token}}, alice) // bob's db
	alice = refreshCookie(alice, rr)
	flashAt(t, h, alice, "/databases", "Database not found.")
	if len(sa.called()) != 0 {
		t.Fatalf("agent called for a foreign database: %v", sa.called())
	}
}

func TestDatabaseImportStartsJob(t *testing.T) {
	h, sa, db := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	token, bob := siteFormToken(t, h, bob, "/databases")
	rr := postForm(h, "/databases/100/import", url.Values{
		"_token": {token}, "path": {"backups/dump.sql"}, "uploaded": {"0"},
	}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("import = %d", rr.Code)
	}
	bob = refreshCookie(bob, rr)
	page := get(t, h, "/databases", bob).Body.String()
	for _, want := range []string{"Import into a_main started.", "deadbeefcafe0002"} {
		if !strings.Contains(page, want) {
			t.Errorf("import flash missing %q:\\n%s", want, page)
		}
	}
	// The agent got the site domain and the path; no removal (not an upload).
	if got := agentParam(t, sa, "db.import_site", "domain"); got != "bob.example" {
		t.Fatalf("db.import_site domain = %v", got)
	}
	if got := agentParam(t, sa, "db.import_site", "path"); got != "backups/dump.sql" {
		t.Fatalf("db.import_site path = %v", got)
	}
	if got := agentParam(t, sa, "db.import_site", "remove_after"); got != false {
		t.Fatalf("db.import_site remove_after = %v", got)
	}
	// Tracked as an import job.
	var kind string
	if err := db.QueryRow(`SELECT kind FROM agent_jobs WHERE job_id = 'deadbeefcafe0002'`).Scan(&kind); err != nil || kind != "Import into a_main" {
		t.Fatalf("agent_jobs kind = %q %v", kind, err)
	}
	// Audited with the website and file.
	var details string
	if err := db.QueryRow(`SELECT details FROM audit_logs WHERE action = 'database.import'`).Scan(&details); err != nil || !strings.Contains(details, "bob.example") || !strings.Contains(details, "backups/dump.sql") {
		t.Fatalf("import audit details = %q %v", details, err)
	}
}

func TestDatabaseImportValidation(t *testing.T) {
	h, sa, _ := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	for _, path := range []string{"nope.txt", "../evil.sql", "sub/../../evil.sql.gz"} {
		token, bob := siteFormToken(t, h, bob, "/databases")
		rr := postForm(h, "/databases/100/import", url.Values{
			"_token": {token}, "path": {path}, "uploaded": {"0"},
		}, bob)
		bob = refreshCookie(bob, rr)
		flashAt(t, h, bob, "/databases", "Choose a .sql or .sql.gz file inside the website folder.")
	}
	if len(sa.called()) != 0 {
		t.Fatalf("agent called on invalid import paths: %v", sa.called())
	}
}

func TestDatabaseImportUploadRemovedAfter(t *testing.T) {
	h, sa, _ := dbUsersEnv(t, "")
	bob := loginAsUser(t, h, "bob")
	// A panel-named upload (the shape the file-upload JS produces) is removed
	// afterwards; a same-looking path that is not an upload is not.
	token, bob := siteFormToken(t, h, bob, "/databases")
	rr := postForm(h, "/databases/100/import", url.Values{
		"_token": {token}, "path": {"tmp/whacp-import-abcdef0123456789.sql"}, "uploaded": {"1"},
	}, bob)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("import upload = %d", rr.Code)
	}
	if got := agentParam(t, sa, "db.import_site", "remove_after"); got != true {
		t.Fatalf("upload remove_after = %v", got)
	}
	token, bob = siteFormToken(t, h, bob, "/databases")
	rr = postForm(h, "/databases/100/import", url.Values{
		"_token": {token}, "path": {"tmp/whacp-import-abcdef0123456789.sql"}, "uploaded": {"0"},
	}, bob)
	if got := agentParam(t, sa, "db.import_site", "remove_after"); got != false {
		t.Fatalf("non-upload remove_after = %v", got)
	}
}

func TestDatabaseImportOnlySiteDatabases(t *testing.T) {
	h, sa, _ := dbUsersEnv(t, "")
	// c_main (102) has no website — importing into it is refused.
	admin := loginAsUser(t, h, "admin")
	token, admin := siteFormToken(t, h, admin, "/databases")
	rr := postForm(h, "/databases/102/import", url.Values{
		"_token": {token}, "path": {"backups/dump.sql"}, "uploaded": {"0"},
	}, admin)
	admin = refreshCookie(admin, rr)
	flashAt(t, h, admin, "/databases", "Only a database of one of your websites can be imported into.")
	// A foreign database (out of scope) is refused the same way.
	alice := loginAsUser(t, h, "alice")
	token, alice = siteFormToken(t, h, alice, "/databases")
	rr = postForm(h, "/databases/100/import", url.Values{
		"_token": {token}, "path": {"backups/dump.sql"}, "uploaded": {"0"},
	}, alice)
	alice = refreshCookie(alice, rr)
	flashAt(t, h, alice, "/databases", "Only a database of one of your websites can be imported into.")
	if len(sa.called()) != 0 {
		t.Fatalf("agent called for refused imports: %v", sa.called())
	}
}
