package http

// This file ports the Security overview (SecurityController::index —
// security.audit + the panel's own checks, the SSH/panel login cards, the
// safety rating) and the anonymous MySQL users removal
// (SecurityController::dropAnonymousUsers).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// secCheck is one overview check. The agent's security.audit checks and the
// panel's own checks (panelChecks) share the shape; Action and Link are the
// panel-side additions of the view's link().
type secCheck struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`   // pass, warn, fail, info
	Severity string `json:"severity"` // low, medium, high
	Detail   string `json:"detail"`
	Remedy   string `json:"remedy"`
	Fix      string `json:"fix"` // "apply" or "undo" when the panel can fix it

	St     string     // the display status (unknown statuses show as info)
	Icon   string     // the bootstrap-icon name for St
	Action *secAction // link(): the fix/undo form (or the anonymous users removal)
	Link   string     // link(): a ported page that handles the check
}

// secAction is the check's action form (the view's $c['action']).
type secAction struct {
	Route   string
	Label   string
	Confirm string
	Undo    bool
	Params  map[string]string
}

// secCheckLinks is link()'s route map, restricted to the ported pages
// (PHP's hasRoute() skips the rest: packages, the database server, DNS, SMTP).
var secCheckLinks = map[string]string{
	"firewall":     "/security/firewall",
	"ssh_root":     "/security/ssh",
	"ssh_password": "/security/ssh",
	"fail2ban":     "/security/fail2ban",
}

// securityFixLabels is the FIXES catalog's labels and confirmations
// (SecurityController::FIXES); securityFix (fail2ban.go) holds the ops.
var securityFixLabels = map[string]map[string][2]string{
	"kernel_settings": {
		"apply": {"Apply recommended settings", "Apply the recommended kernel security settings now? They take effect at once and after restarts; the earlier values are kept so you can undo it."},
		"undo":  {"Undo kernel settings", "Put the earlier kernel settings back?"},
	},
	"proc_hidepid": {
		"apply": {"Hide other accounts' processes", "Mount /proc so that each account sees only its own processes? Root and the group whacp-proc still see all. Monitoring tools running as another account must be added to whacp-proc. Test that SSH logins and service management still work."},
		"undo":  {"Show all processes again", "Let every account see all processes again?"},
	},
	"auto_updates": {
		"apply": {"Turn on automatic security updates", "Install security updates automatically every day? The package for it is installed if needed. The server is never restarted automatically."},
		"undo":  {"Turn off automatic updates", "Stop installing security updates automatically?"},
	},
	"host_persistence": {
		"apply": {"Mark these as known", "Mark every listed scheduled task and service as known? Only do this if you recognise them all (for example your hosting provider's). Each is pinned by its content: if one changes later, it is reported again."},
		"undo":  {"Forget the files marked as known", "Report the scheduled tasks and services you marked as known again?"},
	},
	"database_network": {
		"apply": {"Accept local connections only", "Let only programs on this server connect to the database server (bind-address 127.0.0.1)? The database server is restarted; remote database users can no longer connect."},
	},
}

// linkSecCheck is the view's link(): the mysql_anonymous removal, the
// FIXES-catalog action (with the fix/undo op), and the open-page link.
func linkSecCheck(c *secCheck) {
	if c.ID == "mysql_anonymous" && c.Status == "fail" {
		c.Action = &secAction{Route: "/security/mysql-anonymous", Label: "Remove anonymous users", Confirm: "Remove anonymous users?"}
		return
	}
	if c.Fix != "" {
		if lbl, ok := securityFixLabels[c.ID][c.Fix]; ok {
			c.Action = &secAction{Route: "/security/fix", Label: lbl[0], Confirm: lbl[1], Undo: c.Fix == "undo", Params: map[string]string{"check": c.ID, "op": c.Fix}}
		}
	}
	c.Link = secCheckLinks[c.ID]
}

// secLastLogin is one of the login cards' rows.
type secLastLogin struct {
	User   string
	Method string
	IP     string
	When   string
}

// secWhen is the view's $when(): "Y-m-d H:i UTC" (the raw text when not a
// parseable time).
func secWhen(t string) string {
	if t == "" {
		return "—"
	}
	if ts, err := time.Parse(time.RFC3339, t); err == nil {
		return ts.UTC().Format("2006-01-02 15:04") + " UTC"
	}
	return t
}

// secLogins mirrors the agent's ssh.logins reply.
type secLogins struct {
	Available   bool   `json:"available"`
	Today       [2]int `json:"today"`
	Yesterday   [2]int `json:"yesterday"`
	Week        [2]int `json:"week"`
	LastSuccess *struct {
		Time   string `json:"time"`
		User   string `json:"user"`
		IP     string `json:"ip"`
		Method string `json:"method"`
	} `json:"last_success"`
	LastFailure *struct {
		Time string `json:"time"`
		User string `json:"user"`
		IP   string `json:"ip"`
	} `json:"last_failure"`
}

// loginBar is one row of the day counts (today / yesterday / 7 days), with
// the view's bar widths ($w5).
type loginBar struct {
	Label string
	Ok    int
	Bad   int
	OkW   int
	BadW  int
}

// panelChecks is the view's panelChecks(): the password policy and the
// hardening switches (fork bomb, traceroute).
func (s *Server) panelChecks(ctx context.Context, h map[string]any) []secCheck {
	out := []secCheck{}
	min, _ := s.settings.Int(ctx, "password_min_strength", 65)
	on, _ := s.settings.Bool(ctx, "policy_password_strength", true)
	c := secCheck{ID: "password_strength", Title: "Password strength requirements", Severity: "medium"}
	remedy := fmt.Sprintf("Require a password strength of %d or more under Password Strength Configuration.", 65)
	switch {
	case on && min >= 65:
		c.Status, c.Detail = "pass", fmt.Sprintf("Password strength requirements are strong (minimum strength %d).", min)
	case on:
		c.Status, c.Detail, c.Remedy = "warn", fmt.Sprintf("The minimum password strength is %d.", min), remedy
	default:
		c.Status, c.Detail, c.Remedy = "warn", "Password strength requirements are off.", remedy
	}
	out = append(out, c)

	hulk, _ := s.settings.Bool(ctx, "hulk_enabled", false)
	c = secCheck{ID: "hulk", Title: "cPHulk Brute Force Protection", Severity: "high"}
	if hulk {
		c.Status, c.Detail = "pass", "cPHulk Brute Force Protection is enabled."
	} else {
		c.Status, c.Detail, c.Remedy = "fail", "cPHulk Brute Force Protection is disabled.", "Enable it under cPHulk Brute Force Protection (add your own IP address to its whitelist first)."
	}
	out = append(out, c)

	if fb, ok := h["forkbomb"].(map[string]any); ok {
		enabled, _ := fb["enabled"].(bool)
		c = secCheck{ID: "forkbomb", Title: "Shell Fork Bomb Protection", Severity: "medium"}
		if enabled {
			c.Status, c.Detail = "pass", "Fork bomb protection is enabled."
		} else {
			c.Status, c.Detail, c.Remedy = "warn", "Fork bomb protection is disabled.", "Enable it under Shell Fork Bomb Protection."
		}
		out = append(out, c)
	}
	if tr, ok := h["traceroute"].(map[string]any); ok {
		enabled, _ := tr["enabled"].(bool)
		programs, _ := tr["programs"].([]any)
		none := len(programs) == 0
		c = secCheck{ID: "traceroute", Title: "Traceroute status", Severity: "low"}
		switch {
		case none:
			c.Status, c.Detail = "pass", "Traceroute is not installed."
		case !enabled:
			c.Status, c.Detail = "pass", "Traceroute is disabled for users other than root."
		default:
			c.Status, c.Detail, c.Remedy = "warn", "Traceroute is enabled for every user.", "Disable it under Traceroute Enable/Disable."
		}
		out = append(out, c)
	}
	return out
}

// secStatusOrder / secSeverityWeight are the view's sorting and the safety
// rating's weights (a warning counts half; info does not count).
var secStatusOrder = map[string]int{"fail": 0, "warn": 1, "pass": 2, "info": 3}
var secSeverityWeight = map[string]float64{"high": 3, "critical": 3, "medium": 2, "low": 1}
var secIcons = map[string]string{"pass": "check-circle-fill", "warn": "exclamation-triangle-fill", "fail": "x-circle-fill", "info": "info-circle"}

// secIndexSortScores sorts the checks (status, then title) and precomputes
// the display fields, the counts and the safety rating.
func secIndexSortScores(checks []secCheck) (score int, scoreClass, scoreLabel string, scoreSet bool, failCount, warnCount, passCount int) {
	sort.Slice(checks, func(i, j int) bool {
		oi, oki := secStatusOrder[checks[i].Status]
		if !oki {
			oi = 9
		}
		oj, okj := secStatusOrder[checks[j].Status]
		if !okj {
			oj = 9
		}
		if oi != oj {
			return oi < oj
		}
		return checks[i].Title < checks[j].Title
	})
	var total, got float64
	for i := range checks {
		c := &checks[i]
		switch c.Status {
		case "fail":
			failCount++
		case "warn":
			warnCount++
		case "pass":
			passCount++
		}
		c.St = c.Status
		if _, ok := secIcons[c.St]; !ok {
			c.St = "info"
		}
		c.Icon = secIcons[c.St]
		if c.Status == "info" {
			continue
		}
		w := secSeverityWeight[c.Severity]
		if w == 0 {
			w = 1
		}
		total += w
		switch c.Status {
		case "pass":
			got += w
		case "warn":
			got += w / 2
		}
	}
	scoreClass = "wcp-muted"
	if total > 0 {
		score = int(math.Round(got / total * 100))
		scoreSet = true
		switch {
		case score >= 80:
			scoreClass, scoreLabel = "wcp-good", "Secure"
		case score >= 60:
			scoreClass, scoreLabel = "wcp-warn", "Fair"
		default:
			scoreClass, scoreLabel = "wcp-bad", "Insecure"
		}
	}
	return score, scoreClass, scoreLabel, scoreSet, failCount, warnCount, passCount
}

// securityIndex renders the Security overview (SecurityController::index,
// security.view).
func (s *Server) securityIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	ctx := r.Context()

	// The agent's checks (the page's content needs the agent; PHP hides
	// everything on an agent error).
	var checks []secCheck
	agentMsg := ""
	var ae *agent.Error
	if res, err := s.agent.Call(ctx, "security.audit", nil, 120*time.Second); err != nil {
		if errors.As(err, &ae) && ae.Message != "" {
			agentMsg = ae.Message
		} else {
			s.log.Error("security audit", "error", err)
			agentMsg = "The WhaCP agent could not be reached."
		}
	} else if err := json.Unmarshal(res, &checks); err != nil {
		s.log.Error("security audit", "error", err)
		agentMsg = "The WhaCP agent answered with an unreadable check list."
	}
	var logins *secLogins
	var sshProtected string // "on", "off" or "" (unknown)
	var h map[string]any
	if agentMsg == "" {
		// The three reads are independent: ask the agent for them at once
		// instead of one after the other.
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			if res, err := s.agent.Call(ctx, "ssh.logins", nil, 60*time.Second); err == nil {
				var l secLogins
				if json.Unmarshal(res, &l) == nil {
					logins = &l
				}
			}
		}()
		go func() {
			defer wg.Done()
			if res, err := s.agent.Call(ctx, "hardening.status", nil); err == nil {
				json.Unmarshal(res, &h)
			}
		}()
		go func() {
			defer wg.Done()
			s.sshProtection(ctx, &sshProtected)
		}()
		wg.Wait()
	}

	all := s.panelChecks(ctx, h)
	for i := range checks {
		linkSecCheck(&checks[i])
	}
	all = append(all, checks...)
	score, scoreClass, scoreLabel, scoreSet, failCount, warnCount, passCount := secIndexSortScores(all)

	// The login cards: SSH (the agent) and the panel (the audit log).
	var sshOK, sshBad *secLastLogin
	if logins != nil {
		if l := logins.LastSuccess; l != nil {
			sshOK = &secLastLogin{User: l.User, Method: l.Method, IP: l.IP, When: secWhen(l.Time)}
		}
		if l := logins.LastFailure; l != nil {
			sshBad = &secLastLogin{User: l.User, IP: l.IP, When: secWhen(l.Time)}
		}
	}
	panelOK := s.latestPanelLogin(ctx, false)
	panelBad := s.latestPanelLogin(ctx, true)

	var bars []loginBar
	max := 1
	if logins != nil {
		if sum := logins.Week[0] + logins.Week[1]; sum > max {
			max = sum
		}
	}
	w5 := func(n int) int { return int(math.Round(float64(n)/float64(max)*20)) * 5 }
	if logins != nil && logins.Available {
		bars = []loginBar{
			{"Today", logins.Today[0], logins.Today[1], w5(logins.Today[0]), w5(logins.Today[1])},
			{"Yesterday", logins.Yesterday[0], logins.Yesterday[1], w5(logins.Yesterday[0]), w5(logins.Yesterday[1])},
			{"7 days", logins.Week[0], logins.Week[1], w5(logins.Week[0]), w5(logins.Week[1])},
		}
	}
	canManage, _ := s.authorizer().Can(ctx, user, "security.manage")
	canServerManage, _ := s.authorizer().Can(ctx, user, "server.manage")
	canAudit, _ := s.authorizer().Can(ctx, user, "audit.view")

	data := map[string]any{
		"User":            user,
		"CSRF":            session.CSRFToken(sess),
		"AgentError":      agentMsg,
		"Pending":         s.securityPending(ctx),
		"CanManage":       canManage,
		"CanServerManage": canServerManage,
		"CanAudit":        canAudit,
		"Logins":          logins,
		"LoginBars":       bars,
		"SshProtected":    sshProtected,
		"SshLastOK":       sshOK,
		"SshLastBad":      sshBad,
		"PanelOK":         panelOK,
		"PanelBad":        panelBad,
		"Checks":          all,
		"Score":           score,
		"ScoreClass":      scoreClass,
		"ScoreLabel":      scoreLabel,
		"ScoreSet":        scoreSet,
		"FailCount":       failCount,
		"WarnCount":       warnCount,
		"PassCount":       passCount,
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")

	s.render(w, "security_overview", data)
}

// latestPanelLogin is the view's panelLogin queries (the latest
// auth.login audit row, successful or not).
func (s *Server) latestPanelLogin(ctx context.Context, fail bool) *secLastLogin {
	op := "="
	if fail {
		op = "<>"
	}
	var username, ip, at string
	err := s.db.QueryRowContext(ctx, `SELECT username, ip, created_at FROM audit_logs WHERE action = 'auth.login' AND result `+op+` 'success' ORDER BY id DESC LIMIT 1`).Scan(&username, &ip, &at)
	if err != nil {
		return nil
	}
	return &secLastLogin{User: username, IP: ip, When: secWhen(at)}
}

// dropAnonymousUsers removes the anonymous MySQL users
// (SecurityController::dropAnonymousUsers, security.mysql_anonymous —
// server.manage).
func (s *Server) dropAnonymousUsers(w http.ResponseWriter, r *http.Request) {
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
	to := "/security"
	res, err := s.agent.Call(r.Context(), "db.drop_anonymous_users", nil, 60*time.Second)
	if err != nil {
		s.agentErrorRedirect(w, r, "db drop anonymous users", err, to)
		return
	}
	removed := []string{}
	var out struct {
		Removed []string `json:"removed"`
	}
	if json.Unmarshal(res, &out) == nil {
		removed = out.Removed
	}
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "db.drop_anonymous_users",
		TargetType: "server",
		TargetID:   "mysql",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"hosts": removed},
	})
	msg := "There were no anonymous users."
	if len(removed) > 0 {
		msg = "Removed the anonymous MySQL users (" + strings.Join(removed, ", ") + ")."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// sshProtection reads fail2ban's state: "on" when it runs with an sshd jail,
// "off" when it does not, unchanged when the agent cannot say.
func (s *Server) sshProtection(ctx context.Context, out *string) {
	res, err := s.agent.Call(ctx, "fail2ban.status", nil)
	if err != nil {
		return
	}
	var f2b struct {
		Running bool `json:"running"`
		Jails   []struct {
			Name string `json:"name"`
		} `json:"jails"`
	}
	if json.Unmarshal(res, &f2b) != nil || len(agentObject(res)) == 0 {
		return
	}
	jail := false
	for _, j := range f2b.Jails {
		jail = j.Name == "sshd" || jail
	}
	if f2b.Running && jail {
		*out = "on"
	} else {
		*out = "off"
	}
}
