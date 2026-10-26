package http

// This file ports the mail administration pages (MailAdminController):
// the mail queue (list, headers, actions), the delivery reports and
// statistics, the mail server status + settings, and the tools
// (troubleshooter trace, mailbox permission repair).
//
// All of it is for server administrators (the route permissions plus the
// all-scope gate); the state lives on the server (the agent's Postfix,
// Dovecot and logs), so the handlers call the agent directly and keep the
// audit.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// mailAdminGate is the PHP denied(): the route permission is not enough —
// only all-scope users (server administrators) may use the mail
// administration, everyone else gets the 403 page.
func (s *Server) mailAdminGate(w http.ResponseWriter, r *http.Request) bool {
	if !hosting.IsAll(userFrom(r.Context())) {
		renderStatus(w, s, "error", map[string]any{
			"Title":   "Access denied",
			"Message": "Email administration is for server administrators.",
		}, http.StatusForbidden)
		return false
	}
	return true
}

// mailAgentErr is the page's agent-error notice (the PHP agentError card).
func (s *Server) mailAgentErr(action string, err error) string {
	ae := &agent.Error{}
	if errors.As(err, &ae) && ae.Message != "" {
		return ae.Message
	}
	s.log.Error("mail admin", "action", action, "error", err)
	return "The mail server could not be reached."
}

// mailQueueAge renders the message age like the PHP view (min/h/d).
func mailQueueAge(arrival int64, now time.Time) string {
	sec := now.Unix() - arrival
	if sec < 0 {
		sec = 0
	}
	switch {
	case sec < 3600:
		return fmt.Sprintf("%d min", sec/60)
	case sec < 172800:
		return fmt.Sprintf("%d h", sec/3600)
	default:
		return fmt.Sprintf("%d d", sec/86400)
	}
}

// mailQueueRecipients is the first three recipients + the rest's count.
func mailQueueRecipients(list []string) string {
	if len(list) > 3 {
		return strings.Join(list[:3], ", ") + fmt.Sprintf(" +%d", len(list)-3)
	}
	return strings.Join(list, ", ")
}

// mailQueueRow is one queue message, prepared for the table.
type mailQueueRow struct {
	ID          string
	Sender      string
	Recipients  string
	Status      string
	StatusClass string
	Reason      string
	Size        int64
	Age         string
}

// mailQueueRows prepares the table rows (the PHP view's per-row work).
func mailQueueRows(q *hosting.MailQueue, now time.Time) []mailQueueRow {
	if q == nil {
		return nil
	}
	rows := make([]mailQueueRow, 0, len(q.Messages))
	for _, m := range q.Messages {
		statusClass := "is-ok"
		switch m.Queue {
		case "deferred":
			statusClass = "is-warn"
		case "hold":
			statusClass = "is-off"
		}
		status := m.Queue
		if status != "" {
			status = strings.ToUpper(status[:1]) + status[1:]
		}
		rows = append(rows, mailQueueRow{
			ID: m.ID, Sender: m.Sender, Recipients: mailQueueRecipients(m.Recipients),
			Status: status, StatusClass: statusClass, Reason: mailShorten(m.Reason, 90),
			Size: m.Size, Age: mailQueueAge(m.Arrival, now),
		})
	}
	return rows
}

// mailShorten is the PHP mb_strimwidth(s, 0, n, "…") for the reason cells.
func mailShorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// mailAdminQueue lists the Postfix queue (MailAdminController::queue).
func (s *Server) mailAdminQueue(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	var q *hosting.MailQueue
	agentErr := ""
	if raw, err := s.agent.Call(r.Context(), "mail.queue", nil, 60*time.Second); err != nil {
		agentErr = s.mailAgentErr("mail.queue", err)
	} else if err := json.Unmarshal(raw, &q); err != nil {
		s.log.Error("mail queue", "error", err)
		agentErr = "The mail queue came back unreadable."
	}
	s.render(w, "mail_admin_queue", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Nav":       "queue",
		"Queue":     q,
		"Rows":      mailQueueRows(q, time.Now()),
		"CanManage": s.can(r, "server.manage"),
		"AgentErr":  agentErr,
		"Success":   pullFlashKey(sess, "success"),
		"Error":     pullFlashKey(sess, "error"),
	})
}

