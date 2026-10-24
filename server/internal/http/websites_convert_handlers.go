package http

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// This file ports app/Http/Controllers/AddonConversionController: POST
// /sites/{id}/convert turns an addon domain into a hosting account of its
// own (users.manage). PHP renders the confirmation as a modal on the
// parent's addons page; the panel shows it as its own page (GET) and
// re-renders validation errors in place (the same convention as the website
// form and the workers page).

// accountService builds the one-step account service with the audit hook
// bound to the current actor (same wiring as the website service).
func (s *Server) accountService(r *http.Request, user *identity.User) *hosting.AccountService {
	audit := s.websiteAuditHook(r, user)
	return hosting.NewAccountService(
		hosting.NewUserService(
			identity.NewRepository(s.db),
			s.roles,
			security.NewPolicy(s.settings),
			hosting.NewPlanRepository(s.db),
			hosting.NewPlanLimits(s.db),
			audit,
		),
		identity.NewRepository(s.db),
		s.websiteService(r, user),
		hosting.NewAccountOptionStore(s.db),
		s.db,
		audit,
	)
}

// conversionService builds the addon-conversion service for the current actor.
func (s *Server) conversionService(r *http.Request, user *identity.User) *hosting.AddonConversionService {
	audit := s.websiteAuditHook(r, user)
	return hosting.NewAddonConversionService(
		s.db,
		s.agent,
		s.accountService(r, user),
		identity.NewRepository(s.db),
		hosting.NewWebsiteRepository(s.db),
		s.websiteService(r, user),
		hosting.NewCronService(s.db, s.agent, hosting.NewPlanLimits(s.db), audit),
		s.workerService(r, user),
		hosting.NewPlanLimits(s.db),
		audit,
		s.currentServerID(r),
	)
}

// convertBack is where the conversion lands (PHP: the parent's domains page).
func convertBack(site *hosting.Website) string {
	if site.ParentID.Valid {
		return fmt.Sprintf("/sites/%d/addons", site.ParentID.Int64)
	}
	return fmt.Sprintf("/sites/%d", site.ID)
}

// suggestedUsername is the PHP modal's default: the domain's first label,
// non-alphanumerics stripped, capped at 16 characters.
func suggestedUsername(domain string) string {
	label := domain
	if i := strings.IndexByte(domain, '.'); i >= 0 {
		label = domain[:i]
	}
	var b strings.Builder
	for _, r := range strings.ToLower(label) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "site"
	}
	out := b.String()
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

// convertFormValues is the form's current values: the create defaults
// (username suggested from the domain) or the rejected submission minus the
// passwords (PHP: array_diff_key($input, [password, password_confirmation])).
func convertFormValues(site *hosting.Website, old map[string]string) map[string]string {
	form := map[string]string{"username": suggestedUsername(site.Domain)}
	for k, v := range old {
		if k == "password" || k == "password_confirmation" || k == "_token" {
			continue
		}
		form[k] = v
	}
	return form
}

// convertData assembles the template data for the conversion page.
func (s *Server) convertData(r *http.Request, user *identity.User, site *hosting.Website, form map[string]string, fields map[string]string) (map[string]any, error) {
	conv := s.conversionService(r, user)
	preview, err := conv.Preview(r.Context(), site)
	if err != nil {
		return nil, err
	}
	plans, err := hosting.NewPlanRepository(s.db).Visible(r.Context(), user)
	if err != nil {
		return nil, err
	}
	domains := make([]string, 0, len(preview.Websites))
	for _, w := range preview.Websites {
		domains = append(domains, w.Domain)
	}
	parentID := int64(0)
	if site.ParentID.Valid {
		parentID = site.ParentID.Int64
	}
	if fields == nil {
		fields = map[string]string{}
	}
	return map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sessionFrom(r.Context())),
		"Site":          site,
		"ParentID":      parentID,
		"Preview":       preview,
		"WebsitesText":  strings.Join(domains, ", "),
		"DatabasesText": strings.Join(preview.Databases, ", "),
		"Plans":         plans,
		"Form":          form,
		"Fields":        fields,
	}, nil
}

// siteConvertPage renders the conversion confirmation (the "Convert to
// account" modal on PHP's addons page).
func (s *Server) siteConvertPage(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	site, ok := s.scopedSite(w, r, user)
	if !ok {
		return
	}
	data, err := s.convertData(r, user, site, convertFormValues(site, nil), nil)
	if err != nil {
		s.log.Error("conversion preview", "site", site.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	okMsg, _ := sess.PullFlash("success")
	data["Error"] = errMsg
	data["Success"] = okMsg
	s.render(w, "site_convert", data)
}

// siteConvert converts the addon domain (AddonConversionController::convert).
func (s *Server) siteConvert(w http.ResponseWriter, r *http.Request) {
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
	back := convertBack(site)
	conv := s.conversionService(r, user)
	res, err := conv.Convert(r.Context(), site, in, user)
	if err != nil {
		var ve *hosting.ValidationError
		if errors.As(err, &ve) {
			// Re-render with the field errors and the old input (minus the
			// passwords), as PHP's backWithErrors does.
			data, derr := s.convertData(r, user, site, convertFormValues(site, in), ve.Fields)
			if derr != nil {
				s.log.Error("conversion preview", "site", site.ID, "error", derr)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			s.render(w, "site_convert", data)
			return
		}
		var ue *hosting.UserError
		if errors.As(err, &ue) {
			sess.Flash("error", ue.What)
		} else if ae := (&agent.Error{}); errors.As(err, ae) {
			sess.Flash("error", site.Domain+" was not converted; everything was put back as it was. "+ae.Message)
		} else {
			// Not an agent failure: the steps are not necessarily undone.
			s.log.Error("convert addon", "site", site.ID, "error", err)
			sess.Flash("error", "The conversion of "+site.Domain+" failed; some steps may have been applied. Check the audit log.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", site.Domain+" is now the hosting account "+res.Username+", with its files, databases, DNS zone and email.")
	// PHP lands on the new account's edit page; the /users UI is a later
	// batch, so the parent's addons page carries the message.
	http.Redirect(w, r, back, http.StatusSeeOther)
}
