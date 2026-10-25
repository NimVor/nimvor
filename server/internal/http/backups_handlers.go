package http

// The server-wide backups area (BackupsController, PHP 541 LOC):
// the index page (website + server + storages + account files tabs),
// server configuration backups (the encrypted server backups), remote
// storage administration (S3/SFTP/FTPS/Google Drive/local), the encryption
// key (download with re-auth, import), and the server backup schedules.
//
// Routes (18):
//   GET  /backups                                          (backups.view)
//   POST /backups/server                                   (backups.manage)
//   POST /backups/server/{backup}/delete                  (backups.manage)
//   GET  /backups/server/{backup}/download                (backups.manage)
//   POST /backups/server/{backup}/restore                 (server.manage)
//   POST /backups/storages                                 (backups.manage)
//   POST /backups/storages/{storage}                       (backups.manage)
//   POST /backups/storages/{storage}/active               (backups.manage)
//   POST /backups/storages/{storage}/delete               (backups.manage)
//   POST /backups/storages/{storage}/scan                 (backups.manage)
//   POST /backups/storages/{storage}/test                 (backups.manage)
//   GET  /backups/storages/confirm                        (backups.manage)
//   POST /backups/storages/confirm                        (backups.manage)
//   GET  /backups/storages/google/callback                (backups.manage)
//   POST /backups/key/download                             (backups.manage)
//   POST /backups/key/import                               (backups.manage)
//   POST /backups/schedules                                (backups.manage)
//   POST /backups/schedules/{schedule}/delete             (backups.manage)

