package http

// This file ports ImportController + SitesController::undoImport: importing a
// discovered (non-WhaCP) website into WhaCP management. The agent previews
// the plan, copies the files into a new WhaCP website and switches Nginx over
// (restoring the old configuration on any error); the old configuration and
// files are left in place, and a finished import can be undone.
//
// Routes:
//   GET  /websites/{id}/import      (websites.import, import.run)
//   POST /websites/{id}/import      (websites.import.run, import.run)
//   POST /sites/{id}/import/undo    (sites.import.undo, import.run)
//
// The /websites/{id} page shows the discovered website and its "Import into
// WhaCP" button (WebsitesController::show, the true port of that route).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
)

// importOwner is one selectable owner (administrators may import into any
// account).
type importOwner struct {
	ID          int64
	Username    string
	DisplayName string
}

// importForm is the data of the import preview page.
type importForm struct {
	Found    *hosting.DiscoveredSite
	Plan     *hosting.ImportPlan
	Preview  *hosting.ImportPreview
	Errors   map[string]string
	AgentErr string
	Input    map[string]string
	Owners   []importOwner
}

// discoveryService builds the read-only inventory service.
func (s *Server) discoveryService() *hosting.DiscoveryService {
	return hosting.NewDiscoveryService(s.db, s.agent, s.settings)
}

// importService builds the import service with the audit hook bound to the
// current actor and request.
func (s *Server) importService(r *http.Request, user *identity.User) *hosting.ImportService {
	return hosting.NewImportService(s.db, s.agent, s.websiteService(r, user),
		hosting.NewWebsiteRepository(s.db), s.discoveryService(), s.currentServerID(r),
		s.websiteAuditHook(r, user))
}

// importFound loads the discovered website from the latest inventory.
// ok=false means the page was already answered (agent unavailable) or the
// site is not in the snapshot (redirected with a flashed error).
func (s *Server) importFound(w http.ResponseWriter, r *http.Request, user *identity.User, back, notFound string) (*hosting.DiscoveredSite, bool) {
	inv, err := s.discoveryService().Inventory(r.Context(), s.currentServerID(r), false, userIDPtr(user))
	if err != nil {
		s.renderDiscoveryUnavailable(w, r, err)
		return nil, false
	}
	found := hosting.InventoryWebsite(inv.Data, r.PathValue("id"))
	if found == nil {
		sess := sessionFrom(r.Context())
		sess.Flash("error", notFound)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return nil, false
	}
	return found, true
}

// renderDiscoveryUnavailable answers a page that needs the agent but cannot
// reach it (discovery/unavailable).
func (s *Server) renderDiscoveryUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	msg := "The WhaCP agent could not be reached."
	var ae *agent.Error
	if errors.As(err, &ae) && ae.Message != "" {
		msg = ae.Message
	}
	s.log.Error("discovery unavailable", "error", err)
	s.render(w, "discovery_unavailable", map[string]any{
		"User":      userFrom(r.Context()),
		"CSRF":      session.CSRFToken(sessionFrom(r.Context())),
		"Error":     msg,
		"Back":      "/websites",
		"BackLabel": "Websites",
	})
}

// importPlanFor builds the import plan (and, when it succeeds, the agent
// preview) for the form's input, mapping the failures onto the form.
func (s *Server) importPlanFor(w http.ResponseWriter, r *http.Request, user *identity.User, found *hosting.DiscoveredSite, input map[string]string) *importForm {
	imp := s.importService(r, user)
	f := &importForm{Found: found, Input: input}
	plan, err := imp.Plan(r.Context(), found, input)
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			f.Errors = ve.Fields
		case errors.As(err, &ue):
			f.AgentErr = ue.What
		default:
			s.log.Error("import plan", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return nil
		}
		return f
	}
	f.Plan = plan
	preview, err := imp.Preview(r.Context(), plan)
	switch {
	case err == nil:
		f.Preview = preview
	case errors.As(err, &ue):
		f.AgentErr = ue.What
	default:
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			f.AgentErr = ae.Message
		} else {
			s.log.Error("import preview", "error", err)
		}
	}
	return f
}

