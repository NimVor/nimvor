package http

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mailIndex lists the email domains the user may see, tenant-scoped
// (website-aware). Gated by mail.manage, which the email plan feature withholds
// when disabled. Mirrors MailController::index (route "mail", mail.manage).
func (s *Server) mailIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	domains, err := hosting.NewMailRepository(s.db).Domains(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("list mail domains", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess := sessionFrom(r.Context())
	s.render(w, "mail", map[string]any{
		"User":    user,
		"CSRF":    session.CSRFToken(sess),
		"Domains": domains,
		"Success": pullFlashKey(sess, "success"),
		"Error":   pullFlashKey(sess, "error"),
	})
}

// siteMailService builds the mail service bound to the current actor.
func (s *Server) siteMailService(r *http.Request, user *identity.User) *hosting.MailService {
	return hosting.NewMailService(s.db, hosting.NewMailRepository(s.db), s.agent,
		hosting.NewWebsiteRepository(s.db), hosting.NewAccountOptionStore(s.db),
		hosting.NewPlanLimits(s.db), s.policy,
		s.siteDnsService(r, user), hosting.NewDnsRepository(s.db),
		s.websiteAuditHook(r, user))
}

// siteMail renders the website's email page (MailController::site): the
// domain's page when the user may see it, the "managed by another account"
// notice, or the set-up card. Gated by mail.manage.
func (s *Server) siteMail(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	site := s.showSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.showNotFound(w)
		return
	}
	repo := hosting.NewMailRepository(s.db)
	domain, err := repo.DomainForWebsite(r.Context(), site.ID)
	if err != nil {
		s.log.Error("site mail domain", "site", site.ID, "error", err)
		s.showNotFound(w)
		return
	}
	if domain != nil {
		if owned, err := repo.Domain(r.Context(), s.currentServerID(r), domain.ID, user); err == nil && owned != nil {
			http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", domain.ID), http.StatusSeeOther)
			return
		}
		sess.Flash("error", "Email for this domain is managed by another account.")
		http.Redirect(w, r, fmt.Sprintf("/sites/%d", site.ID), http.StatusSeeOther)
		return
	}
	svc := s.siteMailService(r, user)
	status, err := svc.Status(r.Context())
	if err != nil {
		status = nil // the agent keeps the truth; best effort, as in the PHP
	}
	ready := status != nil && status.Configured && status.Running.Postfix
	s.render(w, "site_mail", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Site":      site,
		"Status":    status,
		"Ready":     ready,
		"Installed": status != nil && status.Installed,
		"Staff":     s.can(r, "server.manage"),
		"Success":   pullFlashKey(sess, "success"),
		"Error":     pullFlashKey(sess, "error"),
	})
}

// mailDomainStore hosts email for a domain (MailController::store, route
// mail.store, mail.manage). The website set-up page may ask for errors back
// there.
func (s *Server) mailDomainStore(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !session.ValidateCSRF(sess, r.PostFormValue("_token")) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	back := "/mail"
	if t := r.PostFormValue("return_to"); siteMailTargetRe.MatchString(t) {
		back = t
	}
	id, err := s.siteMailService(r, user).AddDomain(r.Context(), s.currentServerID(r), postValues(r), user)
	if err != nil {
		var ve *hosting.ValidationError
		var ue *hosting.UserError
		switch {
		case errors.As(err, &ve):
			sess.Flash("error", firstValidationError(ve))
		case errors.As(err, &ue):
			sess.Flash("error", userFacingMsg(ue))
		default:
			s.log.Error("host mail domain", "error", err)
			sess.Flash("error", "Email could not be set up for the domain.")
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Email is hosted for the domain. Publish the DNS records below.")
	http.Redirect(w, r, fmt.Sprintf("/mail/domains/%d", id), http.StatusSeeOther)
}

// mailDomainShow renders one mail domain (MailController::show, route
// mail.domain, mail.manage).
func (s *Server) mailDomainShow(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	repo := hosting.NewMailRepository(s.db)
	domain, err := repo.Domain(r.Context(), s.currentServerID(r), pathIDOrZero(r), user)
	if err != nil {
		s.log.Error("show mail domain", "error", err)
		s.showNotFound(w)
		return
	}
	if domain == nil {
		s.showNotFound(w)
		return
	}
	edit := int64(0)
	if v := r.URL.Query().Get("edit"); v != "" {
		edit, _ = strconv.ParseInt(v, 10, 64)
	}
	s.renderMailDomain(w, r, user, domain, mailDomainEdit{Edit: edit})
}
