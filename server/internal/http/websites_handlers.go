package http

import (
	"database/sql"
	"encoding/json"
	"errors"
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
func (s *Server) currentServerID(r *http.Request) int64 { return 1 }

// websitesIndex lists the websites the signed-in user may see on the active
// server, tenant-scoped. Mirrors WebsitesController::index (route "websites",
// permission websites.view).
func (s *Server) websitesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sites, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list websites", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	okMsg, _ := sess.PullFlash("success")
	s.render(w, "websites", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Sites":     sites,
		"Error":     errMsg,
		"Success":   okMsg,
		"CanManage": s.can(r, "websites.manage"),
	})
}

// websiteShow renders one website (scoped to the user) with its databases.
// A website outside the user's tenant is a 404 — the id never grants access.
// Mirrors WebsitesController::show (route "websites.show", websites.view).
func (s *Server) websiteShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	serverID := s.currentServerID(r)
	// FindFull (not Find): the page shows the app type and the directory
	// listing state, which Find leaves unset.
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), serverID, id, user)
	if errors.Is(err, sql.ErrNoRows) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.log.Error("find website", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	dbs, err := hosting.NewDatabaseRepository(s.db).All(r.Context(), serverID, user, &id)
	if err != nil {
		s.log.Error("website databases", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	okMsg, _ := sess.PullFlash("success")
	s.render(w, "website_show", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Site":      site,
		"AppType":   hosting.AppType(site),
		"Databases": dbs,
		"Error":     errMsg,
		"Success":   okMsg,
		"CanManage": s.can(r, "websites.manage"),
	})
}

// databasesIndex lists the databases and database users the user may see,
// tenant-scoped, with the one-shot secret flash. The mysql feature withholds
// databases.view when a plan disables it, so the permission guard already
// enforces the feature. Mirrors DatabasesController::index (route
// "databases", databases.view) and the _manage view's data.
func (s *Server) databasesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	repo := hosting.NewDatabaseRepository(s.db)
	dbs, err := repo.All(r.Context(), s.currentServerID(r), user, nil)
	if err != nil {
		s.log.Error("list databases", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := repo.DBUsers(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list database users", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
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
	s.render(w, "databases", map[string]any{
		"User":             user,
		"CSRF":             session.CSRFToken(sess),
		"Databases":        dbs,
		"Users":            userRows,
		"RemoteHosts":      remote,
		"Prefix":           hosting.Prefix(user),
		"Secret":           secret,
		"PrivilegeLabels":  hosting.DBPrivilegeLabels,
		"PrivilegeOptions": hosting.DBPrivilegeOptions,
		"Error":            errMsg,
		"Success":          okMsg,
		"CanManage":        s.can(r, "databases.manage"),
	})
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

	var websiteID *int64
	if v := r.FormValue("website_id"); v != "" {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			websiteID = &id
		}
	}
	svc := s.databaseService(r, user)
	_, err := svc.CreateDatabase(r.Context(), s.currentServerID(r), user, r.FormValue("name"), websiteID)
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
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Database created.")
	http.Redirect(w, r, "/databases", http.StatusSeeOther)
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
		http.Redirect(w, r, "/databases", http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Database "+name+" was deleted.")
	http.Redirect(w, r, "/databases", http.StatusSeeOther)
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
	jobs, err := hosting.NewCronRepository(s.db).Jobs(r.Context(), site.ID)
	if err != nil {
		s.log.Error("list cron jobs", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, "site_cron", map[string]any{
		"User": user,
		"CSRF": session.CSRFToken(sessionFrom(r.Context())),
		"Site": site,
		"Jobs": jobs,
	})
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