var queueIDRe = regexp.MustCompile(`^[0-9A-Za-z]{6,20}$`)

// mailAdminMessage is one queued message's headers as JSON (the PHP
// mail.admin.message, fetched by the queue page's modal).
func (s *Server) mailAdminMessage(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	id := r.PathValue("id")
	var res struct {
		ID      string            `json:"id"`
		Headers map[string]string `json:"headers"`
	}
	raw, err := s.agent.Call(r.Context(), "mail.queue_message", map[string]any{"id": id})
	if err != nil {
		msg := "The message could not be read."
		if ae := (&agent.Error{}); errors.As(err, &ae) && ae.Message != "" {
			msg = ae.Message
		}
		s.log.Error("mail queue message", "id", id, "error", err)
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": map[string]string{"message": msg}})
		return
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		s.log.Error("mail queue message", "id", id, "error", err)
		writeJSON(w, http.StatusNotFound, map[string]any{"success": false, "error": map[string]string{"message": "The message could not be read."}})
		return
	}
	headers := res.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "headers": headers})
}

// mailAdminQueueAction acts on the selected queue messages or flushes all
// the deferred ones (MailAdminController::queueAction).
func (s *Server) mailAdminQueueAction(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	action := r.PostFormValue("action")
	if err := r.ParseForm(); err != nil {
		s.log.Error("mail queue action", "error", err)
	}
	ids := []string{}
	for _, v := range r.PostForm["ids[]"] {
		if queueIDRe.MatchString(v) {
			ids = append(ids, v)
		}
	}
	if action != "flush" && len(ids) == 0 {
		sess.Flash("error", "Select messages first.")
		http.Redirect(w, r, "/mail/admin/queue", http.StatusSeeOther)
		return
	}
	if _, err := s.agent.Call(r.Context(), "mail.queue_action", map[string]any{"action": action, "ids": ids}, 120*time.Second); err != nil {
		s.flashMailAdminFailure(sess, "The mail queue action failed.", err)
		http.Redirect(w, r, "/mail/admin/queue", http.StatusSeeOther)
		return
	}
	s.mailAdminAudit(r, user, "mail.queue_"+action, "mail", "success", map[string]any{"count": len(ids)})
	done := map[string]string{"delete": "deleted", "retry": "sent for delivery", "hold": "put on hold", "release": "released", "requeue": "requeued"}
	msg := action
	if d, ok := done[action]; ok {
		msg = d
	}
	if action == "flush" {
		sess.Flash("success", "Delivery of every deferred message is being retried.")
	} else {
		sess.Flash("success", fmt.Sprintf("%d message(s) %s.", len(ids), msg))
	}
	http.Redirect(w, r, "/mail/admin/queue", http.StatusSeeOther)
}

// mailAdminAudit records through the shared audit hook (the PHP
// AuditLogger, server-scoped).
func (s *Server) mailAdminAudit(r *http.Request, user *identity.User, action, target, result string, details map[string]any) {
	s.websiteAuditHook(r, user)(r.Context(), action, "server", target, result, details)
}

// flashMailAdminFailure flashes the agent's message for user-facing agent
// failures, a fallback otherwise (logged).
func (s *Server) flashMailAdminFailure(sess *session.Session, fallback string, err error) {
	ae := &agent.Error{}
	if errors.As(err, &ae) && ae.Message != "" {
		sess.Flash("error", ae.Message)
		return
	}
	s.log.Error("mail admin action", "error", err)
	sess.Flash("error", fallback)
}

// mailReportRanges is the PHP MailAdminController::RANGES.
var mailReportRanges = map[string]int{"1h": 3600, "24h": 86400, "7d": 604800, "30d": 2592000}

