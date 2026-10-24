package http

// This file ports the server-level MongoDB area (MongoController): the page
// with its six sections (databases, users, backups, remotes, config, log) and
// every management action except the document browser, which lands with the
// browser batch.
//
// The route permissions do the gating, exactly as the PHP does — mongo.view
// to read, mongo.manage to change, packages.manage for the repository and
// install jobs, services.manage for the service controls. The extra can()
// checks below mirror the ones the controller repeats inside itself.
//
// Every change is audited against the server by the service layer, and the
// passwords only ever travel panel → agent: they are never rendered back.

import (
	"context"
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

// mongoNotes reads the saved per-database notes, keyed by database name.
func (s *Server) mongoNotes(r *http.Request) map[string]string {
	prefix := fmt.Sprintf("mongo.note.%d.", s.currentServerID(r))
	all, err := s.settings.Prefix(r.Context(), prefix)
	if err != nil {
		s.log.Error("mongo notes", "error", err)
		return map[string]string{}
	}
	out := make(map[string]string, len(all))
	for k, v := range all {
		out[strings.TrimPrefix(k, prefix)] = v
	}
	return out
}

// mongoServerService builds the server-level MongoDB service with the audit
// hook bound to the current actor.
func (s *Server) mongoServerService(r *http.Request, user *identity.User) *hosting.MongoServerService {
	return hosting.NewMongoServerService(s.agent, s.mongoServerAudit(r, user), s.currentServerID(r))
}

// mongoServerAudit binds the audit hook for the server-level Mongo actions.
func (s *Server) mongoServerAudit(r *http.Request, user *identity.User) hosting.Auditor {
	return func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		uid := user.ID
		s.audit.Record(ctx, audit.Entry{
			UserID:     &uid,
			Username:   user.Username,
			Action:     action,
			TargetType: targetType,
			TargetID:   target,
			Result:     result,
			Details:    details,
		})
	}
}

// mongoRemotes builds the saved-remote service.
func (s *Server) mongoRemotes(r *http.Request, user *identity.User) *hosting.MongoRemoteService {
	return hosting.NewMongoRemoteService(s.db, s.crypto, s.agent,
		s.mongoServerAudit(r, user), s.currentServerID(r))
}

// mongoFail writes the failure flash and redirects (the PHP's
// redirectWithFailure): agent and user-facing errors keep their message,
// anything else is logged and replaced.
func (s *Server) mongoFail(w http.ResponseWriter, r *http.Request, back, action string, err error) {
	msg := ""
	var ae *agent.Error
	var ue *hosting.UserError
	switch {
	case errors.As(err, &ae):
		msg = ae.Message
		if msg == "" {
			msg = "The WhaCP agent refused the request."
		}
	case errors.As(err, &ue):
		msg = ue.What
	default:
		s.log.Error("mongo admin", "action", action, "error", err)
		msg = "The WhaCP agent could not be reached."
	}
	sessionFrom(r.Context()).Flash("error", msg)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// mongoOK writes the success flash and redirects.
func (s *Server) mongoOK(w http.ResponseWriter, r *http.Request, back, msg string) {
	sessionFrom(r.Context()).Flash("success", msg)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// mongoForm verifies the CSRF token of a state-changing request. ok=false
// means the response was already written.
func (s *Server) mongoForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return false
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}

// mongoTrackJob records a started job so the job viewer can follow it, and
// flashes its id.
func (s *Server) mongoTrackJob(r *http.Request, user *identity.User, jobID, kind string) {
	if jobID == "" {
		return
	}
	if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID,
		kind, "server", strconv.FormatInt(s.currentServerID(r), 10), &user.ID); err != nil {
		s.log.Error("mongo job track", "job", jobID, "error", err)
	}
	sessionFrom(r.Context()).Flash("job", jobID)
}

