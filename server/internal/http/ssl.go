package http

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// sslHosts GET /ssl/hosts — shows all served hostnames with their cert
// status from the agent's ssl.store inventory. Mirrors
// SslCenterController::hosts.
func (s *Server) sslHosts(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "SSL hosts",
		"Hosts":      nil,
		"Renewal":    nil,
		"AutoSsl":    false,
		"Error":      "",
		"Success":    "",
		"AgentError": nil,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	autoSsl, _ := s.settings.Bool(r.Context(), "auto_ssl", true)
	data["AutoSsl"] = autoSsl
	if raw, err := s.agent.Call(r.Context(), "ssl.store", nil, 60*time.Second); err == nil {
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) == nil {
			if h, ok := parsed["hosts"]; ok {
				data["Hosts"] = h
			}
		}
		if raw2, err2 := s.agent.Call(r.Context(), "ssl.renewal_status", nil, 30*time.Second); err2 == nil {
			var ren any
			_ = json.Unmarshal(raw2, &ren)
			data["Renewal"] = ren
		}
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "ssl_hosts", data)
}

// sslEnableRenewal POST /ssl/renewal — enables certbot's renewal timer via
// agent ssl.renewal_enable. Mirrors SslCenterController::enableRenewal.
func (s *Server) sslEnableRenewal(w http.ResponseWriter, r *http.Request) {
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
	res, err := s.agent.Call(r.Context(), "ssl.renewal_enable", nil, 60*time.Second)
	if err != nil {
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{
			Action: "ssl.renewal_enable", TargetType: "server", TargetID: "certbot",
			Result: audit.Failure, Details: map[string]any{"error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/autossl", http.StatusSeeOther)
		return
	}
	method := ""
	if m, _ := res.(map[string]any); m != nil {
		if mm, _ := m["method"].(string); mm != "" {
			method = mm
		}
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.renewal_enable", TargetType: "server", TargetID: "certbot",
		Result: audit.Success, Details: map[string]any{"method": method},
		UserID: &uid, Username: user.Username,
	})
	msg := "Automatic renewal of Let's Encrypt certificates is on"
	if method != "" {
		msg += " (" + method + ")"
	}
	msg += "."
	sess.Flash("success", msg)
	http.Redirect(w, r, "/ssl/autossl", http.StatusSeeOther)
}

// sslCertificates GET /ssl/certificates — certificate inventory from
// ssl.store. Mirrors SslCenterController::certificates.
func (s *Server) sslCertificates(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Certificates",
		"Certs":      nil,
		"Error":      flashStr(sess, "error"),
		"Success":    flashStr(sess, "success"),
		"AgentError": nil,
	}
	if raw, err := s.agent.Call(r.Context(), "ssl.store", nil, 60*time.Second); err == nil {
		var parsed map[string]any
		if json.Unmarshal(raw, &parsed) == nil {
			if c, ok := parsed["certificates"]; ok {
				data["Certs"] = c
			}
		}
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "ssl_certificates", data)
}

// sslDeleteCertificate POST /ssl/certificates/delete — deletes a
// certificate (letsencrypt or whacp/custom) via agent. Mirrors
// SslCenterController::deleteCertificate.
func (s *Server) sslDeleteCertificate(w http.ResponseWriter, r *http.Request) {
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
	source := r.FormValue("source")
	name := r.FormValue("name")
	var action string
	switch source {
	case "letsencrypt":
		action = "ssl.delete"
	case "whacp":
		action = "ssl.custom_delete"
	default:
		sess.Flash("error", "Only Let's Encrypt and WhaCP certificates can be deleted here.")
		http.Redirect(w, r, "/ssl/certificates", http.StatusSeeOther)
		return
	}
	if _, err := s.agent.Call(r.Context(), action, map[string]any{"cert_name": name}, 60*time.Second); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/certificates", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.certificate_delete", TargetType: "server", TargetID: "ssl",
		Result: audit.Success, Details: map[string]any{"source": source, "name": name},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Certificate "+name+" deleted.")
	http.Redirect(w, r, "/ssl/certificates", http.StatusSeeOther)
}

// sslKeys GET /ssl/keys — lists stored private keys and certificates via
// ssl.keys. Mirrors SslCenterController::keys.
func (s *Server) sslKeys(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Private keys",
		"Keys":       nil,
		"Error":      flashStr(sess, "error"),
		"Success":    flashStr(sess, "success"),
		"AgentError": nil,
	}
	if raw, err := s.agent.Call(r.Context(), "ssl.keys", nil, 60*time.Second); err == nil {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		data["Keys"] = parsed
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "ssl_keys", data)
}

