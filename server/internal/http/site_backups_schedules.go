package http

// This file ports the website schedule routes (panel BackupsController
// storeSchedule / deleteSchedule, PHP BackupScheduler::create / delete):
// an administrator can add or remove the automatic backup schedule of one
// website. Existing backups are never touched when a schedule is removed
// (the panel's note, and its delete's audit trail).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
)

// siteBackupsSchedule adds a schedule (POST /sites/{id}/backup-schedules).
func (s *Server) siteBackupsSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.backupsCSRF(w, r) {
		return
	}
	user := userFrom(r.Context())
	// PHP checks Scope::isAll before resolving anything.
	if user == nil || user.RoleScope != "all" {
		s.backupsBack(w, r, "error", "Automatic backups are set up by your administrator.")
		return
	}
	site := s.backupsSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.backupsFlashNotFound(w, r)
		return
	}
	id, err := s.backupsScheduler(r, user).CreateSite(r.Context(), site, backupScheduleInput(r))
	if err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			// PHP's backWithErrors: re-show the page with field errors and
			// the old input (the page picks these up on the follow-up GET).
			b, _ := json.Marshal(ve.Fields)
			ob, _ := json.Marshal(backupScheduleOld(r))
			sess := sessionFrom(r.Context())
			sess.Flash("sched_errors", string(b))
			sess.Flash("sched_old", string(ob))
			http.Redirect(w, r, "/sites/"+strconv.FormatInt(site.ID, 10)+"/backups", http.StatusSeeOther)
			return
		}
		s.backupsFlashErr(w, r, err)
		return
	}
	_ = id
	s.backupsBack(w, r, "success", "Schedule added.")
}

// siteBackupsScheduleDelete removes a schedule
// (POST /sites/{id}/backup-schedules/{schedule}/delete).
func (s *Server) siteBackupsScheduleDelete(w http.ResponseWriter, r *http.Request) {
	if !s.backupsCSRF(w, r) {
		return
	}
	user := userFrom(r.Context())
	if user == nil || user.RoleScope != "all" {
		s.backupsBack(w, r, "error", "Automatic backups are set up by your administrator.")
		return
	}
	site := s.backupsSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.backupsFlashNotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("schedule"), 10, 64)
	if err != nil || id <= 0 {
		s.backupsBack(w, r, "error", "Schedule not found.")
		return
	}
	if err := s.backupsScheduler(r, user).DeleteSite(r.Context(), site.ID, id); err != nil {
		s.backupsFlashErr(w, r, err)
		return
	}
	s.backupsBack(w, r, "success", "Schedule removed. Existing backups are kept.")
}

// backupScheduleInput parses the "Add schedule" form with PHP's fallbacks
// (create()'s $input ?? defaults; (int) casts; filter_var BOOL on the
// hidden-0 + checkbox-1 pairs).
func backupScheduleInput(r *http.Request) hosting.ScheduleInput {
	in := hosting.ScheduleInput{
		Frequency:        "daily",
		Hour:             3,
		Weekday:          0,
		Retention:        7,
		IncludeFiles:     true,
		IncludeDatabases: true,
		Encrypt:          true,
		KeepLocal:        true,
	}
	if v, ok := r.Form["frequency"]; ok {
		in.Frequency = v[0] // present but empty stays "" (PHP: ?? only for absent)
	}
	in.Hour = backupScheduleInt(r, "hour", in.Hour)
	in.Weekday = backupScheduleInt(r, "weekday", in.Weekday)
	in.Retention = backupScheduleInt(r, "retention", in.Retention)
	if v, err := strconv.ParseInt(r.FormValue("storage_id"), 10, 64); err == nil && v != 0 {
		in.StorageID = sql.NullInt64{Int64: v, Valid: true}
	}
	in.IncludeFiles = backupScheduleFlag(r, "include_files")
	in.IncludeDatabases = backupScheduleFlag(r, "include_databases")
	in.Encrypt = backupScheduleFlag(r, "encrypt")
	in.KeepLocal = backupScheduleFlag(r, "keep_local")
	return in
}

// backupScheduleInt mirrors PHP's (int)($input[$field] ?? $def): an
// absent field keeps the default, a present field that does not parse
// becomes 0 (like PHP's (int)"" and (int)"abc").
func backupScheduleInt(r *http.Request, field string, def int) int {
	v, ok := r.Form[field]
	if !ok {
		return def
	}
	if n, err := strconv.Atoi(v[0]); err == nil {
		return n
	}
	return 0
}

// backupScheduleFlag mirrors PHP's filter_var($input[$field] ?? true,
// FILTER_VALIDATE_BOOL) for the hidden-0 + checkbox-1 pairs.
func backupScheduleFlag(r *http.Request, field string) bool {
	_, ok := r.Form[field]
	if !ok {
		return true
	}
	return backupFlag(r, field)
}

// backupScheduleOld collects the submitted values the page should re-show
// after a validation failure (PHP's old input).
func backupScheduleOld(r *http.Request) map[string]string {
	old := map[string]string{}
	for _, field := range []string{"frequency", "hour", "weekday", "retention", "storage_id"} {
		if v, ok := r.Form[field]; ok && len(v) > 0 {
			old[field] = v[0]
		}
	}
	for _, field := range []string{"include_files", "include_databases", "encrypt", "keep_local"} {
		if _, ok := r.Form[field]; ok {
			if backupFlag(r, field) {
				old[field] = "1"
			} else {
				old[field] = "0"
			}
		}
	}
	return old
}
