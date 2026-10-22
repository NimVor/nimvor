package http

// This file ports the brute-force protection page (SecurityController's
// fail2ban area: the jails, the banned addresses with unban, the
// protection-settings form) and the overview's fix action
// (POST /security/fix — the server-wide security fixes with their undo).

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// fail2banJailCatalog is the view's $jails (the WhaCP jail file's jails).
var fail2banJailCatalog = []struct{ Key, Label string }{
	{"sshd", "SSH login failures"},
	{"recidive", "Repeat offenders (bans for a week)"},
	{"nginx-http-auth", "Nginx basic-auth failures"},
	{"nginx-botsearch", "Scanners probing for common exploits"},
	{"nginx-limit-req", "Clients exceeding Nginx rate limits"},
}

var (
	f2bBanRe    = regexp.MustCompile(`bantime = (\d+)m`)
	f2bFindRe   = regexp.MustCompile(`findtime = (\d+)m`)
	f2bRetryRe  = regexp.MustCompile(`maxretry = (\d+)`)
	f2bIgnoreRe = regexp.MustCompile(`ignoreip = 127\.0\.0\.1/8 ::1 ?(.*)`)
	f2bJailRe   = regexp.MustCompile(`(?m)^\[([a-z0-9-]+)\]\nenabled = true`)
)

// fail2banJailRow is one jail of the Jails table.
type fail2banJailRow struct {
	Name    string
	Label   string
	Banned  int
	HasBanned bool
	Total   int
	Failed  int
}

// fail2banBannedRow is one row of the banned-addresses table.
type fail2banBannedRow struct {
	IP   string
	Jail string
}

// fail2banIndex renders the fail2ban page (SecurityController::fail2ban).
func (s *Server) fail2banIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	var st struct {
		Installed bool     `json:"installed"`
		Running   bool     `json:"running"`
		Managed   string   `json:"managed_config"`
		Jails     []struct {
			Name        string   `json:"name"`
			Banned      int      `json:"currently_banned"`
			TotalBanned int      `json:"total_banned"`
			Failed      int      `json:"currently_failed"`
			BannedIPs   []string `json:"banned_ips"`
		} `json:"jails"`
	}
	agentMsg := ""
	var ae *agent.Error
	if res, err := s.agent.Call(r.Context(), "fail2ban.status", nil); err != nil {
		if errors.As(err, &ae) && ae.Message != "" {
			agentMsg = ae.Message
		} else {
			s.log.Error("fail2ban status", "error", err)
			agentMsg = "The WhaCP agent could not be reached."
		}
	} else if err := json.Unmarshal(res, &st); err != nil {
		s.log.Error("fail2ban status", "error", err)
		agentMsg = "The WhaCP agent answered with an unreadable state."
	}

	ok, _ := s.authorizer().Can(r.Context(), user, "security.manage")
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"F2b":        nil,
		"CanManage":  ok,
		"AgentError": agentMsg,
	}
	if agentMsg == "" {
		// The managed jail file's settings (the view's regex defaults).
		ban, find, retry, ignore := "60", "10", "5", ""
		enabled := []string{"sshd", "recidive"}
		if m := f2bBanRe.FindStringSubmatch(st.Managed); m != nil {
			ban = m[1]
		}
		if m := f2bFindRe.FindStringSubmatch(st.Managed); m != nil {
			find = m[1]
		}
		if m := f2bRetryRe.FindStringSubmatch(st.Managed); m != nil {
			retry = m[1]
		}
		if m := f2bIgnoreRe.FindStringSubmatch(st.Managed); m != nil {
			ignore = strings.TrimSpace(m[1])
		}
		if st.Managed != "" {
			enabled = []string{}
			for _, m := range f2bJailRe.FindAllStringSubmatch(st.Managed, -1) {
				enabled = append(enabled, m[1])
			}
		}
		enabledSet := map[string]bool{}
		for _, e := range enabled {
			enabledSet[e] = true
		}
		labels := map[string]string{}
		for _, j := range fail2banJailCatalog {
			labels[j.Key] = j.Label
		}
		rows := make([]fail2banJailRow, 0, len(st.Jails))
		banned := []fail2banBannedRow{}
		bannedNow, ipCount := 0, 0
		for _, j := range st.Jails {
			row := fail2banJailRow{
				Name: j.Name, Label: labels[j.Name],
				Banned: j.Banned, HasBanned: j.Banned > 0,
				Total: j.TotalBanned, Failed: j.Failed,
			}
			bannedNow += j.Banned
			ipCount += len(j.BannedIPs)
			for _, ip := range j.BannedIPs {
				banned = append(banned, fail2banBannedRow{IP: ip, Jail: j.Name})
			}
			rows = append(rows, row)
		}
		jails := []fail2banJailView{}
		for _, j := range fail2banJailCatalog {
			jails = append(jails, fail2banJailView{Key: j.Key, Label: j.Label, Checked: enabledSet[j.Key]})
		}
		data["F2b"] = &fail2banView{
			Installed: st.Installed,
			Running:   st.Running,
			Jails:     rows,
			Banned:    banned,
			BannedNow: bannedNow,
			IPCount:   ipCount,
			JailCount: len(st.Jails),
			BanTime:   ban,
			FindTime:  find,
			MaxRetry:  retry,
			Ignore:    ignore,
			Options:   jails,
		}
	}
	data["Pending"] = s.securityPending(r.Context())
	data["Job"], _ = sess.PullFlash("job")
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	s.render(w, "security_fail2ban", data)
}

