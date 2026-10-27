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

type chartLine struct{ Class, Label, Points, Now string }

type chartView struct {
	Empty bool
	Label string
	Lines []chartLine
	Scale string
}

// buildChart renders a series as SVG polylines (monitoring/_chart.php): the
// x axis spreads the points evenly in a 600x160 box, the y axis runs to max or
// 15% above the highest value.
func buildChart(series []hosting.SeriesPoint, get map[string]func(hosting.SeriesPoint) float64, order [][3]string, max float64, format func(float64) string) chartView {
	const w, h = 600.0, 160.0
	names := make([]string, 0, len(order))
	for _, o := range order {
		names = append(names, o[1])
	}
	c := chartView{Label: strings.Join(names, ", "), Empty: len(series) < 2}
	if c.Empty {
		return c
	}
	top := max
	if top == 0 {
		top = 1
		for _, o := range order {
			for _, p := range series {
				if v := get[o[0]](p) * 1.15; v > top {
					top = v
				}
			}
		}
	}
	n := float64(len(series))
	for _, o := range order {
		var pts strings.Builder
		for i, p := range series {
			v := get[o[0]](p)
			if v > top {
				v = top
			}
			fmt.Fprintf(&pts, "%.1f,%.1f ", float64(i)*w/(n-1), h-v/top*h)
		}
		last := series[len(series)-1]
		c.Lines = append(c.Lines, chartLine{Class: o[2], Label: o[1], Points: strings.TrimSpace(pts.String()), Now: format(get[o[0]](last))})
	}
	c.Scale = format(top)
	return c
}

// monitoringIndex GET /monitoring — resource history and alerts
// (MonitoringController::index).
func (s *Server) monitoringIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user := userFrom(ctx)
	rangeKey := r.URL.Query().Get("range")
	if _, ok := monitoringRanges[rangeKey]; !ok {
		rangeKey = "24h"
	}
	svc := hosting.NewMonitoringService(s.db, s.currentServerID(r), s.agent, s.settings, hosting.NewPlanLimits(s.db))
	series := svc.Series(ctx, monitoringRanges[rangeKey], 120)
	pct := func(v float64) string { return fmt.Sprintf("%.0f%%", v) }
	num := func(v float64) string { return fmt.Sprintf("%.2f", v) }
	bps := func(v float64) string { return fmtBytes(v) + "/s" }
	get := map[string]func(hosting.SeriesPoint) float64{
		"cpu": func(p hosting.SeriesPoint) float64 { return p.CPU }, "mem": func(p hosting.SeriesPoint) float64 { return p.Mem },
		"load1": func(p hosting.SeriesPoint) float64 { return p.Load1 }, "disk": func(p hosting.SeriesPoint) float64 { return p.Disk },
		"rx": func(p hosting.SeriesPoint) float64 { return p.RX }, "tx": func(p hosting.SeriesPoint) float64 { return p.TX },
	}
	type alertRow struct{ Severity, Message, Since, Started, Resolved string }
	ago := func(iso string) string {
		t, err := time.Parse(time.RFC3339, iso)
		if err != nil {
			return "—"
		}
		sec := int(time.Since(t).Seconds())
		switch {
		case sec < 0:
			sec = 0
			fallthrough
		case sec < 3600:
			return fmt.Sprintf("%d min ago", sec/60)
		case sec < 86400:
			return fmt.Sprintf("%d h ago", sec/3600)
		}
		return fmt.Sprintf("%d d ago", sec/86400)
	}
	cut := func(v string) string {
		if len(v) > 16 {
			return v[:16]
		}
		return v
	}
	var open, recent []alertRow
	for _, a := range svc.OpenAlerts(ctx) {
		open = append(open, alertRow{Severity: a.Severity, Message: a.Message, Since: ago(a.FirstSeen)})
	}
	for _, a := range svc.RecentAlerts(ctx, 50) {
		recent = append(recent, alertRow{Message: a.Message, Started: cut(a.FirstSeen), Resolved: cut(a.ResolvedAt)})
	}
	s.render(w, "monitoring_index", map[string]any{
		"User": user, "CSRF": session.CSRFToken(sessionFrom(ctx)), "Title": "Monitoring", "Range": rangeKey,
		"Ranges": []string{"24h", "7d", "30d"}, "ServerScope": hosting.IsAll(user), "Open": open, "Recent": recent,
		"CPUMem": buildChart(series, get, [][3]string{{"cpu", "CPU", "is-a"}, {"mem", "Memory", "is-b"}}, 100, pct),
		"Load":   buildChart(series, get, [][3]string{{"load1", "Load", "is-c"}}, 0, num),
		"Disk":   buildChart(series, get, [][3]string{{"disk", "Disk", "is-d"}}, 100, pct),
		"Net":    buildChart(series, get, [][3]string{{"rx", "In", "is-a"}, {"tx", "Out", "is-b"}}, 0, bps),
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
	_ = s.tpl.Render(w, "error", map[string]any{"Title": title, "Message": message, "Status": code})
}

// end of file
