package hosting

// This file carries the data of the mail administration pages (the agent's
// mail.queue / mail.report / mail.server_status / mail.trace /
// mail.repair_permissions replies). The admin handlers talk to the agent
// directly — there is no local state to keep, only the audit.

import (
	"fmt"
	"strings"
)

// QueuedMessage is one Postfix queue message (the agent's mailmgr.QueuedMessage).
type QueuedMessage struct {
	Queue      string   `json:"queue"` // active, deferred, hold, incoming, maildrop
	ID         string   `json:"id"`
	Arrival    int64    `json:"arrival"`
	Size       int64    `json:"size"`
	Sender     string   `json:"sender"`
	Recipients []string `json:"recipients"`
	Reason     string   `json:"reason,omitempty"` // why delivery was deferred
}

// MailQueue is the mail.queue reply.
type MailQueue struct {
	Messages []QueuedMessage `json:"messages"`
	Total    int             `json:"total"`
	Counts   map[string]int  `json:"counts"`
}

// MailLogLine is one mail log line (mail.log_tail).
type MailLogLine struct {
	Time    string `json:"time"`
	Ident   string `json:"ident"`
	Message string `json:"message"`
}

// MailLog is the mail.log_tail reply.
type MailLog struct {
	Lines  []MailLogLine `json:"lines"`
	Source string        `json:"source"`
}

// MailServiceStatus is one mail service (Postfix, Dovecot, OpenDKIM, Rspamd).
type MailServiceStatus struct {
	Unit      string `json:"unit"`
	Name      string `json:"name"`
	Active    bool   `json:"active"`
	Enabled   bool   `json:"enabled"`
	Version   string `json:"version"`
	Installed bool   `json:"installed"`
}

// MailServerStatus is the mail.server_status reply.
type MailServerStatus struct {
	Services   []MailServiceStatus          `json:"services"`
	Checks     map[string]string            `json:"checks"`
	Values     map[string]map[string]string `json:"values"`
	Labels     map[string]map[string]string `json:"labels"`
	Configured bool                         `json:"configured"`
}

// MailDelivery is one delivery of the report (the agent's logstats.Delivery).
type MailDelivery struct {
	Time   string `json:"time"`
	ID     string `json:"id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Relay  string `json:"relay"`
	Status string `json:"status"` // sent, delivered, deferred, bounced, rejected
	DSN    string `json:"dsn"`
	Delay  string `json:"delay"`
	Reason string `json:"reason"`
}

// MailRelayer is who sent mail (or a sender domain).
type MailRelayer struct {
	Kind     string `json:"kind"` // sasl, local, client (senders: the domain)
	Name     string `json:"name"`
	Messages int    `json:"messages"`
}

// MailReport is the mail.report's report part (the agent's logstats.Report).
type MailReport struct {
	Totals     map[string]int   `json:"totals"`
	Series     []map[string]any `json:"series"`
	Deliveries []MailDelivery   `json:"deliveries"`
	Relayers   []MailRelayer    `json:"relayers"`
	Senders    []MailRelayer    `json:"senders"`
	Truncated  bool             `json:"truncated"`
}

// MailTraceStep is one step of the mail troubleshooter.
type MailTraceStep struct {
	Depth   int    `json:"depth"`
	Address string `json:"address"`
	Result  string `json:"result"` // mailbox, forward, catchall, remote, backup, external, rejected, loop
	Detail  string `json:"detail"`
}

// MailTrace is the mail.trace reply.
type MailTrace struct {
	Address string          `json:"address"`
	Steps   []MailTraceStep `json:"steps"`
}

// RepairResult is the mail.repair_permissions reply (the agent's RepairStats).
type RepairResult struct {
	Dirs    int      `json:"dirs"`
	Files   int      `json:"files"`
	Fixed   int      `json:"fixed"`
	Skipped []string `json:"skipped,omitempty"`
}

// RepairDone builds the repair flash message (PHP MailAdminController::repair).
func RepairDone(res *RepairResult) string {
	dirs, files, fixed := 0, 0, 0
	var skipped []string
	if res != nil {
		dirs, files, fixed = res.Dirs, res.Files, res.Fixed
		skipped = res.Skipped
	}
	msg := fmt.Sprintf("Checked %d folder(s) and %d file(s); fixed %d.", dirs, files, fixed)
	if len(skipped) > 0 {
		show := skipped
		if len(show) > 5 {
			show = show[:5]
		}
		msg += fmt.Sprintf(" Skipped %d link(s) or special file(s): %s.", len(skipped), strings.Join(show, ", "))
	}
	return msg
}

// MailShorten is the PHP mb_strimwidth(s, 0, n, "…") for message cells
// (truncates whole runes and appends an ellipsis when cut).
func MailShorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