// mailReportStates is the view's status labels and badge classes.
var mailReportStates = map[string][2]string{
	"sent":      {"Sent", "is-ok"},
	"delivered": {"Delivered here", "is-ok"},
	"deferred":  {"Deferred", "is-warn"},
	"bounced":   {"Bounced", "is-error"},
	"rejected":  {"Rejected", "is-error"},
}

// mailReportDeliveryRow is one deliveries-table row, prepared for the view.
type mailReportDeliveryRow struct {
	Time        string
	From        string
	To          string
	StatusLabel string
	StatusClass string
	DSN         string
	Reason      string
	Relay       string
	Delay       string
	ID          string
}

func mailReportDeliveries(report *hosting.MailReport) []mailReportDeliveryRow {
	if report == nil {
		return nil
	}
	rows := make([]mailReportDeliveryRow, 0, len(report.Deliveries))
	for _, d := range report.Deliveries {
		label, cls := d.Status, ""
		if st, ok := mailReportStates[d.Status]; ok {
			label, cls = st[0], st[1]
		}
		t := strings.ReplaceAll(d.Time, "T", " ")
		if len(t) > 19 {
			t = t[:19]
		}
		rows = append(rows, mailReportDeliveryRow{
			Time: t, From: d.From, To: d.To, StatusLabel: label, StatusClass: cls,
			DSN: d.DSN, Reason: mailShorten(d.Reason, 160), Relay: d.Relay, Delay: d.Delay, ID: d.ID,
		})
	}
	return rows
}

// mailRelayerRow is one relayer/sender row with its kind label.
type mailRelayerRow struct {
	KindLabel string
	Name      string
	Messages  int
}

var mailRelayerKinds = map[string]string{"sasl": "mailbox", "local": "server account", "client": "client"}

func mailRelayerRows(rel []hosting.MailRelayer) []mailRelayerRow {
	if len(rel) > 15 {
		rel = rel[:15]
	}
	rows := make([]mailRelayerRow, 0, len(rel))
	for _, r := range rel {
		k, ok := mailRelayerKinds[r.Kind]
		if !ok {
			k = r.Kind
		}
		rows = append(rows, mailRelayerRow{KindLabel: k, Name: r.Name, Messages: r.Messages})
	}
	return rows
}

// mailReportSeriesRow is one "over time" row, pre-computed for the view.
type mailReportSeriesRow struct {
	Start  string
	In     int
	Out    int
	Failed int
	Pct    int
}

func mailReportSeries(report *hosting.MailReport, rangeKey string) []mailReportSeriesRow {
	if report == nil {
		return nil
	}
	max := 1
	for _, s := range report.Series {
		inn, _ := s["received"].(float64)
		out, _ := s["sent"].(float64)
		if int(inn)+int(out) > max {
			max = int(inn) + int(out)
		}
	}
	rows := []mailReportSeriesRow{}
	keep := 16
	if rangeKey == "7d" || rangeKey == "30d" {
		keep = 10
	}
	for _, s := range report.Series {
		inn, _ := s["received"].(float64)
		out, _ := s["sent"].(float64)
		bounced, _ := s["bounced"].(float64)
		rejected, _ := s["rejected"].(float64)
		start, _ := s["start"].(string)
		if len(start) > keep {
			start = start[:keep]
		}
		start = strings.ReplaceAll(start, "T", " ")
		v := int(inn) + int(out)
		pct := int(math.Ceil(float64(v)*100/float64(max)/5)) * 5
		rows = append(rows, mailReportSeriesRow{Start: start, In: int(inn), Out: int(out), Failed: int(bounced) + int(rejected), Pct: pct})
	}
	return rows
}