// fail2banJailView is one protection-settings checkbox.
type fail2banJailView struct {
	Key     string
	Label   string
	Checked bool
}

// fail2banView is the page's data (the view's computed values).
type fail2banView struct {
	Installed bool
	Running   bool
	Jails     []fail2banJailRow
	Banned    []fail2banBannedRow
	BannedNow int
	IPCount   int
	JailCount int
	BanTime   string
	FindTime  string
	MaxRetry  string
	Ignore    string
	Options   []fail2banJailView
}

// agentErrorRedirect flashes the agent's message (an agent error on a
// change flashes the redirect — the Go adaptation of the PHP page's 500).
func agentErrorRedirect(w http.ResponseWriter, r *http.Request, action string, err error, to string) {
	sess := sessionFrom(r.Context())
	var ae *agent.Error
	msg := "The WhaCP agent could not be reached."
	if errors.As(err, &ae) && ae.Message != "" {
		msg = ae.Message
	}
	s.log.Error(action, "error", err)
	sess.Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// fail2banConfigure tests and applies the protection settings
// (SecurityController::configureFail2ban → SecurityService::configureFail2ban).
func (s *Server) fail2banConfigure(w http.ResponseWriter, r *http.Request) {
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
	// PHP: (int)($input['ban_minutes'] ?? 60) — an empty field is the
	// default, a non-numeric one is 0 (the agent's range check refuses it).
	intOr := func(v string, def int) int {
		if v == "" {
			return def
		}
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		return 0
	}
	jails := r.Form["jails[]"]
	if jails == nil {
		jails = []string{}
	}
	ignore := []string{}
	for _, e := range strings.FieldsFunc(r.FormValue("ignore"), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ','
	}) {
		if e != "" {
			ignore = append(ignore, e)
		}
	}
	cfg := map[string]any{
		"jails":        jails,
		"ban_minutes":  intOr(r.FormValue("ban_minutes"), 60),
		"find_minutes": intOr(r.FormValue("find_minutes"), 10),
		"max_retry":    intOr(r.FormValue("max_retry"), 5),
		"ignore":       ignore,
	}
	if _, err := s.agent.Call(r.Context(), "fail2ban.configure", cfg, 60*time.Second); err != nil {
		agentErrorRedirect(w, r, "fail2ban configure", err, "/security/fail2ban")
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "fail2ban.configure",
		TargetType: "server",
		TargetID:   "fail2ban",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    cfg,
	})
	sess.Flash("success", "fail2ban settings tested and applied.")
	http.Redirect(w, r, "/security/fail2ban", http.StatusSeeOther)
}

// fail2banUnban lifts one ban (SecurityController::unban).
func (s *Server) fail2banUnban(w http.ResponseWriter, r *http.Request) {
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
	jail := r.FormValue("jail")
	ip := r.FormValue("ip")
	if net.ParseIP(ip) == nil {
		sess.Flash("error", "That is not an IP address.")
		http.Redirect(w, r, "/security/fail2ban", http.StatusSeeOther)
		return
	}
	if _, err := s.agent.Call(r.Context(), "fail2ban.unban", map[string]any{"jail": jail, "ip": ip}); err != nil {
		agentErrorRedirect(w, r, "fail2ban unban", err, "/security/fail2ban")
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &user.ID,
		Username:   user.Username,
		Action:     "fail2ban.unban",
		TargetType: "server",
		TargetID:   "fail2ban",
		Result:     audit.Success,
		IP:         clientIP(r),
		UserAgent:  r.UserAgent(),
		Details:    map[string]any{"jail": jail, "ip": ip},
	})
	sess.Flash("success", "Address unbanned.")
	http.Redirect(w, r, "/security/fail2ban", http.StatusSeeOther)
}

// securityFixes is the FIXES catalog (SecurityController::FIXES): which
// check accepts which op (database_network has no undo).
var securityFixes = map[string]map[string]bool{
	"kernel_settings":  {"apply": true, "undo": true},
	"proc_hidepid":     {"apply": true, "undo": true},
	"auto_updates":     {"apply": true, "undo": true},
	"host_persistence": {"apply": true, "undo": true},
	"database_network": {"apply": true},
}

