package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// This file ports the write routes of app/Http/Controllers/SitesController:
// creating websites (new/store), saving settings (update), toggling, deleting,
// and adding subdomains and addon domains. All routes are websites.manage
// gated, CSRF-protected, and tenant-scoped; form validation errors re-render
// the form with the field messages and the submitted values (PHP re-renders
// the old input too).

// websiteAuditHook builds the audit callback bound to the current actor and
// request (mirrors the PHP controller wiring: every service records through
// the same AuditLogger).
func (s *Server) websiteAuditHook(r *http.Request, user *identity.User) hosting.Auditor {
	return func(ctx context.Context, action, targetType, target, result string, details map[string]any) {
		// The services pass the type explicitly; keep the action-prefix
		// inference as a fallback for any that do not.
		if targetType == "" {
			targetType = "website"
		}
		if strings.HasPrefix(action, "dns.") {
			targetType = "dns_zone"
		}
		// The scheduler acts as a system user with no row of its own.
		var uid *int64
		if user.ID > 0 {
			id := user.ID
			uid = &id
		}
		s.audit.Record(ctx, audit.Entry{
			UserID:     uid,
			Username:   user.Username,
			Action:     action,
			TargetType: targetType,
			TargetID:   target,
			Result:     result,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    details,
		})
	}
}

// websiteService builds the website write service with the audit hook bound
// to the current actor and request.
func (s *Server) websiteService(r *http.Request, user *identity.User) *hosting.WebsiteService {
	ws := hosting.NewWebsiteService(
		hosting.NewWebsiteRepository(s.db),
		s.agent,
		s.features,
		hosting.NewPlanLimits(s.db),
		hosting.NewDnsRepository(s.db),
		s.settings,
		s.websiteAuditHook(r, user),
		s.currentServerID(r),
	)
	s.onWebsiteCreated(ws, r, user)
	return ws
}

// onWebsiteCreated is the "website.created" listener: DNS (and email) for the
// new website, then AutoSSL (Application::registerListeners).
func (s *Server) onWebsiteCreated(ws *hosting.WebsiteService, r *http.Request, user *identity.User) {
	serverID := s.currentServerID(r)
	ws.OnCreated(func(ctx context.Context, site *hosting.Website) {
		prov := hosting.NewSiteProvisioner(s.db, serverID, s.siteDnsService(r, user), hosting.NewDnsRepository(s.db),
			s.siteMailService(r, user), hosting.NewMailRepository(s.db), hosting.NewAccountOptionStore(s.db), s.settings, s.websiteAuditHook(r, user))
		prov.Provision(ctx, site)
		hosting.NewAutoSslService(s.db, serverID, s.settings, s.agent, nil, nil, nil, nil).Mark(ctx, site)
	})
}

// postValues is the submitted form (first value per key, as PHP $request->post()).
func postValues(r *http.Request) map[string]string {
	if err := r.ParseForm(); err != nil {
		return map[string]string{}
	}
	// A switch posts a hidden 0 before its checkbox's 1; as in PHP's $_POST the
	// last value wins (the first one made every ticked switch read as off).
	out := map[string]string{}
	for k, v := range r.PostForm {
		if len(v) > 0 {
			out[k] = v[len(v)-1]
		}
	}
	return out
}

// siteFormView carries everything the create/edit form template needs.
type siteFormView struct {
	Title          string
	Action         string
	Site           *hosting.Website
	Form           map[string]string
	Errors         map[string]string
	Error          string
	PhpVersions    []string
	DefaultPhp     string
	HardenDefault  bool
	ProfileDefault string
	Owners         []identity.ManagedUser
	AllowedTypes   map[string]bool
}

type iniField struct {
	Key, Hint, Value, Error string
	Select                  bool
	Options                 []string
}

type iniGroup struct {
	Name   string
	Fields []iniField
}