// mailAdminReports renders the delivery reports and statistics
// (MailAdminController::reports).
func (s *Server) mailAdminReports(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	q := r.URL.Query()
	rangeKey := q.Get("range")
	seconds, ok := mailReportRanges[rangeKey]
	if !ok {
		rangeKey, seconds = "24h", 86400
	}
	address := strings.TrimSpace(q.Get("address"))
	status := q.Get("status")
	tab := "reports"
	if q.Get("view") == "stats" {
		tab = "stats"
	}
	if len(address) > 254 {
		address = address[:254]
	}
	now := time.Now()
	var report *hosting.MailReport
	source := ""
	agentErr := ""
	res := struct {
		Report *hosting.MailReport `json:"report"`
		Source string              `json:"source"`
	}{}
	if raw, err := s.agent.Call(r.Context(), "mail.report", map[string]any{
		"since":   now.Add(-time.Duration(seconds) * time.Second).UTC().Format(time.RFC3339),
		"until":   now.UTC().Format(time.RFC3339),
		"address": address, "status": status, "limit": 500,
	}, 120*time.Second); err != nil {
		agentErr = s.mailAgentErr("mail.report", err)
	} else if err := json.Unmarshal(raw, &res); err != nil {
		s.log.Error("mail report", "error", err)
		agentErr = "The delivery reports came back unreadable."
	} else {
		report, source = res.Report, res.Source
	}
	relayers, senders := []mailRelayerRow{}, []mailRelayerRow{}
	if report != nil {
		relayers = mailRelayerRows(report.Relayers)
		senders = mailRelayerRows(report.Senders)
	}
	s.render(w, "mail_admin_reports", map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Nav":        tab,
		"Tab":        tab,
		"Report":     report,
		"Series":     mailReportSeries(report, rangeKey),
		"Deliveries": mailReportDeliveries(report),
		"Relayers":   relayers,
		"Senders":    senders,
		"Source":     source,
		"Range":      rangeKey,
		"Address":    address,
		"Status":     status,
		"AgentErr":   agentErr,
		"Success":    pullFlashKey(sess, "success"),
		"Error":      pullFlashKey(sess, "error"),
	})
}

// mailServerSettingOrder is the PHP view's field order (the agent's extra
// keys go last, alphabetically).
var mailServerSettingOrder = []string{
	"message_size_limit", "smtpd_recipient_limit", "maximal_queue_lifetime",
	"bounce_queue_lifetime", "smtpd_client_connection_rate_limit",
	"smtpd_client_message_rate_limit", "smtpd_helo_required",
	"disable_vrfy_command", "smtp_tls_security_level", "inet_protocols",
	"relayhost", "mail_max_userip_connections", "auth_failure_delay",
	"ssl_min_protocol", "login_greeting",
}

// mailServerChoices are the select fields (the PHP view's choices).
var mailServerChoices = map[string][]string{
	"smtpd_helo_required":     {"yes", "no"},
	"disable_vrfy_command":    {"yes", "no"},
	"smtp_tls_security_level": {"may", "encrypt", "dane"},
	"inet_protocols":          {"all", "ipv4", "ipv6"},
	"ssl_min_protocol":        {"", "TLSv1.2", "TLSv1.3"},
	"authorized_submit_users": {"!nobody, static:anyone", "static:anyone"},
}

// mailSettingRow is one settings field, prepared for the view (the value as
// shown; Choices non-nil means a select).
type mailSettingRow struct {
	Key     string
	Label   string
	Value   string
	Choices []string
}

// mailShownValue is the PHP view's $shown (Postfix values as the form shows
// them: MB, days).
func mailShownValue(k, v string) string {
	switch k {
	case "message_size_limit":
		if v == "" {
			return ""
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return v
		}
		return strconv.FormatInt(int64(math.Round(float64(n)/1048576)), 10)
	case "maximal_queue_lifetime", "bounce_queue_lifetime":
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil {
			return strconv.Itoa(n)
		}
	default:
	}
	return v
}

