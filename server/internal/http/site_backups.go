package http

// This file ports the per-website backups area (the website methods of
// BackupsController + the sites/backups view): the tab page
// (GET /sites/{id}/backups), starting a backup (POST /sites/{id}/backups),
// restoring parts (POST .../backups/{backup}/restore), deleting
// (POST .../backups/{backup}/delete) and streaming a stored part
// (GET .../backups/{backup}/download). The automatic-backup schedules
// (POST /sites/{id}/backup-schedules ...) are a later batch; the page
// already lists the schedules a website has.
//
// Routes:
//   GET  /sites/{id}/backups                    (backups.view)
//   POST /sites/{id}/backups                    (backups.manage)
//   POST /sites/{id}/backups/{backup}/restore   (backups.manage)
//   POST /sites/{id}/backups/{backup}/delete    (backups.manage)
//   GET  /sites/{id}/backups/{backup}/download  (backups.manage)
//
// A hosting account that has not been granted the auto_backups feature
// never sees the scheduled backup copies (or the schedules) — the same
// hidesAutoBackups rule the panel applies.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// backupsSiteOf resolves the website for a /sites/{id} route (nil = not
// found or no access for the signed-in user).
func (s *Server) backupsSiteOf(r *http.Request, id string) *hosting.Website {
	user := userFrom(r.Context())
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), n, user)
	if err != nil || site == nil {
		return nil
	}
	return site
}

// backupsAudit binds the audit hook for the site's backup actions.
func (s *Server) backupsAudit(r *http.Request, user *identity.User) hosting.Auditor {
	return func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		entry := audit.Entry{
			Action:     action,
			TargetType: targetType,
			TargetID:   target,
			Result:     result,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    details,
		}
		if user != nil {
			uid := user.ID
			entry.UserID = &uid
			entry.Username = user.Username
		}
		s.audit.Record(ctx, entry)
	}
}

func (s *Server) backupsService(r *http.Request, user *identity.User) *hosting.BackupService {
	return hosting.NewBackupService(s.db, s.agent, s.crypto, s.currentServerID(r), s.backupsAudit(r, user))
}

func (s *Server) backupsScheduler(r *http.Request) *hosting.BackupScheduler {
	return hosting.NewBackupScheduler(s.db, s.currentServerID(r))
}

// hidesAutoBackups reports whether the signed-in hosting account must not
// see the scheduled (automatic) backups (PHP's BackupsController::
// hidesAutoBackups).
func (s *Server) hidesAutoBackups(r *http.Request, user *identity.User) bool {
	if user == nil || user.RoleScope != "own" {
		return false
	}
	ok, err := s.features.FeatureEnabled(r.Context(), user, "auto_backups")
	return err == nil && !ok
}

func (s *Server) backupsFlashNotFound(w http.ResponseWriter, r *http.Request) {
	sessionFrom(r.Context()).Flash("error", "Website not found.")
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

// backupsBack redirects to the site's backups tab, flashing msg when set.
func (s *Server) backupsBack(w http.ResponseWriter, r *http.Request, key, msg string) {
	sess := sessionFrom(r.Context())
	if msg != "" {
		sess.Flash(key, msg)
	}
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/backups", http.StatusSeeOther)
}

// backupsFlashErr flashes the failure (the user-facing message when there
// is one, the agent's otherwise) and redirects back.
func (s *Server) backupsFlashErr(w http.ResponseWriter, r *http.Request, err error) {
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		s.backupsBack(w, r, "error", ue.What)
		return
	}
	var ae *agent.Error
	msg := "The operation failed; nothing was changed."
	if errors.As(err, &ae) && ae.Message != "" {
		msg = ae.Message
	}
	s.log.Error("backup action", "error", err)
	s.backupsBack(w, r, "error", msg)
}

// backupFlag mirrors PHP's input('x') === '1' for the panel's
// hidden-0 + checkbox-1 pairs: a checked box makes the browser submit
// both values (0 then 1) and PHP reads the last one, so "checked" wins.
func backupFlag(r *http.Request, name string) bool {
	for _, v := range r.Form[name] {
		if v == "1" {
			return true
		}
	}
	return false
}

// backupsCSRF validates the form token (the panel's forms all carry it).
func (s *Server) backupsCSRF(w http.ResponseWriter, r *http.Request) bool {
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}

// actorID is the signed-in user's id for the backups rows' user_id.
func actorID(user *identity.User) sql.NullInt64 {
	if user == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Valid: true, Int64: user.ID}
}

// userIDPtr is the actor for the job tracker (nil = unattended).
func userIDPtr(user *identity.User) *int64 {
	if user == nil {
		return nil
	}
	return &user.ID
}