// iniGroups builds the "Additional PHP directives" editor: every directive the
// catalogue offers, grouped, with the value the website has (or what was just
// submitted) and its error.
func iniGroups(f *siteFormView) ([]iniGroup, int) {
	stored := map[string]string{}
	if f.Site != nil {
		for k, v := range f.Site.PhpSettings.INI {
			stored[k] = v
		}
	}
	var groups []iniGroup
	idx := map[string]int{}
	set := 0
	for _, d := range hosting.PhpDirectives {
		val := stored[d.Key]
		if v, ok := f.Form["ini["+d.Key+"]"]; ok {
			val = v
		}
		if val != "" {
			set++
		}
		fld := iniField{Key: d.Key, Hint: d.Hint, Value: val, Error: f.Errors["ini."+d.Key]}
		switch {
		case d.Kind == "bool":
			fld.Select, fld.Options = true, []string{"On", "Off"}
		case strings.HasPrefix(d.Kind, "enum:"):
			fld.Select, fld.Options = true, strings.Split(strings.TrimPrefix(d.Kind, "enum:"), "|")
		}
		g, ok := idx[d.Group]
		if !ok {
			g = len(groups)
			idx[d.Group] = g
			groups = append(groups, iniGroup{Name: d.Group})
		}
		groups[g].Fields = append(groups[g].Fields, fld)
	}
	return groups, set
}

// renderSiteForm re-renders the website form. A 200 status (not a redirect) is
// deliberate: the submitted values and field errors are rendered inline, so
// nothing is lost on a rejected form (parity with PHP's old input + errors).
func (s *Server) renderSiteForm(w http.ResponseWriter, r *http.Request, user *identity.User, f *siteFormView) {
	data := map[string]any{
		"User":           user,
		"CSRF":           session.CSRFToken(sessionFrom(r.Context())),
		"Title":          f.Title,
		"Action":         f.Action,
		"Site":           f.Site,
		"Form":           f.Form,
		"Errors":         f.Errors,
		"Error":          f.Error,
		"PhpVersions":    f.PhpVersions,
		"DefaultPhp":     f.DefaultPhp,
		"HardenDefault":  f.HardenDefault,
		"ProfileDefault": f.ProfileDefault,
		"Owners":         f.Owners,
		"AllowedTypes":   f.AllowedTypes,
	}
	groups, set := iniGroups(f)
	data["IniGroups"], data["IniSet"] = groups, set
	docSub := f.Form["document_root_sub"]
	if _, ok := f.Form["document_root_sub"]; !ok {
		docSub = "public_html"
		if f.Site != nil && strings.HasPrefix(f.Site.DocumentRoot, f.Site.Root) {
			if sub := strings.TrimLeft(strings.TrimPrefix(f.Site.DocumentRoot, f.Site.Root), "/"); sub != "" {
				docSub = sub
			}
		}
	}
	data["DocSub"] = docSub
	data["DocPrefix"] = "<sites root>/<domain>/"
	if f.Site != nil && f.Site.Root != "" {
		data["DocPrefix"] = f.Site.Root + "/"
	}
	s.render(w, "site_form", data)
}

// siteFormBase fills the view fields shared by create and edit.
func (s *Server) siteFormBase(r *http.Request, user *identity.User, site *hosting.Website) (*siteFormView, error) {
	svc := s.websiteService(r, user)
	versions := svc.PhpVersions(r.Context())
	owners := []identity.ManagedUser{}
	if user.RoleScope == "all" {
		o, err := s.users.ManageableBy(r.Context(), user)
		if err != nil {
			return nil, err
		}
		owners = o
	}
	allowed := map[string]bool{}
	for _, key := range []string{"type_php", "type_static", "type_nodejs", "type_python", "type_proxy"} {
		ok, err := s.features.FeatureEnabled(r.Context(), user, key)
		if err != nil {
			return nil, err
		}
		allowed[key] = ok
	}
	form := map[string]string{}
	if site != nil {
		form = existingSiteForm(site)
	} else if form["kind"] == "" {
		// The create form starts on PHP, the first (and default) type.
		form["kind"] = "php"
		for k, v := range hosting.PhpFormDefaults() {
			form[k] = v
		}
	}
	title := "Create website"
	action := "/websites/new"
	if site != nil {
		title = "Settings"
		action = fmt.Sprintf("/sites/%d/settings", site.ID)
	}
	return &siteFormView{
		Title:          title,
		Action:         action,
		Site:           site,
		Form:           form,
		PhpVersions:    versions,
		DefaultPhp:     svc.DefaultPhpVersion(versions),
		HardenDefault:  svc.HardenNewSites(),
		ProfileDefault: svc.DefaultPhpProfile(),
		Owners:         owners,
		AllowedTypes:   allowed,
	}, nil
}

