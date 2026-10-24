package http

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// This file ports the advanced operations of app/Http/Controllers/SitesController
// (restartPhp, reapply, directoryListing, checkPermissions/repairPermissions)
// and SiteRuntimeController (the shared Node.js/Python runtime page and its
// update). PHP sends the results back to the site's "advanced" tab, which the
// Go panel does not have yet; all of them land on the site overview instead.

// workerService builds the worker write service with the audit hook bound to
// the current actor and request (same wiring as the website service).
func (s *Server) workerService(r *http.Request, user *identity.User) *hosting.WorkerService {
	return hosting.NewWorkerService(s.db, s.agent, hosting.NewPlanLimits(s.db), s.websiteAuditHook(r, user), s.currentServerID(r))
}

// runtimeService builds the runtime reader.
func (s *Server) runtimeService() *hosting.RuntimeService {
	return hosting.NewRuntimeService(s.agent)
}

// provisionApp creates the "app" Worker for a new Node.js/Python site when a
// start command was given (SitesController::provisionApp). It never fails the
// site creation — the Runtime page lets the user fix the app otherwise.
func (s *Server) provisionApp(r *http.Request, user *identity.User, siteID int64, in map[string]string) {
	command := strings.TrimSpace(in["app_command"])
	if command == "" {
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), siteID, nil)
	if err != nil || site == nil {
		return
	}
	port := portInProxy(site.ProxyPass)
	if port == "" {
		port = "3000"
	}
	// Best effort: validation, limit, DB or agent failures are swallowed (the
	// site exists; the Runtime page is the fix point).
	_ = s.workerService(r, user).Save(r.Context(), site, nil, hosting.WorkerInput{
		Name:      "app",
		Command:   command,
		Directory: strings.Trim(in["app_dir"], " /"),
		Runtime:   site.AppRuntime,
		EnvText:   "PORT=" + port + "\n" + in["app_env"],
		Restart:   "always",
		Procs:     hosting.ProcsPtr(1),
		MemoryMB:  0,
		Enabled:   boolPtr(true),
	})
}

func boolPtr(b bool) *bool { return &b }

