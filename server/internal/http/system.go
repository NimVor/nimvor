package http

// SystemController port (panel/app/Http/Controllers/SystemController.php,
// 305 LOC, 14 routes): package management plus server settings (hostname,
// root password, time zone, graphical console, scheduled reboot). The GET
// endpoints render minimal templates; POST endpoints follow the same
// pattern as memcached/ftp installs — fire an agent job (or a synchronous
// call), audit, flash success/error, redirect.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// systemPackages renders GET /packages — components list, updates count,
// broken-package health. The XHR client (data-wcp-pkg-list) receives JSON.
type pkgRow struct {
	Name, Description, Version, Reason, State string
	Installed, Unavailable, Other, Broken     bool
	Kind                                      string // installed | broken | present-broken | unavailable | missing
	ModsecLink                                bool
}

type pkgUpdate struct {
	Name, Current, New string
	Security           bool
}

type pkgProblem struct {
	Package, Kind, Detail string
	Tried                 bool
}

func (s *Server) systemPackages(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := userFrom(ctx)
	sess := sessionFrom(ctx)
	data := map[string]any{
		"User": user, "CSRF": session.CSRFToken(sess), "Title": "Packages", "AgentError": "",
		"Success": pullFlashKey(sess, "success"), "Error": pullFlashKey(sess, "error"),
	}
	var list []any
	if raw, err := s.agent.Call(ctx, "packages.list", nil, 60*time.Second); err != nil {
		data["AgentError"] = agentMessage(err)
	} else if json.Unmarshal(raw, &list) != nil {
		var env struct {
			Data []any `json:"data"`
		}
		if json.Unmarshal(raw, &env) == nil {
			list = env.Data
		}
	}
	// XHR / JSON client expects {success,packages} like PHP.
	if wantsJSON(r) {
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "packages": list})
		return
	}
	var rows []pkgRow
	stats := map[string]int{"installed": 0, "pending": 0, "failed": 0}
	unavailable := 0
	for _, x := range list {
		row := pkgRow{Name: jstr(x, "name"), Description: jstr(x, "description"), Version: jstr(x, "version"), Reason: jstr(x, "reason"), State: jstr(x, "state"),
			Installed: jbool(x, "installed"), Unavailable: jbool(x, "unavailable"), Other: jstr(x, "source") == "other"}
		switch {
		case row.Installed && row.State == "installed":
			row.Kind = "broken"
		case row.Installed:
			row.Kind = "installed"
		case row.State == "installed":
			row.Kind = "present-broken"
		case row.Unavailable:
			row.Kind = "unavailable"
			row.ModsecLink = row.Name == "libnginx-mod-http-modsecurity"
		default:
			row.Kind = "missing"
		}
		if !(!row.Installed && row.Unavailable) {
			switch {
			case row.State == "installed":
				stats["failed"]++
			case row.Installed:
				stats["installed"]++
			default:
				stats["pending"]++
			}
		} else {
			unavailable++
		}
		rows = append(rows, row)
	}
	var ups []pkgUpdate
	var upd any
	if raw, err := s.agent.Call(ctx, "packages.updates", nil, 150*time.Second); err == nil {
		_ = json.Unmarshal(raw, &upd)
		l, _ := jmap(upd)["updates"].([]any)
		for _, u := range l {
			ups = append(ups, pkgUpdate{jstr(u, "name"), jstr(u, "current"), jstr(u, "new"), jbool(u, "security")})
		}
	}
	var problems []pkgProblem
	var health any
	if raw, err := s.agent.Call(ctx, "packages.health", nil, 150*time.Second); err == nil {
		_ = json.Unmarshal(raw, &health)
		kinds := map[string]string{"failed_script": "install script failed", "missing_dependency": "missing dependency", "duplicate": "duplicate version", "half_configured": "not fully installed"}
		pl, _ := jmap(health)["problems"].([]any)
		for _, p := range pl {
			k := jstr(p, "kind")
			if l, ok := kinds[k]; ok {
				k = l
			}
			problems = append(problems, pkgProblem{jstr(p, "package"), k, jstr(p, "detail"), jbool(p, "tried")})
		}
	}
	idx := jstr(upd, "index_refreshed")
	if len(idx) > 10 {
		idx = idx[:10]
	}
	data["Rows"], data["Stats"], data["Unavailable"], data["Updates"], data["Problems"] = rows, stats, unavailable, ups, problems
	data["HasUpdates"], data["UpdateCount"], data["SecurityCount"] = upd != nil, int(jnum(upd, "count")), int(jnum(upd, "security"))
	data["IndexDate"], data["IndexStale"] = idx, jbool(upd, "index_stale")
	data["Repairable"], data["Duplicates"] = jbool(health, "repairable"), jbool(health, "duplicates")
	data["ComponentCount"] = len(rows)
	s.render(w, "server_packages", data)
}