import (
	crand "crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

func crandRead(p []byte) (int, error) { return crand.Read(p) }

func auditEntry(action, targetType, targetID, result string, r *http.Request, user *identity.User, details map[string]any) audit.Entry {
	e := audit.Entry{Action: action, TargetType: targetType, TargetID: targetID, Result: result, IP: clientIP(r), UserAgent: r.UserAgent(), Details: details}
	if user != nil {
		e.UserID = &user.ID
		e.Username = user.Username
	}
	return e
}

const backupPending = "backup_storage_pending"

// backupsService builds the backup service for this request.
func (s *Server) backupsAdminService(r *http.Request, user *identity.User) *hosting.BackupService {
	return hosting.NewBackupService(s.db, s.agent, s.crypto, s.currentServerID(r), s.backupsAudit(r, user))
}

func (s *Server) backupSched(r *http.Request, user *identity.User) *hosting.BackupScheduler {
	return hosting.NewBackupScheduler(s.db, s.currentServerID(r), s.backupsAudit(r, user))
}

func (s *Server) accountBackupSvc(r *http.Request, user *identity.User) *hosting.AccountBackupService {
	return hosting.NewAccountBackupService(s.db, s.agent, s.currentServerID(r), s.backupsAudit(r, user))
}

// backupsIsAdmin reports whether the user is a server admin (Scope::isAll).
func backupsIsAdmin(u *identity.User) bool {
	return u != nil && u.RoleScope == "all"
}

// backupsAdminOnly redirects non-admins.
func (s *Server) backupsAdminOnly(w http.ResponseWriter, r *http.Request, to string) bool {
	if backupsIsAdmin(userFrom(r.Context())) {
		return false
	}
	sess := sessionFrom(r.Context())
	sess.Flash("error", "Only administrators can manage server backups and storage.")
	http.Redirect(w, r, to, http.StatusSeeOther)
	return true
}

// backupsIndex renders GET /backups.
func (s *Server) backupsIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := userFrom(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	svc := s.backupsAdminService(r, user)
	// Best effort refresh
	if err := svc.Refresh(ctx); err != nil {
		s.log.Error("backups refresh", "error", err)
	}
	isAdmin := backupsIsAdmin(user)
	// Website backups visible to this user.
	var (
		backups []hosting.Backup
		err     error
	)
	if isAdmin {
		backups, err = svc.ListAllSite(ctx, 200)
	} else {
		cond, args := hosting.Condition(user, "w.owner_id")
		backups, err = svc.ListForUserSQL(ctx, s.currentServerID(r), cond, args, 200)
	}
	if err != nil {
		s.log.Error("backups list", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Hide auto backups for own-scope without feature.
	if !isAdmin && s.hidesAutoBackups(r, user) {
		tmp := backups[:0]
		for _, b := range backups {
			if !b.ScheduleID.Valid {
				tmp = append(tmp, b)
			}
		}
		backups = tmp
	}
	// Server backups, schedules, storages, key, account files only for admin.
	var (
		serverBackups []hosting.Backup
		schedules     []hosting.BackupSchedule
		storages      []hosting.StorageDetailed
		keyInfo       *hosting.KeyInfo
		panelURL      string
		accountFiles  []hosting.AccountFile
		accounts      []hosting.AccountForBackup
	)
	if isAdmin {
		serverBackups, _ = svc.ListServer(ctx)
		schedules, _ = s.backupSched(r, user).ListServer(ctx)
		storages, _ = svc.StoragesDetailed(ctx)
		keyInfo = svc.KeyInfo(ctx)
		panelURL, _ = s.settings.String(ctx, "panel_url", "")
		accountFiles = s.accountBackupSvc(r, user).Files(ctx)
		accounts = s.accountBackupSvc(r, user).Accounts(ctx)
	}
	sess := sessionFrom(r.Context())
	mk := map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sess),
		"Admin":         isAdmin,
		"Backups":       backups,
		"Schedules":     schedules,
		"ServerBackups": serverBackups,
		"Storages":      storages,
		"KeyInfo":       keyInfo,
		"PanelURL":      panelURL,
		"AccountFiles":  accountFiles,
		"Accounts":      accounts,
		"IsAdminRole":   user.RoleName == "admin",
		"CanManage":     s.can(r, "backups.manage"),
		"StorageTypes":  hosting.StorageTypes,
		"Success":       mustPullFlash(sess, "success"),
		"Error":         mustPullFlash(sess, "error"),
		"Errors":        mustJSONFlash(sess, "errors"),
		"Old":           mustJSONFlash(sess, "old"),
	}
	// Sub-tab flash for storage confirmation? Already handled in confirm page.
	s.render(w, "backups_index", mk)
}

func mustPullFlash(sess *session.Session, k string) string {
	v, _ := sess.PullFlash(k)
	return v
}

func mustJSONFlash(sess *session.Session, k string) map[string]string {
	raw, ok := sess.PullFlash(k)
	if !ok || raw == "" {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]string{}
	}
	return m
}

func (s *Server) backupsHidesAuto(r *http.Request, user *identity.User) bool {
	if user == nil || user.RoleScope != "own" {
		return false
	}
	ok, err := s.features.FeatureEnabled(r.Context(), user, "auto_backups")
	return err == nil && !ok
}

// adminAct wraps a privileged admin operation: Scope::isAll check, validation/
// user-facing error handling, job flashing.
func (s *Server) backupAdminAct(w http.ResponseWriter, r *http.Request, fn func() (string, string, error)) {
	if s.backupsAdminOnly(w, r, "/backups#server") {
		return
	}
	msg, job, err := fn()
	if err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			sess := sessionFrom(r.Context())
			bj, _ := json.Marshal(ve.Fields)
			bo, _ := json.Marshal(r.Form)
			// Store for next render to reopen modal
			sess.Flash("errors", string(bj))
			sess.Flash("old", string(bo))
			http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
			return
		}
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#server")
			return
		}
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			s.backupFlashError(w, r, ae.Message, "/backups#server")
			return
		}
		s.log.Error("backup admin act", "error", err)
		s.backupFlashError(w, r, "The operation failed; nothing was changed.", "/backups#server")
		return
	}
	sess := sessionFrom(r.Context())
	if job != "" {
		sess.Flash("job", job)
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, "/backups#server", http.StatusSeeOther)
}