// mongoIndex renders GET /mongo (MongoController::index). Only the active
// tab's data is read, so a large server's config or log is not fetched on
// every page view.
func (s *Server) mongoIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	section := r.URL.Query().Get("section")
	if !hosting.MongoSectionValid(section) {
		section = "databases"
	}
	svc := s.mongoServerService(r, user)

	data := map[string]any{
		"User":        user,
		"CSRF":        session.CSRFToken(sess),
		"Tab":         "mongo",
		"Section":     section,
		"CanManage":   s.can(r, "mongo.manage"),
		"CanPackages": s.can(r, "packages.manage"),
		"CanServices": s.can(r, "services.manage"),
		"Roles":       hosting.MongoRoleChoices,
		"Overview":    &hosting.MongoServerOverview{Databases: []hosting.MongoDatabase{}, Users: []hosting.MongoUser{}},
		"Backups":     []hosting.MongoBackup{},
		"Notes":       map[string]string{},
		"Remotes":     []hosting.MongoRemote{},
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	data["Job"], _ = sess.PullFlash("job")

	status, err := svc.Status(r.Context())
	if err != nil {
		data["AgentError"] = agentMessage(err)
		s.log.Error("mongo status", "error", err)
		s.render(w, "server_mongo", data)
		return
	}
	data["Status"] = status

	if !status.Installed || !status.Running {
		// The install card needs the repository state; a failure here must
		// not hide the rest of the page.
		if repo, err := svc.Repository(r.Context()); err == nil {
			data["Repository"] = repo
		}
	}

	if status.Installed && status.Running {
		// A failure reading the databases/users is shown in place rather
		// than blanking the page.
		if ov, err := svc.Overview(r.Context()); err != nil {
			data["OverviewError"] = agentMessage(err)
			s.log.Error("mongo overview", "error", err)
		} else {
			data["Overview"] = ov
		}
		if section == "config" {
			if cfg, err := svc.Config(r.Context()); err == nil {
				data["Config"] = cfg
			}
		}
		if section == "log" {
			if lg, err := svc.Log(r.Context(), 300); err == nil {
				data["Log"] = lg
			}
		}
		if section == "databases" || section == "backups" {
			if b, err := svc.Backups(r.Context(), ""); err == nil {
				data["Backups"] = b
			}
		}
		if section == "databases" {
			data["Notes"] = s.mongoNotes(r)
		}
		if section == "remotes" {
			if list, err := s.mongoRemotes(r, user).List(r.Context()); err == nil {
				data["Remotes"] = list
			}
		}
	}
	s.render(w, "server_mongo", data)
}

// agentMessage is the agent's own message, or a generic one.
func agentMessage(err error) string {
	var ae *agent.Error
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	return "The WhaCP agent could not be reached."
}

// mongoCreateDatabase handles POST /mongo/databases, optionally creating a
// user in the new database in the same step (MongoController::createDatabase).
func (s *Server) mongoCreateDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	userName := strings.TrimSpace(r.FormValue("user"))
	svc := s.mongoServerService(r, user)
	if err := svc.CreateDatabase(r.Context(), name); err != nil {
		s.mongoFail(w, r, "/mongo", "database_create", err)
		return
	}
	msg := "Created database " + name + "."
	if userName != "" {
		role := r.FormValue("role")
		if role == "" {
			role = "readWrite"
		}
		if err := svc.CreateUser(r.Context(), userName, r.FormValue("password"), name, role); err != nil {
			s.mongoFail(w, r, "/mongo", "user_create", err)
			return
		}
		msg = "Created database " + name + " and user " + userName + "."
	}
	s.mongoOK(w, r, "/mongo", msg)
}

// mongoDropDatabase handles POST /mongo/databases/drop.
func (s *Server) mongoDropDatabase(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	name := r.FormValue("name")
	if err := s.mongoServerService(r, userFrom(r.Context())).
		DropDatabase(r.Context(), name, r.FormValue("confirm")); err != nil {
		s.mongoFail(w, r, "/mongo", "database_drop", err)
		return
	}
	s.mongoOK(w, r, "/mongo", "Dropped database "+name+".")
}

// mongoBulkDropDatabases handles POST /mongo/databases/bulk-drop.
func (s *Server) mongoBulkDropDatabases(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	names := []string{}
	for _, n := range r.Form["names[]"] {
		if n = strings.TrimSpace(n); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		names = r.Form["names"]
	}
	if len(names) == 0 {
		sessionFrom(r.Context()).Flash("error", "Select at least one database.")
		http.Redirect(w, r, "/mongo", http.StatusSeeOther)
		return
	}
	svc := s.mongoServerService(r, userFrom(r.Context()))
	dropped := 0
	for _, n := range names {
		if err := svc.DropDatabase(r.Context(), n, n); err != nil {
			s.mongoFail(w, r, "/mongo", "database_drop", err)
			return
		}
		dropped++
	}
	msg := fmt.Sprintf("Dropped %d database.", dropped)
	if dropped != 1 {
		msg = fmt.Sprintf("Dropped %d databases.", dropped)
	}
	s.mongoOK(w, r, "/mongo", msg)
}