// sslCreateCsr POST /ssl/keys — generates a private key + CSR (and
// optionally a self-signed certificate) via the agent's ssl.csr_create
// action. Mirrors SslCenterController::createCsr (ssl.manage).
func (s *Server) sslCreateCsr(w http.ResponseWriter, r *http.Request) {
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
	// names is a comma/whitespace-separated list, lowercased and trimmed.
	namesRaw := r.FormValue("names")
	var names []string
	for _, f := range strings.FieldsFunc(namesRaw, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' }) {
		if n := strings.ToLower(strings.TrimSpace(f)); n != "" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		sess.Flash("error", "Give at least one domain name for the CSR.")
		http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
		return
	}
	selfSignedDays := 0
	if r.FormValue("self_signed") == "1" {
		days := 365
		if d, err := strconv.Atoi(r.FormValue("self_signed_days")); err == nil && d > 0 {
			days = d
		}
		if days < 1 {
			days = 1
		}
		if days > 825 {
			days = 825
		}
		selfSignedDays = days
	}
	keyType := r.FormValue("key_type")
	if keyType == "" {
		keyType = "ec256"
	}
	params := map[string]any{
		"names":               names,
		"organization":        r.FormValue("organization"),
		"organizational_unit": r.FormValue("organizational_unit"),
		"locality":            r.FormValue("locality"),
		"province":            r.FormValue("province"),
		"country":             strings.ToUpper(r.FormValue("country")),
		"email":               r.FormValue("email"),
		"key_type":            keyType,
		"self_signed_days":    selfSignedDays,
	}
	raw, err := s.agent.Call(r.Context(), "ssl.csr_create", params, 120*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
		return
	}
	var res struct {
		ID string `json:"id"`
	}
	keyID := ""
	_ = json.Unmarshal(raw, &res)
	if res.ID != "" {
		keyID = res.ID
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.csr_create", TargetType: "server", TargetID: "ssl",
		Result:  audit.Success,
		Details: map[string]any{"id": keyID, "names": names, "key_type": keyType, "self_signed": selfSignedDays > 0},
		UserID:  &uid, Username: user.Username,
	})
	sess.Flash("success", "Key and CSR created. Send the CSR to your certificate authority; the private key stays on this server.")
	target := "/ssl/keys"
	if keyID != "" {
		target = "/ssl/keys?id=" + url.QueryEscape(keyID)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// sslInstallKey POST /ssl/keys/install — installs a certificate whose
// private key was generated on the server (SslCenterController::installKey,
// ssl.manage). Uses hosting.SslService.InstallFromKey, which calls agent
// ssl.custom with key_id and patches the website to HTTPS.
func (s *Server) sslInstallKey(w http.ResponseWriter, r *http.Request) {
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
	keyID := r.FormValue("key_id")
	if keyID == "" {
		keyID = r.FormValue("id")
	}
	back := "/ssl/keys"
	if keyID != "" {
		back = "/ssl/keys?id=" + url.QueryEscape(keyID)
	}
	siteID, err := strconv.ParseInt(r.FormValue("website_id"), 10, 64)
	if err != nil || siteID <= 0 {
		sess.Flash("error", "Choose a website.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	repo := hosting.NewWebsiteRepository(s.db)
	site, err := repo.FindFull(r.Context(), s.currentServerID(r), siteID, user)
	if errors.Is(err, sql.ErrNoRows) || site == nil || err != nil {
		sess.Flash("error", "Choose a website.")
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	cert := r.FormValue("certificate")
	chain := r.FormValue("chain")
	selfSigned := r.FormValue("self_signed") == "1"
	svc := s.sslService(r, user)
	if err := svc.InstallFromKey(r.Context(), site, keyID, cert, chain, selfSigned); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Certificate installed on "+site.Domain+" and HTTPS enabled.")
	http.Redirect(w, r, "/sites/"+strconv.FormatInt(site.ID, 10)+"/ssl", http.StatusSeeOther)
}

// sslDeleteKey POST /ssl/keys/delete — removes a private key via
// ssl.key_delete. Mirrors SslCenterController::deleteKey.
func (s *Server) sslDeleteKey(w http.ResponseWriter, r *http.Request) {
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
	name := r.FormValue("name")
	if _, err := s.agent.Call(r.Context(), "ssl.key_delete", map[string]any{"name": name}, 60*time.Second); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.key_delete", TargetType: "server", TargetID: "ssl",
		Result: audit.Success, Details: map[string]any{"name": name},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Key deleted.")
	http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
}

// sslCsrDownload GET /ssl/keys/csr — downloads a CSR PEM file for the
// given key id (SslCenterController::csrDownload, ssl.manage). Uses agent
// ssl.key to fetch the key record; if a CSR exists it is served as
// application/pkcs10 with Content-Disposition.
func (s *Server) sslCsrDownload(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	id := r.URL.Query().Get("id")
	if id == "" {
		sess.Flash("error", "That CSR does not exist.")
		http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
		return
	}
	raw, err := s.agent.Call(r.Context(), "ssl.key", map[string]any{"id": id}, 30*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
		return
	}
	var key struct {
		ID  string `json:"id"`
		CSR string `json:"csr"`
	}
	if err := json.Unmarshal(raw, &key); err != nil || strings.TrimSpace(key.CSR) == "" {
		sess.Flash("error", "That CSR does not exist.")
		http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
		return
	}
	// Sanitize the file name like the PHP panel does.
	filename := unsafeCSRFilename.ReplaceAllString(strings.ToLower(key.ID), "_") + ".csr"
	if filename == ".csr" {
		filename = "key.csr"
	}
	w.Header().Set("Content-Type", "application/pkcs10")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.csr_download", TargetType: "server", TargetID: "ssl",
		Result: audit.Success, Details: map[string]any{"id": key.ID},
		UserID: &uid, Username: user.Username,
	})
	_, _ = w.Write([]byte(key.CSR))
}

var unsafeCSRFilename = regexp.MustCompile(`[^a-z0-9.-]`)

// sslTls GET /ssl/tls — current TLS configuration from ssl.tls_get.
// Mirrors SslCenterController::tls.
func (s *Server) sslTls(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "TLS settings",
		"Tls":        nil,
		"Error":      flashStr(sess, "error"),
		"Success":    flashStr(sess, "success"),
		"AgentError": nil,
	}
	if raw, err := s.agent.Call(r.Context(), "ssl.tls_get", nil, 60*time.Second); err == nil {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		data["Tls"] = parsed
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "ssl_tls", data)
}

// sslSaveTls POST /ssl/tls — applies TLS configuration via ssl.tls_set.
// Mirrors SslCenterController::saveTls.
func (s *Server) sslSaveTls(w http.ResponseWriter, r *http.Request) {
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
	profile := map[string]any{
		"protocols":       r.FormValue("protocols"),
		"ciphers":         r.FormValue("ciphers"),
		"session_tickets": r.FormValue("session_tickets") == "1",
	}
	res, err := s.agent.Call(r.Context(), "ssl.tls_set", profile, 120*time.Second)
	if err != nil {
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{
			Action: "ssl.tls", TargetType: "server", TargetID: "nginx",
			Result: audit.Failure, Details: mergeAny(profile, map[string]any{"error": err.Error()}),
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/tls", http.StatusSeeOther)
		return
	}
	moved := 0
	if m, _ := res.(map[string]any); m != nil {
		if mv, ok := m["migrated_vhosts"].(float64); ok {
			moved = int(mv)
		}
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.tls", TargetType: "server", TargetID: "nginx",
		Result: audit.Success, Details: profile,
		UserID: &uid, Username: user.Username,
	})
	msg := "TLS configuration applied and Nginx reloaded."
	if moved > 0 {
		msg += " " + strconv.Itoa(moved) + " website configuration(s) now use it."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, "/ssl/tls", http.StatusSeeOther)
}

// sslServices GET /ssl/services — panel/mail/host SSL status via
// ssl.services, plus ssl.account for the active (or ?provider=) provider.
// Mirrors SslCenterController::services.
func (s *Server) sslServices(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":           session.CSRFToken(sess),
		"User":           user,
		"Title":          "Services SSL",
		"Svc":            nil,
		"Account":        nil,
		"ActiveProvider": "letsencrypt",
		"DefaultEmail":   "",
		"Error":          flashStr(sess, "error"),
		"Success":        flashStr(sess, "success"),
		"AgentError":     nil,
	}
	if user != nil {
		data["DefaultEmail"] = user.Email
	}
	if prov, _ := s.settings.String(r.Context(), "acme_provider", "letsencrypt"); prov != "" {
		data["ActiveProvider"] = prov
	}
	if raw, err := s.agent.Call(r.Context(), "ssl.services", nil, 60*time.Second); err == nil {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		data["Svc"] = parsed
		shown := r.URL.Query().Get("provider")
		if shown == "" {
			shown = data["ActiveProvider"].(string)
		}
		if raw2, err2 := s.agent.Call(r.Context(), "ssl.account", map[string]any{"provider": shown}, 30*time.Second); err2 == nil {
			var a any
			_ = json.Unmarshal(raw2, &a)
			data["Account"] = a
		}
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "ssl_services", data)
}

