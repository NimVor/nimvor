package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// currentServerID resolves the active server for the request. Multi-server
// switching (session _server_id) is a later concern; for now the local server
// (id 1) is used, matching ServerContext's default.
func (s *Server) currentServerID(r *http.Request) int64 {
	if r == nil {
		return 1
	}
	return serverIDFrom(r.Context())
}

// databasesPageData builds the databases-page data shared by GET /databases
// and GET /sites/{id}/databases (DatabasesController::common + the _manage
// view's $userDbs): the tenant-scoped database list (optionally scoped to
// one website), the scoped database-user list with per-user grants and the
// still-free databases, the remote-hosts flag, the shown-once secret flash,
// and the name prefix. On a lookup failure it logs, writes a 500 and
// reports ok=false.
func (s *Server) databasesPageData(r *http.Request, w http.ResponseWriter, user *identity.User, websiteID *int64) (map[string]any, bool) {
	repo := hosting.NewDatabaseRepository(s.db)
	dbs, err := repo.All(r.Context(), s.currentServerID(r), user, websiteID)
	if err != nil {
		s.log.Error("list databases", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	users, err := repo.DBUsers(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list database users", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	// Per-user database access (the Users tab "Databases" column and the
	// assign form's options), from the grants listed with the databases —
	// the PHP _manage view's $userDbs.
	userDbs := map[int64][]hosting.Grant{}
	for _, d := range dbs {
		for _, g := range d.Grants {
			userDbs[g.UserID] = append(userDbs[g.UserID], hosting.Grant{DatabaseID: d.ID, Name: d.Name, Privileges: g.Privileges})
		}
	}
	userRows := make([]dbUserRow, 0, len(users))
	for _, u := range users {
		row := dbUserRow{User: u}
		row.Databases = userDbs[u.ID]
		granted := map[int64]bool{}
		for _, g := range row.Databases {
			granted[g.DatabaseID] = true
		}
		for _, d := range dbs {
			if !granted[d.ID] {
				row.Free = append(row.Free, d)
			}
		}
		userRows = append(userRows, row)
	}
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	okMsg, _ := sess.PullFlash("success")
	jobMsg, _ := sess.PullFlash("job")
	var secret *secretFlash
	if raw, _ := sess.PullFlash("secret"); raw != "" {
		var sf secretFlash
		if err := json.Unmarshal([]byte(raw), &sf); err == nil {
			secret = &sf
		}
	}
	svc := s.databaseService(r, user)
	remote, err := svc.RemoteHostsAllowed(r.Context(), user)
	if err != nil {
		s.log.Error("settings lookup failed", "error", err)
		remote = true // the definition's default
	}
	// Live server state (per-database sizes + table counts from db.overview),
	// or nil when the database server could not be queried — the page then
	// shows a warning and falls back to the WhaCP records. Mirrors
	// DatabasesController::common.
	live, err := svc.Live(r.Context())
	if err != nil {
		s.log.Error("database overview", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	sizes := map[string]*dbOverviewRow{}
	if live != nil {
		for i := range live.Databases {
			d := &live.Databases[i]
			sizes[d.Name] = &dbOverviewRow{Name: d.Name, Tables: d.Tables, SizeText: formatBytes(d.Bytes)}
		}
	}
	// Databases present on the server but not under WhaCP management — the
	// "other databases" section, all-scope only, and only on the global page.
	var unmanaged []dbOverviewRow
	if websiteID == nil && hosting.IsAll(user) && live != nil {
		names, err := repo.AllNames(r.Context(), s.currentServerID(r))
		if err != nil {
			s.log.Error("list all databases", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return nil, false
		}
		managed := make(map[string]bool, len(names))
		for _, n := range names {
			managed[n] = true
		}
		for i := range live.Databases {
			if !managed[live.Databases[i].Name] {
				unmanaged = append(unmanaged, dbOverviewRow{
					Name:     live.Databases[i].Name,
					Tables:   live.Databases[i].Tables,
					SizeText: formatBytes(live.Databases[i].Bytes),
				})
			}
		}
	}
	serverLabel := "Database server unavailable"
	if live != nil {
		serverLabel = dbServerLabel(live.Version)
	}
	returnTo := "/databases"
	var site *hosting.Website
	if websiteID != nil {
		siteID := *websiteID
		if found, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), siteID, user); err == nil {
			site = found
			returnTo = fmt.Sprintf("/sites/%d/databases", siteID)
		}
	}
	return map[string]any{
		"User":             user,
		"CSRF":             session.CSRFToken(sess),
		"Databases":        dbs,
		"Users":            userRows,
		"RemoteHosts":      remote,
		"Prefix":           hosting.Prefix(user),
		"Secret":           secret,
		"PrivilegeLabels":  hosting.DBPrivilegeLabels,
		"PrivilegeOptions": hosting.DBPrivilegeOptions,
		"Site":             site,
		"ReturnTo":         returnTo,
		"Job":              jobMsg,
		"Error":            errMsg,
		"Success":          okMsg,
		"CanManage":        s.can(r, "databases.manage"),
		// Live server state (nil/empty → the warning + "unknown" fallbacks).
		"Live":        live != nil,
		"ServerLabel": serverLabel,
		"Sizes":       sizes,
		"Unmanaged":   unmanaged,
	}, true
}

// databasesIndex lists the databases and database users the user may see,
// tenant-scoped, with the one-shot secret flash. The mysql feature withholds
// databases.view when a plan disables it, so the permission guard already
// enforces the feature. Mirrors DatabasesController::index (route
// "databases", databases.view).
func (s *Server) databasesIndex(w http.ResponseWriter, r *http.Request) {
	data, ok := s.databasesPageData(r, w, userFrom(r.Context()), nil)
	if !ok {
		return
	}
	s.render(w, "databases", data)
}

// siteDatabases renders one website's databases page: the same data as the
// global page, with the databases scoped to the site and the operations'
// return_to pointing back to it. The website is resolved under tenant scope
// first (a foreign id is a 404). Mirrors DatabasesController::site (route
// "sites.databases", databases.view).
func (s *Server) siteDatabases(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if _, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), s.currentServerID(r), id, user); errors.Is(err, hosting.ErrWebsiteNotFound) {
		s.notFound(w, r)
		return
	} else if err != nil {
		s.log.Error("find website", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data, ok := s.databasesPageData(r, w, user, &id)
	if !ok {
		return
	}
	s.render(w, "site_databases", data)
}

// can reports whether the request's user holds a permission (for conditional
// UI). Errors fail closed.
func (s *Server) can(r *http.Request, permission string) bool {
	ok, err := s.authorizer().Can(r.Context(), userFrom(r.Context()), permission)
	return err == nil && ok
}

// databasesStore creates a database via the agent, tenant-scoped and plan-
// limited. CSRF-protected (state change) and permission-gated (databases.manage).
// Mirrors DatabasesController::store (database branch).
func (s *Server) databasesStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := s.databasesBack(r)
	var websiteID *int64
	if v := r.FormValue("website_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			websiteID = &id
		}
	}
	svc := s.databaseService(r, user)
	// A switch posts a hidden 0 and then its checkbox's 1: the last value counts.
	withUser := lastPostValue(r, "create_user") == "1"
	var err error
	var secretUser, secretPassword string
	if withUser {
		_, _, secretUser, secretPassword, err = svc.CreateWithUserStore(r.Context(), s.currentServerID(r), user, r.FormValue("name"), r.FormValue("username"), websiteID, lastPostValue(r, "store_password") == "1")
	} else {
		_, err = svc.CreateDatabase(r.Context(), s.currentServerID(r), user, r.FormValue("name"), websiteID)
	}
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			msg := "Please correct the form."
			for _, m := range ve.Fields {
				msg = m
				break
			}
			sess.Flash("error", msg)
		case errors.As(err, &ue):
			sess.Flash("error", ue.What)
		default:
			s.log.Error("create database", "error", err)
			sess.Flash("error", "The database could not be created.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if withUser && secretPassword != "" {
		s.flashSecret(sess, "Database user created", secretUser, secretPassword)
	}
	sess.Flash("success", "Database created.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// databaseService builds the database service with an audit hook bound to the
// current actor and request.
func (s *Server) databaseService(r *http.Request, user *identity.User) *hosting.DatabaseService {
	return hosting.NewDatabaseService(
		hosting.NewDatabaseRepository(s.db),
		hosting.NewWebsiteRepository(s.db),
		hosting.NewPlanLimits(s.db),
		s.agent,
		s.crypto,
		s.settings,
		func(action, targetType, target string, details map[string]any) {
			uid := user.ID
			s.audit.Record(r.Context(), audit.Entry{
				UserID: &uid, Username: user.Username,
				Action: action, TargetType: targetType, TargetID: target,
				Result: audit.Success, IP: clientIP(r), UserAgent: r.UserAgent(),
				Details: details,
			})
		},
	)
}

// databasesDrop deletes a database (destructive). CSRF-protected and
// databases.manage-gated; the typed confirm must match the database name.
// Mirrors DatabasesController::drop.
func (s *Server) databasesDrop(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := s.databasesBack(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	name, err := s.databaseService(r, user).DropDatabase(r.Context(), s.currentServerID(r), user, id, r.FormValue("confirm"))
	if err != nil {
		var ue *hosting.UserError
		switch {
		case errors.Is(err, hosting.ErrDatabaseNotFound):
			sess.Flash("error", "Database not found.")
		case errors.As(err, &ue):
			sess.Flash("error", ue.What)
		default:
			s.log.Error("drop database", "error", err)
			sess.Flash("error", "The database could not be deleted.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Database "+name+" was deleted.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// databasesDump starts a dump job for a database (the file is written to the
// server backup directory). CSRF + databases.manage. Mirrors
// DatabasesController::dump (route databases.dump).
func (s *Server) databasesDump(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := s.databasesBack(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	jobID, name, err := s.databaseService(r, user).DumpDatabase(r.Context(), s.currentServerID(r), user, id)
	if err != nil {
		if msg, ok := dbUserErrorFor(err, "Database not found."); ok {
			sess.Flash("error", msg)
		} else {
			s.log.Error("dump database", "error", err)
			sess.Flash("error", "The dump could not be started.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	uid := user.ID
	if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, "Dump "+name, "database", strconv.FormatInt(id, 10), &uid); err != nil {
		s.log.Error("track job", "error", err)
	}
	sess.Flash("job", jobID)
	sess.Flash("success", "Dump started. The file is written to the server backup directory.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// databasesImport imports a .sql or .sql.gz file from the database's website
// folder as a job. Only a database of one of the actor's websites can be
// imported into. CSRF + databases.manage. Mirrors DatabasesController::import
// (route databases.import).
func (s *Server) databasesImport(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := s.databasesBack(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	serverID := s.currentServerID(r)
	db, err := hosting.NewDatabaseRepository(s.db).Find(r.Context(), serverID, id, user)
	var site *hosting.Website
	if err == nil && db.WebsiteID.Valid {
		if found, ferr := hosting.NewWebsiteRepository(s.db).Find(r.Context(), serverID, db.WebsiteID.Int64, user); ferr == nil {
			site = found
		}
	}
	if err != nil || site == nil {
		sess.Flash("error", "Only a database of one of your websites can be imported into.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	jobID, err := s.databaseService(r, user).ImportFromSite(r.Context(), db, site, r.FormValue("path"), r.FormValue("uploaded") == "1")
	if err != nil {
		if msg, ok := dbUserErrorFor(err, "Only a database of one of your websites can be imported into."); ok {
			sess.Flash("error", msg)
		} else {
			s.log.Error("import database", "error", err)
			sess.Flash("error", "The import could not be started.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	uid := user.ID
	if err := hosting.NewJobTracker(s.db).Track(r.Context(), serverID, jobID, "Import into "+db.Name, "database", strconv.FormatInt(id, 10), &uid); err != nil {
		s.log.Error("track job", "error", err)
	}
	sess.Flash("job", jobID)
	sess.Flash("success", "Import into "+db.Name+" started.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// notFound renders a 404 page (JSON or HTML).
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if wantsJSON(r) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"success": false,
			"error":   map[string]string{"message": "Not found."},
		})
		return
	}
	renderStatus(w, s, "error", map[string]any{"Title": "Not found", "Message": "That page does not exist."}, http.StatusNotFound)
}

// siteCron lists a website's cron jobs. The website is resolved under tenant
// scope first (a foreign id is 404); the jobs hang off it. Gated by cron.view,
// which the cron plan feature withholds when disabled. Mirrors
// CronController::show (route "sites.cron").
func (s *Server) siteCron(w http.ResponseWriter, r *http.Request) {
	site := s.cronSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.notFound(w, r)
		return
	}
	// ?edit=<job> opens the form pre-filled for that job (the PHP's
	// CronController::show).
	var editID int64
	if v, err := strconv.ParseInt(r.URL.Query().Get("edit"), 10, 64); err == nil && v > 0 {
		editID = v
	}
	s.cronPageData(w, r, userFrom(r.Context()), site, editID, nil, nil)
}

// The workers page and its write routes live in websites_workers_handlers.go
// (WorkersController::show/store/toggle/restart/delete).

// siteSubdomains lists a website's subdomains/addon domains (its children).
// Gated by the subdomains plan feature (route name sites.subdomains) AND the
// websites.manage permission. The parent website is resolved under tenant scope
// (foreign id → 404). Mirrors SitesController::subdomains.
func (s *Server) siteSubdomains(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	serverID := s.currentServerID(r)
	site, err := hosting.NewWebsiteRepository(s.db).Find(r.Context(), serverID, id, user)
	if errors.Is(err, hosting.ErrWebsiteNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.log.Error("find website", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	children, err := hosting.NewWebsiteRepository(s.db).Children(r.Context(), serverID, site.ID)
	if err != nil {
		s.log.Error("list subdomains", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "site_subdomains", map[string]any{
		"User":     user,
		"CSRF":     session.CSRFToken(sessionFrom(r.Context())),
		"Site":     site,
		"Children": children,
	})
}
