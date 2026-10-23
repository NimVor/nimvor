// This file ports the outgoing mail limits pages (MailRateController): the
// settings, this hour's use per website account, and letting a blocked
// account send again.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mailRateBack is where the limits changes come back (the PHP BACK).
const mailRateBack = "/mail/admin/sending-limits"

// mailRateAudit records through the shared audit hook (PHP:
// $audit->record($action, 'server', 'mail-limits', …)).
func (s *Server) mailRateAudit(r *http.Request, user *identity.User, action, target, result string, details map[string]any) {
	s.mailAdminAudit(r, user, action, "mail-limits", result, details)
}

// mailRateErrMsg is the agent's user-facing message, or the fallback.
func mailRateErrMsg(fallback string, err error) string {
	ae := &agent.Error{}
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	return fallback
}

// mailRateIndex renders the limits settings + accounts + events
// (MailRateController::index).
func (s *Server) mailRateIndex(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	var st *hosting.RateState
	agentErr := ""
	if raw, err := s.agent.Call(r.Context(), "mail.rate_get", nil, 60*time.Second); err != nil {
		agentErr = s.mailAgentErr("mail.rate_get", err)
	} else if err := json.Unmarshal(raw, &st); err != nil {
		s.log.Error("mail limits state", "error", err)
		agentErr = "The mail sending limits came back unreadable."
	}
	s.render(w, "mail_admin_sending_limits", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Nav":       "limits",
		"State":     st,
		"Accounts":  mailRateAccountRows(st),
		"Events":    mailRateEventRows(st),
		"Max":       hosting.RateMax,
		"CanManage": s.can(r, "server.manage"),
		"AgentErr":  agentErr,
		"Success":   pullFlashKey(sess, "success"),
		"Error":     pullFlashKey(sess, "error"),
	})
}

// mailRateAccountRow is one accounts-table row, prepared for the view.
type mailRateAccountRow struct {
	Name    string
	Sent    int
	Blocked bool
	Limit   string // "" = the default limit
}

func mailRateAccountRows(st *hosting.RateState) []mailRateAccountRow {
	rows := []mailRateAccountRow{}
	if st == nil {
		return rows
	}
	used := map[string]hosting.RateAccountUsage{}
	if st.Usage != nil {
		for _, u := range st.Usage.Accounts {
			used[u.Name] = u
		}
	}
	for _, name := range st.Accounts {
		row := mailRateAccountRow{Name: name}
		if u, ok := used[name]; ok {
			row.Sent = u.Sent
			row.Blocked = u.Blocked
		}
		if l, ok := st.Config.Accounts[name]; ok {
			row.Limit = strconv.Itoa(l)
		}
		rows = append(rows, row)
	}
	return rows
}

// mailRateEventRow is one limits-reached row, prepared for the view.
type mailRateEventRow struct {
	When    string
	Account string
	Limit   int
}

func mailRateEventRows(st *hosting.RateState) []mailRateEventRow {
	out := []mailRateEventRow{}
	if st == nil || st.Usage == nil {
		return out
	}
	events := st.Usage.Events
	for i := len(events) - 1; i >= 0 && len(out) < 50; i-- {
		ev := events[i]
		account := ev.Name
		if account == "" {
			account = "UID " + strconv.Itoa(ev.UID)
		}
		when := "—"
		if ev.Time > 0 {
			when = time.Unix(ev.Time, 0).Format("2006-01-02 15:04")
		}
		out = append(out, mailRateEventRow{When: when, Account: account, Limit: ev.Limit})
	}
	return out
}

// mailRateSave applies the limits form (MailRateController::save).
func (s *Server) mailRateSave(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	_ = r.ParseForm()
	fail := func(details map[string]any, msg string) {
		d := map[string]any{"error": msg}
		for k, v := range details {
			d[k] = v
		}
		s.mailRateAudit(r, user, "mail.rate_settings", "mail-limits", "failure", d)
		sess.Flash("error", msg)
		http.Redirect(w, r, mailRateBack, http.StatusSeeOther)
	}
	st, err := hosting.RateRead(r.Context(), s.agent)
	if err != nil {
		fail(nil, mailRateErrMsg("The mail sending limits could not be read.", err))
		return
	}
	accounts := map[string]string{}
	for k, vals := range r.PostForm {
		name, ok := strings.CutPrefix(k, "accounts[")
		if !ok {
			continue
		}
		name = strings.TrimSuffix(name, "]")
		if name != "" && len(vals) > 0 {
			accounts[name] = vals[0]
		}
	}
	cfg, msg := hosting.RateValidate(r.PostFormValue("enabled"), r.PostFormValue("per_hour"), accounts, st.Accounts)
	if msg != "" {
		fail(nil, msg)
		return
	}
	sites, err := hosting.NewWebsiteRepository(s.db).All(r.Context(), s.currentServerID(r), user)
	if err != nil {
		s.log.Error("mail limits: websites", "error", err)
		fail(cfg, "The mail sending limits could not be saved.")
		return
	}
	svc := s.websiteService(r, user)
	if err := hosting.RateSave(r.Context(), s.agent, cfg, st.Config.Enabled, sites, svc.Reapply); err != nil {
		var ue *hosting.UserError
		if errors.As(err, &ue) && ue.What != "" {
			fail(cfg, ue.What)
		} else {
			fail(cfg, mailRateErrMsg("The mail sending limits could not be saved.", err))
		}
		return
	}
	s.mailRateAudit(r, user, "mail.rate_settings", "mail-limits", "success", cfg)
	if cfg["enabled"].(bool) {
		sess.Flash("success", "Mail sending limits saved; they are on.")
	} else {
		sess.Flash("success", "Mail sending limits saved; they are off.")
	}
	http.Redirect(w, r, mailRateBack, http.StatusSeeOther)
}

// mailRateReset lets an account (or all) send again this hour
// (MailRateController::reset).
func (s *Server) mailRateReset(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	_ = r.ParseForm()
	account := r.PostFormValue("account")
	target := "all"
	if account != "" {
		target = account
	}
	if err := hosting.RateReset(r.Context(), s.agent, account); err != nil {
		msg := mailRateErrMsg("The mail sending limits could not be reset.", err)
		s.mailRateAudit(r, user, "mail.rate_reset", target, "failure", map[string]any{"error": msg})
		sess.Flash("error", msg)
		http.Redirect(w, r, mailRateBack, http.StatusSeeOther)
		return
	}
	s.mailRateAudit(r, user, "mail.rate_reset", target, "success", nil)
	if account != "" {
		sess.Flash("success", account+" may send again this hour.")
	} else {
		sess.Flash("success", "All accounts may send again this hour.")
	}
	http.Redirect(w, r, mailRateBack, http.StatusSeeOther)
}