// sslAccountRegister POST /ssl/services/account — registers an ACME account.
// Stub: CSRF-checked, flashes a friendly error.
// sslAccountRegister POST /ssl/services/account — registers (or updates)
// an ACME account for a provider, persists it as the active acme_provider,
// and redirects to /ssl/services#lets-encrypt-account.
// Mirrors SslCenterController::accountRegister (ssl.manage).
func (s *Server) sslAccountRegister(w http.ResponseWriter, r *http.Request) {
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
	provider := r.FormValue("provider")
	if provider == "" {
		provider = "letsencrypt"
	}
	params := map[string]any{
		"provider":     provider,
		"email":        strings.ToLower(strings.TrimSpace(r.FormValue("email"))),
		"agree_tos":    r.FormValue("agree_tos") == "1",
		"staging":      r.FormValue("staging") == "1",
		"eab_kid":      strings.TrimSpace(r.FormValue("eab_kid")),
		"eab_hmac_key": strings.TrimSpace(r.FormValue("eab_hmac_key")),
	}
	raw, err := s.agent.Call(r.Context(), "ssl.account_register", params, 120*time.Second)
	uid := user.ID
	if err != nil {
		// EAB HMAC key is a secret — never audit it.
		s.audit.Record(r.Context(), audit.Entry{
			Action: "ssl.account_register", TargetType: "server", TargetID: provider,
			Result: audit.Failure,
			Details: map[string]any{
				"provider": provider,
				"email":    params["email"],
				"error":    err.Error(),
			},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/services?provider="+url.QueryEscape(provider)+"#lets-encrypt-account", http.StatusSeeOther)
		return
	}
	var res struct {
		Updated bool `json:"updated"`
	}
	_ = json.Unmarshal(raw, &res)
	if err := s.settings.Set(r.Context(), "acme_provider", provider, user.ID); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/services#lets-encrypt-account", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.account_register", TargetType: "server", TargetID: provider,
		Result: audit.Success,
		Details: map[string]any{
			"provider": provider, "email": params["email"], "updated": res.Updated,
		},
		UserID: &uid, Username: user.Username,
	})
	label := map[string]string{"letsencrypt": "Let's Encrypt", "zerossl": "ZeroSSL", "buypass": "Buypass Go"}[provider]
	if label == "" {
		label = provider
	}
	if res.Updated {
		sess.Flash("success", "The "+label+" account contact was updated to "+params["email"].(string)+". It is the active certificate authority.")
	} else {
		sess.Flash("success", "A "+label+" account was registered with "+params["email"].(string)+". It is now the active certificate authority; certificates can be issued.")
	}
	http.Redirect(w, r, "/ssl/services#lets-encrypt-account", http.StatusSeeOther)
}

