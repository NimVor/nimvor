package http

// This file ports the per-website SSL/HTTPS area (SslController +
// SslService): the certificate state (inspected from the agent), issuing
// Let's Encrypt certificates, uploading custom certificates, renewal,
// disabling HTTPS, the HTTPS options (redirect/HSTS) and the security
// headers. The certificate work happens on the agent; the website row is
// switched through WebsiteService.patch (Nginx regenerated + reload gated
// by nginx -t, previous state restored on failure). Routes:
//   GET  /sites/{id}/ssl            (ssl.view)
//   POST /sites/{id}/ssl/issue      (ssl.manage)
//   POST /sites/{id}/ssl/custom     (ssl.manage)
//   POST /sites/{id}/ssl/renew      (ssl.manage)
//   POST /sites/{id}/ssl/disable    (ssl.manage)
//   POST /sites/{id}/ssl/options    (ssl.manage)
//   POST /sites/{id}/security-headers (ssl.manage)

import (
	"errors"
	"net/http"
	"net/mail"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// sslCertView is the computed certificate state (the view's $st).
type sslCertView struct {
	State       string // valid | expiring | expired | error
	Label       string // Secure | Expires soon | Expired | Certificate problem
	Expires     string // Y-m-d
	Days        int
	Covers      string
	Issuer      string
	AutoRenewal string // active (…) | not scheduled | manual (uploaded certificate)
	AutoOK      bool   // the auto-renewal badge is green
}

// sslService builds the SSL service with the audit hook bound to the actor.
func (s *Server) sslService(r *http.Request, user *identity.User) *hosting.SslService {
	return hosting.NewSslService(s.agent, s.websiteService(r, user), s.settings, s.websiteAuditHook(r, user), s.currentServerID(r))
}

// sslSiteOf resolves the website (nil = not found / no access).
func (s *Server) sslSiteOf(r *http.Request, id string) *hosting.Website {
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

// siteSsl renders the website's SSL page (ssl.view).
func (s *Server) siteSsl(w http.ResponseWriter, r *http.Request) {
	site := s.sslSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.notFound(w, r)
		return
	}
	sess := sessionFrom(r.Context())
	svc := s.sslService(r, userFrom(r.Context()))
	cert := svc.Certificate(r.Context(), site)
	renewal := svc.RenewalStatus(r.Context())
	warnDays, err := s.settings.Int(r.Context(), "ssl_warning_days", 14)
	if err != nil {
		warnDays = 14
	}
	var cv *sslCertView
	if site.HTTPS && cert != nil {
		cv = &sslCertView{Expires: cert.NotAfter.UTC().Format("2006-01-02"), Days: cert.DaysRemaining, Issuer: cert.Issuer}
		for _, n := range cert.DNSNames {
			if cv.Covers != "" {
				cv.Covers += ", "
			}
			cv.Covers += n
		}
		switch {
		case cert.Error != "":
			cv.State, cv.Label = "error", "Certificate problem"
		case cert.Expired:
			cv.State, cv.Label = "expired", "Expired"
		case cert.DaysRemaining <= warnDays:
			cv.State, cv.Label = "expiring", "Expires soon"
		default:
			cv.State, cv.Label = "valid", "Secure"
		}
		if site.SSLMode == "letsencrypt" {
			if renewal != nil && renewal.TimerActive {
				cv.AutoRenewal = "active (" + renewal.Method + ")"
				cv.AutoOK = true
			} else {
				cv.AutoRenewal = "not scheduled"
			}
		} else {
			cv.AutoRenewal = "manual (uploaded certificate)"
		}
	}
	certbotMissing := renewal != nil && !renewal.CertbotInstalled
	data := map[string]any{
		"User":           userFrom(r.Context()),
		"CSRF":           session.CSRFToken(sess),
		"Site":           site,
		"Cert":           (*sslCertView)(nil),
		"CertbotMissing": certbotMissing,
		"WarnDays":       warnDays,
		"DefaultEmail":   userFrom(r.Context()).Email,
		"CanManage":      s.can(r, "ssl.manage"),
		"Success":        "",
		"Error":          "",
	}
	if cv != nil {
		data["Cert"] = cv
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "sites_ssl", data)
}

// sslFlashErr redirects back with the error flash (the PHP's
// redirectWithFailure).
func (s *Server) sslFlashErr(w http.ResponseWriter, r *http.Request, err error) {
	sess := sessionFrom(r.Context())
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		sess.Flash("error", ue.What)
	} else {
		var ae *agent.Error
		if errors.As(err, &ae) && ae.Message != "" {
			sess.Flash("error", ae.Message)
		} else {
			s.log.Error("ssl action", "error", err)
			sess.Flash("error", "The operation failed; the website was left as it was.")
		}
	}
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/ssl", http.StatusSeeOther)
}