// systemInstall POST /packages/install — queue a component install job.
func (s *Server) systemInstall(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	name := r.PostFormValue("name")
	if name == "" {
		systemFlashErr(w, r, "A component name is required.", "/packages")
		return
	}
	if wantsJSON(r) {
		res, err := s.agent.Call(r.Context(), "packages.install", map[string]any{"name": name}, 60*time.Second)
		if err != nil {
			code := http.StatusServiceUnavailable
			var ae *agent.Error
			if errors.As(err, &ae) && (ae.Code == "invalid_params" || ae.Code == "conflict") {
				code = http.StatusUnprocessableEntity
			}
			writeJSON(w, code, map[string]any{"success": false, "error": agentErrMap(err)})
			return
		}
		var env struct {
			JobID string `json:"job_id"`
		}
		_ = json.Unmarshal(res, &env)
		if env.JobID == "" {
			// Unwrap {success,data,job_id}.
			var full map[string]any
			_ = json.Unmarshal(res, &full)
			if jid, ok := full["job_id"].(string); ok {
				env.JobID = jid
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "name": name, "job_id": env.JobID})
		return
	}
	job := s.systemJob(r, user, "packages.install", map[string]any{"name": name}, "Install "+name)
	systemFlashJob(w, r, "Installing "+name+"…", job, "/packages")
}

// systemRefresh POST /packages/refresh — refresh the package index.
func (s *Server) systemRefresh(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	job := s.systemJob(r, user, "packages.refresh", nil, "Refresh package index")
	systemFlashJob(w, r, "Refreshing the package index…", job, "/packages")
}

// systemUpgrade POST /packages/upgrade — install pending updates.
func (s *Server) systemUpgrade(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	job := s.systemJob(r, user, "packages.upgrade", nil, "Install updates")
	systemFlashJob(w, r, "Installing updates…", job, "/packages")
}

// systemRepair POST /packages/repair — repair broken packages (optionally
// remove duplicates).
func (s *Server) systemRepair(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	dups := r.PostFormValue("remove_duplicates") == "1"
	label := "Repair packages"
	if dups {
		label = "Remove duplicate packages"
	}
	job := s.systemJob(r, user, "packages.repair", map[string]any{"remove_duplicates": dups}, label)
	msg := "Repairing packages…"
	if dups {
		msg = "Removing duplicate packages and repairing…"
	}
	systemFlashJob(w, r, msg, job, "/packages")
}

// systemHostname POST /server/hostname — set host name; keep mail in sync.
func (s *Server) systemHostname(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	h := strings.TrimSpace(strings.ToLower(r.PostFormValue("hostname")))
	if h == "" {
		systemFlashErr(w, r, "A host name is required.", "/server#settings")
		return
	}
	ip := s.systemServerIP(r)
	params := map[string]any{"hostname": h}
	if ip != "" {
		params["ip"] = ip
	}
	if _, err := s.agent.Call(r.Context(), "server.set_hostname", params, 60*time.Second); err != nil {
		systemFlashErr(w, r, agentMessage(err), "/server#settings")
		s.systemAudit(r, user, "server.hostname", "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Failure, map[string]any{"hostname": h, "error": err.Error()})
		return
	}
	// Keep mail server HELO/greeting in step, best-effort.
	mailMsg := ""
	if mailRaw, err := s.agent.Call(r.Context(), "mail.status", nil, 30*time.Second); err == nil {
		var mail map[string]any
		if json.Unmarshal(mailRaw, &mail) == nil {
			if configured, _ := mail["configured"].(bool); configured {
				if _, err := s.agent.Call(r.Context(), "mail.configure", map[string]any{"hostname": h, "confirm_edit_package_config": true}, 180*time.Second); err == nil {
					mailMsg = " The mail server was reconfigured to use it."
					s.systemAudit(r, user, "mail.configure", "server", "mail", audit.Success, map[string]any{"hostname": h, "reason": "hostname change"})
				} else {
					mailMsg = " The mail server could not be updated automatically — reapply it under Email if you host mail."
				}
			}
		}
	}
	s.systemAudit(r, user, "server.hostname", "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Success, map[string]any{"hostname": h, "ip": ip})
	sess := sessionFrom(r.Context())
	msg := "Host name changed to " + h + "."
	if ip != "" {
		msg += " /etc/hosts now resolves it to " + ip + "."
	}
	msg += mailMsg
	sess.Flash("success", msg)
	http.Redirect(w, r, "/server#settings", http.StatusSeeOther)
}

// systemHostnameReferences GET /server/hostname-references — find where an
// old host name still appears in managed configuration.
func (s *Server) systemHostnameReferences(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	old := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("old")))
	data := map[string]any{
		"User":            user,
		"CSRF":            session.CSRFToken(sessionFrom(r.Context())),
		"Title":           "Host name references",
		"Old":             old,
		"Report":          nil,
		"Error":           "",
		"WebmailStale":    false,
		"WebmailHostname": "",
	}
	if old != "" {
		reportRaw, err := s.agent.Call(r.Context(), "server.hostname_references", map[string]any{"old": old}, 60*time.Second)
		if err != nil {
			data["Error"] = agentMessage(err)
		} else {
			var report any
			if json.Unmarshal(reportRaw, &report) == nil {
				data["Report"] = report
			}
		}
		if mailRaw, err := s.agent.Call(r.Context(), "mail.status", nil, 30*time.Second); err == nil {
			var mail map[string]any
			if json.Unmarshal(mailRaw, &mail) == nil {
				if wm, ok := mail["webmail"].(map[string]any); ok {
					if enabled, _ := wm["enabled"].(bool); enabled {
						hostname, _ := wm["hostname"].(string)
						data["WebmailHostname"] = hostname
						data["WebmailStale"] = strings.ToLower(hostname) == old
					}
				}
			}
		}
	}
	s.render(w, "server_hostname_references", data)
}