func (s *Server) backupFlashError(w http.ResponseWriter, r *http.Request, msg, to string) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// backupNeedCSRF is the CSRF guard for POST routes.
func (s *Server) backupNeedCSRF(w http.ResponseWriter, r *http.Request) bool {
	_ = r.ParseForm()
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}

// backupsCreateServer handles POST /backups/server.
func (s *Server) backupsCreateServer(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	opts := hosting.ServerCreateOptions{
		Label:   r.FormValue("label"),
		Encrypt: true,
	}
	if sid := r.FormValue("storage_id"); sid != "" {
		if id, err := strconv.ParseInt(sid, 10, 64); err == nil && id > 0 {
			opts.StorageID = sql.NullInt64{Valid: true, Int64: id}
			opts.KeepLocal = r.FormValue("keep_local") == "1"
		}
	}
	// keep_local defaults to true when no remote
	if !opts.StorageID.Valid {
		opts.KeepLocal = true
	}
	s.backupAdminAct(w, r, func() (string, string, error) {
		job, err := svc.CreateServer(r.Context(), opts, actorID(user))
		if err != nil {
			return "", "", err
		}
		if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Server configuration backup", "server", fmt.Sprintf("%d", s.currentServerID(r)), userIDPtr(user)); err != nil {
			s.log.Error("track backup job", "error", err)
		}
		return "Server configuration backup started.", job, nil
	})
}

// backupsDeleteServer handles POST /backups/server/{backup}/delete.
func (s *Server) backupsDeleteServer(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	id, _ := strconv.ParseInt(r.PathValue("backup"), 10, 64)
	s.backupAdminAct(w, r, func() (string, string, error) {
		b, err := svc.FindServer(r.Context(), id)
		if err != nil {
			return "", "", err
		}
		if b == nil {
			return "", "", &hosting.UserError{What: "Backup not found."}
		}
		if err := svc.Delete(r.Context(), b); err != nil {
			return "", "", err
		}
		return "Backup deleted.", "", nil
	})
}

// backupsRestoreServer handles POST /backups/server/{backup}/restore.
func (s *Server) backupsRestoreServer(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	id, _ := strconv.ParseInt(r.PathValue("backup"), 10, 64)
	// This route is server.manage per ROUTE-MATRIX, but our guard already
	// checked backups.manage — we additionally check server.manage here.
	if !s.can(r, "server.manage") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	components := r.Form["components[]"]
	if len(components) == 0 {
		components = r.Form["components"]
	}
	extract := r.FormValue("extract_only") == "1"
	confirm := r.FormValue("confirm")
	s.backupAdminAct(w, r, func() (string, string, error) {
		b, err := svc.FindServer(r.Context(), id)
		if err != nil {
			return "", "", err
		}
		if b == nil {
			return "", "", &hosting.UserError{What: "Backup not found."}
		}
		job, err := svc.RestoreServer(r.Context(), b, components, extract, confirm)
		if err != nil {
			return "", "", err
		}
		kind := "Restore server configuration"
		msg := "Restore started. A safety backup of the current configuration is taken first; any invalid configuration is rolled back."
		if extract {
			kind = "Extract server backup"
			msg = "Extracting the backup for review; the job log shows where. Nothing on the server changes."
		}
		if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, kind, "server", fmt.Sprintf("%d", s.currentServerID(r)), userIDPtr(user)); err != nil {
			s.log.Error("track restore job", "error", err)
		}
		return msg, job, nil
	})
}

