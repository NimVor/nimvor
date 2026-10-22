package http

// This file ports the per-website MongoDB management area
// (SiteMongoController + the write side of SiteMongoService): the page
// (GET /sites/{id}/mongo — databases / users / backups sections), creating
// and dropping the account's prefixed databases, creating users + changing
// passwords + dropping, and the backup actions (back up now, restore,
// delete — the first two run as agent jobs). All names stay under the
// account's "<system_user>_" prefix; the drop-style actions refuse any
// name that is not. The full database browser (mongo.browse/browser/export)
// is a later batch.
// Routes (all websites.manage):
//   GET  /sites/{id}/mongo
//   POST /sites/{id}/mongo/databases
//   POST /sites/{id}/mongo/databases/drop
//   POST /sites/{id}/mongo/users
//   POST /sites/{id}/mongo/users/password
//   POST /sites/{id}/mongo/users/drop
//   POST /sites/{id}/mongo/backup
//   POST /sites/{id}/mongo/restore
//   POST /sites/{id}/mongo/backups/delete

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mongoSiteOf resolves the website (nil = not found / no access).
func (s *Server) mongoSiteOf(r *http.Request, id string) *hosting.Website {
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

func (s *Server) mongoService(r *http.Request, user *identity.User) *hosting.SiteMongoService {
	return hosting.NewSiteMongoService(s.agent, s.mongoAudit(r, user))
}

// mongoAudit binds the audit hook for the site's Mongo actions.
func (s *Server) mongoAudit(r *http.Request, user *identity.User) hosting.Auditor {
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

func (s *Server) mongoFlashErr(w http.ResponseWriter, r *http.Request, err error) {
	sess := sessionFrom(r.Context())
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		sess.Flash("error", ue.What)
	} else {
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			sess.Flash("error", ae.Message)
		} else {
			s.log.Error("mongo action", "error", err)
			sess.Flash("error", "The operation failed; nothing was changed.")
		}
	}
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/mongo", http.StatusSeeOther)
}

func (s *Server) mongoFlashNotFound(w http.ResponseWriter, r *http.Request) {
	sessionFrom(r.Context()).Flash("error", "Website not found.")
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

func (s *Server) mongoRedirectWith(w http.ResponseWriter, r *http.Request, msg string) {
	sessionFrom(r.Context()).Flash("success", msg)
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/mongo", http.StatusSeeOther)
}

func (s *Server) mongoRedirectJob(w http.ResponseWriter, r *http.Request, job, msg string) {
	sessionFrom(r.Context()).Flash("job", job)
	s.mongoRedirectWith(w, r, msg)
}

// mongoPageData renders the site's MongoDB page (the PHP's sites/mongo
// view variables).
func (s *Server) mongoPageData(w http.ResponseWriter, r *http.Request, user *identity.User, site *hosting.Website) {
	sess := sessionFrom(r.Context())
	ctx := r.Context()
	svc := s.mongoService(r, user)
	section := r.URL.Query().Get("section")
	if section != "databases" && section != "users" && section != "backups" {
		section = "databases"
	}
	var (
		status   *hosting.MongoStatus
		overview = &hosting.MongoOverview{Databases: []hosting.MongoDatabase{}, Users: []hosting.MongoUser{}}
		backups  = []hosting.MongoBackup{}
	)
	var agentError string
	if st, err := svc.Status(ctx); err != nil {
		agentError = mongoAgentMessage(err)
		if agentError == "" {
			agentError = "The server could not be reached."
			s.log.Error("mongo status", "error", err)
		}
	} else {
		status = st
		if status.Installed && status.Running {
			if ov, err := svc.Overview(ctx, site); err != nil {
				agentError = mongoAgentMessage(err)
				if agentError == "" {
					agentError = "MongoDB could not be queried."
					s.log.Error("mongo overview", "error", err)
				}
			} else {
				overview = ov
				if section == "backups" {
					if b, err := svc.Backups(ctx, site); err != nil {
						agentError = mongoAgentMessage(err)
						if agentError == "" {
							agentError = "The backups could not be listed."
							s.log.Error("mongo backups", "error", err)
						}
					} else {
						backups = b
					}
				}
			}
		}
	}
	// Pre-compute the human sizes + the created timestamps for the view.
	type dbRow struct {
		hosting.MongoDatabase
		SizeText string
	}
	type userRow struct {
		hosting.MongoUser
		RolesText string
	}
	type backupRow struct {
		hosting.MongoBackup
		SizeText    string
		CreatedText string
	}
	var dbRows []dbRow
	for _, d := range overview.Databases {
		dbRows = append(dbRows, dbRow{MongoDatabase: d, SizeText: formatBytes(d.Size)})
	}
	var userRows []userRow
	for _, u := range overview.Users {
		userRows = append(userRows, userRow{MongoUser: u, RolesText: joinRoles(u.Roles)})
	}
	var backupRows []backupRow
	for _, b := range backups {
		backupRows = append(backupRows, backupRow{MongoBackup: b, SizeText: formatBytes(b.Size), CreatedText: mongoCreatedText(b.Created)})
	}
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sessionFrom(r.Context())),
		"Site":       site,
		"Section":    section,
		"Status":     status,
		"Databases":  dbRows,
		"Users":      userRows,
		"Backups":    backupRows,
		"Prefix":     svc.Prefix(site),
		"AgentError": agentError,
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	data["Job"], _ = sess.PullFlash("job")
	s.render(w, "site_mongo", data)
}

