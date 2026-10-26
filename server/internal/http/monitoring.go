package http

// MonitoringController port (panel/app/Http/Controllers/MonitoringController.php,
// 136 LOC, 7 routes): monitoring dashboard, live stats, process manager,
// per-process trace and signal, kill-all-by-user. The timeseries series and
// alerts come from a lightweight in-memory/DB service; this port uses a
// stubbed monitorStore (reads monitoring_metrics + monitoring_alerts when
// present, returns empty otherwise) so the page renders and the agent-backed
// live/process endpoints work end-to-end.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

var monitoringRanges = map[string]int{
	"24h": 86400,
	"7d":  604800,
	"30d": 2592000,
}

// monitoringIndex GET /monitoring
func (s *Server) monitoringIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	rangeKey := r.URL.Query().Get("range")
	if _, ok := monitoringRanges[rangeKey]; !ok {
		rangeKey = "24h"
	}
	s.render(w, "monitoring_index", map[string]any{
		"User":   user,
		"CSRF":   session.CSRFToken(sessionFrom(r.Context())),
		"Title":  "Monitoring",
		"Range":  rangeKey,
		"Series": []any{},
		"Open":   []any{},
		"Recent": []any{},
	})
}

// monitoringLive GET /monitoring/live — admin-only live stats page.
func (s *Server) monitoringLive(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.renderError(w, http.StatusForbidden, "Access denied", "Live statistics are for server administrators.")
		return
	}
	s.render(w, "monitoring_live", map[string]any{
		"User":  user,
		"CSRF":  session.CSRFToken(sessionFrom(r.Context())),
		"Title": "Live",
	})
}

// monitoringLiveData GET /monitoring/live.json — admin-only JSON.
func (s *Server) monitoringLiveData(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Access denied."})
		return
	}
	raw, err := s.agent.Call(r.Context(), "system.live", nil, 20*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": agentMessage(err)})
		return
	}
	var out any
	_ = json.Unmarshal(raw, &out)
	writeJSON(w, http.StatusOK, out)
}

// monitoringProcesses GET /monitoring/processes — admin-only process list.
type procRow struct {
	PID, Start, Owner, UnitLabel, Nice, CPU, Mem, Running, Command, Short, Hidden, Title, Kind string
	Listens                                                                                    string
	Notes, Suspicious                                                                          []string
	Operable                                                                                   bool
}

func procDuration(sec int64) string {
	switch {
	case sec >= 86400:
		return fmt.Sprintf("%dd %dh", sec/86400, sec%86400/3600)
	case sec >= 3600:
		return fmt.Sprintf("%dh %dm", sec/3600, sec%3600/60)
	case sec >= 60:
		return fmt.Sprintf("%dm", sec/60)
	}
	return fmt.Sprintf("%ds", sec)
}