// ---------------------------------------------------------------------------
// The tab page (BackupsController::show).
// ---------------------------------------------------------------------------

// siteBackupsShow renders GET /sites/{id}/backups.
func (s *Server) siteBackupsShow(w http.ResponseWriter, r *http.Request) {
	site := s.backupsSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.backupsFlashNotFound(w, r)
		return
	}
	ctx := r.Context()
	user := userFrom(r.Context())
	svc := s.backupsService(r, user)
	// Best effort: finished agent jobs are promoted to completed/failed.
	if err := svc.Refresh(ctx); err != nil {
		s.log.Error("backups refresh", "error", err)
	}
	backups, err := svc.ListSite(ctx, site.ID)
	if err != nil {
		s.log.Error("backups list", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	schedules, err := s.backupsScheduler(r).ListSite(ctx, site.ID)
	if err != nil {
		s.log.Error("backup schedules list", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	hideAuto := s.hidesAutoBackups(r, user)
	if hideAuto {
		kept := backups[:0]
		for _, b := range backups {
			if !b.ScheduleID.Valid {
				kept = append(kept, b)
			}
		}
		backups = kept
		schedules = []hosting.BackupSchedule{}
	}
	var storages []hosting.Storage
	if user != nil && user.RoleScope == "all" {
		storages, err = svc.Storages(ctx)
		if err != nil {
			s.log.Error("backups storages", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	dbs, err := svc.SiteDatabases(ctx, site.ID)
	if err != nil {
		s.log.Error("backups site databases", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The newest completed backup, for the "Last backup …" line.
	lastAgo := ""
	for i := range backups {
		if backups[i].Status == "completed" {
			lastAgo = timeAgo(backups[i].CreatedAt)
			break
		}
	}
	sess := sessionFrom(r.Context())
	jobMsg, _ := sess.PullFlash("job")
	okMsg, _ := sess.PullFlash("success")
	errMsg, _ := sess.PullFlash("error")
	s.render(w, "site_backups", map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sess),
		"Site":          site,
		"Backups":       backups,
		"Schedules":     schedules,
		"Storages":      storages,
		"StorageLabels": hosting.StorageLabels,
		"Databases":     dbs,
		"LastAgo":     lastAgo,
		"CanManage":   s.can(r, "backups.manage"),
		"CanSchedule": user != nil && user.RoleScope == "all",
		"Job":         jobMsg,
		"Success":     okMsg,
		"Error":       errMsg,
	})
}

// ---------------------------------------------------------------------------
// Back up now (BackupsController::create).
// ---------------------------------------------------------------------------

// siteBackupsCreate starts a backup job (POST /sites/{id}/backups).
func (s *Server) siteBackupsCreate(w http.ResponseWriter, r *http.Request) {
	if !s.backupsCSRF(w, r) {
		return
	}
	site := s.backupsSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.backupsFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	// encrypt / keep_local default to on (PHP's input('encrypt', '1')) —
	// the form always sends both values, so absence means "1".
	encrypt, keepLocal := true, true
	if _, ok := r.Form["encrypt"]; ok && !backupFlag(r, "encrypt") {
		encrypt = false
	}
	if _, ok := r.Form["keep_local"]; ok && !backupFlag(r, "keep_local") {
		keepLocal = false
	}
	opts := hosting.CreateSiteOptions{
		Files:     backupFlag(r, "files"),
		Databases: backupFlag(r, "databases"),
		Encrypt:   encrypt,
		KeepLocal: keepLocal,
		Label:     r.FormValue("label"),
	}
	// Remote storage is configured by administrators for the whole server.
	if user != nil && user.RoleScope == "all" {
		if id, err := strconv.ParseInt(r.FormValue("storage_id"), 10, 64); err == nil && id > 0 {
			opts.StorageID = sql.NullInt64{Valid: true, Int64: id}
		}
	}
	job, err := s.backupsService(r, user).CreateSite(r.Context(), site, opts, actorID(user))
	if err != nil {
		s.backupsFlashErr(w, r, err)
		return
	}
	if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Backup "+site.Domain, "website", fmt.Sprintf("%d", site.ID), userIDPtr(user)); err != nil {
		s.log.Error("track backup job", "job", job, "error", err)
	}
	sess := sessionFrom(r.Context())
	sess.Flash("job", job)
	s.backupsBack(w, r, "success", "Backup started.")
}

// ---------------------------------------------------------------------------
// Restore (BackupsController::restore).
// ---------------------------------------------------------------------------

// siteBackupsRestore restores parts of one backup (POST .../restore).
func (s *Server) siteBackupsRestore(w http.ResponseWriter, r *http.Request) {
	if !s.backupsCSRF(w, r) {
		return
	}
	site := s.backupsSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.backupsFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsService(r, user)
	backupID, err := strconv.ParseInt(r.PathValue("backup"), 10, 64)
	if err != nil {
		s.backupsBack(w, r, "error", "Backup not found.")
		return
	}
	b, err := svc.FindSite(r.Context(), site.ID, backupID)
	if err != nil {
		s.log.Error("backups find", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if b == nil || (s.hidesAutoBackups(r, user) && b.ScheduleID.Valid) {
		s.backupsBack(w, r, "error", "Backup not found.")
		return
	}
	// The checkboxes submit restore_databases[] once per checked database.
	var restoreDBs []string
	for _, v := range r.PostForm["restore_databases[]"] {
		if v != "" {
			restoreDBs = append(restoreDBs, v)
		}
	}
	job, err := svc.RestoreSite(r.Context(), site, b, backupFlag(r, "restore_files"), restoreDBs, r.FormValue("confirm_domain"))
	if err != nil {
		s.backupsFlashErr(w, r, err)
		return
	}
	if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Restore "+site.Domain, "website", fmt.Sprintf("%d", site.ID), userIDPtr(user)); err != nil {
		s.log.Error("track restore job", "job", job, "error", err)
	}
	sess := sessionFrom(r.Context())
	sess.Flash("job", job)
	s.backupsBack(w, r, "success", "Restore started. A safety backup of the current files is taken first.")
}

// ---------------------------------------------------------------------------
// Delete (BackupsController::delete).
// ---------------------------------------------------------------------------

// siteBackupsDelete deletes one backup (POST .../delete).
func (s *Server) siteBackupsDelete(w http.ResponseWriter, r *http.Request) {
	if !s.backupsCSRF(w, r) {
		return
	}
	site := s.backupsSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.backupsFlashNotFound(w, r)
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsService(r, user)
	backupID, err := strconv.ParseInt(r.PathValue("backup"), 10, 64)
	if err != nil {
		s.backupsBack(w, r, "error", "Backup not found.")
		return
	}
	b, err := svc.FindSite(r.Context(), site.ID, backupID)
	if err != nil {
		s.log.Error("backups find", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if b == nil || (s.hidesAutoBackups(r, user) && b.ScheduleID.Valid) {
		s.backupsBack(w, r, "error", "Backup not found.")
		return
	}
	if err := svc.Delete(r.Context(), b); err != nil {
		s.backupsFlashErr(w, r, err)
		return
	}
	s.backupsBack(w, r, "success", "Backup deleted.")
}

// ---------------------------------------------------------------------------
// Download (BackupsController::download + stream).
// ---------------------------------------------------------------------------

// siteBackupsDownload streams one stored part of a backup
// (GET .../download?file=...).
func (s *Server) siteBackupsDownload(w http.ResponseWriter, r *http.Request) {
	site := s.backupsSiteOf(r, r.PathValue("id"))
	user := userFrom(r.Context())
	backupID, err := strconv.ParseInt(r.PathValue("backup"), 10, 64)
	var b *hosting.Backup
	if site != nil && err == nil {
		if b, err = s.backupsService(r, user).FindSite(r.Context(), site.ID, backupID); err != nil {
			s.log.Error("backups find", "error", err)
		}
	}
	if b == nil || (s.hidesAutoBackups(r, user) && b.ScheduleID.Valid) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Not found"))
		return
	}
	file := r.URL.Query().Get("file")
	first, next, err := s.backupsService(r, user).Download(r.Context(), b, file)
	if err != nil {
		s.backupsBack(w, r, "error", backupUserMessage(err))
		return
	}
	name := sanitizeDownloadName(b.Domain + "-" + b.BackupID + "-" + file)
	if name == "" {
		name = "backup"
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Content-Length", strconv.FormatInt(first.Size, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	chunk := first
	offset := int64(0)
	flusher, _ := w.(http.Flusher)
	for {
		if _, err := w.Write(chunk.Data); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		offset += int64(len(chunk.Data))
		if chunk.EOF || len(chunk.Data) == 0 {
			break
		}
		chunk, err = next(offset)
		if err != nil {
			s.log.Error("backup download", "error", err)
			return
		}
	}
}

// userErrorMessage extracts the user-facing message of an error.
func backupUserMessage(err error) string {
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		return ue.What
	}
	var ae *agent.Error
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	return "The operation failed; nothing was changed."
}

// sanitizeDownloadName is the PHP's preg_replace('/[^A-Za-z0-9._-]/','_').
func sanitizeDownloadName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