// siteMongo renders the site's MongoDB page (sites.mongo — websites.manage).
func (s *Server) siteMongo(w http.ResponseWriter, r *http.Request) {
	site := s.mongoSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.notFound(w, r)
		return
	}
	s.mongoPageData(w, r, userFrom(r.Context()), site)
}

// mongoAct resolves the site, runs fn, and flashes the result. fn returns
// (message, jobID, err) — a non-empty jobID is tracked (when trackKind is
// set, like the PHP's $this->jobs->track) and flashed.
func (s *Server) mongoAct(w http.ResponseWriter, r *http.Request, trackKind string, fn func(ctx context.Context, site *hosting.Website) (string, string, error)) {
	if !s.mongoCSRF(w, r) {
		return
	}
	site := s.mongoSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.mongoFlashNotFound(w, r)
		return
	}
	msg, job, err := fn(r.Context(), site)
	if err != nil {
		s.mongoFlashErr(w, r, err)
		return
	}
	if job != "" {
		if trackKind != "" {
			user := userFrom(r.Context())
			if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, trackKind, "website", fmt.Sprintf("%d", site.ID), &user.ID); err != nil {
				s.log.Error("track mongo job", "job", job, "error", err)
			}
		}
		s.mongoRedirectJob(w, r, job, msg)
		return
	}
	s.mongoRedirectWith(w, r, msg)
}

// mongoCSRF verifies the form's CSRF token (the panel validates every POST).
func (s *Server) mongoCSRF(w http.ResponseWriter, r *http.Request) bool {
	if !session.ValidateCSRF(sessionFrom(r.Context()), formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}

// siteMongoCreateDatabase creates a prefixed database (sites.mongo.database_create).
func (s *Server) siteMongoCreateDatabase(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.mongoFlashErr(w, r, errors.New("bad request"))
		return
	}
	name := r.FormValue("name")
	s.mongoAct(w, r, "", func(ctx context.Context, site *hosting.Website) (string, string, error) {
		full, err := s.mongoService(r, userFrom(r.Context())).CreateDatabase(ctx, site, name)
		return "Created database " + full + ".", "", err
	})
}

// siteMongoDropDatabase drops one of the site's own databases (sites.mongo.database_drop).
func (s *Server) siteMongoDropDatabase(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.mongoFlashErr(w, r, errors.New("bad request"))
		return
	}
	name := r.FormValue("name")
	s.mongoAct(w, r, "", func(ctx context.Context, site *hosting.Website) (string, string, error) {
		err := s.mongoService(r, userFrom(r.Context())).DropDatabase(ctx, site, name)
		return "Database dropped.", "", err
	})
}