// systemHostnameWebmail POST /server/hostname-references/webmail —
// re-apply webmail for the current hostname.
func (s *Server) systemHostnameWebmail(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	h := s.systemCurrentHostname(r)
	if h == "" {
		systemFlashErr(w, r, "The server host name could not be read.", "/server/hostname-references")
		return
	}
	mailRaw, err := s.agent.Call(r.Context(), "mail.status", nil, 30*time.Second)
	if err != nil {
		systemFlashErr(w, r, agentMessage(err), "/server/hostname-references")
		return
	}
	var mail map[string]any
	_ = json.Unmarshal(mailRaw, &mail)
	wm, _ := mail["webmail"].(map[string]any)
	if enabled, _ := wm["enabled"].(bool); !enabled {
		sess := sessionFrom(r.Context())
		sess.Flash("success", "Webmail is not turned on; nothing to re-apply.")
		http.Redirect(w, r, "/server/hostname-references", http.StatusSeeOther)
		return
	}
	phpVer, _ := wm["php"].(string)
	_, err = s.agent.Call(r.Context(), "mail.webmail_configure", map[string]any{"enabled": true, "hostname": h, "php_version": phpVer}, 180*time.Second)
	if err != nil {
		systemFlashErr(w, r, agentMessage(err), "/server/hostname-references")
		return
	}
	s.systemAudit(r, user, "mail.webmail_configure", "server", "mail", audit.Success, map[string]any{"hostname": h, "reason": "hostname references cleanup"})
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Webmail now answers on "+h+" and uses its Let's Encrypt certificate when one exists for that name.")
	http.Redirect(w, r, "/server/hostname-references?old="+h, http.StatusSeeOther)
}

// systemHostnameRemail POST /server/hostname-references/mail — re-apply
// mail for the current hostname.
func (s *Server) systemHostnameRemail(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	h := s.systemCurrentHostname(r)
	if h == "" {
		systemFlashErr(w, r, "The server host name could not be read.", "/server/hostname-references")
		return
	}
	mailRaw, err := s.agent.Call(r.Context(), "mail.status", nil, 30*time.Second)
	if err != nil {
		systemFlashErr(w, r, agentMessage(err), "/server/hostname-references")
		return
	}
	var mail map[string]any
	_ = json.Unmarshal(mailRaw, &mail)
	if configured, _ := mail["configured"].(bool); !configured {
		sess := sessionFrom(r.Context())
		sess.Flash("success", "Email is not set up on this server; nothing to re-apply.")
		http.Redirect(w, r, "/server/hostname-references", http.StatusSeeOther)
		return
	}
	_, err = s.agent.Call(r.Context(), "mail.configure", map[string]any{"hostname": h, "confirm_edit_package_config": true}, 180*time.Second)
	if err != nil {
		systemFlashErr(w, r, agentMessage(err), "/server/hostname-references")
		return
	}
	s.systemAudit(r, user, "mail.configure", "server", "mail", audit.Success, map[string]any{"hostname": h, "reason": "hostname references cleanup"})
	sess := sessionFrom(r.Context())
	sess.Flash("success", "The mail server was re-applied with the host name "+h+". Re-publish each mail domain's DNS so its records point to it.")
	http.Redirect(w, r, "/server/hostname-references?old="+h, http.StatusSeeOther)
}

