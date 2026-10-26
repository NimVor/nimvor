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
	data, ok := s.sslBase(w, r, "hosts", "SSL/TLS")
	if !ok {
		return
	}
	warn := data["WarnDays"].(int)
	raw, err := s.agent.Call(r.Context(), "ssl.store", nil, 60*time.Second)
	var rows []sslHostRow
	counts := map[string]int{}
	var renewal any
	if err != nil {
		data["AgentError"] = agentMessage(err)
	} else {
		var store any
		_ = json.Unmarshal(raw, &store)
		sites := s.sslSitesByName(r.Context(), r)
		list, _ := jmap(store)["hosts"].([]any)
		for _, h := range list {
			c := jmap(h)["certificate"]
			row := sslHostRow{Name: jstr(h, "name"), File: jstr(h, "file"), HTTPS: jbool(h, "https"), Enabled: jbool(h, "enabled"),
				Covered: jbool(h, "covered"), Whacp: jbool(h, "whacp"), HasCert: c != nil}
			if jbool(h, "redirects_to_https") {
				row.Redirect = "HTTP redirects"
			}
			row.State = sslHostState(row.HTTPS, row.Covered, c, warn)
			counts[row.State]++
			if c != nil {
				row.Cert = sslCert(c, warn)
			}
			row.SiteID = sites[strings.ToLower(row.Name)]
			rows = append(rows, row)
		}
		if raw2, err2 := s.agent.Call(r.Context(), "ssl.renewal_status", nil, 60*time.Second); err2 == nil {
			_ = json.Unmarshal(raw2, &renewal)
		} else {
			data["AgentError"] = agentMessage(err2)
		}
	}
	autoSsl, _ := s.settings.Bool(r.Context(), "auto_ssl", true)
	rs := map[string]any{"Known": renewal != nil}
	if renewal != nil {
		rs["Active"] = jbool(renewal, "timer_active")
		rs["Method"] = jstr(renewal, "method")
		rs["NoCertbot"] = !jbool(renewal, "certbot_installed")
		rs["CanEnable"] = jbool(renewal, "can_enable")
	}
	data["Rows"], data["Counts"], data["Renewal"], data["AutoSsl"] = rows, counts, rs, autoSsl
	data["Total"] = len(rows)
	s.render(w, "ssl_hosts", data)
}