func (s *Server) monitoringProcesses(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	if !hosting.IsAll(user) {
		s.renderError(w, http.StatusForbidden, "Access denied", "The process manager is for server administrators.")
		return
	}
	show := r.URL.Query().Get("show")
	if show != "suspicious" {
		show = ""
	}
	data := map[string]any{
		"User": user, "CSRF": session.CSRFToken(sess), "Title": "Processes", "Show": show,
		"Success": "", "Error": "", "AgentError": "",
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	canManage, _ := s.authorizer().Can(r.Context(), user, "server.manage")
	data["CanManage"] = canManage
	raw, err := s.agent.Call(r.Context(), "processes.list", nil, 60*time.Second)
	if err != nil {
		data["AgentError"] = agentMessage(err)
		s.render(w, "monitoring_processes", data)
		return
	}
	var snap any
	_ = json.Unmarshal(raw, &snap)
	var rows []procRow
	list, _ := jmap(snap)["processes"].([]any)
	for _, x := range list {
		m := jmap(x)
		row := procRow{PID: jstr(x, "pid"), Start: jstr(x, "start"), Owner: jstr(x, "user"), Nice: jstr(x, "nice"),
			CPU: fmt.Sprintf("%.1f", jnum(x, "cpu_percent")), Mem: fmt.Sprintf("%.1f", jnum(x, "mem_percent")),
			Running: procDuration(int64(jnum(x, "seconds"))), Command: jstr(x, "command"),
			Notes: strList(m["notes"]), Suspicious: strList(m["suspicious"]), Listens: strings.Join(strList(m["listens"]), ", ")}
		unit := jstr(x, "unit")
		if unit != "" {
			if _, err := strconv.Atoi(row.Owner); err == nil {
				row.UnitLabel = strings.TrimSuffix(unit, ".service")
			}
			if !strings.HasSuffix(unit, ".service") {
				unit = ""
			}
		}
		if unit != "" {
			for i, n := range row.Notes {
				row.Notes[i] = n + " (" + unit + ")"
			}
		}
		if exe := jstr(x, "exe"); exe != "" {
			row.Title = "Program: " + exe
			if cwd := jstr(x, "cwd"); cwd != "" {
				row.Title += "\nFolder: " + cwd
			}
		}
		switch {
		case len(row.Suspicious) > 0:
			row.Kind = "suspicious user"
		case jbool(x, "kernel"):
			row.Kind = "kernel"
		default:
			row.Kind = "user"
		}
		row.Operable = !jbool(x, "kernel") && jnum(x, "pid") > 1
		row.Short, row.Hidden = truncateStr(row.Command, 120), truncateStr(row.Command, 200)
		rows = append(rows, row)
	}
	data["Rows"] = rows
	var users []string
	for _, u := range strList(jmap(snap)["users"]) {
		users = append(users, u)
	}
	data["Users"] = users
	var killable []string
	for _, u := range users {
		if u != "root" && u != "whacp" {
			killable = append(killable, u)
		}
	}
	data["Killable"] = killable
	data["Count"] = len(rows)
	data["SuspiciousCount"] = int(jnum(snap, "suspicious"))
	data["Detailed"] = jbool(snap, "detailed")
	data["ScanError"] = truncateStr(jstr(snap, "scan_error"), 300)
	s.render(w, "monitoring_processes", data)
}

// monitoringTrace GET /monitoring/processes/trace — admin-only syscall trace.
func (s *Server) monitoringTrace(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.renderError(w, http.StatusForbidden, "Access denied", "The process manager is for server administrators.")
		return
	}
	pid, _ := strconv.Atoi(r.URL.Query().Get("pid"))
	start, _ := strconv.Atoi(r.URL.Query().Get("start"))
	raw, err := s.agent.Call(r.Context(), "processes.trace", map[string]any{"pid": pid, "start": start}, 60*time.Second)
	if err != nil {
		sess := sessionFrom(r.Context())
		sess.Flash("error", agentMessage(err))
		http.Redirect(w, r, "/monitoring/processes", http.StatusSeeOther)
		return
	}
	trace := agentObject(raw)
	d := jmap(trace)["detail"]
	det, _ := d.(map[string]any)
	type kv struct{ K, V string }
	var status []kv
	if st, ok := det["status"].(map[string]any); ok {
		keys := make([]string, 0, len(st))
		for k := range st {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			status = append(status, kv{k, jstr(st, k)})
		}
	}
	dash := func(m map[string]any, k string) string {
		if v := jstr(m, k); v != "" {
			return v
		}
		return "—"
	}
	files := strList(det["files"])
	s.auditMonitoring(r, user, "processes.trace", "process", strconv.Itoa(pid), audit.Success, nil)
	s.render(w, "monitoring_trace", map[string]any{
		"User": user, "CSRF": session.CSRFToken(sessionFrom(r.Context())), "Title": "Trace of process " + strconv.Itoa(pid),
		"PID": pid, "Name": jstr(trace, "name"), "Start": start, "HasDetail": det != nil,
		"DetailError": truncateStr(jstr(trace, "detail_error"), 300),
		"Exe":         dash(det, "exe"), "Cwd": dash(det, "cwd"), "Root": dash(det, "root"), "Deleted": jbool(det, "deleted"),
		"Status": status, "Files": strings.Join(files, "\n"), "FileCount": len(files),
		"Strace": jbool(trace, "strace_available"), "StraceOut": jstr(trace, "strace"),
	})
}