// mongoCreateUser handles POST /mongo/users.
func (s *Server) mongoCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := strings.TrimSpace(r.FormValue("user"))
	role := r.FormValue("role")
	if role == "" {
		role = "readWrite"
	}
	if err := s.mongoServerService(r, userFrom(r.Context())).CreateUser(r.Context(),
		user, r.FormValue("password"), strings.TrimSpace(r.FormValue("db")), role); err != nil {
		s.mongoFail(w, r, "/mongo?section=users", "user_create", err)
		return
	}
	s.mongoOK(w, r, "/mongo?section=users", "Created user "+user+".")
}

// mongoSetPassword handles POST /mongo/users/password.
func (s *Server) mongoSetPassword(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	if err := s.mongoServerService(r, userFrom(r.Context())).SetPassword(r.Context(),
		strings.TrimSpace(r.FormValue("user")), strings.TrimSpace(r.FormValue("db")),
		r.FormValue("password")); err != nil {
		s.mongoFail(w, r, "/mongo?section=users", "user_password", err)
		return
	}
	s.mongoOK(w, r, "/mongo?section=users", "Password changed.")
}

// mongoDropUser handles POST /mongo/users/drop.
func (s *Server) mongoDropUser(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := r.FormValue("user")
	if err := s.mongoServerService(r, userFrom(r.Context())).DropUser(r.Context(),
		user, r.FormValue("db")); err != nil {
		s.mongoFail(w, r, "/mongo?section=users", "user_drop", err)
		return
	}
	s.mongoOK(w, r, "/mongo?section=users", "Dropped user "+user+".")
}

// mongoEnableRepository handles POST /mongo/repository (packages.manage).
func (s *Server) mongoEnableRepository(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !s.can(r, "packages.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, "/mongo", http.StatusSeeOther)
		return
	}
	job, err := s.mongoServerService(r, user).EnableRepository(r.Context())
	if err != nil {
		s.mongoFail(w, r, "/mongo", "mongo.repository.enable", err)
		return
	}
	s.mongoTrackJob(r, user, job, "Enable MongoDB repository")
	s.mongoOK(w, r, "/mongo", "Adding MongoDB's repository…")
}

// mongoInstall handles POST /mongo/install (packages.manage).
func (s *Server) mongoInstall(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	if !s.can(r, "packages.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, "/mongo", http.StatusSeeOther)
		return
	}
	job, err := s.mongoServerService(r, user).Install(r.Context())
	if err != nil {
		s.mongoFail(w, r, "/mongo", "mongo.install", err)
		return
	}
	s.mongoTrackJob(r, user, job, "Install MongoDB")
	s.mongoOK(w, r, "/mongo", "Installing MongoDB…")
}

// mongoServiceControl handles POST /mongo/service (services.manage).
func (s *Server) mongoServiceControl(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	if !s.can(r, "services.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, "/mongo", http.StatusSeeOther)
		return
	}
	op := r.FormValue("op")
	switch op {
	case "start", "stop", "restart", "enable":
	default:
		sessionFrom(r.Context()).Flash("error", "Unknown service operation.")
		http.Redirect(w, r, "/mongo", http.StatusSeeOther)
		return
	}
	if err := s.mongoServerService(r, userFrom(r.Context())).Service(r.Context(), op); err != nil {
		s.mongoFail(w, r, "/mongo", "services.control", err)
		return
	}
	done := op + "ed"
	if op == "enable" {
		done = "set to start at boot"
	}
	s.mongoOK(w, r, "/mongo", "The MongoDB service was "+done+".")
}

