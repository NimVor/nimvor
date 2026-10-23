package http

// This file ports AppsController: the website's Applications page
// (GET /sites/{id}/apps — the Softaculous-style catalog + the
// installations history) and the one-click install
// (POST /sites/{id}/apps/{app} — onto this website or a subdomain),
// both gated by apps.install. The mechanics live in the hosting
// AppInstaller / AppCatalogService.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// appCatalog builds the catalog service.
func (s *Server) appCatalog() *hosting.AppCatalogService {
	return hosting.NewAppCatalogService(s.db, s.settings)
}

// appInstaller builds the installer service bound to the current actor.
func (s *Server) appInstaller(r *http.Request, user *identity.User) *hosting.AppInstaller {
	hook := s.websiteAuditHook(r, user)
	audit := func(action, targetType, target string, details map[string]any) {
		hook(r.Context(), action, targetType, target, "success", details)
	}
	return hosting.NewAppInstaller(
		s.db, s.agent, s.databaseService(r, user),
		hosting.NewWebsiteRepository(s.db), s.appCatalog(), s.websiteService(r, user),
		audit,
	)
}

// appTarget is one install target (the website + its subdomains).
type appTarget struct {
	ID     int64
	Domain string
	Docroot string
	IsPHP  bool
}

// appTargets is where an application may be installed from this page:
// the website itself + each subdomain (AppsController::targets).
func appTargets(site *hosting.Website, children []hosting.Website) []appTarget {
	entry := func(w *hosting.Website) appTarget {
		return appTarget{
			ID: w.ID, Domain: w.Domain, Docroot: hosting.RelativeDocroot(w),
			IsPHP: w.Kind == "php" && w.PhpVersion != "",
		}
	}
	out := []appTarget{entry(site)}
	for i := range children {
		// Subdomains end with ".<parent domain>"; addon domains do not.
		if strings.HasSuffix(children[i].Domain, "."+site.Domain) {
			out = append(out, entry(&children[i]))
		}
	}
	return out
}

// appInstallRowT is one installations-table row shaped for the template.
type appInstallRowT struct {
	Title        string
	Path         string
	AdminUser    string
	DatabaseName string
	Status       string // the badge label
	StatusClass  string // the badge color class
	Error        string
	JobID        string
	FinishURL    string
	WordPressURL string
}

// appRowT shapes an app_installs row (AppInstallRow) + the built-in app
// knowledge for the template.
func appRowT(site *hosting.Website, row hosting.AppInstallRow) appInstallRowT {
	meta, ok := hosting.AppsMeta(row.App)
	title := row.App
	if ok {
		title = meta.Title
	}
	status := row.Status
	statusClass := "text-bg-secondary"
	switch status {
	case "succeeded":
		statusClass = "text-bg-success"
	case "running":
		status = "activating"
		statusClass = "text-bg-warning"
	case "failed":
		statusClass = "text-bg-danger"
	}
	out := appInstallRowT{
		Title: title, Path: row.Path, AdminUser: row.AdminUser, DatabaseName: row.DatabaseName,
		Status: status, StatusClass: statusClass, Error: row.Error, JobID: row.JobID,
	}
	if row.Status == "succeeded" {
		if row.App == "wordpress" {
			scheme := "http://"
			if site.HTTPS {
				scheme = "https://"
			}
			out.WordPressURL = scheme + site.Domain + "/wp-admin/"
		}
		out.FinishURL = hosting.FinishURL(site, row.App)
	}
	return out
}

// siteApps renders the website's Applications page (AppsController::show).
func (s *Server) siteApps(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	imp := s.appInstaller(r, user)
	_ = imp.Refresh(r.Context(), site) // the agent keeps the truth; best effort
	installs, err := imp.History(r.Context(), site.ID)
	if err != nil {
		s.log.Error("app history", "site", site.ID, "error", err)
	}
	catalog, err := s.appCatalog().ForCustomer(r.Context())
	if err != nil {
		s.log.Error("app catalog", "error", err)
	}
	// ChildrenFull: the slim Children select lacks php_version/root/
	// document_root, which the target list needs.
	children, _ := hosting.NewWebsiteRepository(s.db).ChildrenFull(r.Context(), s.currentServerID(r), site.ID)
	secret := appSecret{}
	if b, _ := sess.PullFlash("secret"); b != "" {
		_ = json.Unmarshal([]byte(b), &secret)
	}
	installerEnabled := s.appCatalog().InstallerEnabled(r.Context())
	isPHP := site.Kind == "php" && site.PhpVersion != ""
	rows := make([]appInstallRowT, 0, len(installs))
	for _, i := range installs {
		rows = append(rows, appRowT(site, i))
	}
	data := map[string]any{
		"User":             user,
		"CSRF":             session.CSRFToken(sess),
		"Site":             site,
		"Catalog":          catalog,
		"Installs":         rows,
		"Secret":           secret,
		"Docroot":          hosting.RelativeDocroot(site),
		"Email":            user.Email,
		"InstallerEnabled": installerEnabled,
		"IsPHP":            isPHP,
		"Targets":          appTargets(site, children),
		"Success":          pullFlashKey(sess, "success"),
		"Error":            pullFlashKey(sess, "error"),
		"Job":              pullFlashKey(sess, "job"),
	}
	s.render(w, "apps_site", data)
}

// appSecret is the flashed one-time credentials (the secret partial).
type appSecret struct {
	Title    string `json:"title"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// siteAppInstall runs the one-click install (AppsController::install).
func (s *Server) siteAppInstall(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	app := r.PathValue("app")
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	back := fmt.Sprintf("/sites/%d/apps", site.ID)

	// The application may go on this website or one of its subdomains.
	targetID := site.ID
	if v, err := strconv.Atoi(r.PostFormValue("target_id")); err == nil && v > 0 {
		targetID = int64(v)
	}
	target := s.showSiteOf(r, strconv.FormatInt(targetID, 10))
	if target == nil {
		sess.Flash("error", "Choose this website or one of its subdomains.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if targetID != site.ID && (target.ParentID.Int64 != site.ID || !target.ParentID.Valid) {
		sess.Flash("error", "Choose this website or one of its subdomains.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}

	imp := s.appInstaller(r, user)
	input := postValues(r)
	res, err := imp.Install(r.Context(), s.currentServerID(r), target, app, input, user)
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			sess.Flash("error", firstValidationError(ve))
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("app install", "app", app, "error", err)
			sess.Flash("error", "The application could not be installed.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	// The page redirects to the target so its progress is shown there.
	back = fmt.Sprintf("/sites/%d/apps", target.ID)
	meta, ok := hosting.AppsMeta(app)
	title := app
	if ok {
		title = meta.Title
	}
	_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), res.Job,
		"Install "+title, "website", fmt.Sprint(target.ID), &user.ID)
	if res.AdminPassword != "" {
		b, _ := json.Marshal(appSecret{Title: "WordPress administrator", User: res.AdminUser, Password: res.AdminPassword})
		sess.Flash("secret", string(b))
	}
	sess.Flash("success", title+" is being installed.")
	sess.Flash("job", res.Job)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// firstValidationError is the first field error (PHP's ValidationException
// message is the first field).
func firstValidationError(ve *hosting.ValidationError) string {
	// The fixed display order the view uses.
	for _, f := range []string{"name", "path", "title", "admin_user", "admin_email", "username", "password", "sftp_password", "quota_mb", "dir"} {
		if m, ok := ve.Fields[f]; ok {
			return m
		}
	}
	for _, m := range ve.Fields {
		return m
	}
	return "Please correct the form."
}