// sslServiceCert POST /ssl/services/cert — selects a certificate for the
// panel or the mail server (ssl.service_cert agent call).
// Mirrors SslCenterController::serviceCert (ssl.manage).
func (s *Server) sslServiceCert(w http.ResponseWriter, r *http.Request) {
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
	service := r.FormValue("service")
	name := r.FormValue("cert_name")
	uid := user.ID
	if _, err := s.agent.Call(r.Context(), "ssl.service_cert", map[string]any{
		"service": service, "cert_name": name,
	}, 120*time.Second); err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "ssl.service_cert", TargetType: "server", TargetID: service,
			Result: audit.Failure, Details: map[string]any{"cert_name": name, "error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/services", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.service_cert", TargetType: "server", TargetID: service,
		Result: audit.Success, Details: map[string]any{"cert_name": name},
		UserID: &uid, Username: user.Username,
	})
	label := "The mail server"
	reload := ""
	if service == "panel" {
		label = "The panel"
		reload = " Reload the page if the browser still shows the old one."
	}
	if name == "" {
		sess.Flash("success", label+" uses its previous certificate again."+reload)
	} else {
		sess.Flash("success", label+" now uses the certificate "+name+"."+reload)
	}
	http.Redirect(w, r, "/ssl/services", http.StatusSeeOther)
}