// mongoConfig handles POST /mongo/config (the managed settings).
func (s *Server) mongoConfig(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, "/mongo?section=config", http.StatusSeeOther)
		return
	}
	port, err := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
	if err != nil || port < 1 || port > 65535 {
		sessionFrom(r.Context()).Flash("error", "Enter a port between 1 and 65535.")
		http.Redirect(w, r, "/mongo?section=config", http.StatusSeeOther)
		return
	}
	if err := s.mongoServerService(r, userFrom(r.Context())).SetConfig(r.Context(),
		port, strings.TrimSpace(r.FormValue("bind_ip")), r.FormValue("authorization") == "1"); err != nil {
		s.mongoFail(w, r, "/mongo?section=config", "mongo.config_set", err)
		return
	}
	s.mongoOK(w, r, "/mongo?section=config", "MongoDB configuration saved and the service restarted.")
}

// mongoConfigFile handles POST /mongo/config-file (whole file, rolled back by
// the agent on failure).
func (s *Server) mongoConfigFile(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, "/mongo?section=config", http.StatusSeeOther)
		return
	}
	if err := s.mongoServerService(r, userFrom(r.Context())).
		WriteConfig(r.Context(), r.FormValue("content")); err != nil {
		s.mongoFail(w, r, "/mongo?section=config", "mongo.config_write", err)
		return
	}
	s.mongoOK(w, r, "/mongo?section=config", "MongoDB configuration file saved and the service restarted.")
}

// mongoAdminPassword handles POST /mongo/admin-password.
func (s *Server) mongoAdminPassword(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, "/mongo?section=config", http.StatusSeeOther)
		return
	}
	if err := s.mongoServerService(r, userFrom(r.Context())).
		SetAdminPassword(r.Context(), r.FormValue("password")); err != nil {
		s.mongoFail(w, r, "/mongo?section=config", "mongo.admin_password", err)
		return
	}
	s.mongoOK(w, r, "/mongo?section=config", "The MongoDB admin password was set.")
}

// mongoAuth handles POST /mongo/auth.
func (s *Server) mongoAuth(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, "/mongo?section=config", http.StatusSeeOther)
		return
	}
	enable := r.FormValue("enable") == "1"
	if err := s.mongoServerService(r, userFrom(r.Context())).
		SetAuthorization(r.Context(), enable); err != nil {
		s.mongoFail(w, r, "/mongo?section=config", "mongo.auth_set", err)
		return
	}
	msg := "Authentication disabled; MongoDB restarted."
	if enable {
		msg = "Authentication enabled; MongoDB restarted."
	}
	s.mongoOK(w, r, "/mongo?section=config", msg)
}

// mongoNote handles POST /mongo/note (a free-text note per database, kept in
// settings and capped at 500 characters like the PHP's mb_substr).
func (s *Server) mongoNote(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, "/mongo", http.StatusSeeOther)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/mongo", http.StatusSeeOther)
		return
	}
	note := []rune(strings.TrimSpace(r.FormValue("note")))
	if len(note) > 500 {
		note = note[:500]
	}
	user := userFrom(r.Context())
	key := fmt.Sprintf("mongo.note.%d.%s", s.currentServerID(r), name)
	if err := s.settings.Set(r.Context(), key, string(note), user.ID); err != nil {
		s.log.Error("mongo note", "error", err)
		sessionFrom(r.Context()).Flash("error", "The note could not be saved.")
		http.Redirect(w, r, "/mongo", http.StatusSeeOther)
		return
	}
	if len(note) == 0 {
		s.mongoOK(w, r, "/mongo", "Note cleared.")
		return
	}
	s.mongoOK(w, r, "/mongo", "Note saved.")
}

// mongoRemoteAdd handles POST /mongo/remotes.
func (s *Server) mongoRemoteAdd(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	back := "/mongo?section=remotes"
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	port, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
	name, err := s.mongoRemotes(r, user).Add(r.Context(), hosting.MongoRemoteInput{
		Name:     r.FormValue("name"),
		Host:     r.FormValue("host"),
		Port:     port,
		Username: r.FormValue("username"),
		Password: r.FormValue("password"),
		AuthDB:   r.FormValue("auth_db"),
		TLS:      r.FormValue("tls") == "1",
	})
	if err != nil {
		s.mongoFail(w, r, back, "mongo.remote_add", err)
		return
	}
	s.mongoOK(w, r, back, "Remote server “"+name+"” added.")
}