// siteMongoCreateUser creates a prefixed user in one of the site's databases
// (sites.mongo.user_create).
func (s *Server) siteMongoCreateUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.mongoFlashErr(w, r, errors.New("bad request"))
		return
	}
	user, password, db := r.FormValue("user"), r.FormValue("password"), r.FormValue("db")
	role := r.FormValue("role")
	if role == "" {
		role = "readWrite"
	}
	s.mongoAct(w, r, "", func(ctx context.Context, site *hosting.Website) (string, string, error) {
		full, err := s.mongoService(r, userFrom(r.Context())).CreateUser(ctx, site, user, password, db, role)
		return "Created user " + full + ".", "", err
	})
}

// siteMongoSetPassword changes one of the site's users' passwords
// (sites.mongo.user_password).
func (s *Server) siteMongoSetPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.mongoFlashErr(w, r, errors.New("bad request"))
		return
	}
	user, db, password := r.FormValue("user"), r.FormValue("db"), r.FormValue("password")
	s.mongoAct(w, r, "", func(ctx context.Context, site *hosting.Website) (string, string, error) {
		err := s.mongoService(r, userFrom(r.Context())).SetPassword(ctx, site, user, db, password)
		return "Password changed.", "", err
	})
}

// siteMongoDropUser drops one of the site's users (sites.mongo.user_drop).
func (s *Server) siteMongoDropUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.mongoFlashErr(w, r, errors.New("bad request"))
		return
	}
	user, db := r.FormValue("user"), r.FormValue("db")
	s.mongoAct(w, r, "", func(ctx context.Context, site *hosting.Website) (string, string, error) {
		err := s.mongoService(r, userFrom(r.Context())).DropUser(ctx, site, user, db)
		return "User dropped.", "", err
	})
}

// siteMongoBackup starts a backup of one of the site's databases
// (sites.mongo.backup — an agent job).
func (s *Server) siteMongoBackup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.mongoFlashErr(w, r, errors.New("bad request"))
		return
	}
	db := r.FormValue("db")
	s.mongoAct(w, r, "Back up MongoDB database "+db, func(ctx context.Context, site *hosting.Website) (string, string, error) {
		job, _, err := s.mongoService(r, userFrom(r.Context())).Backup(ctx, site, db)
		return "Backing up " + db + "…", job, err
	})
}

// siteMongoRestore restores a database from one of the site's archives
// (sites.mongo.restore — an agent job).
func (s *Server) siteMongoRestore(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.mongoFlashErr(w, r, errors.New("bad request"))
		return
	}
	file, drop := r.FormValue("file"), r.FormValue("drop") == "1"
	s.mongoAct(w, r, "Restore MongoDB backup", func(ctx context.Context, site *hosting.Website) (string, string, error) {
		job, err := s.mongoService(r, userFrom(r.Context())).Restore(ctx, site, file, drop)
		return "Restoring from the backup…", job, err
	})
}

// siteMongoDeleteBackup deletes one of the site's backup archives
// (sites.mongo.backup_delete).
func (s *Server) siteMongoDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.mongoFlashErr(w, r, errors.New("bad request"))
		return
	}
	file := r.FormValue("file")
	s.mongoAct(w, r, "", func(ctx context.Context, site *hosting.Website) (string, string, error) {
		err := s.mongoService(r, userFrom(r.Context())).DeleteBackup(ctx, site, file)
		return "Backup deleted.", "", err
	})
}

// joinRoles renders the roles list ("a, b").
func joinRoles(roles []string) string {
	out := ""
	for i, r := range roles {
		if i > 0 {
			out += ", "
		}
		out += r
	}
	return out
}

// mongoCreatedText renders a backup's created time "2006-01-02 15:04" (UTC).
func mongoCreatedText(ts string) string {
	if ts == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04")
}

// mongoAgentMessage is the agent's own failure message ("" = none).
func mongoAgentMessage(err error) string {
	var ae *agent.Error
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	return ""
}