// backupsDownloadServer handles GET /backups/server/{backup}/download?file=.
func (s *Server) backupsDownloadServer(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !backupsIsAdmin(user) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Not found"))
		return
	}
	svc := s.backupsAdminService(r, user)
	id, _ := strconv.ParseInt(r.PathValue("backup"), 10, 64)
	b, err := svc.FindServer(r.Context(), id)
	if err != nil {
		s.log.Error("find server backup", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if b == nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("Not found"))
		return
	}
	file := r.URL.Query().Get("file")
	first, next, err := svc.Download(r.Context(), b, file)
	if err != nil {
		// Match PHP: redirect with failure to /backups
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups")
			return
		}
		s.backupFlashError(w, r, "That file is not stored on this server.", "/backups")
		return
	}
	name := sanitizeDownloadName("server-" + b.BackupID + "-" + file)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(first.Size, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
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
			s.log.Error("backup download chunk", "error", err)
			return
		}
	}
}

// backupsStoreStorage handles POST /backups/storages.
func (s *Server) backupsStoreStorage(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	if s.backupsAdminOnly(w, r, "/backups#storages") {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	input := collectForm(r)
	// For gdrive, we need panel_url
	panelURL, _ := s.settings.String(r.Context(), "panel_url", "")
	pendingName, pendingCfg, err := svc.BuildStorage(r.Context(), input)
	if err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.flashValidation(w, r, ve, input, "/backups#storages")
			return
		}
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#storages")
			return
		}
		s.log.Error("build storage", "error", err)
		s.backupFlashError(w, r, "Invalid input.", "/backups#storages")
		return
	}
	if pendingCfg["type"] == "gdrive" {
		base := strings.TrimRight(panelURL, "/")
		if !strings.HasPrefix(base, "https://") {
			msg := "Google Drive needs the panel to be reachable over HTTPS."
			s.backupFlashError(w, r, msg, "/backups#storages")
			return
		}
		sealed, _ := svc.SealPending(pendingName, pendingCfg)
		state := randomState()
		sess := sessionFrom(r.Context())
		bs, _ := json.Marshal(map[string]string{"state": state, "sealed": sealed, "kind": "gdrive"})
		sess.Set(backupPending, string(bs))
		uri := svc.GoogleRedirectURI(base)
		authURL := svc.GoogleAuthURL(pendingName, pendingCfg, uri, state)
		http.Redirect(w, r, authURL, http.StatusSeeOther)
		return
	}
	trust, err := svc.TestStorage(r.Context(), pendingCfg)
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#storages")
			return
		}
		var ae *agent.Error
		if errors.As(err, &ae) {
			s.backupFlashError(w, r, ae.Message, "/backups#storages")
			return
		}
		s.log.Error("test storage", "error", err)
		s.backupFlashError(w, r, "The storage could not be reached.", "/backups#storages")
		return
	}
	if len(trust) > 0 {
		sealed, _ := svc.SealPending(pendingName, pendingCfg)
		bs, _ := json.Marshal(map[string]string{"sealed": sealed, "kind": asStr(pendingCfg["type"])})
		// Store trust as JSON too
		tj, _ := json.Marshal(trust)
		// Merge into pending JSON
		var m map[string]string
		_ = json.Unmarshal(bs, &m)
		m["trust"] = string(tj)
		nb, _ := json.Marshal(m)
		sess := sessionFrom(r.Context())
		sess.Set(backupPending, string(nb))
		http.Redirect(w, r, "/backups/storages/confirm", http.StatusSeeOther)
		return
	}
	if _, err := svc.InsertStorage(r.Context(), pendingName, pendingCfg); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.flashValidation(w, r, ve, input, "/backups#storages")
			return
		}
		s.backupFlashError(w, r, err.Error(), "/backups#storages")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Storage connected and tested.")
	http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
}