// securityFix applies (or undoes) one server-wide security fix
// (SecurityController::fix, security.fix — server.manage, role admin).
func (s *Server) securityFix(w http.ResponseWriter, r *http.Request) {
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
	to := "/security" // the overview's port is a later batch
	fail := func(msg string) {
		sess.Flash("error", msg)
		http.Redirect(w, r, to, http.StatusSeeOther)
	}
	if user.RoleName != "admin" {
		fail("Only administrators change server-wide security settings.")
		return
	}
	check, op := r.FormValue("check"), r.FormValue("op")
	if !securityFixes[check][op] {
		fail("Unknown fix.")
		return
	}
	undo := op == "undo"
	record := func(details map[string]any) {
		if details == nil {
			details = map[string]any{}
		}
		details["check"] = check
		details["op"] = op
		s.audit.Record(r.Context(), audit.Entry{
			UserID:     &user.ID,
			Username:   user.Username,
			Action:     "security.fix",
			TargetType: "server",
			TargetID:   check,
			Result:     audit.Success,
			IP:         clientIP(r),
			UserAgent:  r.UserAgent(),
			Details:    details,
		})
	}
	agentFail := func(err error) {
		var ae *agent.Error
		msg := "The WhaCP agent could not be reached."
		if errors.As(err, &ae) && ae.Message != "" {
			msg = ae.Message
		}
		s.log.Error("security fix", "check", check, "error", err)
		sess.Flash("error", msg)
	}
	switch check {
	case "kernel_settings":
		res, err := s.agent.Call(r.Context(), "security.fix_kernel", map[string]any{"undo": undo}, 60*time.Second)
		if err != nil {
			agentFail(err)
			return
		}
		var kr struct {
			Changed []string `json:"changed"`
		}
		if err := json.Unmarshal(res, &kr); err != nil {
			kr = struct {
				Changed []string `json:"changed"`
			}{}
		}
		record(map[string]any{"changed": kr.Changed})
		msg := "The earlier kernel settings are back."
		if !undo {
			msg = "Recommended kernel settings applied"
			if len(kr.Changed) > 0 {
				msg += ": " + strings.Join(kr.Changed, ", ")
			}
			msg += "."
		}
		sess.Flash("success", msg)
	case "proc_hidepid":
		if _, err := s.agent.Call(r.Context(), "security.hide_processes", map[string]any{"enabled": !undo}, 60*time.Second); err != nil {
			agentFail(err)
			return
		}
		record(nil)
		if undo {
			sess.Flash("success", "Every account sees all processes again.")
		} else {
			sess.Flash("success", "Each account now sees only its own processes.")
		}
	case "auto_updates":
		res, err := s.agent.Call(r.Context(), "security.auto_updates", map[string]any{"enabled": !undo})
		if err != nil {
			agentFail(err)
			return
		}
		var au struct {
			JobID string `json:"job_id"`
		}
		if err := json.Unmarshal(res, &au); err != nil {
			agentFail(err)
			return
		}
		kind := "Turn on automatic security updates"
		if undo {
			kind = "Turn off automatic updates"
		}
		serverID := s.currentServerID(r)
		if err := hosting.NewJobTracker(s.db).Track(r.Context(), serverID, au.JobID, kind, "server", strconv.FormatInt(serverID, 10), &user.ID); err != nil {
			s.log.Error("track job", "job", au.JobID, "error", err)
		}
		sess.Flash("job", au.JobID)
		record(nil)
		if undo {
			sess.Flash("success", "Turning off automatic updates…")
		} else {
			sess.Flash("success", "Turning on automatic security updates…")
		}
	case "host_persistence":
		res, err := s.agent.Call(r.Context(), "security.host_known", map[string]any{"forget": undo})
		if err != nil {
			agentFail(err)
			return
		}
		var hk struct {
			Marked []string `json:"marked"`
		}
		if err := json.Unmarshal(res, &hk); err != nil {
			hk = struct {
				Marked []string `json:"marked"`
			}{}
		}
		record(map[string]any{"files": hk.Marked})
		if undo {
			sess.Flash("success", "The files marked as known are reported again.")
		} else {
			sess.Flash("success", "Marked as known: "+strings.Join(hk.Marked, ", ")+".")
		}
	case "database_network":
		rows, err := s.db.Query(`SELECT username, host FROM database_users WHERE server_id = ? AND host NOT IN ('localhost', '127.0.0.1', '::1') ORDER BY username`, s.currentServerID(r))
		if err != nil {
			s.log.Error("remote database users", "error", err)
			fail("The database users could not be listed.")
			return
		}
		var remote []string
		for rows.Next() {
			var u, h string
			if err := rows.Scan(&u, &h); err == nil {
				remote = append(remote, u+"@"+h)
			}
		}
		rows.Close()
		if len(remote) > 0 {
			fail("These database users connect from other hosts: " + strings.Join(remote, ", ") +
				". Remove them (or change their host to localhost) first, or keep remote access.")
			return
		}
		if _, err := s.agent.Call(r.Context(), "db.server_settings", map[string]any{
			"settings": map[string]any{"bind_address": "127.0.0.1"},
			"restart":  true,
		}, 200*time.Second); err != nil {
			agentFail(err)
			return
		}
		record(nil)
		sess.Flash("success", "The database server now accepts connections from this server only.")
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}
