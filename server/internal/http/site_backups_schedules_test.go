package http

// Tests for the website schedule routes (site_backups_schedules.go +
// hosting/backup_scheduler.go CreateSite/DeleteSite): adding a schedule
// (with the panel's validation: frequency / hour / weekday / retention /
// storage turned on / files-or-databases, old input re-shown on failure),
// removing one (existing backups kept, other sites' schedules untouched)
// and the administrator-only rule.

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
)

// bannerAdmin is the re-shown administrator-notice error flash.
const bannerAdmin = `<p class="alert alert-error" role="alert">Automatic backups are set up by your administrator.</p>`

// seedBackupStorage inserts one backup storage (config sealed with the env's
// zero panel key, as in TestSiteBackupsAdminStorage).
func seedBackupStorage(t *testing.T, db *sql.DB, id int64, name string, active int) {
	t.Helper()
	var key [32]byte
	c := crypto.NewWithKey(key)
	enc, err := c.Encrypt([]byte(`{"type":"sftp","host":"backups.example","username":"b"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO backup_storages (id,name,kind,config_enc,active,test_ok,test_error,created_at,tested_at)
		VALUES (?, ?, 'sftp', ?, ?, 1, '', '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z')`, id, name, enc, active); err != nil {
		t.Fatal(err)
	}
}

// backupScheduleSeed inserts one site schedule for a website and returns
// its id.
func backupScheduleSeed(t *testing.T, db *sql.DB, websiteID int64) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO backup_schedules (server_id, website_id, kind, all_sites, frequency, hour, weekday, include_files, include_databases, encrypt, storage_id, keep_local, retention, enabled, created_at)
		VALUES (1, ?, 'site', 0, 'daily', 3, 0, 1, 1, 1, NULL, 1, 7, 1, '2026-10-20T12:00:00Z')`, websiteID)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSiteBackupsScheduleStore(t *testing.T) {
	h, sa, db := backupWEnv(t)
	admin := loginAsUser(t, h, "admin")

	// The default form (a browser submits the hidden 0 plus the checked 1;
	// keep_local is submitted unchecked, but without a remote storage PHP
	// still keeps the copy on the server).
	rr := backupPost(t, h, admin, "/sites/10/backup-schedules", url.Values{
		"frequency":         {"daily"},
		"hour":              {"3"},
		"weekday":           {"0"},
		"retention":         {"7"},
		"include_files":     {"0", "1"},
		"include_databases": {"0", "1"},
		"encrypt":           {"0", "1"},
		"keep_local":        {"0"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST schedule = %d", rr.Code)
	}
	if calls := sa.called(); len(calls) != 0 {
		t.Errorf("agent calls = %v", calls)
	}
	var (
		kind, freq                                string
		websiteID, hour, weekday                  int
		files, dbs, enc, keep, enabled, retention int
		allSites                                  int
	)
	if err := db.QueryRow(`SELECT kind, frequency, website_id, hour, weekday, include_files,
		include_databases, encrypt, keep_local, enabled, retention, all_sites
		FROM backup_schedules WHERE server_id = 1`).
		Scan(&kind, &freq, &websiteID, &hour, &weekday, &files, &dbs, &enc, &keep, &enabled, &retention, &allSites); err != nil {
		t.Fatalf("schedule not stored: %v", err)
	}
	if kind != "site" || freq != "daily" || websiteID != 10 || hour != 3 || weekday != 0 ||
		files != 1 || dbs != 1 || enc != 1 || keep != 1 || enabled != 1 || retention != 7 || allSites != 0 {
		t.Errorf("row = %s %s site=%d %02d wd=%d f=%d d=%d e=%d k=%d en=%d r=%d all=%d",
			kind, freq, websiteID, hour, weekday, files, dbs, enc, keep, enabled, retention, allSites)
	}
	// The success flash and the new row are on the next GET.
	rr = get(t, h, "/sites/10/backups", admin)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET after create = %d", rr.Code)
	}
	body := rr.Body.String()
	if !pageHas(body, "Schedule added.") || !pageHas(body, "Every day at 03:00") ||
		!pageHas(body, "Add schedule") {
		t.Errorf("page missing the schedule: %s", body)
	}

	// A custom weekly schedule with a different day, hour and retention,
	// files only, unencrypted.
	rr = backupPost(t, h, admin, "/sites/10/backup-schedules", url.Values{
		"frequency":         {"weekly"},
		"hour":              {"5"},
		"weekday":           {"2"},
		"retention":         {"30"},
		"include_files":     {"0", "1"},
		"include_databases": {"0"},
		"encrypt":           {"0"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST custom schedule = %d", rr.Code)
	}
	var (
		freq2                                           string
		hour2, weekday2, files2, dbs2, enc2, retention2 int
	)
	if err := db.QueryRow(`SELECT frequency, hour, weekday, include_files, include_databases,
		encrypt, retention FROM backup_schedules WHERE website_id = 10 AND frequency = 'weekly'`).
		Scan(&freq2, &hour2, &weekday2, &files2, &dbs2, &enc2, &retention2); err != nil {
		t.Fatalf("custom schedule not stored: %v", err)
	}
	if hour2 != 5 || weekday2 != 2 || files2 != 1 || dbs2 != 0 || enc2 != 0 || retention2 != 30 {
		t.Errorf("custom row = %s %02d wd=%d f=%d d=%d e=%d r=%d", freq2, hour2, weekday2, files2, dbs2, enc2, retention2)
	}
	rr = get(t, h, "/sites/10/backups", admin)
	if !pageHas(rr.Body.String(), "Every Tuesday at 05:00") {
		t.Error("page missing the weekly schedule line")
	}
	if !pageHas(rr.Body.String(), "not encrypted") {
		t.Error("page missing the unencrypted note")
	}

	// With a remote storage, keep_local is honoured and the copy is shown.
	seedBackupStorage(t, db, 1, "Nightly", 1)
	rr = backupPost(t, h, admin, "/sites/10/backup-schedules", url.Values{
		"frequency":         {"daily"},
		"hour":              {"23"},
		"weekday":           {"0"},
		"retention":         {"7"},
		"include_files":     {"0", "1"},
		"include_databases": {"0", "1"},
		"encrypt":           {"0", "1"},
		"storage_id":        {"1"},
		"keep_local":        {"0"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST storage schedule = %d", rr.Code)
	}
	var (
		keep3     int
		storageID int64
	)
	if err := db.QueryRow(`SELECT keep_local, COALESCE(storage_id, -1) FROM backup_schedules
		WHERE website_id = 10 AND hour = 23`).Scan(&keep3, &storageID); err != nil {
		t.Fatalf("storage schedule not stored: %v", err)
	}
	if keep3 != 0 || storageID != 1 {
		t.Errorf("storage row = keep_local %d storage %d", keep3, storageID)
	}
	rr = get(t, h, "/sites/10/backups", admin)
	if !pageHas(rr.Body.String(), "copy to Nightly") {
		t.Error("page missing the storage copy note")
	}
}

func TestSiteBackupsScheduleStoreValidation(t *testing.T) {
	h, _, db := backupWEnv(t)
	admin := loginAsUser(t, h, "admin")

	// Each bad field re-shows the page with its message (PHP's
	// backWithErrors) and stores nothing.
	cases := []struct {
		name string
		form url.Values
		msg  string
	}{
		{"frequency", url.Values{"frequency": {"monthly"}, "hour": {"3"}, "weekday": {"0"}, "retention": {"7"},
			"include_files": {"1"}, "include_databases": {"1"}, "encrypt": {"1"}}, "Choose daily or weekly."},
		{"hour", url.Values{"frequency": {"daily"}, "hour": {"25"}, "weekday": {"0"}, "retention": {"7"},
			"include_files": {"1"}, "include_databases": {"1"}, "encrypt": {"1"}}, "Choose an hour between 0 and 23."},
		{"weekday", url.Values{"frequency": {"weekly"}, "hour": {"3"}, "weekday": {"7"}, "retention": {"7"},
			"include_files": {"1"}, "include_databases": {"1"}, "encrypt": {"1"}}, "Choose a weekday."},
		{"retention", url.Values{"frequency": {"daily"}, "hour": {"3"}, "weekday": {"0"}, "retention": {"0"},
			"include_files": {"1"}, "include_databases": {"1"}, "encrypt": {"1"}}, "Keep between 1 and 365 backups."},
		{"nothing", url.Values{"frequency": {"daily"}, "hour": {"3"}, "weekday": {"0"}, "retention": {"7"},
			"include_files": {"0"}, "include_databases": {"0"}, "encrypt": {"1"}}, "Back up the files, the databases or both."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := backupPost(t, h, admin, "/sites/10/backup-schedules", tc.form)
			if rr.Code != http.StatusSeeOther {
				t.Fatalf("POST = %d", rr.Code)
			}
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM backup_schedules`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("schedules stored = %d", n)
			}
			rr = get(t, h, "/sites/10/backups", admin)
			if rr.Code != http.StatusOK {
				t.Fatalf("GET = %d", rr.Code)
			}
			body := rr.Body.String()
			if !pageHas(body, tc.msg) {
				t.Errorf("missing %q in the re-shown form", tc.msg)
			}
		})
	}

	// The old input is kept in the re-shown form (the retention value).
	rr := backupPost(t, h, admin, "/sites/10/backup-schedules", url.Values{
		"frequency": {"daily"}, "hour": {"25"}, "weekday": {"0"}, "retention": {"30"},
		"include_files": {"1"}, "include_databases": {"1"}, "encrypt": {"1"},
	})
	rr = get(t, h, "/sites/10/backups", admin)
	if !pageHas(rr.Body.String(), `value="30"`) || !pageHas(rr.Body.String(), "Choose an hour between 0 and 23.") {
		t.Error("old input not kept on the re-shown form")
	}

	// A storage that is off (or unknown) cannot be chosen.
	seedBackupStorage(t, db, 2, "Cold", 0)
	rr = backupPost(t, h, admin, "/sites/10/backup-schedules", url.Values{
		"frequency": {"daily"}, "hour": {"3"}, "weekday": {"0"}, "retention": {"7"},
		"include_files": {"1"}, "include_databases": {"1"}, "encrypt": {"1"},
		"storage_id": {"2"},
	})
	rr = get(t, h, "/sites/10/backups", admin)
	if !pageHas(rr.Body.String(), "Choose a storage that is turned on.") {
		t.Error("missing the turned-off storage error")
	}
	rr = backupPost(t, h, admin, "/sites/10/backup-schedules", url.Values{
		"frequency": {"daily"}, "hour": {"3"}, "weekday": {"0"}, "retention": {"7"},
		"include_files": {"1"}, "include_databases": {"1"}, "encrypt": {"1"},
		"storage_id": {"99"},
	})
	rr = get(t, h, "/sites/10/backups", admin)
	if !pageHas(rr.Body.String(), "Choose a storage that is turned on.") {
		t.Error("missing the unknown storage error")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM backup_schedules`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("schedules stored = %d", n)
	}
}

func TestSiteBackupsScheduleStoreNonAdmin(t *testing.T) {
	h, _, db := backupWEnv(t)

	// A hosting account with backups.manage still cannot schedule.
	bob := loginAsUser(t, h, "bob")
	rr := backupPost(t, h, bob, "/sites/10/backup-schedules", url.Values{
		"frequency": {"daily"}, "hour": {"3"}, "weekday": {"0"}, "retention": {"7"},
		"include_files": {"1"}, "include_databases": {"1"}, "encrypt": {"1"},
	})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST (bob) = %d", rr.Code)
	}
	rr = get(t, h, "/sites/10/backups", bob)
	if !pageHas(rr.Body.String(), bannerAdmin) {
		t.Error("missing the administrator notice flash")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM backup_schedules`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("schedules stored = %d", n)
	}

	// A viewer without backups.manage gets a 403 at the route guard.
	carol := loginAsUser(t, h, "carol")
	token, carol := siteFormToken(t, h, carol, "/sites/20/backups")
	rr = postForm(h, "/sites/20/backup-schedules", url.Values{"_token": {token}, "frequency": {"daily"}}, carol)
	if rr.Code != http.StatusForbidden {
		t.Errorf("POST (carol) = %d", rr.Code)
	}
}