// backupsConfirmStorage handles GET /backups/storages/confirm.
func (s *Server) backupsConfirmStorage(w http.ResponseWriter, r *http.Request) {
	if s.backupsAdminOnly(w, r, "/backups#storages") {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	pendingRaw := sessionFrom(r.Context()).Get(backupPending)
	if pendingRaw == "" {
		http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
		return
	}
	var pending map[string]string
	if err := json.Unmarshal([]byte(pendingRaw), &pending); err != nil || pending["trust"] == "" {
		http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
		return
	}
	sealed := pending["sealed"]
	name, cfg, err := svc.UnsealPending(sealed)
	if err != nil {
		sessionFrom(r.Context()).Remove(backupPending)
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#storages")
			return
		}
		http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
		return
	}
	var trust map[string]string
	_ = json.Unmarshal([]byte(pending["trust"]), &trust)
	sess := sessionFrom(r.Context())
	s.render(w, "backups_confirm_storage", map[string]any{
		"User":    user,
		"CSRF":    session.CSRFToken(sess),
		"Name":    name,
		"Where":   hosting.DescribeStorage(cfg),
		"Trust":   trust,
		"Success": mustPullFlash(sess, "success"),
		"Error":   mustPullFlash(sess, "error"),
	})
}

// backupsTrustStorage handles POST /backups/storages/confirm.
func (s *Server) backupsTrustStorage(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	if s.backupsAdminOnly(w, r, "/backups#storages") {
		return
	}
	sess := sessionFrom(r.Context())
	pendingRaw := sess.Get(backupPending)
	sess.Remove(backupPending)
	if pendingRaw == "" {
		http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
		return
	}
	var pending map[string]string
	if err := json.Unmarshal([]byte(pendingRaw), &pending); err != nil || pending["trust"] == "" {
		http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
		return
	}
	if r.FormValue("cancel") != "" {
		sess.Flash("success", "The storage was not added.")
		http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	name, cfg, err := svc.UnsealPending(pending["sealed"])
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#storages")
			return
		}
		http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
		return
	}
	// Test again with pinned key
	if _, err := svc.TestStorage(r.Context(), cfg); err != nil {
		s.backupFlashError(w, r, err.Error(), "/backups#storages")
		return
	}
	if _, err := svc.InsertStorage(r.Context(), name, cfg); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.flashValidation(w, r, ve, map[string]string{}, "/backups#storages")
			return
		}
		s.backupFlashError(w, r, err.Error(), "/backups#storages")
		return
	}
	sess.Flash("success", "Storage connected and tested.")
	http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
}

// backupsGoogleCallback handles GET /backups/storages/google/callback.
func (s *Server) backupsGoogleCallback(w http.ResponseWriter, r *http.Request) {
	if s.backupsAdminOnly(w, r, "/backups#storages") {
		return
	}
	sess := sessionFrom(r.Context())
	pendingRaw := sess.Get(backupPending)
	sess.Remove(backupPending)
	if pendingRaw == "" {
		s.backupFlashError(w, r, "No Google Drive connection is in progress. Add the storage again.", "/backups#storages")
		return
	}
	var pending map[string]string
	_ = json.Unmarshal([]byte(pendingRaw), &pending)
	if pending["kind"] != "gdrive" {
		s.backupFlashError(w, r, "No Google Drive connection is in progress. Add the storage again.", "/backups#storages")
		return
	}
	if pending["state"] != r.URL.Query().Get("state") {
		s.backupFlashError(w, r, "The Google Drive connection could not be verified (state mismatch). Add the storage again.", "/backups#storages")
		return
	}
	errParam := r.URL.Query().Get("error")
	code := r.URL.Query().Get("code")
	if errParam != "" || code == "" {
		msg := "Google did not return an authorisation code"
		if errParam != "" {
			msg += " (" + errParam + ")"
		}
		msg += "."
		if errParam == "access_denied" {
			msg = "Google Drive access was not granted."
		}
		s.backupFlashError(w, r, msg, "/backups#storages")
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	name, cfg, err := svc.UnsealPending(pending["sealed"])
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#storages")
			return
		}
		http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
		return
	}
	panelURL, _ := s.settings.String(r.Context(), "panel_url", "")
	redirectURI := svc.GoogleRedirectURI(panelURL)
	if _, err := svc.GoogleComplete(r.Context(), name, cfg, code, redirectURI, nil); err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#storages")
			return
		}
		s.log.Error("google complete", "error", err)
		s.backupFlashError(w, r, "Google Drive connection failed.", "/backups#storages")
		return
	}
	sess.Flash("success", "Google Drive connected. Backups go to the \"WhaCP backups\" folder.")
	http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
}