// sslHostnameIssue POST /ssl/services/hostname — asks ACME for a
// certificate for the server's hostname (agent ssl.hostname_issue); tracks
// the returned job. Mirrors SslCenterController::hostnameIssue (ssl.manage).
func (s *Server) sslHostnameIssue(w http.ResponseWriter, r *http.Request) {
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
	email := strings.ToLower(strings.TrimSpace(user.Email))
	provider, _ := s.settings.String(r.Context(), "acme_provider", "letsencrypt")
	if provider == "" {
		provider = "letsencrypt"
	}
	raw, err := s.agent.Call(r.Context(), "ssl.hostname_issue", map[string]any{
		"email": email, "provider": provider,
	}, 60*time.Second)
	uid := user.ID
	if err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "ssl.hostname_issue", TargetType: "server", TargetID: "hostname",
			Result: audit.Failure, Details: map[string]any{"error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/services", http.StatusSeeOther)
		return
	}
	var res struct {
		JobID    string `json:"job_id"`
		Hostname string `json:"hostname"`
	}
	_ = json.Unmarshal(raw, &res)
	hostname := res.Hostname
	if hostname == "" {
		hostname = "the host name"
	}
	if res.JobID != "" {
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), res.JobID, "Let's Encrypt for "+hostname, "server", itoa(s.currentServerID(r)), &uid)
		sess.Flash("job", res.JobID)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.hostname_issue", TargetType: "server", TargetID: "hostname",
		Result: audit.Success, Details: map[string]any{"hostname": hostname, "job_id": res.JobID},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Asking Let's Encrypt for a certificate for "+hostname+"; the window shows its progress. When it is issued, choose it for the panel and the mail server below.")
	http.Redirect(w, r, "/ssl/services", http.StatusSeeOther)
}

// sslMailTls POST /ssl/services/mail-tls — applies TLS settings for the
// mail server via agent ssl.mail_tls. Mirrors SslCenterController::mailTls.
func (s *Server) sslMailTls(w http.ResponseWriter, r *http.Request) {
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
	profile := map[string]any{
		"protocols": r.FormValue("protocols"),
		"ciphers":   r.FormValue("ciphers"),
	}
	uid := user.ID
	if _, err := s.agent.Call(r.Context(), "ssl.mail_tls", profile, 120*time.Second); err != nil {
		details := map[string]any{"protocols": profile["protocols"], "ciphers": profile["ciphers"], "error": err.Error()}
		s.audit.Record(r.Context(), audit.Entry{
			Action: "ssl.mail_tls", TargetType: "server", TargetID: "mail",
			Result: audit.Failure, Details: details,
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/services", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.mail_tls", TargetType: "server", TargetID: "mail",
		Result: audit.Success, Details: profile,
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "The mail server's TLS configuration was applied; Postfix and Dovecot were reloaded.")
	http.Redirect(w, r, "/ssl/services", http.StatusSeeOther)
}

// sslAutoSsl GET /ssl/autossl — AutoSSL status/log. Best-effort pulls
// ssl.autossl_status from the agent. Mirrors SslCenterController::autossl.
func (s *Server) sslAutoSsl(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "AutoSSL",
		"Status":     nil,
		"Error":      flashStr(sess, "error"),
		"Success":    flashStr(sess, "success"),
		"AgentError": nil,
	}
	if raw, err := s.agent.Call(r.Context(), "ssl.autossl_status", nil, 60*time.Second); err == nil {
		var parsed any
		_ = json.Unmarshal(raw, &parsed)
		data["Status"] = parsed
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "ssl_autossl", data)
}

// sslAutoSslRun POST /ssl/autossl/run — kicks off AutoSSL for all pending
// domains. Mirrors SslCenterController::autosslRun.
func (s *Server) sslAutoSslRun(w http.ResponseWriter, r *http.Request) {
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
	res, err := s.agent.Call(r.Context(), "ssl.autossl_run", nil, 300*time.Second)
	if err != nil {
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{
			Action: "ssl.autossl_run", TargetType: "server", TargetID: "ssl",
			Result: audit.Failure, Details: map[string]any{"error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/autossl", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.autossl_run", TargetType: "server", TargetID: "ssl",
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	if m, _ := res.(map[string]any); m != nil {
		if issued, ok := m["issued"].(float64); ok && issued > 0 {
			sess.Flash("success", "Issued "+strconv.Itoa(int(issued))+" certificate(s).")
			http.Redirect(w, r, "/ssl/autossl", http.StatusSeeOther)
			return
		}
	}
	sess.Flash("success", "AutoSSL run finished.")
	http.Redirect(w, r, "/ssl/autossl", http.StatusSeeOther)
}

// --- helpers -----------------------------------------------------------

func mergeAny(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}