func TestSiteBackupsScheduleDelete(t *testing.T) {
	h, sa, db := backupWEnv(t)
	admin := loginAsUser(t, h, "admin")
	id := backupScheduleSeed(t, db, 10)
	// A schedule of another website (carol's) — deleting it from bob's page
	// must not touch it.
	other := backupScheduleSeed(t, db, 20)

	rr := backupPost(t, h, admin, fmt.Sprintf("/sites/10/backup-schedules/%d/delete", id), url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST delete = %d", rr.Code)
	}
	if calls := sa.called(); len(calls) != 0 {
		t.Errorf("agent calls = %v", calls)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM backup_schedules WHERE id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("schedule still stored")
	}
	rr = get(t, h, "/sites/10/backups", admin)
	if !pageHas(rr.Body.String(), "Schedule removed. Existing backups are kept.") {
		t.Error("missing the removal flash")
	}

	// Unknown and other-website schedules: "Schedule not found.", untouched.
	rr = backupPost(t, h, admin, "/sites/10/backup-schedules/999/delete", url.Values{})
	rr = get(t, h, "/sites/10/backups", admin)
	if !pageHas(rr.Body.String(), "Schedule not found.") {
		t.Error("missing the not-found flash (unknown id)")
	}
	rr = backupPost(t, h, admin, fmt.Sprintf("/sites/10/backup-schedules/%d/delete", other), url.Values{})
	rr = get(t, h, "/sites/10/backups", admin)
	if !pageHas(rr.Body.String(), "Schedule not found.") {
		t.Error("missing the not-found flash (other website)")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM backup_schedules WHERE id = ?`, other).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("another website's schedule was deleted")
	}

	// A hosting account cannot remove a schedule.
	bob := loginAsUser(t, h, "bob")
	rr = backupPost(t, h, bob, fmt.Sprintf("/sites/10/backup-schedules/%d/delete", other), url.Values{})
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("POST delete (bob) = %d", rr.Code)
	}
	rr = get(t, h, "/sites/10/backups", bob)
	if !pageHas(rr.Body.String(), bannerAdmin) {
		t.Error("missing the administrator notice flash")
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM backup_schedules WHERE id = ?`, other).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("schedule deleted by a hosting account")
	}
}
