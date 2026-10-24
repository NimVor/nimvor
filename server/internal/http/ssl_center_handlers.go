package http

// The server-administration SSL/TLS area (SslCenterController): the hosts
// Nginx serves, AutoSSL, the certificate store, the keys and CSRs made on the
// server, the panel's and the mail server's certificates, and the server-wide
// TLS configuration.
//
// Every action here starts with the PHP's denied(): only an all-scope
// administrator may open these pages — a customer's own websites have their
// SSL pages under /sites/{id}/ssl. The route permissions then decide between
// looking (ssl.view) and changing (ssl.manage or server.manage).

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// sslCenterTabs are the page's tabs, in the panel's order.
var sslCenterTabs = [][3]string{
	{"hosts", "SSL hosts", "/ssl/hosts"},
	{"autossl", "AutoSSL", "/ssl/autossl"},
	{"certificates", "Certificates", "/ssl/certificates"},
	{"keys", "Keys and CSRs", "/ssl/keys"},
	{"tls", "TLS configuration", "/ssl/tls"},
	{"services", "Services SSL", "/ssl/services"},
}

// sslHostRow is one host name with its certificate state and, when the host
// belongs to one of this server's websites, the website's id.
type sslHostRow struct {
	hosting.SslHost
	State  string // ok | warn | none
	SiteID int64
}

// sslCertState classifies a host the way the PHP view does: without HTTPS it
// is "none"; unreadable, expired, not covering, or inside the warning window
// is "warn"; otherwise "ok".
func sslCertState(https, covered bool, c *hosting.SslCertDetail, warnDays int) string {
	if !https {
		return "none"
	}
	if c == nil {
		return "warn"
	}
	if !covered || c.Expired || c.Error != "" || c.DaysRemaining <= warnDays {
		return "warn"
	}
	return "ok"
}

// sslCenterDenied is the PHP's denied(): the area is for server
// administrators.
func (s *Server) sslCenterDenied(w http.ResponseWriter, r *http.Request) bool {
	if hosting.IsAll(userFrom(r.Context())) {
		return false
	}
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Access denied",
		"Message": "The SSL/TLS section is for server administrators. Each website has its own SSL page.",
	}, http.StatusForbidden)
	return true
}

// sslCenter builds the administrator SSL/TLS service.
func (s *Server) sslCenter(r *http.Request, user *identity.User) *hosting.SslCenterService {
	return hosting.NewSslCenterService(s.agent, s.websiteAuditHook(r, user), s.currentServerID(r))
}

// autoSSL builds the AutoSSL service (queue + log + scheduler pass).
func (s *Server) autoSSL(r *http.Request, user *identity.User) *hosting.AutoSSLService {
	return hosting.NewAutoSSLService(s.db, hosting.NewWebsiteRepository(s.db),
		s.sslService(r, user), s.siteDnsService(r, user), s.siteOverview(r, user),
		s.settings, s.currentServerID(r), s.sslLookup, s.agent)
}

// sslCenterPage renders one tab, with the shared chrome (tabs, flash, the
// agent error banner, the warning window).
func (s *Server) sslCenterPage(w http.ResponseWriter, r *http.Request, tab, title string, data map[string]any, agentErr error) {
	sess := sessionFrom(r.Context())
	data["User"] = userFrom(r.Context())
	data["CSRF"] = session.CSRFToken(sess)
	data["Tab"] = "ssl"
	data["Section"] = tab
	data["Tabs"] = sslCenterTabs
	data["Title"] = title
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	if job, ok := sess.PullFlash("job"); ok {
		data["Job"] = job
	}
	if agentErr != nil {
		data["AgentError"] = agentMessage(agentErr)
	}
	data["WarnDays"] = 14
	if v, err := s.settings.Int(r.Context(), "ssl_warning_days", 14); err == nil {
		data["WarnDays"] = v
	}
	s.render(w, "ssl_center_"+tab, data)
}