// importOwners is the account list for the owner select (administrators only;
// others import into themselves, like the PHP view's $owners).
func (s *Server) importOwners(r *http.Request) []importOwner {
	user := userFrom(r.Context())
	if user == nil || user.RoleScope != "all" {
		return nil
	}
	rows, err := s.db.Query(r.Context(),
		`SELECT id, username, display_name FROM users WHERE is_active = 1 ORDER BY username`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []importOwner{}
	for rows.Next() {
		var o importOwner
		if rows.Scan(&o.ID, &o.Username, &o.DisplayName) == nil {
			out = append(out, o)
		}
	}
	return out
}

// renderImportPage renders the import preview page (websites/import).
func (s *Server) renderImportPage(w http.ResponseWriter, r *http.Request, user *identity.User, f *importForm, jobFlash string) {
	kindLabel := ""
	systemUser, phpVersion := f.Input["system_user"], f.Input["php_version"]
	if f.Plan != nil {
		kindLabel = showKindLabel(f.Plan.Row.Kind)
		systemUser = f.Plan.Row.SystemUser
		phpVersion = f.Plan.Row.PhpVersion
	}
	data := map[string]any{
		"User":         user,
		"CSRF":         session.CSRFToken(sessionFrom(r.Context())),
		"Found":        f.Found,
		"Plan":         f.Plan,
		"Preview":      f.Preview,
		"Errors":       f.Errors,
		"AgentErr":     f.AgentErr,
		"Input":        f.Input,
		"Owners":       f.Owners,
		"Job":          jobFlash,
		"KindLabel":    kindLabel,
		"SystemUser":   systemUser,
		"PHPVersion":   phpVersion,
	}
	s.render(w, "import", data)
}

// importPreview renders the import plan (ImportController::preview).
func (s *Server) importPreview(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	found, ok := s.importFound(w, r, user, "/websites", "That website is no longer in the discovery snapshot. Run discovery again.")
	if !ok {
		return
	}
	q := r.URL.Query()
	input := map[string]string{
		"system_user": q.Get("system_user"),
		"php_version": q.Get("php_version"),
	}
	f := s.importPlanFor(w, r, user, found, input)
	if f == nil {
		return
	}
	f.Owners = s.importOwners(r)
	sess := sessionFrom(r.Context())
	job, _ := sess.PullFlash("job")
	s.renderImportPage(w, r, user, f, job)
}

// importRun starts the import (ImportController::run).
func (s *Server) importRun(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	id := r.PathValue("id")
	back := "/websites/" + id + "/import"
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if r.PostFormValue("confirm") != "1" {
		sess.Flash("error", "Confirm that you reviewed the plan.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	found, ok := s.importFound(w, r, user, back, "That website is no longer in the discovery snapshot.")
	if !ok {
		return
	}
	input := map[string]string{
		"system_user": r.PostFormValue("system_user"),
		"php_version": r.PostFormValue("php_version"),
	}
	f := s.importPlanFor(w, r, user, found, input)
	if f == nil {
		return
	}
	var ve *hosting.ValidationError
	if f.Plan == nil && errors.As(planError(f), &ve) {
		// Rejected again: the plan re-renders with the field errors.
		f.Owners = s.importOwners(r)
		s.renderImportPage(w, r, user, f, "")
		return
	}
	if f.Plan == nil {
		// A user-facing failure (blocked preview or plan error): back with a
		// flashed message, as PHP's backWithErrors/redirectWithFailure do.
		msg := "Please correct the form."
		if f.AgentErr != "" {
			msg = f.AgentErr
		}
		sess.Flash("error", msg)
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	ownerID := userIDPtr(user)
	if user.RoleScope == "all" {
		ownerID = nil
		if v, err := strconv.ParseInt(r.PostFormValue("owner_id"), 10, 64); err == nil && v > 0 {
			ownerID = &v
		}
	}
	imp := s.importService(r, user)
	siteID, job, err := imp.Run(r.Context(), f.Plan, user.ID, ownerID)
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sess.Flash("error", userFacingMsg(ue))
			http.Redirect(w, r, back, http.StatusSeeOther)
			return
		}
		s.log.Error("import run", "error", err)
		sess.Flash("error", "The import could not be started.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job,
		"Import "+found.Domain, "website", strconv.FormatInt(siteID, 10), userIDPtr(user)); err != nil {
		s.log.Error("track import job", "job", job, "error", err)
	}
	sess.Flash("job", job)
	sess.Flash("success", "Import started. The old website keeps serving visitors until the switch at the end.")
	http.Redirect(w, r, fmt.Sprintf("/sites/%d", siteID), http.StatusSeeOther)
}

// planError exposes the form's validation failure (nil when there was none),
// so importRun can tell the two rejected-plan cases apart.
func planError(f *importForm) error {
	if len(f.Errors) > 0 {
		return &hosting.ValidationError{Fields: f.Errors}
	}
	if f.AgentErr != "" {
		return &hosting.UserError{What: f.AgentErr}
	}
	return nil
}

// userFacingMsg is the flashed text of a user-facing error (what + why + fix).
func userFacingMsg(e *hosting.UserError) string {
	parts := []string{e.What}
	if e.Why != "" {
		parts = append(parts, e.Why)
	}
	if e.Fix != "" {
		parts = append(parts, e.Fix)
	}
	return strings.Join(parts, " ")
}

// importUndo undoes a finished import (SitesController::undoImport).
func (s *Server) importUndo(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
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
	if err := s.importService(r, user).Undo(r.Context(), site); err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sess.Flash("error", ue.What)
		} else {
			s.log.Error("import undo", "id", site.ID, "error", err)
			sess.Flash("error", "The import could not be undone.")
		}
		http.Redirect(w, r, fmt.Sprintf("/sites/%d", site.ID), http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Import undone: the original configuration serves "+site.Domain+" again, and the WhaCP copy was removed.")
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// GET /websites/{id} — the discovered website (WebsitesController::show).
// ---------------------------------------------------------------------------

// websiteShow renders the discovered website (read-only detail) with the
// "Import into WhaCP" action. Mirrors WebsitesController::show (route
// "websites.show", websites.view). A website that is not in the latest
// discovery snapshot is a 404 — the id never grants access to managed data.
func (s *Server) websiteShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	inv, err := s.discoveryService().Inventory(r.Context(), s.currentServerID(r), false, userIDPtr(user))
	if err != nil {
		s.renderDiscoveryUnavailable(w, r, err)
		return
	}
	site := hosting.InventoryWebsite(inv.Data, r.PathValue("id"))
	if site == nil {
		renderStatus(w, s, "error", map[string]any{
			"Title":   "Website not found",
			"Message": "This website was not found in the latest discovery snapshot. It may have been removed or renamed.",
		}, http.StatusNotFound)
		return
	}
	// The cron jobs that reference this website's directory or domain.
	var cron []hosting.DiscoveredCronJob
	base := ""
	if site.DocumentRoot != "" {
		if i := strings.LastIndex(site.DocumentRoot, "/"); i > 0 {
			base = site.DocumentRoot[:i]
		}
	}
	for _, j := range hosting.InventoryCronJobs(inv.Data) {
		if (site.DocumentRoot != "" && base != "" && strings.Contains(j.Command, base)) ||
			strings.Contains(j.Command, site.Domain) {
			cron = append(cron, j)
		}
	}
	warnDays, _ := s.settings.Int(r.Context(), "ssl_warning_days", 14)
	expiresDate := ""
	if site.SSL != nil && site.SSL.NotAfter != "" {
		expiresDate = site.SSL.NotAfter
		if t, err := time.Parse(time.RFC3339, site.SSL.NotAfter); err == nil {
			expiresDate = t.UTC().Format("2006-01-02")
		}
	}
	data := map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sessionFrom(r.Context())),
		"Site":          site,
		"Cron":          cron,
		"DiscoveredAgo": timeAgo(inv.CreatedAt),
		"DiscoveredAt":  stampUTC(inv.CreatedAt),
		"ExpiresDate":   expiresDate,
		"SSLBadge":      discoveredSSLBadge(site, warnDays),
		"CanImport":     s.can(r, "import.run"),
	}
	s.render(w, "website_show", data)
}

// discoveredSSLBadge is the SSL card's status label for a discovered site
// (the view's $st computation).
func discoveredSSLBadge(site *hosting.DiscoveredSite, warnDays int) string {
	if site.SSL == nil {
		return ""
	}
	if site.SSL.Status == "valid" && site.SSL.DaysRemaining <= warnDays {
		return "expiring"
	}
	return site.SSL.Status
}

// stampUTC renders a snapshot time as "Y-m-d H:i" UTC ("" when unreadable).
func stampUTC(iso string) string {
	if iso == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339, iso); err == nil {
		return t.UTC().Format("2006-01-02 15:04")
	}
	return iso
}