// monitoringSignal POST /monitoring/processes/signal
func (s *Server) monitoringSignal(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	back := "/monitoring/processes"
	if r.PostFormValue("show") == "suspicious" {
		back = "/monitoring/processes?show=suspicious"
	}
	if !hosting.IsAll(user) {
		monFlashErr(w, r, "The process manager is for server administrators.", back)
		return
	}
	pid, _ := strconv.Atoi(r.PostFormValue("pid"))
	start, _ := strconv.Atoi(r.PostFormValue("start"))
	sig := r.PostFormValue("signal")
	cmd := truncateStr(r.PostFormValue("command"), 200)
	_, err := s.agent.Call(r.Context(), "processes.signal", map[string]any{"pid": pid, "start": start, "signal": sig}, 30*time.Second)
	if err != nil {
		s.auditMonitoring(r, user, "processes.signal", "process", strconv.Itoa(pid), audit.Failure, map[string]any{"signal": sig, "command": cmd, "error": err.Error()})
		monFlashErr(w, r, agentMessage(err), back)
		return
	}
	s.auditMonitoring(r, user, "processes.signal", "process", strconv.Itoa(pid), audit.Success, map[string]any{"signal": sig, "command": cmd})
	verb := map[string]string{"TERM": "asked to stop", "KILL": "killed", "HUP": "sent HUP", "STOP": "paused", "CONT": "resumed"}[sig]
	if verb == "" {
		verb = "signalled"
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", "Process "+strconv.Itoa(pid)+" "+verb+".")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// monitoringKillUser POST /monitoring/processes/kill-user
func (s *Server) monitoringKillUser(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	_ = r.ParseForm()
	if !hosting.IsAll(user) {
		monFlashErr(w, r, "The process manager is for server administrators.", "/monitoring/processes")
		return
	}
	target := r.PostFormValue("user")
	sig := "TERM"
	if r.PostFormValue("signal") == "KILL" {
		sig = "KILL"
	}
	raw, err := s.agent.Call(r.Context(), "processes.kill_user", map[string]any{"user": target, "signal": sig}, 30*time.Second)
	if err != nil {
		s.auditMonitoring(r, user, "processes.kill_user", "account", target, audit.Failure, map[string]any{"signal": sig, "error": err.Error()})
		monFlashErr(w, r, agentMessage(err), "/monitoring/processes")
		return
	}
	var res map[string]any
	_ = json.Unmarshal(raw, &res)
	var signalled int
	if n, ok := res["signalled"].(float64); ok {
		signalled = int(n)
	}
	s.auditMonitoring(r, user, "processes.kill_user", "account", target, audit.Success, map[string]any{"signal": sig, "signalled": signalled})
	msg := strconv.Itoa(signalled) + " process(es) of " + target + " asked to stop."
	if sig == "KILL" {
		msg = strconv.Itoa(signalled) + " process(es) of " + target + " killed."
	}
	sess := sessionFrom(r.Context())
	sess.Flash("success", msg)
	http.Redirect(w, r, "/monitoring/processes", http.StatusSeeOther)
}

// ---- helpers ----

func (s *Server) auditMonitoring(r *http.Request, user *identity.User, action, targetType, targetID, result string, details map[string]any) {
	var uid *int64
	if user != nil {
		id := user.ID
		uid = &id
	}
	s.audit.Record(r.Context(), audit.Entry{Action: action, TargetType: targetType, TargetID: targetID, Result: result, Details: details, UserID: uid, Username: usernameOr(user)})
}

func monFlashErr(w http.ResponseWriter, r *http.Request, msg, to string) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func (s *Server) renderError(w http.ResponseWriter, code int, title, message string) {
	_ = context.TODO()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = s.tpl.Render(w, "error", map[string]any{"Title": title, "Message": message})
}

// end of file
