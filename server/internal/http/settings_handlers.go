package http

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// settingsIndex GET /settings — lists every panel setting key/value from the
// settings table, plus a simple form that updates submitted scalars by key.
// Mirrors SettingsController::index (route "settings", settings.view).
func (s *Server) settingsIndex(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	vals, err := s.settings.All(r.Context())
	if err != nil {
		s.log.Error("load settings", "error", err)
		vals = map[string]string{}
	}
	data := map[string]any{
		"CSRF":    session.CSRFToken(sess),
		"User":    user,
		"Title":   "Panel settings",
		"Values":  vals,
		"Success": "",
		"Error":   "",
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	s.render(w, "settings_index", data)
}

// settingsUpdate POST /settings — persists submitted scalar settings. Only
// scalar keys (string/int/bool shapes as sent via form inputs) are written;
// complex structured keys are ignored. Mirrors SettingsController::update.
func (s *Server) settingsUpdate(w http.ResponseWriter, r *http.Request) {
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
	tab := r.FormValue("tab")
	if !isSafeTab(tab) {
		tab = ""
	}
	saved := 0
	for key, vals := range r.Form {
		if key == "_token" || key == "tab" {
			continue
		}
		if !isKnownSettingKey(key) {
			continue
		}
		v := ""
		if len(vals) > 0 {
			v = vals[0]
		}
		var val any
		switch {
		case strings.HasSuffix(key, "_enabled") || key == "auto_ssl" || key == "auto_ssl_replace" ||
			key == "av_quarantine" || key == "av_scan_on_upload" || key == "av_scan_on_extract" ||
			key == "apps_installer_enabled":
			val = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "on")
		case looksInt(v):
			n, _ := strconv.Atoi(v)
			val = n
		default:
			val = v
		}
		if err := s.settings.Set(r.Context(), key, val, user.ID); err != nil {
			sess.Flash("error", err.Error())
			http.Redirect(w, r, "/settings"+hashFor(tab), http.StatusSeeOther)
			return
		}
		saved++
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "settings.update", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: map[string]any{"saved": saved},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Settings saved.")
	http.Redirect(w, r, "/settings"+hashFor(tab), http.StatusSeeOther)
}

// settingsBasic GET /settings/basic — server-basic settings page (hostname,
// public IPv4 addresses, nameservers, contact). Best-effort: fetches
// system.info from the agent and falls back to stored settings.
// Mirrors WebHostSettingsController::basic.
func (s *Server) settingsBasic(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	vals, _ := s.settings.All(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Basic settings",
		"Values":     vals,
		"Hostname":   "",
		"Addresses":  []string{},
		"Success":    "",
		"Error":      "",
		"AgentError": nil,
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	if raw, err := s.agent.Call(r.Context(), "system.info", nil, 30*time.Second); err == nil {
		var sys map[string]any
		if json.Unmarshal(raw, &sys) == nil {
			if sm, _ := sys["system"].(map[string]any); sm != nil {
				if h, _ := sm["hostname"].(string); h != "" {
					data["Hostname"] = h
				}
				if ifaces, _ := sm["interfaces"].([]any); ifaces != nil {
					addrs := []string{}
					for _, ifi := range ifaces {
						if im, ok := ifi.(map[string]any); ok {
							if alist, _ := im["addresses"].([]any); alist != nil {
								for _, a := range alist {
									as, _ := a.(string)
									if idx := strings.Index(as, "/"); idx >= 0 {
										as = as[:idx]
									}
									if isPublicIPv4(as) {
										addrs = append(addrs, as)
									}
								}
							}
						}
					}
					data["Addresses"] = dedup(addrs)
				}
			}
		}
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "settings_basic", data)
}

// settingsBasicSave POST /settings/basic — saves the keys that live on the
// basic page (panel_name, panel_url, dns_default_ipv4, dns_hostmaster,
// dns_nameservers). Mirrors WebHostSettingsController::saveBasic.
func (s *Server) settingsBasicSave(w http.ResponseWriter, r *http.Request) {
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
	keys := []string{"panel_name", "panel_url", "dns_default_ipv4", "dns_hostmaster"}
	for _, k := range keys {
		if err := s.settings.Set(r.Context(), k, r.FormValue(k), user.ID); err != nil {
			sess.Flash("error", err.Error())
			http.Redirect(w, r, "/settings/basic", http.StatusSeeOther)
			return
		}
	}
	nsList := []string{}
	for i := 1; i <= 4; i++ {
		ns := strings.ToLower(strings.TrimRight(strings.TrimSpace(r.FormValue("ns"+strconv.Itoa(i))), "."))
		if ns == "" {
			continue
		}
		if !validHostname(ns) {
			sess.Flash("error", "Enter a host name such as ns"+strconv.Itoa(i)+".example.com.")
			http.Redirect(w, r, "/settings/basic#nameservers", http.StatusSeeOther)
			return
		}
		nsList = append(nsList, ns)
	}
	nsVal := ""
	if len(nsList) > 0 {
		nsVal = strings.Join(dedup(nsList), ", ")
	}
	if err := s.settings.Set(r.Context(), "dns_nameservers", nsVal, user.ID); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/settings/basic", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "settings.basic.save", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Saved.")
	http.Redirect(w, r, "/settings/basic", http.StatusSeeOther)
}

// settingsTweak GET /settings/tweak — tweak-settings page stub; renders the
// stored values for the keys that live on the tweak page with a friendly
// notice that per-tab categorisation is not fully ported. Mirrors
// WebHostSettingsController::tweak.
func (s *Server) settingsTweak(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	vals, _ := s.settings.All(r.Context())
	_ = hosting.IsAll(user)
	data := map[string]any{
		"CSRF":    session.CSRFToken(sess),
		"User":    user,
		"Title":   "Tweak settings",
		"Values":  vals,
		"Stub":    false,
		"Success": "",
		"Error":   "",
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	s.render(w, "settings_tweak", data)
}

// settingsTweakSave POST /settings/tweak — saves submitted tweak settings.
// Mirrors WebHostSettingsController::saveTweak (without the password-confirm
// and reset flows, which are deferred).
func (s *Server) settingsTweakSave(w http.ResponseWriter, r *http.Request) {
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
	tab := r.FormValue("tab")
	saved := 0
	for key, vals := range r.Form {
		if key == "_token" || key == "tab" {
			continue
		}
		if !isTweakKey(key) {
			continue
		}
		v := ""
		if len(vals) > 0 {
			v = vals[0]
		}
		var val any
		if strings.HasPrefix(key, "alert_") || strings.HasPrefix(key, "av_") ||
			strings.HasPrefix(key, "session_") || strings.HasPrefix(key, "dashboard_") ||
			strings.HasPrefix(key, "discovery_") || strings.HasPrefix(key, "nginx_client_") {
			if looksInt(v) {
				n, _ := strconv.Atoi(v)
				val = n
			} else {
				val = v
			}
		} else {
			val = v
		}
		if err := s.settings.Set(r.Context(), key, val, user.ID); err != nil {
			sess.Flash("error", err.Error())
			http.Redirect(w, r, "/settings/tweak"+hashFor(tab), http.StatusSeeOther)
			return
		}
		saved++
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "settings.tweak.save", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: map[string]any{"saved": saved, "tab": tab},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Saved.")
	http.Redirect(w, r, "/settings/tweak"+hashFor(tab), http.StatusSeeOther)
}

// settingsMail POST /settings/mail — saves mail configuration keys
// (mail_host, mail_port, mail_encryption, mail_username, mail_from,
// mail_alert_to). Mirrors SettingsController::mail. The real MailConfig
// service is not ported, so values are persisted in settings as plain keys
// and the flash notes outgoing email is not yet wired up.
func (s *Server) settingsMail(w http.ResponseWriter, r *http.Request) {
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
	keys := []string{"mail_host", "mail_port", "mail_encryption", "mail_username", "mail_from", "mail_alert_to"}
	for _, k := range keys {
		if err := s.settings.Set(r.Context(), k, r.FormValue(k), user.ID); err != nil {
			sess.Flash("error", err.Error())
			http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
			return
		}
	}
	if pw := r.FormValue("mail_password"); pw != "" {
		if err := s.settings.Set(r.Context(), "mail_password", pw, user.ID); err != nil {
			sess.Flash("error", err.Error())
			http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
			return
		}
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "settings.mail", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Email settings saved. Send a test message to check them.")
	http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
}

// settingsMailTest POST /settings/mail/test — sends a short test email to
// the current user's address via the internal SMTP Mailer.
// Mirrors SettingsController::testMail (settings.mail.test, settings.manage).
func (s *Server) settingsMailTest(w http.ResponseWriter, r *http.Request) {
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
	to := strings.TrimSpace(user.Email)
	if to == "" {
		sess.Flash("error", "Your account has no email address on file; set one on your profile first.")
		http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
		return
	}
	uid := user.ID
	if err := s.mailer.Send(r.Context(), []string{to}, "WhaCP test message", "This is a test message from WhaCP.\n\nIf you can read it, outgoing email works.\n"); err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "settings.mail_test", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
			Result: audit.Failure, Details: map[string]any{"to": to, "error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "settings.mail_test", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: map[string]any{"to": to},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Test message sent to "+to+".")
	http.Redirect(w, r, "/settings#email", http.StatusSeeOther)
}

// --- helpers ------------------------------------------------------------

func hashFor(tab string) string {
	if !isSafeTab(tab) {
		return ""
	}
	return "#" + tab
}

func isSafeTab(t string) bool {
	if t == "" {
		return false
	}
	if len(t) > 20 {
		return false
	}
	for _, c := range t {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// knownSettingKeys is the allowlist of keys the generic POST /settings form
// may write, so a rogue POST cannot smuggle arbitrary keys into the table.
// Keep this in sync with SettingsService::definitions() keys that this
// migration has seen in PHP.
var knownSettingKeys = map[string]bool{
	"panel_name": true, "panel_url": true, "session_idle_minutes": true,
	"discovery_cache_seconds": true, "dashboard_refresh_seconds": true,
	"alert_disk_percent": true, "alert_memory_percent": true, "alert_load_per_core": true,
	"alert_webhook_url": true, "dns_nameservers": true, "dns_hostmaster": true,
	"dns_default_ipv4": true, "acme_provider": true, "auto_ssl": true,
	"auto_ssl_replace": true, "av_scan_days": true, "av_quarantine": true,
	"av_scan_on_upload": true, "av_scan_on_extract": true, "av_scan_max_mb": true,
	"apps_installer_enabled": true, "nginx_client_max_body_mb": true,
}

func isKnownSettingKey(k string) bool { return knownSettingKeys[k] }

// tweakKeys are the keys saved by POST /settings/tweak.
var tweakKeys = map[string]bool{
	"session_idle_minutes": true, "discovery_cache_seconds": true,
	"dashboard_refresh_seconds": true, "alert_disk_percent": true,
	"alert_memory_percent": true, "alert_load_per_core": true,
	"alert_webhook_url": true, "av_scan_days": true, "av_quarantine": true,
	"av_scan_on_upload": true, "av_scan_on_extract": true, "av_scan_max_mb": true,
	"apps_installer_enabled": true, "nginx_client_max_body_mb": true,
}

func isTweakKey(k string) bool { return tweakKeys[k] }

func looksInt(v string) bool {
	if v == "" {
		return false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isPublicIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return false
	}
	nums := make([]int, 4)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 255 {
			return false
		}
		nums[i] = n
	}
	if nums[0] == 10 || nums[0] == 127 {
		return false
	}
	if nums[0] == 172 && nums[1] >= 16 && nums[1] <= 31 {
		return false
	}
	if nums[0] == 192 && nums[1] == 168 {
		return false
	}
	if nums[0] == 169 && nums[1] == 254 {
		return false
	}
	return true
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