// systemRootPassword POST /server/root-password — admins only, re-confirm
// their panel password; password policy checked before sending to agent.
func (s *Server) systemRootPassword(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	if user.RoleName != "admin" {
		systemFlashErr(w, r, "Only administrators can change the root password.", "/server#root-password")
		return
	}
	current := r.PostFormValue("current_password")
	newPw := r.PostFormValue("root_password")
	confirm := r.PostFormValue("root_password_confirmation")
	hash, err := s.users.PasswordHash(r.Context(), user.ID)
	if err != nil || hash == "" || !identity.VerifyPassword(hash, current) {
		s.systemAudit(r, user, "server.root_password", "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Denied, map[string]any{"reason": "wrong panel password"})
		systemFlashErr(w, r, "Your panel password is not correct.", "/server#root-password")
		return
	}
	if newPw != confirm {
		systemFlashErr(w, r, "The new passwords do not match.", "/server#root-password")
		return
	}
	if msg := systemRootPasswordProblem(newPw); msg != "" {
		systemFlashErr(w, r, msg, "/server#root-password")
		return
	}
	if _, err := s.agent.Call(r.Context(), "server.root_password", map[string]any{"password": newPw}, 60*time.Second); err != nil {
		s.systemAudit(r, user, "server.root_password", "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Failure, map[string]any{"error": err.Error()})
		systemFlashErr(w, r, agentMessage(err), "/server#root-password")
		return
	}
	s.systemAudit(r, user, "server.root_password", "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Success, nil)
	sess := sessionFrom(r.Context())
	sess.Flash("success", "The root password was changed.")
	http.Redirect(w, r, "/server#settings", http.StatusSeeOther)
}

// systemTimezone POST /server/timezone.
func (s *Server) systemTimezone(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	tz := r.PostFormValue("timezone")
	if tz == "" {
		systemFlashErr(w, r, "A time zone is required.", "/server#settings")
		return
	}
	if _, err := s.agent.Call(r.Context(), "server.set_timezone", map[string]any{"timezone": tz}, 30*time.Second); err != nil {
		systemFlashErr(w, r, agentMessage(err), "/server#settings")
		return
	}
	s.systemAudit(r, user, "server.timezone", "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Success, map[string]any{"timezone": tz})
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Time zone changed. Cron jobs and backup schedules now use "+tz+".")
	http.Redirect(w, r, "/server#settings", http.StatusSeeOther)
}

// systemGraphical POST /server/graphical — toggle the graphical login screen.
func (s *Server) systemGraphical(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	on := r.PostFormValue("enabled") == "1"
	if _, err := s.agent.Call(r.Context(), "server.graphical_set", map[string]any{"enabled": on}, 60*time.Second); err != nil {
		systemFlashErr(w, r, agentMessage(err), "/server#settings")
		return
	}
	s.systemAudit(r, user, "server.graphical", "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Success, map[string]any{"enabled": on})
	sess := sessionFrom(r.Context())
	if on {
		sess.Flash("success", "The graphical login screen is on again.")
	} else {
		sess.Flash("success", "The graphical login screen is off; the server boots to the text console.")
	}
	http.Redirect(w, r, "/server#settings", http.StatusSeeOther)
}

// systemReboot POST /server/reboot — schedule a reboot in N minutes.
func (s *Server) systemReboot(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	minutes := 1
	if n, err := strconv.Atoi(r.PostFormValue("minutes")); err == nil && n >= 1 {
		minutes = n
	}
	confirm := r.PostFormValue("confirm")
	if _, err := s.agent.Call(r.Context(), "server.reboot", map[string]any{"confirm": confirm, "minutes": minutes}, 30*time.Second); err != nil {
		systemFlashErr(w, r, agentMessage(err), "/server#settings")
		return
	}
	s.systemAudit(r, user, "server.reboot", "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Success, map[string]any{"minutes": minutes})
	sess := sessionFrom(r.Context())
	sess.Flash("success", "The server restarts in "+strconv.Itoa(minutes)+" minute(s). Websites are unavailable for a short time.")
	http.Redirect(w, r, "/server#settings", http.StatusSeeOther)
}

// systemCancelReboot POST /server/reboot/cancel.
func (s *Server) systemCancelReboot(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if _, err := s.agent.Call(r.Context(), "server.reboot_cancel", nil, 30*time.Second); err != nil {
		systemFlashErr(w, r, agentMessage(err), "/server#settings")
		return
	}
	s.systemAudit(r, user, "server.reboot_cancel", "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Success, nil)
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Restart cancelled.")
	http.Redirect(w, r, "/server#settings", http.StatusSeeOther)
}

// ---- helpers ----

func (s *Server) systemJob(r *http.Request, user *identity.User, action string, params map[string]any, label string) string {
	res, err := s.agent.Call(r.Context(), action, params, 120*time.Second)
	if err != nil {
		return ""
	}
	var env struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(res, &env)
	if env.JobID == "" {
		var full map[string]any
		_ = json.Unmarshal(res, &full)
		if jid, ok := full["job_id"].(string); ok {
			env.JobID = jid
		} else if data, ok := full["data"].(map[string]any); ok {
			if jid, _ := data["job_id"].(string); jid != "" {
				env.JobID = jid
			}
		}
	}
	if env.JobID != "" {
		uid := user.ID
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), env.JobID, label, "server", strconv.FormatInt(s.currentServerID(r), 10), &uid)
	}
	s.systemAudit(r, user, action, "server", strconv.FormatInt(s.currentServerID(r), 10), audit.Success, params)
	return env.JobID
}

func (s *Server) systemAudit(r *http.Request, user *identity.User, action, targetType, targetID, result string, details map[string]any) {
	var uid *int64
	if user != nil {
		id := user.ID
		uid = &id
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Result:     result,
		Details:    details,
		UserID:     uid,
		Username:   usernameOr(user),
	})
}

func usernameOr(u *identity.User) string {
	if u == nil {
		return ""
	}
	return u.Username
}

func systemFlashJob(w http.ResponseWriter, r *http.Request, msg, job, to string) {
	sess := sessionFrom(r.Context())
	if job != "" {
		sess.Flash("job", job)
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func systemFlashErr(w http.ResponseWriter, r *http.Request, msg, to string) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) systemCurrentHostname(r *http.Request) string {
	raw, err := s.agent.Call(r.Context(), "system.info", nil, 30*time.Second)
	if err != nil {
		return ""
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	// system.info may wrap {success,data:{system:{hostname:...}}}.
	if data, ok := env["data"].(map[string]any); ok {
		if sys, ok := data["system"].(map[string]any); ok {
			if h, ok := sys["hostname"].(string); ok {
				return strings.ToLower(h)
			}
		}
	}
	if sys, ok := env["system"].(map[string]any); ok {
		if h, ok := sys["hostname"].(string); ok {
			return strings.ToLower(h)
		}
	}
	return ""
}

func (s *Server) systemServerIP(r *http.Request) string {
	// system.info returns the server's primary IP under system.ip on recent
	// agents; fall back to empty string when missing.
	raw, err := s.agent.Call(r.Context(), "system.info", nil, 30*time.Second)
	if err != nil {
		return ""
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	read := func(m map[string]any) string {
		if ip, ok := m["ip"].(string); ok {
			return ip
		}
		return ""
	}
	if data, ok := env["data"].(map[string]any); ok {
		if sys, ok := data["system"].(map[string]any); ok {
			if ip := read(sys); ip != "" {
				return ip
			}
		}
	}
	if sys, ok := env["system"].(map[string]any); ok {
		return read(sys)
	}
	return ""
}

func agentErrMap(err error) map[string]any {
	var ae *agent.Error
	if errors.As(err, &ae) {
		return map[string]any{"code": ae.Code, "message": ae.Message}
	}
	return map[string]any{"code": "agent_unreachable", "message": err.Error()}
}

// systemRootPasswordProblem enforces the same rules as PHP: ≥14 chars,
// ≥3 of {lower,upper,digit,symbol}, no colons or control characters.
func systemRootPasswordProblem(pw string) string {
	n := utf8.RuneCountInString(pw)
	cls := 0
	if strings.ContainsAny(pw, "abcdefghijklmnopqrstuvwxyz") {
		cls++
	}
	if strings.ContainsAny(pw, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
		cls++
	}
	if strings.ContainsAny(pw, "0123456789") {
		cls++
	}
	hasSym := false
	for _, r := range pw {
		if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') {
			hasSym = true
			break
		}
	}
	if hasSym {
		cls++
	}
	if n < 14 || cls < 3 {
		return "Use at least 14 characters with three of: lower case, upper case, digits, symbols."
	}
	if strings.Contains(pw, ":") {
		return "The password must not contain colons or control characters."
	}
	for _, r := range pw {
		if r < 0x20 {
			return "The password must not contain colons or control characters."
		}
	}
	return ""
}

// writeJSON lives in server.go (shared helper); system handlers call it for
// JSON responses from e.g. systemPackages and systemInstall.
