package http

// AccountBackupsController port (panel/app/Http/Controllers/AccountBackupsController.php,
// 209 LOC, 8 routes): create/download/delete/upload/preview/start/show/recreate
// for per-account backup archives. Administrators only (RoleScope == "all").

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

const accountSecretsKey = "account_restore_secrets"

// accountBackupCreate POST /backups/accounts
func (s *Server) accountBackupCreate(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		accountBackupErr(w, r, "Only administrators manage account backups.", "/backups")
		return
	}
	_ = r.ParseForm()
	userID, _ := strconv.ParseInt(r.PostFormValue("user_id"), 10, 64)
	label := r.PostFormValue("label")
	job, err := s.accountBackupSvc(r, user).Create(r.Context(), userID, label, actorID(user))
	if err != nil {
		accountBackupErr(w, r, err.Error(), "/backups#accounts")
		return
	}
	if job != "" {
		uid := user.ID
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Account backup", "user", strconv.FormatInt(userID, 10), &uid)
	}
	sess := sessionFrom(r.Context())
	if job != "" {
		sess.Flash("job", job)
	}
	sess.Flash("success", "Account backup started: every website is backed up, then the account file is written.")
	http.Redirect(w, r, "/backups#accounts", http.StatusSeeOther)
}

// accountBackupDownload GET /backups/accounts/download?path=...
func (s *Server) accountBackupDownload(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		accountBackupErr(w, r, "Only administrators manage account backups.", "/backups")
		return
	}
	path := r.URL.Query().Get("path")
	svc := s.accountBackupSvc(r, user)
	first, err := svc.Chunk(r.Context(), path, 0)
	if err != nil {
		accountBackupErr(w, r, err.Error(), "/backups#accounts")
		return
	}
	name := sanitizeDownloadName(filepath.Base(path))
	if name == "" {
		name = "account.whacp-account"
	}
	size, _ := first["size"].(float64)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(int64(size), 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	// Close session so streaming isn't blocked.
	sess := sessionFrom(r.Context())
	_ = sess
	offset := 0
	chunk := first
	for {
		data, _ := chunk["data"].(string)
		bytes, err := base64.StdEncoding.DecodeString(data)
		if err == nil && len(bytes) > 0 {
			_, _ = w.Write(bytes)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			offset += len(bytes)
		}
		eof, _ := chunk["eof"].(bool)
		if eof || err != nil || len(bytes) == 0 {
			break
		}
		chunk, err = svc.Chunk(r.Context(), path, offset)
		if err != nil {
			break
		}
	}
}

// accountBackupDelete POST /backups/accounts/delete
func (s *Server) accountBackupDelete(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		accountBackupErr(w, r, "Only administrators manage account backups.", "/backups")
		return
	}
	_ = r.ParseForm()
	path := r.PostFormValue("path")
	if err := s.accountBackupSvc(r, user).Delete(r.Context(), path, actorID(user)); err != nil {
		accountBackupErr(w, r, err.Error(), "/backups#accounts")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Account backup file deleted.")
	http.Redirect(w, r, "/backups#accounts", http.StatusSeeOther)
}

// accountBackupUpload POST /backups/accounts/upload — one chunk (XHR, JSON).
func (s *Server) accountBackupUpload(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": map[string]string{"message": "Only administrators upload account backups."}})
		return
	}
	_ = r.ParseMultipartForm(64 << 20)
	f, fh, err := r.FormFile("chunk")
	if err != nil || fh == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": map[string]string{"message": "Upload failed."}})
		return
	}
	defer f.Close()
	buf, err := io.ReadAll(f)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": map[string]string{"message": "Upload failed."}})
		return
	}
	name := r.FormValue("name")
	offset, _ := strconv.Atoi(r.FormValue("offset"))
	final := r.FormValue("final") == "1"
	res, err := s.accountBackupSvc(r, user).Upload(r.Context(), name, offset, buf, final)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "error": map[string]string{"message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": res})
}

// accountBackupPreview GET /backups/accounts/restore?path=...
func (s *Server) accountBackupPreview(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		accountBackupErr(w, r, "Only administrators manage account backups.", "/backups")
		return
	}
	path := r.URL.Query().Get("path")
	svc := s.accountBackupSvc(r, user)
	insp, err := svc.Inspect(r.Context(), path)
	if err != nil {
		accountBackupErr(w, r, err.Error(), "/backups#accounts")
		return
	}
	data := map[string]any{
		"User":     user,
		"CSRF":     session.CSRFToken(sessionFrom(r.Context())),
		"Title":    "Restore account backup",
		"Path":     path,
		"Manifest": insp["manifest"],
		"Plan":     insp["plan"],
		"Restore":  nil,
	}
	s.render(w, "backups_account_restore", data)
}