// sslCenterFlash redirects with a message (the PHP's redirectWithSuccess /
// redirectWithFailure).
func (s *Server) sslCenterFlash(w http.ResponseWriter, r *http.Request, url, flash, msg string) {
	sessionFrom(r.Context()).Flash(flash, msg)
	http.Redirect(w, r, url, http.StatusSeeOther)
}

// sslCenterForm parses and CSRF-checks a state-changing request.
func (s *Server) sslCenterForm(w http.ResponseWriter, r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return false
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return false
	}
	return true
}

// sslCenterFail turns an error into the PHP's failure flash.
func (s *Server) sslCenterFail(w http.ResponseWriter, r *http.Request, url, action string, err error) {
	msg := agentMessage(err)
	var ue *hosting.UserError
	if errors.As(err, &ue) {
		msg = ue.What
	} else {
		var ae *agent.Error
		if !errors.As(err, &ae) {
			s.log.Error("ssl center", "action", action, "error", err)
		}
	}
	s.sslCenterFlash(w, r, url, "error", msg)
}

// sslHostsByID maps the server's websites by lowercased host name (domain and
// aliases), so the hosts table can link the managed ones.
func (s *Server) sslSitesByName(r *http.Request) map[string]int64 {
	user := userFrom(r.Context())
	out := map[string]int64{}
	sites, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("ssl sites by name", "error", err)
		return out
	}
	for _, w := range sites {
		out[strings.ToLower(w.Domain)] = w.ID
		for _, a := range w.Aliases {
			out[strings.ToLower(a)] = w.ID
		}
	}
	return out
}

// sslCenterHosts renders GET /ssl/hosts (SslCenterController::hosts): every
// host name Nginx serves, with the certificate that answers for it.
func (s *Server) sslCenterHosts(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	user := userFrom(r.Context())
	svc := s.sslCenter(r, user)
	store, err := svc.Store(r.Context())
	if err != nil {
		s.sslCenterPage(w, r, "hosts", "SSL/TLS", map[string]any{"Rows": []sslHostRow{}}, err)
		return
	}
	renewal, rerr := svc.RenewalStatus(r.Context())
	if rerr != nil {
		renewal = nil
	}
	autoSsl, _ := s.settings.Bool(r.Context(), "auto_ssl", false)
	warnDays, _ := s.settings.Int(r.Context(), "ssl_warning_days", 14)
	sites := s.sslSitesByName(r)
	rows := make([]sslHostRow, 0, len(store.Hosts))
	counts := map[string]int{"ok": 0, "warn": 0, "none": 0}
	for _, h := range store.Hosts {
		st := sslCertState(h.HTTPS, h.Covered, h.Certificate, warnDays)
		counts[st]++
		rows = append(rows, sslHostRow{SslHost: h, State: st,
			SiteID: sites[strings.ToLower(h.Name)]})
	}
	s.sslCenterPage(w, r, "hosts", "SSL/TLS", map[string]any{
		"Rows":      rows,
		"Counts":    counts,
		"Renewal":   renewal,
		"AutoSsl":   autoSsl,
		"CanManage": s.can(r, "ssl.manage"),
	}, nil)
}

// sslCenterCertificates renders GET /ssl/certificates.
func (s *Server) sslCenterCertificates(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	svc := s.sslCenter(r, userFrom(r.Context()))
	store, err := svc.Store(r.Context())
	if err != nil {
		s.sslCenterPage(w, r, "certificates", "SSL/TLS",
			map[string]any{"Certificates": []hosting.SslCertEntry{}}, err)
		return
	}
	s.sslCenterPage(w, r, "certificates", "SSL/TLS", map[string]any{
		"Certificates": store.Certificates,
		"CanManage":    s.can(r, "ssl.manage"),
	}, nil)
}

// sslCertificateDelete deletes an unused certificate (deleteCertificate).
func (s *Server) sslCertificateDelete(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if err := s.sslCenter(r, userFrom(r.Context())).
		DeleteCertificate(r.Context(), r.FormValue("source"), name); err != nil {
		s.sslCenterFail(w, r, "/ssl/certificates", "session.certificate_delete", err)
		return
	}
	s.sslCenterFlash(w, r, "/ssl/certificates", "success", "Certificate "+name+" deleted.")
}

