package http

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
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

// sslInstallKey POST /ssl/keys/install — installs a custom private key +
// certificate via ssl.custom_install. Stub: returns a friendly error
// until file upload is wired.
func (s *Server) sslInstallKey(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Custom key/cert installation is not yet wired in the Go panel.")
	http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
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

// sslCsrDownload GET /ssl/keys/csr — downloads a CSR file. Stub: returns
// a friendly error.
func (s *Server) sslCsrDownload(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", "CSR download is not yet wired in the Go panel.")
	http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
}

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
func (s *Server) sslAccountRegister(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "ACME account registration is not yet wired in the Go panel.")
	http.Redirect(w, r, "/ssl/services", http.StatusSeeOther)
}

// sslServiceCert POST /ssl/services/cert — issues a certificate for a
// service. Stub.
func (s *Server) sslServiceCert(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Service certificate issuance is not yet wired in the Go panel.")
	http.Redirect(w, r, "/ssl/services", http.StatusSeeOther)
}

// sslHostnameIssue POST /ssl/services/hostname — issues a certificate for
// the panel hostname. Stub.
func (s *Server) sslHostnameIssue(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Hostname certificate issuance is not yet wired in the Go panel.")
	http.Redirect(w, r, "/ssl/services", http.StatusSeeOther)
}

// sslMailTls POST /ssl/services/mail-tls — configures mail TLS. Stub.
func (s *Server) sslMailTls(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "Mail TLS setup is not yet wired in the Go panel.")
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