// sslEnableRenewal POST /ssl/renewal — enables certbot's renewal timer via
// agent ssl.renewal_enable. Mirrors SslCenterController::enableRenewal.
func (s *Server) sslEnableRenewal(w http.ResponseWriter, r *http.Request) {
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	if m := agentObject(res); m != nil {
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
	data, ok := s.sslBase(w, r, "certificates", "SSL/TLS")
	if !ok {
		return
	}
	warn := data["WarnDays"].(int)
	sources := map[string]string{"letsencrypt": "Let's Encrypt", "whacp": "Installed in WhaCP", "other": "Elsewhere (read only)"}
	var rows []sslCertRow
	if raw, err := s.agent.Call(r.Context(), "ssl.store", nil, 60*time.Second); err != nil {
		data["AgentError"] = agentMessage(err)
	} else {
		var store any
		_ = json.Unmarshal(raw, &store)
		list, _ := jmap(store)["certificates"].([]any)
		for _, x := range list {
			c := jmap(x)["certificate"]
			day := func(k string) string {
				v := jstr(c, k)
				if len(v) > 10 {
					v = v[:10]
				}
				return v
			}
			row := sslCertRow{Name: jstr(x, "name"), Source: jstr(x, "source"), SourceLabel: sources[jstr(x, "source")], Cert: sslCert(c, warn),
				Names: strings.Join(strList(jmap(c)["dns_names"]), ", "), Subject: jstr(c, "subject"), Issuer: jstr(c, "issuer"),
				Valid: day("not_before") + " – " + day("not_after"), Fingerprint: jstr(c, "fingerprint_sha256"), File: jstr(c, "file"),
				UsedBy: strings.Join(strList(jmap(x)["used_by"]), ", "), Deletable: jbool(x, "deletable") && data["CanManage"].(bool),
				KeyReadable: jbool(c, "key_world_readable")}
			if row.Names == "" {
				row.Names = jstr(c, "common_name")
			}
			if n := int(jnum(c, "chain_length")); n > 0 {
				row.Meta = " · chain of " + strconv.Itoa(n)
			}
			rows = append(rows, row)
		}
	}
	data["Rows"], data["Total"] = rows, len(rows)
	s.render(w, "ssl_certificates", data)
}

// sslDeleteCertificate POST /ssl/certificates/delete — deletes a
// certificate (letsencrypt or whacp/custom) via agent. Mirrors
// SslCenterController::deleteCertificate.
func (s *Server) sslDeleteCertificate(w http.ResponseWriter, r *http.Request) {
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	param := "cert_name"
	switch source {
	case "letsencrypt":
		action = "ssl.delete"
	case "whacp":
		action, param = "ssl.custom_delete", "name"
	default:
		sess.Flash("error", "Only Let's Encrypt and WhaCP certificates can be deleted here.")
		http.Redirect(w, r, "/ssl/certificates", http.StatusSeeOther)
		return
	}
	if _, err := s.agent.Call(r.Context(), action, map[string]any{param: name}, 60*time.Second); err != nil {
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
	data, ok := s.sslBase(w, r, "keys", "SSL/TLS")
	if !ok {
		return
	}
	typeLabels := map[string]string{"ecdsa256": "ECDSA P-256 (recommended)", "ecdsa384": "ECDSA P-384", "rsa2048": "RSA 2048", "rsa3072": "RSA 3072", "rsa4096": "RSA 4096"}
	label := func(t string) string {
		if l, ok := typeLabels[t]; ok {
			return l
		}
		return t
	}
	var keys []sslKeyRow
	var types []sslKeyType
	var open any
	call := func(action string, p any) any {
		raw, err := s.agent.Call(r.Context(), action, p, 60*time.Second)
		if err != nil {
			data["AgentError"] = agentMessage(err)
			return nil
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		return v
	}
	list := call("ssl.keys", nil)
	if list != nil {
		ks, _ := jmap(list)["keys"].([]any)
		for _, k := range ks {
			created := jstr(k, "created")
			if len(created) > 10 {
				created = created[:10]
			}
			has := ""
			if jbool(k, "has_csr") {
				has = "CSR"
			}
			if jbool(k, "self_signed") {
				has += " · self-signed"
			}
			keys = append(keys, sslKeyRow{ID: jstr(k, "id"), Names: strings.Join(strList(jmap(k)["names"]), ", "), Type: jstr(k, "key_type"), Created: created, Has: has})
		}
		for _, t := range strList(jmap(list)["key_types"]) {
			types = append(types, sslKeyType{Value: t, Label: label(t), Selected: t == "ecdsa256"})
		}
	}
	if id := r.URL.Query().Get("id"); id != "" && list != nil {
		open = call("ssl.key", map[string]any{"id": id})
	}
	if open != nil {
		names := strList(jmap(open)["names"])
		kv := &sslKeyView{ID: jstr(open, "id"), Names: strings.Join(names, ", "), Subject: jstr(open, "subject"), Type: label(jstr(open, "key_type")),
			PublicKey: jstr(open, "public_key_sha256"), CSR: jstr(open, "csr"), SelfSigned: jbool(open, "self_signed")}
		repo := hosting.NewWebsiteRepository(s.db)
		if sites, err := repo.All(r.Context(), s.currentServerID(r), userFrom(r.Context())); err == nil {
			var fit, rest []sslSiteOpt
			for _, st := range sites {
				o := sslSiteOpt{ID: st.ID, Domain: st.Domain}
				if hasFold(names, st.Domain) {
					fit = append(fit, o)
				} else {
					rest = append(rest, o)
				}
			}
			kv.Sites = append(fit, rest...)
		}
		data["Key"] = kv
	}
	data["Keys"], data["KeyTypes"] = keys, types
	s.render(w, "ssl_keys", data)
}

type sslKeyRow struct{ ID, Names, Type, Created, Has string }
type sslKeyType struct {
	Value, Label string
	Selected     bool
}
type sslSiteOpt struct {
	ID     int64
	Domain string
}
type sslKeyView struct {
	ID, Names, Subject, Type, PublicKey, CSR string
	SelfSigned                               bool
	Sites                                    []sslSiteOpt
}

func hasFold(l []string, v string) bool {
	for _, x := range l {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}

// sslCreateCsr POST /ssl/keys — generates a private key + CSR (and
// optionally a self-signed certificate) via the agent's ssl.csr_create
// action. Mirrors SslCenterController::createCsr (ssl.manage).
func (s *Server) sslCreateCsr(w http.ResponseWriter, r *http.Request) {
	if !s.sslAdminOnly(w, r) {
		return
	}
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
		keyType = "ecdsa256"
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
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	name := r.FormValue("id")
	if _, err := s.agent.Call(r.Context(), "ssl.key_delete", map[string]any{"id": name}, 60*time.Second); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "ssl.key_delete", TargetType: "server", TargetID: "ssl",
		Result: audit.Success, Details: map[string]any{"id": name},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Key "+name+" deleted.")
	http.Redirect(w, r, "/ssl/keys", http.StatusSeeOther)
}

// sslCsrDownload GET /ssl/keys/csr — downloads a CSR PEM file for the
// given key id (SslCenterController::csrDownload, ssl.manage). Uses agent
// ssl.key to fetch the key record; if a CSR exists it is served as
// application/pkcs10 with Content-Disposition.
func (s *Server) sslCsrDownload(w http.ResponseWriter, r *http.Request) {
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	// Sanitize the file name to ASCII letters/digits/underscores so it
	// is always a safe on-disk name.
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
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	if m := agentObject(res); m != nil {
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

// sslAccountRegister POST /ssl/services/account — registers (or updates)
// an ACME account for a provider, persists it as the active acme_provider,
// and redirects to /ssl/services#lets-encrypt-account.
// Mirrors SslCenterController::accountRegister (ssl.manage).
func (s *Server) sslAccountRegister(w http.ResponseWriter, r *http.Request) {
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	if !s.sslAdminOnly(w, r) {
		return
	}
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
	if m := agentObject(res); m != nil {
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