// sslRenewalEnable turns on certbot's renewal timer (enableRenewal).
func (s *Server) sslRenewalEnable(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	method, err := s.sslCenter(r, userFrom(r.Context())).EnableRenewal(r.Context())
	if err != nil {
		s.sslCenterFail(w, r, "/ssl/autossl", "ssl.renewal_enable", err)
		return
	}
	s.sslCenterFlash(w, r, "/ssl/autossl", "success",
		"Automatic renewal of Let's Encrypt certificates is on ("+method+").")
}

// sslCenterKeys renders GET /ssl/keys, with one key opened when ?id= is set.
func (s *Server) sslCenterKeys(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	user := userFrom(r.Context())
	svc := s.sslCenter(r, user)
	list, err := svc.Keys(r.Context())
	if err != nil {
		s.sslCenterPage(w, r, "keys", "SSL/TLS",
			map[string]any{"Keys": []hosting.SslKeyMeta{}, "KeyTypes": []string{}}, err)
		return
	}
	var key *hosting.SslKeyMeta
	if id := strings.TrimSpace(r.URL.Query().Get("id")); id != "" {
		if k, err := svc.Key(r.Context(), id); err != nil {
			s.log.Error("ssl key", "id", id, "error", err)
		} else {
			key = k
		}
	}
	sites, _ := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	// The websites the open key's names fit come first in the install picker.
	fit, other := []hosting.Website{}, []hosting.Website{}
	for _, w := range sites {
		if key != nil && sslKeyFits(*key, w.Domain) {
			fit = append(fit, w)
		} else {
			other = append(other, w)
		}
	}
	labels := map[string]string{
		"ecdsa256": "ECDSA P-256 (recommended)", "ecdsa384": "ECDSA P-384",
		"rsa2048": "RSA 2048", "rsa3072": "RSA 3072", "rsa4096": "RSA 4096",
	}
	s.sslCenterPage(w, r, "keys", "SSL/TLS", map[string]any{
		"Keys":       list.Keys,
		"KeyTypes":   list.KeyTypes,
		"KeyLabels":  labels,
		"Key":        key,
		"SitesFit":   fit,
		"SitesOther": other,
		"Websites":   sites,
		"CanManage":  s.can(r, "ssl.manage"),
	}, nil)
}

// sslCSRDownload serves the CSR of one key (csrDownload).
func (s *Server) sslCSRDownload(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	key, err := s.sslCenter(r, userFrom(r.Context())).
		Key(r.Context(), strings.TrimSpace(r.URL.Query().Get("id")))
	if err != nil || key == nil || key.CSR == "" {
		s.sslCenterFlash(w, r, "/ssl/keys", "error", "That CSR does not exist.")
		return
	}
	file := sslUnsafeName.ReplaceAllString(key.ID, "_") + ".csr"
	w.Header().Set("Content-Type", "application/pkcs10")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(key.CSR))
}

// sslUnsafeName is the CSR file name's sanitiser (PHP
// preg_replace('/[^a-z0-9.-]/', '_', …)).
var sslUnsafeName = regexp.MustCompile(`[^a-z0-9.-]`)

// sslKeyCreate makes a key and a CSR (createCsr).
func (s *Server) sslKeyCreate(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	names := hosting.SslTrimmedNames(r.FormValue("names"))
	days := 0
	if r.FormValue("self_signed") == "1" {
		days = 365
		if n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("self_signed_days"))); err == nil {
			days = n
		}
		if days < 1 {
			days = 1
		}
		if days > 825 {
			days = 825
		}
	}
	in := hosting.SslCSRInput{
		Names:            names,
		Organization:     r.FormValue("organization"),
		OrganizationUnit: r.FormValue("organizational_unit"),
		Locality:         r.FormValue("locality"),
		Province:         r.FormValue("province"),
		Country:          strings.ToUpper(strings.TrimSpace(r.FormValue("country"))),
		Email:            r.FormValue("email"),
		KeyType:          r.FormValue("key_type"),
		SelfSignedDays:   days,
	}
	id, err := s.sslCenter(r, userFrom(r.Context())).CreateCSR(r.Context(), in)
	if err != nil {
		s.sslCenterFail(w, r, "/ssl/keys", "ssl.csr_create", err)
		return
	}
	s.sslCenterFlash(w, r, "/ssl/keys?id="+url.QueryEscape(id), "success",
		"Key and CSR created. Send the CSR to your certificate authority; the private key stays on this server.")
}