// backupsScanStorage handles POST /backups/storages/{storage}/scan.
func (s *Server) backupsScanStorage(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	if s.backupsAdminOnly(w, r, "/backups#storages") {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	id, _ := strconv.ParseInt(r.PathValue("storage"), 10, 64)
	res, err := svc.FindInStorage(r.Context(), id, actorID(user))
	if err != nil {
		s.backupFlashError(w, r, err.Error(), "/backups#storages")
		return
	}
	msg := fmt.Sprintf("%d backup(s) added, %d already known.", res["imported"], res["known"])
	if nw, ok := res["no_website"].([]string); ok && len(nw) > 0 {
		limit := nw
		if len(limit) > 20 {
			limit = limit[:20]
		}
		msg += " Create these websites first, then search again: " + strings.Join(limit, ", ") + "."
	}
	if sk, ok := res["skipped"].([]string); ok && len(sk) > 0 {
		limit := sk
		if len(limit) > 5 {
			limit = limit[:5]
		}
		msg += " Not added (" + fmt.Sprintf("%d", len(sk)) + "): " + strings.Join(limit, "; ")
		if len(sk) > 5 {
			msg += "; …"
		}
		msg += "."
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", msg)
	http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
}

// backupsDeleteStorage handles POST /backups/storages/{storage}/delete.
func (s *Server) backupsDeleteStorage(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	if s.backupsAdminOnly(w, r, "/backups#storages") {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	id, _ := strconv.ParseInt(r.PathValue("storage"), 10, 64)
	if err := svc.DeleteStorage(r.Context(), id); err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#storages")
			return
		}
		s.log.Error("delete storage", "error", err)
		s.backupFlashError(w, r, "Could not delete storage.", "/backups#storages")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Storage removed.")
	http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
}

// backupsTestStorage handles POST /backups/storages/{storage}/test.
func (s *Server) backupsTestStorage(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	if s.backupsAdminOnly(w, r, "/backups#storages") {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	id, _ := strconv.ParseInt(r.PathValue("storage"), 10, 64)
	ok, errMsg, err := svc.RetestStorage(r.Context(), id)
	if err != nil {
		s.log.Error("retest storage", "error", err)
		s.backupFlashError(w, r, "Test failed.", "/backups#storages")
		return
	}
	sess := sessionFrom(r.Context())
	if !ok {
		// PHP throws UserFacingException which becomes redirect with failure
		sess.Flash("error", "The test failed: "+errMsg)
		http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
		return
	}
	sess.Flash("success", "The storage works: it can be turned on.")
	http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
}

// backupsActivateStorage handles POST /backups/storages/{storage}/active.
func (s *Server) backupsActivateStorage(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	if s.backupsAdminOnly(w, r, "/backups#storages") {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	id, _ := strconv.ParseInt(r.PathValue("storage"), 10, 64)
	on := r.FormValue("active") == "1"
	if err := svc.SetStorageActive(r.Context(), id, on); err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#storages")
			return
		}
		s.log.Error("activate storage", "error", err)
		s.backupFlashError(w, r, "Could not update storage.", "/backups#storages")
		return
	}
	sess := sessionFrom(r.Context())
	if on {
		sess.Flash("success", "Storage turned on: new backups can use it.")
	} else {
		sess.Flash("success", "Storage turned off: new backups do not use it (backups already there stay restorable).")
	}
	http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
}

// backupsUpdateStorage handles POST /backups/storages/{storage}.
func (s *Server) backupsUpdateStorage(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	if s.backupsAdminOnly(w, r, "/backups#storages") {
		return
	}
	user := userFrom(r.Context())
	svc := s.backupsAdminService(r, user)
	id, _ := strconv.ParseInt(r.PathValue("storage"), 10, 64)
	input := collectForm(r)
	if err := svc.UpdateStorage(r.Context(), id, input); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			input["storage_id"] = fmt.Sprintf("%d", id)
			s.flashValidation(w, r, ve, input, fmt.Sprintf("/backups#storage-edit-%d", id))
			return
		}
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#storages")
			return
		}
		s.log.Error("update storage", "error", err)
		s.backupFlashError(w, r, "Could not save storage.", "/backups#storages")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Storage saved and tested.")
	http.Redirect(w, r, "/backups#storages", http.StatusSeeOther)
}

