package http

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mailPackages is the list installed by POST /mail/install (MailController::PACKAGES).
var mailPackages = []string{"postfix", "dovecot-imapd", "dovecot-lmtpd", "dovecot-sieve", "opendkim"}

// mailInstall POST /mail/install — installs the mail server stack via the
// agent packages.install and tracks the resulting job.
// Mirrors MailController::install (mail.install, packages.manage).
func (s *Server) mailInstall(w http.ResponseWriter, r *http.Request) {
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
	res, err := s.agent.Call(r.Context(), "packages.install", map[string]any{"names": mailPackages}, 120*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return
	}
	job := ""
	if m, _ := res.(map[string]any); m != nil {
		if j, _ := m["job_id"].(string); j != "" {
			job = j
		}
	}
	uid := user.ID
	if job != "" {
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Install the mail server", "server", "mail", &uid)
		sess.Flash("job", job)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "packages.install", TargetType: "server", TargetID: "mail", Result: audit.Success,
		Details: map[string]any{"names": mailPackages},
		UserID:  &uid, Username: user.Username,
	})
	sess.Flash("success", "Installing Postfix, Dovecot and OpenDKIM. Then set up email below.")
	http.Redirect(w, r, "/mail", http.StatusSeeOther)
}

// mailSetup POST /mail/setup — runs mail.configure on the agent with a
// hostname and optional confirm flag. Mirrors MailController::setup
// (mail.setup, server.manage).
func (s *Server) mailSetup(w http.ResponseWriter, r *http.Request) {
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
	host := strings.ToLower(strings.Trim(r.FormValue("hostname"), " ."))
	if !validHostname(host) {
		sess.Flash("error", "Enter the mail server's full host name, e.g. mail.example.com.")
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return
	}
	confirm := r.FormValue("confirm") == "1"
	res, err := s.agent.Call(r.Context(), "mail.configure", map[string]any{"hostname": host, "confirm_edit_package_config": confirm}, 180*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return
	}
	notes := []string{}
	if m, _ := res.(map[string]any); m != nil {
		if arr, _ := m["notes"].([]any); arr != nil {
			for _, n := range arr {
				if s, ok := n.(string); ok {
					notes = append(notes, s)
				}
			}
		}
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "mail.configure", TargetType: "server", TargetID: "mail", Result: audit.Success,
		Details: map[string]any{"hostname": host},
		UserID:  &uid, Username: user.Username,
	})
	msg := "Email is set up."
	if len(notes) > 0 {
		msg += " " + strings.Join(notes, " ")
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, "/mail", http.StatusSeeOther)
}

// mailInstallSpam POST /mail/spam/install — installs Rspamd via agent
// packages.install and tracks the job. Mirrors MailController::installSpam
// (mail.spam.install, packages.manage).
func (s *Server) mailInstallSpam(w http.ResponseWriter, r *http.Request) {
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
	res, err := s.agent.Call(r.Context(), "packages.install", map[string]any{"name": "rspamd"}, 120*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return
	}
	job := ""
	if m, _ := res.(map[string]any); m != nil {
		if j, _ := m["job_id"].(string); j != "" {
			job = j
		}
	}
	uid := user.ID
	if job != "" {
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Install the spam filter", "server", "mail", &uid)
		sess.Flash("job", job)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "packages.install", TargetType: "server", TargetID: "mail", Result: audit.Success,
		Details: map[string]any{"name": "rspamd"},
		UserID:  &uid, Username: user.Username,
	})
	sess.Flash("success", "Installing Rspamd. Then turn the spam filter on below.")
	http.Redirect(w, r, "/mail", http.StatusSeeOther)
}