// sslKeyInstall installs a certificate issued for one of the keys on a
// website (installKey).
func (s *Server) sslKeyInstall(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	keyID := strings.TrimSpace(r.FormValue("key_id"))
	back := "/ssl/keys?id=" + url.QueryEscape(keyID)
	siteID, _ := strconv.ParseInt(strings.TrimSpace(r.FormValue("website_id")), 10, 64)
	site, err := hosting.NewWebsiteRepository(s.db).FindFull(r.Context(), s.currentServerID(r), siteID, user)
	if err != nil || site == nil {
		s.sslCenterFlash(w, r, back, "error", "Choose a website.")
		return
	}
	if err := s.sslService(r, user).InstallFromKey(r.Context(), site, keyID,
		r.FormValue("certificate"), r.FormValue("chain"), r.FormValue("self_signed") == "1"); err != nil {
		s.sslCenterFail(w, r, back, "ssl.install_key", err)
		return
	}
	s.sslCenterFlash(w, r, fmt.Sprintf("/sites/%d/ssl", site.ID), "success",
		"Certificate installed on "+site.Domain+" and HTTPS enabled.")
}

// sslKeyDelete deletes a stored key and its CSR (deleteKey).
func (s *Server) sslKeyDelete(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	id := strings.TrimSpace(r.FormValue("id"))
	if err := s.sslCenter(r, userFrom(r.Context())).DeleteKey(r.Context(), id); err != nil {
		s.sslCenterFail(w, r, "/ssl/keys", "ssl.key_delete", err)
		return
	}
	s.sslCenterFlash(w, r, "/ssl/keys", "success", "Key "+id+" deleted.")
}

// sslCenterTLS renders GET /ssl/tls (the server-wide TLS configuration).
func (s *Server) sslCenterTLS(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	tls, err := s.sslCenter(r, userFrom(r.Context())).TLS(r.Context())
	if err != nil {
		s.sslCenterPage(w, r, "tls", "SSL/TLS", map[string]any{}, err)
		return
	}
	s.sslCenterPage(w, r, "tls", "SSL/TLS", map[string]any{
		"TLS":       tls,
		"CanManage": s.can(r, "server.manage"),
	}, nil)
}

// sslTLSSave applies the TLS profile (saveTls).
func (s *Server) sslTLSSave(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	profile := hosting.SslTLSProfile{
		Protocols:      r.FormValue("protocols"),
		Ciphers:        r.FormValue("ciphers"),
		SessionTickets: r.FormValue("session_tickets") == "1",
	}
	moved, err := s.sslCenter(r, userFrom(r.Context())).SetTLS(r.Context(), profile)
	if err != nil {
		s.sslCenterFail(w, r, "/ssl/tls", "ssl.tls", err)
		return
	}
	msg := "TLS configuration applied and Nginx reloaded."
	if moved > 0 {
		msg += fmt.Sprintf(" %d website configuration(s) now use it.", moved)
	}
	s.sslCenterFlash(w, r, "/ssl/tls", "success", msg)
}