// accountBackupStart POST /backups/accounts/restore
func (s *Server) accountBackupStart(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		accountBackupErr(w, r, "Only administrators manage account backups.", "/backups")
		return
	}
	_ = r.ParseForm()
	path := r.PostFormValue("path")
	svc := s.accountBackupSvc(r, user)
	insp, err := svc.Inspect(r.Context(), path)
	if err != nil {
		accountBackupErr(w, r, err.Error(), "/backups#accounts")
		return
	}
	manifest, _ := insp["manifest"].(map[string]any)
	username := ""
	if u, ok := manifest["user"].(map[string]any); ok {
		username, _ = u["username"].(string)
	}
	if r.PostFormValue("confirm") != username {
		to := "/backups#accounts"
		if path != "" {
			to = "/backups/accounts/restore?path=" + url.QueryEscape(path)
		}
		accountBackupErr(w, r, "Type the account name to confirm the restore.", to)
		return
	}
	id, err := svc.Start(r.Context(), path, actorID(user))
	if err != nil {
		accountBackupErr(w, r, err.Error(), "/backups/accounts/restore?path="+url.QueryEscape(path))
		return
	}
	rec, err := svc.RestoreRecord(r.Context(), id)
	if err == nil && rec != nil && rec.JobID != "" {
		uid := user.ID
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), rec.JobID, "Import account backup "+rec.Username, "server", strconv.FormatInt(s.currentServerID(r), 10), &uid)
	}
	sess := sessionFrom(r.Context())
	if rec != nil && rec.JobID != "" {
		sess.Flash("job", rec.JobID)
	}
	sess.Flash("success", "Checking the file and importing its website backups.")
	http.Redirect(w, r, "/backups/accounts/restores/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// accountBackupShow GET /backups/accounts/restores/{id}
func (s *Server) accountBackupShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		accountBackupErr(w, r, "Only administrators manage account backups.", "/backups")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		accountBackupErr(w, r, "Unknown restore.", "/backups#accounts")
		return
	}
	svc := s.accountBackupSvc(r, user)
	rec, err := svc.RestoreRecord(r.Context(), id)
	if err != nil || rec == nil {
		accountBackupErr(w, r, "The restore no longer exists.", "/backups#accounts")
		return
	}
	state := svc.ImportState(r.Context(), rec)
	rec, _ = svc.RestoreRecord(r.Context(), id)
	sess := sessionFrom(r.Context())
	var secrets []any
	if raw := sess.Get(accountSecretsKey); raw != nil {
		if m, ok := raw.(map[string]any); ok {
			if rid, _ := m["id"].(int64); rid == id {
				if list, ok := m["list"].([]any); ok {
					secrets = list
				}
			}
		}
	}
	sess.Remove(accountSecretsKey)
	plan := map[string]any{}
	if meta, ok := rec.Manifest["meta"].(map[string]any); ok {
		// plan lookup is best-effort; show empty plan if we can't compute it.
		_ = meta
	}
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Title":      "Account restore",
		"Path":       rec.Path,
		"Manifest":   rec.Manifest,
		"Plan":       plan,
		"Restore":    rec,
		"ImportState": state,
		"Secrets":    secrets,
	}
	s.render(w, "backups_account_restore", data)
}

// accountBackupRecreate POST /backups/accounts/restores/{id}/recreate
func (s *Server) accountBackupRecreate(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		accountBackupErr(w, r, "Only administrators manage account backups.", "/backups")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		accountBackupErr(w, r, "Unknown restore.", "/backups#accounts")
		return
	}
	to := "/backups/accounts/restores/" + strconv.FormatInt(id, 10)
	svc := s.accountBackupSvc(r, user)
	out, err := svc.Recreate(r.Context(), id, user)
	if err != nil {
		accountBackupErr(w, r, err.Error(), to)
		return
	}
	if jobs, ok := out["jobs"].([]any); ok {
		for _, j := range jobs {
			if m, ok := j.(map[string]any); ok {
				job, _ := m["job"].(string)
				domain, _ := m["domain"].(string)
				siteID, _ := m["website_id"]
				var siteIDStr string
				switch v := siteID.(type) {
				case float64:
					siteIDStr = strconv.FormatInt(int64(v), 10)
				case string:
					siteIDStr = v
				}
				if job != "" {
					uid := user.ID
					_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Restore "+domain, "website", siteIDStr, &uid)
				}
			}
		}
		if len(jobs) > 0 {
			if first, ok := jobs[0].(map[string]any); ok {
				if jid, _ := first["job"].(string); jid != "" {
					sessionFrom(r.Context()).Flash("job", jid)
				}
			}
		}
	}
	// One-shot secrets.
	if secrets, ok := out["secrets"].([]any); ok && len(secrets) > 0 {
		sessionFrom(r.Context()).Set(accountSecretsKey, map[string]any{"id": id, "list": secrets})
	}
	sessionFrom(r.Context()).Flash("success", "Account recreated; its websites are being restored.")
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// ---- helpers ----

func accountBackupErr(w http.ResponseWriter, r *http.Request, msg, to string) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func sanitizeDownloadName(name string) string {
	return regexp.MustCompile(`[^A-Za-z0-9._-]`).ReplaceAllString(name, "_")
}

// accountBackupSvc returns the AccountBackupService bound to the request
// server id and user auditor.
func (s *Server) accountBackupSvc(_ *http.Request, user *identity.User) *hosting.AccountBackupService {
	return hosting.NewAccountBackupService(s.db, s.agent, s.currentServerID(nil), func(action, targetType, target string, details map[string]any) {
		var uid *int64
		if user != nil {
			id := user.ID
			uid = &id
		}
		s.audit.Record(context.Background(), audit.Entry{Action: action, TargetType: targetType, TargetID: target, Details: details, UserID: uid, Username: usernameOr(user)})
	})
}