// mongoRemoteDelete handles POST /mongo/remotes/delete.
func (s *Server) mongoRemoteDelete(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	back := "/mongo?section=remotes"
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	id, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err := s.mongoRemotes(r, user).Delete(r.Context(), id); err != nil {
		s.mongoFail(w, r, back, "mongo.remote_delete", err)
		return
	}
	s.mongoOK(w, r, back, "Remote server removed.")
}

// mongoRemoteTest handles POST /mongo/remotes/test.
func (s *Server) mongoRemoteTest(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	back := "/mongo?section=remotes"
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	id, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	ping, err := s.mongoRemotes(r, user).Ping(r.Context(), id)
	if err != nil {
		s.mongoFail(w, r, back, "mongo.remote_ping", err)
		return
	}
	version := ping.Version
	if version == "" {
		version = "(unknown)"
	}
	s.mongoOK(w, r, back, fmt.Sprintf("Connected — MongoDB %s, %d database(s).", version, ping.Databases))
}

// mongoRemoteDatabases handles POST /mongo/remotes/databases: the JSON
// endpoint behind the "Get DB from server" modal.
func (s *Server) mongoRemoteDatabases(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !s.can(r, "mongo.manage") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"success": false, "error": map[string]string{"message": "You may not do this."}})
		return
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), formToken(r)) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"success": false, "error": map[string]string{"message": "Page expired."}})
		return
	}
	id, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	list, err := s.mongoRemotes(r, user).Databases(r.Context(), id)
	if err != nil {
		code := http.StatusBadGateway
		var ae *agent.Error
		var ue *hosting.UserError
		if errors.As(err, &ae) && ae.Status >= 400 && ae.Status < 600 {
			code = ae.Status
		}
		msg := agentMessage(err)
		if errors.As(err, &ue) {
			msg = ue.What
		}
		writeJSON(w, code, map[string]any{"success": false, "error": map[string]string{"message": msg}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"databases": list}})
}

// mongoRemotePull handles POST /mongo/remotes/pull: a background copy.
func (s *Server) mongoRemotePull(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	back := "/mongo?section=remotes"
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	id, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	job, err := s.mongoRemotes(r, user).Pull(r.Context(), id,
		strings.TrimSpace(r.FormValue("remote_db")),
		strings.TrimSpace(r.FormValue("target_db")),
		r.FormValue("drop") == "1")
	if err != nil {
		s.mongoFail(w, r, back, "mongo.remote_pull", err)
		return
	}
	s.mongoTrackJob(r, user, job, "Copy a MongoDB database from a remote server")
	s.mongoOK(w, r, back, "Copying the database from the remote server…")
}

// mongoBackup handles POST /mongo/backup.
func (s *Server) mongoBackup(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	db := strings.TrimSpace(r.FormValue("db"))
	// The PHP checks the permission for the page it returns to: the backup
	// card lives on the databases tab.
	back := "/mongo"
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	res, err := s.mongoServerService(r, user).Backup(r.Context(), db)
	if err != nil {
		s.mongoFail(w, r, back, "mongo.backup", err)
		return
	}
	if res.JobID != "" {
		s.mongoTrackJob(r, user, res.JobID, "Back up MongoDB database "+db)
	}
	s.mongoOK(w, r, back, "Backing up "+db+"…")
}

// mongoRestore handles POST /mongo/restore.
func (s *Server) mongoRestore(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	back := "/mongo"
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	db := strings.TrimSpace(r.FormValue("db"))
	job, err := s.mongoServerService(r, user).Restore(r.Context(),
		r.FormValue("file"), db, r.FormValue("drop") == "1")
	if err != nil {
		s.mongoFail(w, r, back, "mongo.restore", err)
		return
	}
	s.mongoTrackJob(r, user, job, "Restore MongoDB database "+db)
	s.mongoOK(w, r, back, "Restoring "+db+"…")
}

// mongoDeleteBackup handles POST /mongo/backups/delete.
func (s *Server) mongoDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if !s.mongoForm(w, r) {
		return
	}
	back := "/mongo"
	if !s.can(r, "mongo.manage") {
		sessionFrom(r.Context()).Flash("error", "You may not do this.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if err := s.mongoServerService(r, userFrom(r.Context())).
		DeleteBackup(r.Context(), r.FormValue("file")); err != nil {
		s.mongoFail(w, r, back, "mongo.backup_delete", err)
		return
	}
	s.mongoOK(w, r, back, "Backup deleted.")
}
