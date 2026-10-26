package http

// AccountBackupsController port (panel/app/Http/Controllers/AccountBackupsController.php,
// 209 LOC, 8 routes): create/download/delete/upload/preview/start/show/recreate
// for per-account backup archives. Administrators only (RoleScope == "all").

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
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
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{Action: "backup.account", TargetType: "user", TargetID: strconv.FormatInt(userID, 10), Result: audit.Success, UserID: &uid, Username: user.Username})
	if job != "" {
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
	manifest, _ := insp["manifest"].(map[string]any)
	sess := sessionFrom(r.Context())
	data := s.restoreView(r, manifest, nil)
	data["User"], data["CSRF"], data["Path"] = user, session.CSRFToken(sess), path
	data["Title"] = "Restore the account " + hosting.ManifestUsername(manifest)
	data["Error"], data["Success"] = pullFlashKey(sess, "error"), pullFlashKey(sess, "success")
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
	username := hosting.ManifestUsername(manifest)
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
	if raw := sess.Get(accountSecretsKey); raw != "" {
		if m := agentObject(json.RawMessage(raw)); m != nil {
			if rid, _ := m["id"].(float64); int64(rid) == id {
				if list, ok := m["list"].([]any); ok {
					secrets = list
				}
			}
		}
	}
	sess.Remove(accountSecretsKey)
	data := s.restoreView(r, rec.Manifest, rec)
	data["User"], data["CSRF"], data["Path"] = user, session.CSRFToken(sess), rec.Path
	data["Title"] = "Restore the account " + rec.Username
	data["ImportState"], data["Secrets"] = state, secrets
	data["Error"], data["Success"] = pullFlashKey(sess, "error"), pullFlashKey(sess, "success")
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
	out, err := svc.RecreateAccount(r.Context(), id, user, s.restoreDeps(r, user))
	uid := user.ID
	if err == nil {
		res := audit.Success
		if n, _ := out["problems"].(int); n > 0 {
			res = audit.Failure
		}
		s.audit.Record(r.Context(), audit.Entry{Action: "backup.account_restore", TargetType: "user", TargetID: strconv.FormatInt(out["account_id"].(int64), 10), Result: res, UserID: &uid, Username: user.Username,
			Details: map[string]any{"problems": out["problems"]}})
	}
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
		if blob, err := json.Marshal(map[string]any{"id": id, "list": secrets}); err == nil {
			sessionFrom(r.Context()).Set(accountSecretsKey, string(blob))
		}
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

type restoreSite struct {
	Domain, Kind, Databases, Here, HereCls string
}

// restoreView describes what a backup file holds and what restoring it would
// do here (AccountRestoreController::show): the account, each website and
// whether the plan exists. Pure reads.
func (s *Server) restoreView(r *http.Request, manifest map[string]any, rec *hosting.AccountRestore) map[string]any {
	ctx := r.Context()
	meta, _ := manifest["meta"].(map[string]any)
	u, _ := meta["user"].(map[string]any)
	username := hosting.ManifestUsername(manifest)
	count := func(k string) int { l, _ := meta[k].([]any); return len(l) }

	account := "create"
	var scope sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT r.scope FROM users u JOIN roles r ON r.id = u.role_id WHERE u.username = ?`, username).Scan(&scope); err == nil {
		account = "existing"
		if scope.String == "all" {
			account = "conflict"
		}
	}
	planFound := true
	if p := jstr(u, "plan"); p != "" {
		var n int
		_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM plans WHERE name = ?`, p).Scan(&n)
		planFound = n > 0
	}
	var sites []restoreSite
	list, _ := meta["websites"].([]any)
	for _, it := range list {
		w, _ := it.(map[string]any)
		domain := jstr(w, "domain")
		var owner sql.NullString
		here, cls := "will be created", "text-bg-success"
		if err := s.db.QueryRowContext(ctx, `SELECT u.username FROM websites w LEFT JOIN users u ON u.id = w.owner_id WHERE w.domain = ?`, domain).Scan(&owner); err == nil {
			if owner.String == username {
				here, cls = "exists in the account: restored into it", "text-bg-warning"
			} else {
				here, cls = "belongs to someone else: skipped", "text-bg-danger"
			}
		}
		kind := jstr(w, "type")
		if kind == "" {
			kind = "main"
		}
		if jstr(w, "kind") == "php" {
			kind += " · PHP " + jstr(w, "php_version")
		} else if k := jstr(w, "kind"); k != "" {
			kind += " · " + k
		}
		dbs := strings.Join(strList(w["databases"]), ", ")
		if dbs == "" {
			dbs = "—"
		}
		sites = append(sites, restoreSite{domain, kind, dbs, here, cls})
	}
	role := jstr(u, "role")
	if role == "" {
		role = "customer"
	}
	created := jstr(manifest, "created_at")
	if len(created) > 16 {
		created = created[:16]
	}
	data := map[string]any{
		"Username": username, "Role": role, "PlanName": jstr(u, "plan"), "Parent": jstr(u, "parent"),
		"Account": account, "PlanFound": planFound, "Created": created, "Label": jstr(manifest, "label"),
		"KeyID": jstr(manifest, "key_id"), "Sites": sites,
		"DBs": count("databases"), "DBUsers": count("database_users"), "Zones": count("dns_zones"), "Crons": count("cron_jobs"),
		"Restore": nil, "Status": "", "ShowPlan": true,
	}
	if rec != nil {
		data["Restore"], data["Status"] = rec, rec.Status
		data["ShowPlan"] = rec.Status == "importing" || rec.Status == "imported"
		if len(rec.UpdatedAt) > 16 {
			data["Updated"] = rec.UpdatedAt[:16]
		} else {
			data["Updated"] = rec.UpdatedAt
		}
		type line struct{ Level, Text string }
		var report []line
		for _, it := range rec.Report {
			m, _ := it.(map[string]any)
			report = append(report, line{jstr(m, "level"), jstr(m, "text")})
		}
		data["Report"] = report
	}
	return data
}

// restoreDeps binds the services an account restore drives to the acting
// administrator, so every step is audited as theirs.
func (s *Server) restoreDeps(r *http.Request, user *identity.User) hosting.RestoreDeps {
	return hosting.RestoreDeps{
		Accounts:  s.accountService(r, user),
		Websites:  s.websiteService(r, user),
		Databases: s.databaseService(r, user),
		Dns:       s.siteDnsService(r, user),
		DnsRepo:   hosting.NewDnsRepository(s.db),
		Cron:      s.cronService(r, user),
		Backups:   s.backupsAdminService(r, user),
		Users:     identity.NewRepository(s.db),
		Sites:     hosting.NewWebsiteRepository(s.db),
	}
}