// flashOpsError flashes the user-facing message (the error's What) or a
// generic fallback for an unexpected failure, then redirects back.
func (s *Server) flashOpsError(w http.ResponseWriter, r *http.Request, sess *session.Session, back, fallback string, err error) {
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		sess.Flash("error", ue.What)
	} else {
		s.log.Error("website op", "error", err)
		sess.Flash("error", fallback)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// siteRestartPhp reloads the site's PHP-FPM unit (SitesController::restartPhp).
func (s *Server) siteRestartPhp(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
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
	back := fmt.Sprintf("/sites/%d", site.ID)
	if err := s.websiteService(r, user).RestartPhp(r.Context(), site); err != nil {
		s.flashOpsError(w, r, sess, back, "The PHP-FPM service could not be reloaded.", err)
		return
	}
	sess.Flash("success", "PHP "+site.PhpVersion+" was reloaded.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// siteReapply re-applies the stored configuration (SitesController::reapply).
func (s *Server) siteReapply(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
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
	back := fmt.Sprintf("/sites/%d", site.ID)
	if err := s.websiteService(r, user).Reapply(r.Context(), site); err != nil {
		s.flashOpsError(w, r, sess, back, "The configuration could not be re-applied.", err)
		return
	}
	sess.Flash("success", "Configuration re-applied from the panel.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// siteDirectoryListing toggles directory listing (SitesController::directoryListing).
func (s *Server) siteDirectoryListing(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
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
	back := fmt.Sprintf("/sites/%d", site.ID)
	on := r.PostFormValue("enabled") == "1"
	if err := s.websiteService(r, user).SetDirectoryListing(r.Context(), site, on); err != nil {
		s.flashOpsError(w, r, sess, back, "Directory listing could not be changed.", err)
		return
	}
	msg := "Directory listing is off."
	if on {
		msg = "Directory listing is on: folders without an index page list their files."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// siteCheckPermissions reports the site's file permissions as JSON
// (SitesController::checkPermissions) — the one JSON answer among the website
// ops: 404 for a website that does not exist or is not visible, 502 when the
// agent check fails, else the agent's dry-run result.
func (s *Server) siteCheckPermissions(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Website not found."})
		return
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), id, user)
	if errors.Is(err, hosting.ErrWebsiteNotFound) || site == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "Website not found."})
		return
	}
	if err != nil {
		s.log.Error("find website", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "Internal error."})
		return
	}
	res, err := s.websiteService(r, user).Permissions(r.Context(), site, false)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// siteRepairPermissions repairs ownership and permissions
// (SitesController::repairPermissions).
func (s *Server) siteRepairPermissions(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
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
	back := fmt.Sprintf("/sites/%d", site.ID)
	res, err := s.websiteService(r, user).Permissions(r.Context(), site, true)
	if err != nil {
		s.flashOpsError(w, r, sess, back, "The file permissions could not be repaired.", err)
		return
	}
	checked, _ := res["checked"].(float64)
	fixed, _ := res["fixed"].(float64)
	skipped, _ := res["skipped_count"].(float64)
	msg := fmt.Sprintf("Checked %d file(s) and folder(s); repaired %d.", int(checked), int(fixed))
	if int(skipped) > 0 {
		msg += fmt.Sprintf(" %d link(s) or special file(s) were left as they are.", int(skipped))
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// runtimeOption is one selectable runtime for the page's version select.
type runtimeOption struct {
	Value    string
	Label    string
	Selected bool
}

// siteRuntime renders the Node.js/Python runtime page (SiteRuntimeController::index).
// tab is "nodejs" or "python"; the page adapts to the site's app type.
func (s *Server) siteRuntime(tab string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFrom(r.Context())
		site, ok := s.scopedSite(w, r, user)
		if !ok {
			return
		}
		s.renderRuntime(w, r, user, site, tab, "", nil)
	}
}

// siteRuntimeUpdate changes the app runtime (SiteRuntimeController::update):
// store the runtime, then re-apply the site's "app" worker on it (best effort
// — a failed agent call is swallowed, as in PHP). A validation failure
// re-renders the page with the field error; a missing site redirects to the
// list with a flash (PHP's act() does not 404 here).
func (s *Server) siteRuntimeUpdate(tab string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := sessionFrom(r.Context())
		user := userFrom(r.Context())
		id, perr := strconv.ParseInt(r.PathValue("id"), 10, 64)
		site, ferr := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), id, user)
		if perr != nil || ferr != nil || site == nil {
			sess.Flash("error", "Website not found.")
			http.Redirect(w, r, "/websites", http.StatusSeeOther)
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
		rt := strings.TrimSpace(r.PostFormValue("app_runtime"))
		// PHP redirects to the tab of the site's (pre-update) app type.
		toTab := "nodejs"
		if hosting.AppType(site) == "python" {
			toTab = "python"
		}
		back := fmt.Sprintf("/sites/%d/%s", site.ID, toTab)
		if err := s.websiteService(r, user).SetRuntime(r.Context(), site, rt); err != nil {
			var ve *hosting.ValidationError
			if errors.As(err, &ve) {
				s.renderRuntime(w, r, user, site, tab, rt, ve.Fields)
				return
			}
			s.flashOpsError(w, r, sess, back, "The runtime could not be changed.", err)
			return
		}
		ws := s.workerService(r, user)
		if workers, werr := ws.ForSite(r.Context(), site.ID); werr == nil {
			for i := range workers {
				if workers[i].Name == "app" {
					_ = ws.ChangeRuntime(r.Context(), site, workers[i], rt)
				}
			}
		}
		sess.Flash("success", "Runtime set to "+rt+".")
		http.Redirect(w, r, back, http.StatusSeeOther)
	}
}

// renderRuntime renders the runtime page: the selectable versions for the
// site's app type, the "app" worker (when one exists), and — on a rejected
// update — the field error with the submitted value (PHP old input).
func (s *Server) renderRuntime(w http.ResponseWriter, r *http.Request, user *identity.User, site *hosting.Website, tab, oldRuntime string, fields map[string]string) {
	// The page adapts to the site's app type (SiteRuntimeController::index):
	// both routes render the same page, and the tab / update route / label /
	// versions all come from the type, never the route.
	typ := hosting.AppType(site)
	pageTab := "nodejs"
	label := "Node.js"
	prefix := "node"
	if typ == "python" {
		pageTab = "python"
		label = "Python"
		prefix = "python"
	}
	// Build the version select: the selectable runtimes for this app type,
	// with the stored runtime selected. On a rejected update the submitted
	// value is shown selected instead (old input, as on the other forms).
	showOld := oldRuntime != "" && oldRuntime != site.AppRuntime
	options := []runtimeOption{}
	for _, v := range s.runtimeService().Selectable(r.Context()) {
		if !strings.HasPrefix(v, prefix+":") {
			continue
		}
		// The stored runtime is selected, unless a rejected submission is
		// being shown instead (old input).
		sel := !showOld && v == site.AppRuntime
		options = append(options, runtimeOption{Value: v, Label: label + " " + strings.TrimPrefix(v, prefix+":"), Selected: sel})
	}
	if showOld {
		options = append([]runtimeOption{{Value: oldRuntime, Label: oldRuntime, Selected: true}}, options...)
	}
	if len(options) == 0 {
		lbl := site.AppRuntime
		if lbl == "" {
			lbl = "None installed"
		}
		options = []runtimeOption{{Value: site.AppRuntime, Label: lbl, Selected: true}}
	}
	var app *hosting.Worker
	if workers, err := hosting.NewWorkerRepository(s.db).List(r.Context(), site.ID); err == nil {
		for i := range workers {
			if workers[i].Name == "app" {
				app = &workers[i]
			}
		}
	}
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	okMsg, _ := sess.PullFlash("success")
	s.render(w, "site_runtime", map[string]any{
		"User":    user,
		"CSRF":    session.CSRFToken(sess),
		"Site":    site,
		"Tab":     pageTab,
		"AppType": typ,
		"Label":   label,
		"Options": options,
		"App":     app,
		"Errors":  fields,
		"Port":    portInProxy(site.ProxyPass),
		"Error":   errMsg,
		"Success": okMsg,
	})
}