// backupsDownloadKey handles POST /backups/key/download.
func (s *Server) backupsDownloadKey(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	user := userFrom(r.Context())
	if user == nil || user.RoleName != "admin" {
		s.backupFlashError(w, r, "Only administrators can download the backup key.", "/backups#server")
		return
	}
	// Verify panel password
	hash, err := s.users.PasswordHash(r.Context(), user.ID)
	if err != nil {
		s.log.Error("password hash", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if hash == "" || !identity.VerifyPassword(hash, r.FormValue("current_password")) {
		s.audit.Record(r.Context(), auditEntry("backup.key_export", "server", fmt.Sprintf("%d", s.currentServerID(r)), "denied", r, user, map[string]any{"reason": "wrong panel password"}))
		s.backupFlashError(w, r, "Your panel password is not correct.", "/backups#key-download")
		return
	}
	pass := r.FormValue("passphrase")
	if pass != r.FormValue("passphrase_confirmation") {
		s.backupFlashError(w, r, "The passphrases do not match.", "/backups#key-download")
		return
	}
	if len([]rune(pass)) < 12 {
		s.backupFlashError(w, r, "Use a passphrase of at least 12 characters.", "/backups#key-download")
		return
	}
	svc := s.backupsAdminService(r, user)
	keyID, file, err := svc.ExportKey(r.Context(), pass)
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#server")
			return
		}
		s.log.Error("export key", "error", err)
		s.backupFlashError(w, r, "Could not export key.", "/backups#server")
		return
	}
	safeID := sanitizeDownloadName(keyID)
	name := "whacp-backup-key-" + safeID + ".json"
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(file))
}

// backupsImportKey handles POST /backups/key/import.
func (s *Server) backupsImportKey(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	user := userFrom(r.Context())
	if user == nil || user.RoleName != "admin" {
		s.backupFlashError(w, r, "Only administrators can import a backup key.", "/backups#server")
		return
	}
	if err := r.ParseMultipartForm(4096); err != nil {
		s.backupFlashError(w, r, "Choose the key file (whacp-backup-key-….json).", "/backups#key-import")
		return
	}
	f, _, err := r.FormFile("key_file")
	if err != nil {
		s.backupFlashError(w, r, "Choose the key file (whacp-backup-key-….json).", "/backups#key-import")
		return
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil || len(content) == 0 || len(content) > 4096 {
		s.backupFlashError(w, r, "Choose the key file (whacp-backup-key-….json).", "/backups#key-import")
		return
	}
	pass := r.FormValue("passphrase")
	svc := s.backupsAdminService(r, user)
	keyID, already, err := svc.ImportKey(r.Context(), string(content), pass)
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#key-import")
			return
		}
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			s.backupFlashError(w, r, ae.Message, "/backups#key-import")
			return
		}
		s.log.Error("import key", "error", err)
		s.backupFlashError(w, r, "Could not import key.", "/backups#key-import")
		return
	}
	sess := sessionFrom(r.Context())
	if already {
		sess.Flash("success", "This server already has that backup key.")
	} else {
		sess.Flash("success", "Backup key "+keyID+" installed: backups encrypted with it can be restored here.")
	}
	http.Redirect(w, r, "/backups#server", http.StatusSeeOther)
}