// existingSiteForm pre-fills the form from a stored website (PHP old input).
func existingSiteForm(site *hosting.Website) map[string]string {
	in := map[string]string{
		"domain":                  site.Domain,
		"aliases":                 strings.Join(site.Aliases, ", "),
		"system_user":             site.SystemUser,
		"php_version":             site.PhpVersion,
		"app_runtime":             site.AppRuntime,
		"proxy_pass":              site.ProxyPass,
		"memory_limit_mb":         strconv.Itoa(site.PhpSettings.MemoryLimitMB),
		"upload_max_mb":           strconv.Itoa(site.PhpSettings.UploadMaxMB),
		"max_execution_seconds":   strconv.Itoa(site.PhpSettings.MaxExecutionSeconds),
		"max_children":            strconv.Itoa(site.PhpSettings.MaxChildren),
		"start_servers":           strconv.Itoa(site.PhpSettings.StartServers),
		"min_spare_servers":       strconv.Itoa(site.PhpSettings.MinSpareServers),
		"max_spare_servers":       strconv.Itoa(site.PhpSettings.MaxSpareServers),
		"idle_timeout_seconds":    strconv.Itoa(site.PhpSettings.IdleTimeoutSeconds),
		"max_requests":            strconv.Itoa(site.PhpSettings.MaxRequests),
		"request_timeout_seconds": strconv.Itoa(site.PhpSettings.RequestTimeoutSeconds),
		"slowlog_seconds":         strconv.Itoa(site.PhpSettings.SlowlogSeconds),
		"php_profile":             site.PhpSettings.Profile,
		"disable_functions":       site.PhpSettings.DisableFunctions,
		"pm":                      site.PhpSettings.PM,
		"client_max_body_mb":      strconv.Itoa(site.ClientMaxBodyMB),
		"redirects":               hosting.RedirectsText(site.Redirects),
	}
	if site.PhpSettings.Harden {
		in["harden"] = "on"
	}
	kind := hosting.AppType(site)
	if kind == "" {
		kind = site.Kind
	}
	in["kind"] = kind
	if m := portInProxy(site.ProxyPass); m != "" {
		in["app_port"] = m
	}
	if site.OwnerID.Valid {
		in["owner_id"] = strconv.FormatInt(site.OwnerID.Int64, 10)
	}
	return in
}

func portInProxy(proxy string) string {
	if i := strings.LastIndexByte(proxy, ':'); i >= 0 {
		return proxy[i+1:]
	}
	return ""
}