// websitesIndex lists the managed websites plus the other (discovered,
// not-yet-managed) websites on the server with the import entry points.
// Mirrors WebsitesController::index (route "websites", websites.view).
func (s *Server) websitesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sites, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list websites", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The other (unmanaged) websites from the latest discovery snapshot. The
	// section is absent when the agent cannot be asked (as in PHP: the page
	// renders with discoveredAt = null).
	var discovered []hosting.DiscoveredSite
	var warnings []string
	var catchAll int
	var discoveredAt string
	if inv, invErr := s.discoveryService().Inventory(r.Context(), s.currentServerID(r), false, userIDPtr(user)); invErr == nil {
		for _, d := range hosting.InventoryWebsites(inv.Data) {
			if discoveredSiteManaged(d) {
				continue // created by WhaCP: listed as managed, not discovered
			}
			discovered = append(discovered, d)
		}
		var head struct {
			Websites struct {
				CatchAllVhosts int      `json:"catch_all_vhosts"`
				Warnings       []string `json:"warnings"`
			} `json:"websites"`
		}
		if json.Unmarshal(inv.Data, &head) == nil {
			warnings = head.Websites.Warnings
			catchAll = head.Websites.CatchAllVhosts
		}
		discoveredAt = inv.CreatedAt
	}
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	okMsg, _ := sess.PullFlash("success")
	s.render(w, "websites", map[string]any{
		"User":           user,
		"CSRF":           session.CSRFToken(sess),
		"Sites":          sites,
		"Error":          errMsg,
		"Success":        okMsg,
		"CanManage":      s.can(r, "websites.manage"),
		"Discovered":     discovered,
		"DiscoveredAt":   discoveredAt,
		"DiscoveredAgo":  timeAgo(discoveredAt),
		"Total":          len(discovered),
		"CatchAll":       catchAll,
		"Warnings":       warnings,
		"CanImport":      s.can(r, "import.run"),
	})
}

// discoveredSiteManaged reports whether discovery found the website's
// configuration in the WhaCP location (config file names whacp-*), i.e. the
// website is managed and belongs in the managed list, not the other list.
func discoveredSiteManaged(d hosting.DiscoveredSite) bool {
	for _, f := range d.ConfigFiles {
		if i := strings.LastIndex(f, "/"); i >= 0 && strings.HasPrefix(f[i+1:], "whacp-") {
			return true
		}
	}
	return false
}