// sslCenterServices renders GET /ssl/services: the panel's and the mail
// server's certificates, the host name and the ACME account.
func (s *Server) sslCenterServices(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	user := userFrom(r.Context())
	svc := s.sslCenter(r, user)
	services, err := svc.Services(r.Context())
	if err != nil {
		s.sslCenterPage(w, r, "services", "SSL/TLS", map[string]any{}, err)
		return
	}
	active, _ := s.settings.String(r.Context(), "acme_provider", "")
	if active == "" {
		active = "letsencrypt"
	}
	// ?provider= previews another CA's account before switching to it.
	shown := strings.TrimSpace(r.URL.Query().Get("provider"))
	if shown == "" {
		shown = active
	}
	account, aerr := svc.Account(r.Context(), shown)
	if aerr != nil {
		account = nil
	}
	// Does one of the Let's Encrypt certificates cover the host name?
	hostCert := false
	if services.Hostname != "" {
		for i, le := range services.LetsEncrypt {
			for _, n := range le.Names {
				if strings.EqualFold(n, services.Hostname) {
					services.LetsEncrypt[i].CoversHostname = true
					hostCert = true
				}
			}
		}
	}
	s.sslCenterPage(w, r, "services", "SSL/TLS", map[string]any{
		"Services":       services,
		"Account":        account,
		"ActiveProvider": active,
		"DefaultEmail":   user.Email,
		"HostHasCert":    hostCert,
		"CanManage":      s.can(r, "server.manage"),
	}, nil)
}

// sslAccountRegister registers or updates the ACME account (accountRegister)
// and makes the provider the active certificate authority.
func (s *Server) sslAccountRegister(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	provider := r.FormValue("provider")
	if provider == "" {
		provider = "letsencrypt"
	}
	in := hosting.SslAccountInput{
		Provider:   provider,
		Email:      strings.ToLower(strings.TrimSpace(r.FormValue("email"))),
		AgreeTOS:   r.FormValue("agree_tos") == "1",
		Staging:    r.FormValue("staging") == "1",
		EABKID:     strings.TrimSpace(r.FormValue("eab_kid")),
		EABHMACKey: strings.TrimSpace(r.FormValue("eab_hmac_key")),
	}
	res, err := s.sslCenter(r, user).RegisterAccount(r.Context(), in)
	back := "/ssl/services?provider=" + url.QueryEscape(provider) + "#lets-encrypt-account"
	if err != nil {
		s.sslCenterFail(w, r, back, "ssl.account_register", err)
		return
	}
	if err := s.settings.Set(r.Context(), "acme_provider", provider, user.ID); err != nil {
		s.log.Error("ssl acme provider", "error", err)
	}
	label := hosting.ProviderLabel(provider)
	msg := "A " + label + " account was registered with " + in.Email +
		". It is now the active certificate authority; certificates can be issued."
	if res.Updated {
		msg = "The " + label + " account contact was updated to " + in.Email +
			". It is the active certificate authority."
	}
	s.sslCenterFlash(w, r, "/ssl/services#lets-encrypt-account", "success", msg)
}

// sslServiceCert points the panel or the mail server at a certificate
// (serviceCert).
func (s *Server) sslServiceCert(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	service := r.FormValue("service")
	name := strings.TrimSpace(r.FormValue("cert_name"))
	if err := s.sslCenter(r, userFrom(r.Context())).SetServiceCert(r.Context(), service, name); err != nil {
		s.sslCenterFail(w, r, "/ssl/services", "ssl.service_cert", err)
		return
	}
	label := "The mail server"
	if service == "panel" {
		label = "The panel"
	}
	msg := label + " now uses the certificate " + name + "."
	if name == "" {
		msg = label + " uses its previous certificate again."
	} else if service == "panel" {
		msg += " Reload the page if the browser still shows the old one."
	}
	s.sslCenterFlash(w, r, "/ssl/services", "success", msg)
}

// sslMailTLS applies the mail server's TLS profile (mailTls).
func (s *Server) sslMailTLS(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	if err := s.sslCenter(r, userFrom(r.Context())).SetMailTLS(r.Context(),
		r.FormValue("protocols"), r.FormValue("ciphers")); err != nil {
		s.sslCenterFail(w, r, "/ssl/services", "ssl.mail_tls", err)
		return
	}
	s.sslCenterFlash(w, r, "/ssl/services", "success",
		"The mail server's TLS configuration was applied; Postfix and Dovecot were reloaded.")
}