// mailSpam POST /mail/spam — configures Rspamd thresholds via agent
// mail.spam_configure. Mirrors MailController::spam (mail.spam, server.manage).
func (s *Server) mailSpam(w http.ResponseWriter, r *http.Request) {
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
	mark, err1 := strconv.ParseFloat(r.FormValue("add_header"), 64)
	reject, err2 := strconv.ParseFloat(r.FormValue("reject"), 64)
	enabled := r.FormValue("enabled") == "1"
	if err1 != nil || err2 != nil || mark < 1 || mark > 50 || reject <= mark || reject > 100 {
		sess.Flash("error", "Use a marking score of 1–50 and a higher rejection score (at most 100).")
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return
	}
	params := map[string]any{
		"enabled":         enabled,
		"add_header":      mark,
		"reject":          reject,
		"confirm_replace": r.FormValue("confirm_replace") == "1",
	}
	if _, err := s.agent.Call(r.Context(), "mail.spam_configure", params, 120*time.Second); err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "mail.spam_configure", TargetType: "server", TargetID: "mail",
		Result: audit.Success, Details: params,
		UserID: &uid, Username: user.Username,
	})
	if enabled {
		sess.Flash("success", "The spam filter is on.")
	} else {
		sess.Flash("success", "The spam filter is off.")
	}
	http.Redirect(w, r, "/mail", http.StatusSeeOther)
}

// mailInstallWebmail POST /mail/webmail/install — installs Roundcube via
// agent mail.webmail_install and tracks the job. Mirrors
// MailController::installWebmail (mail.webmail.install, packages.manage).
func (s *Server) mailInstallWebmail(w http.ResponseWriter, r *http.Request) {
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
	res, err := s.agent.Call(r.Context(), "mail.webmail_install", nil, 180*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return
	}
	job := ""
	if m, _ := res.(map[string]any); m != nil {
		if j, _ := m["job_id"].(string); j != "" {
			job = j
		}
	}
	uid := user.ID
	if job != "" {
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), job, "Install webmail", "server", "mail", &uid)
		sess.Flash("job", job)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "mail.webmail_install", TargetType: "server", TargetID: "mail", Result: audit.Success,
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Installing Roundcube. Then turn webmail on below.")
	http.Redirect(w, r, "/mail", http.StatusSeeOther)
}

// mailWebmail POST /mail/webmail — enables/disables Roundcube via agent
// mail.webmail_configure. Mirrors MailController::webmail (mail.webmail,
// server.manage).
func (s *Server) mailWebmail(w http.ResponseWriter, r *http.Request) {
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
	enabled := r.FormValue("enabled") == "1"
	params := map[string]any{"enabled": enabled}
	if enabled {
		host := strings.ToLower(strings.TrimRight(strings.TrimSpace(r.FormValue("hostname")), "."))
		if !validHostname(host) {
			sess.Flash("error", "Enter the full host name webmail answers on, e.g. webmail.example.com.")
			http.Redirect(w, r, "/mail", http.StatusSeeOther)
			return
		}
		params["hostname"] = host
	}
	res, err := s.agent.Call(r.Context(), "mail.webmail_configure", params, 180*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/mail", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "mail.webmail_configure", TargetType: "server", TargetID: "mail",
		Result: audit.Success, Details: params,
		UserID: &uid, Username: user.Username,
	})
	if enabled {
		url := ""
		notes := []string{}
		if m, _ := res.(map[string]any); m != nil {
			if u, _ := m["url"].(string); u != "" {
				url = u
			}
			if arr, _ := m["notes"].([]any); arr != nil && len(arr) > 1 {
				for i, n := range arr {
					if i == 0 {
						continue
					}
					if s, ok := n.(string); ok {
						notes = append(notes, s)
					}
				}
			}
		}
		msg := "Webmail is on"
		if url != "" {
			msg += " at " + url + "."
		}
		if len(notes) > 0 {
			msg += " " + strings.Join(notes, " ")
		}
		sess.Flash("success", msg)
	} else {
		sess.Flash("success", "Webmail is off.")
	}
	http.Redirect(w, r, "/mail", http.StatusSeeOther)
}

// validHostname checks that host looks like a fully-qualified domain name
// (same regex as PHP MailController: ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$).
func validHostname(host string) bool {
	if host == "" {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return false
	}
	for i, l := range labels {
		ll := len(l)
		if ll == 0 || ll > 63 {
			return false
		}
		if l[0] == '-' || l[ll-1] == '-' {
			return false
		}
		isTLD := i == len(labels)-1
		for _, c := range l {
			isAlphaNum := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
			if !isAlphaNum && c != '-' {
				return false
			}
			if isTLD && (c == '-' || (c >= '0' && c <= '9')) {
				return false
			}
		}
	}
	return true
}