// backupsStoreServerSchedule handles POST /backups/schedules.
func (s *Server) backupsStoreServerSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	if s.backupsAdminOnly(w, r, "/backups#server") {
		return
	}
	user := userFrom(r.Context())
	in := parseServerScheduleInput(r)
	sched := s.backupSched(r, user)
	if _, err := sched.CreateServer(r.Context(), in); err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			s.flashValidation(w, r, ve, collectForm(r), "/backups#server")
			return
		}
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#server")
			return
		}
		s.log.Error("create server schedule", "error", err)
		s.backupFlashError(w, r, "Could not add schedule.", "/backups#server")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Schedule added.")
	http.Redirect(w, r, "/backups#server", http.StatusSeeOther)
}

// backupsDeleteServerSchedule handles POST /backups/schedules/{schedule}/delete.
func (s *Server) backupsDeleteServerSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.backupNeedCSRF(w, r) {
		return
	}
	if s.backupsAdminOnly(w, r, "/backups#server") {
		return
	}
	user := userFrom(r.Context())
	id, _ := strconv.ParseInt(r.PathValue("schedule"), 10, 64)
	if err := s.backupSched(r, user).DeleteServer(r.Context(), id); err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			s.backupFlashError(w, r, ue.What, "/backups#server")
			return
		}
		s.log.Error("delete server schedule", "error", err)
		s.backupFlashError(w, r, "Could not delete schedule.", "/backups#server")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Schedule removed.")
	http.Redirect(w, r, "/backups#server", http.StatusSeeOther)
}

// helpers

func collectForm(r *http.Request) map[string]string {
	_ = r.ParseForm()
	out := map[string]string{}
	for k, v := range r.Form {
		if len(v) > 0 {
			out[k] = v[len(v)-1]
		}
	}
	return out
}

func parseServerScheduleInput(r *http.Request) hosting.ServerScheduleInput {
	_ = r.ParseForm()
	in := hosting.ServerScheduleInput{
		Scope:     r.FormValue("scope"),
		Frequency: r.FormValue("frequency"),
		Encrypt:   r.FormValue("encrypt") == "1",
		KeepLocal: r.FormValue("keep_local") == "1",
	}
	if in.Scope == "" {
		in.Scope = "server"
	}
	if in.Frequency == "" {
		in.Frequency = "daily"
	}
	in.Hour, _ = strconv.Atoi(r.FormValue("hour"))
	in.Weekday, _ = strconv.Atoi(r.FormValue("weekday"))
	in.Retention, _ = strconv.Atoi(r.FormValue("retention"))
	if in.Retention == 0 {
		in.Retention = 7
	}
	if v := r.FormValue("storage_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			in.StorageID = sql.NullInt64{Valid: true, Int64: id}
		}
	}
	in.IncludeFiles = r.FormValue("include_files") == "1"
	in.IncludeDatabases = r.FormValue("include_databases") == "1"
	// For server scope, files/dbs checkboxes are not shown; default true for all_sites
	if r.FormValue("scope") == "all_sites" {
		// default to true if not submitted (PHP filter_var default true)
		if _, ok := r.Form["include_files"]; !ok {
			in.IncludeFiles = true
		}
		if _, ok := r.Form["include_databases"]; !ok {
			in.IncludeDatabases = true
		}
	}
	return in
}

func (s *Server) flashValidation(w http.ResponseWriter, r *http.Request, ve *hosting.ValidationError, old map[string]string, to string) {
	sess := sessionFrom(r.Context())
	bj, _ := json.Marshal(ve.Fields)
	bo, _ := json.Marshal(old)
	sess.Flash("errors", string(bj))
	sess.Flash("old", string(bo))
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func randomState() string {
	b := make([]byte, 16)
	_, _ = io.ReadFull(crandReader, b)
	const hex = "0123456789abcdef"
	out := make([]byte, 32)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0xF]
	}
	return string(out)
}

func asStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

var crandReader io.Reader = &crandWrapper{}

type crandWrapper struct{}

func (c *crandWrapper) Read(p []byte) (int, error) {
	return crandRead(p)
}