func (s *Server) sslFlashNotFound(w http.ResponseWriter, r *http.Request) {
	sessionFrom(r.Context()).Flash("error", "Website not found.")
	http.Redirect(w, r, "/websites", http.StatusSeeOther)
}

func (s *Server) sslRedirectWith(w http.ResponseWriter, r *http.Request, msg string) {
	sessionFrom(r.Context()).Flash("success", msg)
	http.Redirect(w, r, "/sites/"+r.PathValue("id")+"/ssl", http.StatusSeeOther)
}

// sslAct runs one action: resolve the site, call the service, flash the
// result (the PHP's act()).
func (s *Server) sslAct(w http.ResponseWriter, r *http.Request, fn func(svc *hosting.SslService, site *hosting.Website) (string, error)) {
	site := s.sslSiteOf(r, r.PathValue("id"))
	if site == nil {
		s.sslFlashNotFound(w, r)
		return
	}
	msg, err := fn(s.sslService(r, userFrom(r.Context())), site)
	if err != nil {
		s.sslFlashErr(w, r, err)
		return
	}
	s.sslRedirectWith(w, r, msg)
}

// siteSslIssue (sites.ssl.issue — ssl.manage).
func (s *Server) siteSslIssue(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.sslFlashErr(w, r, errors.New("bad request"))
		return
	}
	// Expiry notices go to the account's own email address (none if it is
	// not valid) — the PHP's FILTER_VALIDATE_EMAIL gate.
	email := userFrom(r.Context()).Email
	if _, err := mail.ParseAddress(email); err != nil {
		email = ""
	}
	s.sslAct(w, r, func(svc *hosting.SslService, site *hosting.Website) (string, error) {
		if err := svc.Issue(r.Context(), site, email, r.FormValue("include_aliases") == "1", r.FormValue("staging") == "1"); err != nil {
			return "", err
		}
		return "Certificate issued. The website now uses HTTPS and HTTP requests are redirected.", nil
	})
}

// siteSslCustom (sites.ssl.custom — ssl.manage).
func (s *Server) siteSslCustom(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.sslFlashErr(w, r, errors.New("bad request"))
		return
	}
	s.sslAct(w, r, func(svc *hosting.SslService, site *hosting.Website) (string, error) {
		if err := svc.InstallCustom(r.Context(), site, r.FormValue("certificate"), r.FormValue("private_key"), r.FormValue("chain")); err != nil {
			return "", err
		}
		return "Certificate installed and HTTPS enabled.", nil
	})
}

// siteSslRenew (sites.ssl.renew — ssl.manage).
func (s *Server) siteSslRenew(w http.ResponseWriter, r *http.Request) {
	s.sslAct(w, r, func(svc *hosting.SslService, site *hosting.Website) (string, error) {
		if err := svc.Renew(r.Context(), site); err != nil {
			return "", err
		}
		return "Certificate renewed and Nginx reloaded.", nil
	})
}

// siteSslDisable (sites.ssl.disable — ssl.manage).
func (s *Server) siteSslDisable(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.sslFlashErr(w, r, errors.New("bad request"))
		return
	}
	s.sslAct(w, r, func(svc *hosting.SslService, site *hosting.Website) (string, error) {
		if err := svc.Disable(r.Context(), site, r.FormValue("delete_certificate") == "1"); err != nil {
			return "", err
		}
		return "HTTPS disabled for this website.", nil
	})
}

// siteSslSecurityHeaders (sites.security_headers — ssl.manage).
func (s *Server) siteSslSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.sslFlashErr(w, r, errors.New("bad request"))
		return
	}
	on := r.FormValue("security_headers") == "1"
	s.sslAct(w, r, func(svc *hosting.SslService, site *hosting.Website) (string, error) {
		if err := svc.SecurityHeaders(r.Context(), site, on); err != nil {
			return "", err
		}
		if on {
			return "Security headers are on.", nil
		}
		return "Security headers are off.", nil
	})
}

// siteSslOptions (sites.ssl.options — ssl.manage).
func (s *Server) siteSslOptions(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.sslFlashErr(w, r, errors.New("bad request"))
		return
	}
	s.sslAct(w, r, func(svc *hosting.SslService, site *hosting.Website) (string, error) {
		if err := svc.Options(r.Context(), site, r.FormValue("force_https") == "1", r.FormValue("hsts") == "1"); err != nil {
			return "", err
		}
		return "HTTPS options saved and applied.", nil
	})
}