func mailSettingRows(svc string, status *hosting.MailServerStatus) []mailSettingRow {
	labels := map[string]string{}
	values := map[string]string{}
	if status != nil {
		labels = status.Labels[svc]
		values = status.Values[svc]
	}
	rows := []mailSettingRow{}
	added := map[string]bool{}
	add := func(k string) {
		if added[k] {
			return
		}
		added[k] = true
		row := mailSettingRow{Key: k, Label: labels[k], Value: mailShownValue(k, values[k])}
		row.Choices = mailServerChoices[k]
		rows = append(rows, row)
	}
	for _, k := range mailServerSettingOrder {
		if _, ok := labels[k]; ok {
			add(k)
		}
	}
	rest := []string{}
	for k := range labels {
		if !added[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		add(k)
	}
	return rows
}

// mailServiceRow is one services-table row, prepared for the view.
type mailServiceRow struct {
	Name      string
	Unit      string
	Version   string
	Installed bool
	Active    bool
	Enabled   bool
	StateNote string
	Check     string // "" = no check for this service, "ok" = valid, else the problem
}

func mailServiceRows(status *hosting.MailServerStatus) []mailServiceRow {
	if status == nil {
		return nil
	}
	rows := make([]mailServiceRow, 0, len(status.Services))
	for _, s := range status.Services {
		row := mailServiceRow{Name: s.Name, Unit: s.Unit, Version: s.Version, Installed: s.Installed, Active: s.Active, Enabled: s.Enabled}
		if !s.Installed {
			if s.Name == "OpenDKIM" {
				row.StateNote = "created by Email → Set up and start"
			} else {
				row.StateNote = "install it under Server → Packages"
			}
		} else if s.Enabled {
			row.StateNote = "starts at boot"
		} else {
			row.StateNote = "not started at boot"
		}
		switch s.Name {
		case "Postfix", "Dovecot":
			key := strings.ToLower(s.Name)
			if c, ok := status.Checks[key]; ok {
				if c == "" {
					row.Check = "ok"
				} else {
					row.Check = mailShorten(c, 200)
				}
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// mailAdminServer renders the mail server status + settings + log tail
// (MailAdminController::server).
func (s *Server) mailAdminServer(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	var status *hosting.MailServerStatus
	var log *hosting.MailLog
	agentErr := ""
	if raw, err := s.agent.Call(r.Context(), "mail.server_status", nil, 60*time.Second); err != nil {
		agentErr = s.mailAgentErr("mail.server_status", err)
	} else if err := json.Unmarshal(raw, &status); err != nil {
		s.log.Error("mail server status", "error", err)
		agentErr = "The mail server status came back unreadable."
	}
	if raw, err := s.agent.Call(r.Context(), "mail.log_tail", map[string]any{"lines": 100}, 60*time.Second); err != nil {
		s.log.Error("mail log tail", "error", err)
	} else if err := json.Unmarshal(raw, &log); err != nil {
		s.log.Error("mail log tail", "error", err)
	}
	// PHP: older agents without the flag are assumed configured.
	configured := true
	if status != nil {
		configured = status.Configured
	}
	canManage := s.can(r, "server.manage") && configured
	s.render(w, "mail_admin_server", map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"Nav":        "server",
		"Status":     status,
		"Services":   mailServiceRows(status),
		"Log":        log,
		"Configured": configured,
		"Postfix":    mailSettingRows("postfix", status),
		"Dovecot":    mailSettingRows("dovecot", status),
		"CanManage":  canManage,
		"AgentErr":   agentErr,
		"Success":    pullFlashKey(sess, "success"),
		"Error":      pullFlashKey(sess, "error"),
	})
}

// mailAdminSaveSettings applies the changed Postfix/Dovecot settings
// (MailAdminController::saveSettings). Only the fields that changed are
// sent; the agent checks and reloads, rolling back on failure.
func (s *Server) mailAdminSaveSettings(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		s.log.Error("mail settings", "error", err)
	}
	pf := map[string]string{}
	dc := map[string]string{}
	current := map[string]string{}
	// name[key] -> key. (strings.Trim with a word is a character-SET cut: it ate
	// the first/last letters of keys such as smtpd_helo_required and
	// message_size_limit.)
	bracketKey := func(k, name string) (string, bool) {
		rest, ok := strings.CutPrefix(k, name+"[")
		if !ok {
			return "", false
		}
		key, ok := strings.CutSuffix(rest, "]")
		return key, ok && key != ""
	}
	// Two passes: the current[...] snapshot must be complete before the
	// changed-only comparison (the form map's order is random).
	for k, vals := range r.PostForm {
		if key, ok := bracketKey(k, "current"); ok && len(vals) > 0 {
			current[key] = vals[0]
		}
	}
	for k, vals := range r.PostForm {
		if len(vals) == 0 {
			continue
		}
		v := vals[0]
		if key, ok := bracketKey(k, "postfix"); ok {
			// Only fields that changed are sent (unchanged Postfix values
			// stay as Postfix has them).
			if cur, had := current[key]; !had || cur != v {
				pf[key] = v
			}
		} else if key, ok := bracketKey(k, "dovecot"); ok {
			dc[key] = v
		}
	}
	if _, err := s.agent.Call(r.Context(), "mail.settings_set", map[string]any{"postfix": pf, "dovecot": dc}, 120*time.Second); err != nil {
		s.flashMailAdminFailure(sess, "The mail server settings could not be applied.", err)
		http.Redirect(w, r, "/mail/admin/server", http.StatusSeeOther)
		return
	}
	s.mailAdminAudit(r, user, "mail.settings", "mail", "success", map[string]any{"postfix": pf, "dovecot": dc})
	sess.Flash("success", "Mail server settings applied.")
	http.Redirect(w, r, "/mail/admin/server", http.StatusSeeOther)
}

// mailAdminTools renders the troubleshooter trace + the mailbox permission
// repair form (MailAdminController::tools).
func (s *Server) mailAdminTools(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	address := strings.TrimSpace(r.URL.Query().Get("address"))
	if len(address) > 254 {
		address = address[:254]
	}
	var trace *hosting.MailTrace
	agentErr := ""
	if address != "" {
		if raw, err := s.agent.Call(r.Context(), "mail.trace", map[string]any{"address": address}, 60*time.Second); err != nil {
			agentErr = s.mailAgentErr("mail.trace", err)
		} else if err := json.Unmarshal(raw, &trace); err != nil {
			s.log.Error("mail trace", "error", err)
			agentErr = "The trace came back unreadable."
		}
	}
	domains := []string{}
	if ds, err := hosting.NewMailRepository(s.db).Domains(r.Context(), s.currentServerID(r), user); err != nil {
		s.log.Error("mail domains", "error", err)
	} else {
		for _, d := range ds {
			domains = append(domains, d.Domain)
		}
	}
	s.render(w, "mail_admin_tools", map[string]any{
		"User":      user,
		"CSRF":      session.CSRFToken(sess),
		"Nav":       "tools",
		"Address":   address,
		"Trace":     trace,
		"Domains":   domains,
		"CanManage": s.can(r, "server.manage"),
		"AgentErr":  agentErr,
		"Success":   pullFlashKey(sess, "success"),
		"Error":     pullFlashKey(sess, "error"),
	})
}

// mailAdminRepair fixes mailbox folder ownership/permissions
// (MailAdminController::repair).
func (s *Server) mailAdminRepair(w http.ResponseWriter, r *http.Request) {
	if !s.mailAdminGate(w, r) {
		return
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	domain := strings.ToLower(r.PostFormValue("domain"))
	target := domain
	if target == "" {
		target = "mail"
	}
	var res hosting.RepairResult
	raw, err := s.agent.Call(r.Context(), "mail.repair_permissions", map[string]any{"domain": domain}, 600*time.Second)
	if err != nil {
		s.mailAdminAudit(r, user, "mail.repair_permissions", target, "failure", map[string]any{"error": s.mailAgentErr("mail.repair_permissions", err)})
		s.flashMailAdminFailure(sess, "The mailbox permissions could not be repaired.", err)
		http.Redirect(w, r, "/mail/admin/tools", http.StatusSeeOther)
		return
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		s.log.Error("mail repair", "error", err)
	}
	s.mailAdminAudit(r, user, "mail.repair_permissions", target, "success", map[string]any{"fixed": res.Fixed})
	sess.Flash("success", hosting.RepairDone(&res))
	http.Redirect(w, r, "/mail/admin/tools", http.StatusSeeOther)
}