// websitesNew shows the create-website form (SitesController::create).
func (s *Server) websitesNew(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	f, err := s.siteFormBase(r, user, nil)
	if err != nil {
		s.log.Error("website form", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.renderSiteForm(w, r, user, f)
}

// websitesStore creates a website (SitesController::store).
func (s *Server) websitesStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	in := postValues(r)
	if !session.ValidateCSRF(sess, in["_token"]) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	svc := s.websiteService(r, user)
	id, err := svc.Create(r.Context(), in, user, nil)
	if err != nil {
		s.handleSiteFormError(w, r, user, nil, in, err, "/websites/new")
		return
	}
	// Node.js/Python sites get their app started as a Worker on the chosen
	// runtime (SitesController::store → provisionApp, best effort — the
	// Runtime page is the fix point), and land on that runtime page.
	back, msg := fmt.Sprintf("/sites/%d", id), "Website created. Nginx and PHP-FPM were validated and reloaded."
	if in["kind"] == "nodejs" || in["kind"] == "python" {
		s.provisionApp(r, user, id, in)
		back, msg = fmt.Sprintf("/sites/%d/%s", id, in["kind"]), "Website created. Set up your app below."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// websiteSettings shows the edit form (SitesController::settings, GET).
func (s *Server) websiteSettings(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	f, err := s.siteFormBase(r, user, site)
	if err != nil {
		s.log.Error("website form", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.renderSiteForm(w, r, user, f)
}

// websiteSettingsSave updates a website's settings (SitesController::update).
func (s *Server) websiteSettingsSave(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	in := postValues(r)
	if !session.ValidateCSRF(sess, in["_token"]) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	svc := s.websiteService(r, user)
	if err := svc.Update(r.Context(), site, in, user); err != nil {
		s.handleSiteFormError(w, r, user, site, in, err, fmt.Sprintf("/sites/%d/settings", site.ID))
		return
	}
	sess.Flash("success", "Settings saved and applied.")
	http.Redirect(w, r, fmt.Sprintf("/sites/%d", site.ID), http.StatusSeeOther)
}

// handleSiteFormError renders a rejected form: validation errors inline with
// the submitted values, user-facing failures as a message, unexpected errors
// flashed and redirected.
func (s *Server) handleSiteFormError(w http.ResponseWriter, r *http.Request, user *identity.User, site *hosting.Website, in map[string]string, err error, backTo string) {
	var ve *hosting.ValidationError
	var ue *hosting.UserError
	switch {
	case errors.As(err, &ve):
		f, fErr := s.siteFormBase(r, user, site)
		if fErr != nil {
			sess := sessionFrom(r.Context())
			sess.Flash("error", "Please correct the form.")
			http.Redirect(w, r, backTo, http.StatusSeeOther)
			return
		}
		f.Form = in
		f.Errors = ve.Fields
		s.renderSiteForm(w, r, user, f)
	case errors.As(err, &ue):
		f, fErr := s.siteFormBase(r, user, site)
		if fErr != nil {
			sess := sessionFrom(r.Context())
			sess.Flash("error", ue.What)
			http.Redirect(w, r, backTo, http.StatusSeeOther)
			return
		}
		f.Form = in
		f.Error = ue.What
		s.renderSiteForm(w, r, user, f)
	default:
		s.log.Error("website save", "error", err)
		sess := sessionFrom(r.Context())
		sess.Flash("error", "The website could not be saved.")
		http.Redirect(w, r, backTo, http.StatusSeeOther)
	}
}

// websiteToggle enables or disables a website (SitesController::toggle).
func (s *Server) websiteToggle(w http.ResponseWriter, r *http.Request) {
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
	svc := s.websiteService(r, user)
	if err := svc.SetEnabled(r.Context(), site, !site.Enabled); err != nil {
		s.log.Error("toggle website", "id", site.ID, "error", err)
		sess.Flash("error", "The website could not be changed.")
		http.Redirect(w, r, fmt.Sprintf("/sites/%d", site.ID), http.StatusSeeOther)
		return
	}
	if site.Enabled {
		sess.Flash("success", "Website disabled. Its files and settings are kept.")
	} else {
		sess.Flash("success", "Website enabled.")
	}
	back := fmt.Sprintf("/sites/%d", site.ID)
	switch r.PostFormValue("return_to") {
	case "websites":
		back = "/websites"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// websiteDelete removes a website (SitesController::destroy). The typed
// confirm_domain must match the website's domain.
func (s *Server) websiteDelete(w http.ResponseWriter, r *http.Request) {
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
	if r.PostFormValue("confirm_domain") != site.Domain {
		sess.Flash("error", "Type the domain name to confirm deletion.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	svc := s.websiteService(r, user)
	warnings, err := svc.Delete(r.Context(), site,
		r.PostFormValue("remove_files") == "1", r.PostFormValue("remove_user") == "1")
	if err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sess.Flash("error", ue.What)
		} else {
			s.log.Error("delete website", "id", site.ID, "error", err)
			sess.Flash("error", "The website could not be deleted.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	msg := "Website " + site.Domain + " was removed."
	if len(warnings) > 0 {
		msg += " " + strings.Join(warnings, " ")
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

// siteAddons shows the addon domains of a website (SitesController::addons).
func (s *Server) siteAddons(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	repo := hosting.NewWebsiteRepository(s.db)
	children, err := repo.Children(r.Context(), s.currentServerID(r), site.ID)
	if err != nil {
		s.log.Error("list addons", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	addons := []hosting.Website{}
	for _, c := range children {
		if !strings.HasSuffix(c.Domain, "."+site.Domain) {
			addons = append(addons, c)
		}
	}
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	okMsg, _ := sess.PullFlash("success")
	s.render(w, "site_addons", map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Site":       site,
		"Addons":     addons,
		"CanConvert": s.can(r, "users.manage"),
		"Error":      errMsg,
		"Success":    okMsg,
		"OldDomain":  "",
	})
}

// siteSubdomainsStore adds a subdomain (SitesController::storeSubdomain).
func (s *Server) siteSubdomainsStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	in := postValues(r)
	if !session.ValidateCSRF(sess, in["_token"]) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	s.storeChild(w, r, user, site, in, "subdomain")
}

// siteAddonsStore adds an addon domain (SitesController::storeAddon).
func (s *Server) siteAddonsStore(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	in := postValues(r)
	if !session.ValidateCSRF(sess, in["_token"]) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	s.storeChild(w, r, user, site, in, "addon")
}

// storeChild runs CreateChild and redirects with the result message (PHP
// storeSubdomain/storeAddon share the flow).
func (s *Server) storeChild(w http.ResponseWriter, r *http.Request, user *identity.User, parent *hosting.Website, in map[string]string, typ string) {
	sess := sessionFrom(r.Context())
	svc := s.websiteService(r, user)
	back := fmt.Sprintf("/sites/%d/%s", parent.ID, typ+"s")
	childID, dnsAdded, err := svc.CreateChild(r.Context(), parent, typ, in, user)
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			s.renderChildError(w, r, user, parent, in, ve, typ)
		case errors.As(err, &ue):
			sess.Flash("error", ue.What)
			http.Redirect(w, r, back, http.StatusSeeOther)
		default:
			s.log.Error("create child site", "parent", parent.ID, "error", err)
			sess.Flash("error", "The domain could not be created.")
			http.Redirect(w, r, back, http.StatusSeeOther)
		}
		return
	}
	// Re-fetch the child unscoped (the caller just created it, PHP re-fetches
	// with find(server, id, null) for the message).
	child, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), childID, nil)
	if err != nil || child == nil {
		child = &hosting.Website{ID: childID, Domain: in["subdomain"] + "." + parent.Domain, DocumentRoot: ""}
	}
	what := "Addon domain"
	if typ == "subdomain" {
		what = "Subdomain"
	}
	msg := what + " " + child.Domain + " created. Its files are in " + child.DocumentRoot + "."
	if dnsAdded {
		msg += " A DNS record was added."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// renderChildError re-renders the subdomain/addon page with the field error
// and the submitted value.
func (s *Server) renderChildError(w http.ResponseWriter, r *http.Request, user *identity.User, parent *hosting.Website, in map[string]string, ve *hosting.ValidationError, typ string) {
	repo := hosting.NewWebsiteRepository(s.db)
	children, _ := repo.Children(r.Context(), s.currentServerID(r), parent.ID)
	field := "subdomain"
	if typ == "addon" {
		field = "domain"
	}
	s.render(w, "site_"+typ+"s", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sessionFrom(r.Context())),
		"Site":      parent,
		"Children":  children,
		"Addons":    children,
		"Error":     ve.Fields[field],
		"OldLabel":  in[field],
		"OldDomain": in[field],
		"OldWWW":    in["www"],
		"OldDNS":    in["dns"],
	})
}

// scopedSite loads a website under the user's tenant scope (the controller's
// site() helper) and 404s when it does not exist or is not visible.
func (s *Server) scopedSite(w http.ResponseWriter, r *http.Request, user *identity.User) (*hosting.Website, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return nil, false
	}
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), id, user)
	if errors.Is(err, hosting.ErrWebsiteNotFound) || site == nil {
		s.notFound(w, r)
		return nil, false
	}
	if err != nil {
		s.log.Error("find website", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	return site, true
}