// sslHostnameIssue asks Let's Encrypt for the server's host name
// (hostnameIssue); expiry notices go to the administrator who asked.
func (s *Server) sslHostnameIssue(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	email := strings.TrimSpace(user.Email)
	if !strings.Contains(email, "@") {
		email = ""
	}
	provider, _ := s.settings.String(r.Context(), "acme_provider", "")
	if provider == "" {
		provider = "letsencrypt"
	}
	res, err := s.sslCenter(r, user).IssueHostname(r.Context(), email, provider)
	if err != nil {
		s.sslCenterFail(w, r, "/ssl/services", "ssl.hostname_issue", err)
		return
	}
	hostname := res.Hostname
	if hostname == "" {
		hostname = "the host name"
	}
	if res.JobID != "" {
		if err := hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r),
			res.JobID, "Let's Encrypt for "+hostname, "server",
			strconv.FormatInt(s.currentServerID(r), 10), &user.ID); err != nil {
			s.log.Error("ssl hostname job", "error", err)
		}
		sessionFrom(r.Context()).Flash("job", res.JobID)
	}
	s.sslCenterFlash(w, r, "/ssl/services", "success",
		"Asking Let's Encrypt for a certificate for "+hostname+
			"; the window shows its progress. When it is issued, choose it for the panel and the mail server below.")
}

// sslCenterAutossl renders GET /ssl/autossl.
func (s *Server) sslCenterAutossl(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	user := userFrom(r.Context())
	svc := s.sslCenter(r, user)
	auto := s.autoSSL(r, user)
	renewal, err := svc.RenewalStatus(r.Context())
	var agentErr error
	if err != nil {
		agentErr = err
	}
	renewLog := []string{}
	if err == nil {
		if lines, err := svc.RenewalLog(r.Context()); err == nil {
			renewLog = lines
		} else {
			agentErr = err
		}
	}
	log, _ := auto.RecentLog(r.Context(), 300)
	queued, _ := auto.Queued(r.Context())
	var next any
	if last, ok := auto.LastSchedulerRun(r.Context()); ok {
		next = last.Add(5 * time.Minute)
	}
	autoNew, _ := s.settings.Bool(r.Context(), "auto_ssl", false)
	autoReplace, _ := s.settings.Bool(r.Context(), "auto_ssl_replace", false)
	s.sslCenterPage(w, r, "autossl", "SSL/TLS", map[string]any{
		"Renewal":     renewal,
		"RenewLog":    renewLog,
		"Log":         log,
		"Queued":      queued,
		"NextCheck":   next,
		"AutoNew":     autoNew,
		"AutoReplace": autoReplace,
		"CanManage":   s.can(r, "ssl.manage"),
	}, agentErr)
}

// sslAutosslRun queues every website that needs a certificate and handles the
// first few now (autosslRun).
func (s *Server) sslAutosslRun(w http.ResponseWriter, r *http.Request) {
	if s.sslCenterDenied(w, r) {
		return
	}
	if !s.sslCenterForm(w, r) {
		return
	}
	user := userFrom(r.Context())
	auto := s.autoSSL(r, user)
	n, err := auto.Queue(r.Context(), true)
	if err != nil {
		s.sslCenterFail(w, r, "/ssl/autossl", "ssl.autossl_run", err)
		return
	}
	s.websiteAuditHook(r, user)(r.Context(), "ssl.autossl_run", "server",
		strconv.FormatInt(s.currentServerID(r), 10), "success", map[string]any{"queued": n})
	if n == 0 {
		s.sslCenterFlash(w, r, "/ssl/autossl", "success",
			"Every website already has a valid certificate; nothing to do.")
		return
	}
	// The first three now (each can take a minute), the rest with the
	// scheduler every five minutes.
	done := auto.Process(r.Context(), 3)
	s.sslCenterFlash(w, r, "/ssl/autossl", "success", fmt.Sprintf(
		"%d website(s) queued; %d handled now, the rest within the next scheduler runs (every 5 minutes). See the log below.",
		n, len(done)))
}

// sslKeyFits reports whether one of the key's names is the website's domain
// (case-insensitively), which orders the install picker.
func sslKeyFits(key hosting.SslKeyMeta, domain string) bool {
	for _, n := range key.Names {
		if strings.EqualFold(n, domain) {
			return true
		}
	}
	return false
}
