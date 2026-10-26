package http

// This file ports the database server manager (DbServerController): the
// overview page with the live db.server_status data, the settings form
// (WhaCP's managed option file), the processlist with kill, flush, a
// checked restart, and the database-server log viewer (the log sources
// only). Everything runs in the agent as root; the panel checks the
// server-administrator scope and records the changes in the audit log
// (target: the database server). The page reuses the Go shell; the
// bootstrap markup (cards, forms) is ported as static HTML.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

const dbServerPage = "/databases/server"

// dbServerDenied is the PHP denied(): the database server is managed by
// server administrators (all-scope), everyone else gets the 403 page.
func (s *Server) dbServerDenied(w http.ResponseWriter, r *http.Request) bool {
	user := userFrom(r.Context())
	if hosting.IsAll(user) {
		return false
	}
	renderStatus(w, s, "error", map[string]any{
		"Title":   "Access denied",
		"Message": "The database server is managed by server administrators.",
	}, http.StatusForbidden)
	return true
}

// dbServerIndex is the overview (DbServerController::index,
// databases.server, server.view).
func (s *Server) dbServerIndex(w http.ResponseWriter, r *http.Request) {
	s.dbServerPage(w, r, "overview")
}

// dbServerSettingsPage is the settings form (settingsPage,
// databases.server.settings_page, server.view).
func (s *Server) dbServerSettingsPage(w http.ResponseWriter, r *http.Request) {
	s.dbServerPage(w, r, "settings")
}

// dbServerConnections is the processlist (connections,
// databases.server.connections, server.view).
func (s *Server) dbServerConnections(w http.ResponseWriter, r *http.Request) {
	s.dbServerPage(w, r, "connections")
}

func (s *Server) dbServerPage(w http.ResponseWriter, r *http.Request, section string) {
	if s.dbServerDenied(w, r) {
		return
	}
	ctx := r.Context()
	raw, err := s.agent.Call(ctx, "db.server_status", nil, 60*time.Second)
	var status map[string]any
	var agentErr *agent.Error
	if err != nil {
		if !errors.As(err, &agentErr) {
			s.log.Error("db.server_status", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else if err := json.Unmarshal(raw, &status); err != nil {
		s.log.Error("db.server_status", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var conns []dbServerConn
	if agentErr == nil && status["running"] == true && section == "connections" {
		raw2, err2 := s.agent.Call(ctx, "db.processlist", nil, 30*time.Second)
		if err2 != nil {
			var ae *agent.Error
			if !errors.As(err2, &ae) {
				s.log.Error("db.processlist", "error", err2)
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			agentErr = ae
		} else {
			var pl struct {
				Connections []map[string]any `json:"connections"`
			}
			if err2 := json.Unmarshal(raw2, &pl); err2 == nil {
				conns = make([]dbServerConn, 0, len(pl.Connections))
				for _, c := range pl.Connections {
					conns = append(conns, dbServerConnFrom(c))
				}
			}
		}
	}
	var view *dbServerView
	if status != nil {
		view = buildDbServerView(section, status, conns)
	}
	title := map[string]string{"overview": "Database server", "settings": "Database server settings", "connections": "Database connections"}[section]
	subtitle := "MariaDB / MySQL"
	if status != nil {
		if v, ok := status["variables"].(map[string]any); ok {
			if vc, ok := v["version_comment"].(string); ok && strings.TrimSpace(vc) != "" {
				subtitle = strings.TrimSpace(vc)
			}
		}
	}
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	okMsg, _ := sess.PullFlash("success")
	s.render(w, "db_server", map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"View":       view,
		"Title":      title,
		"Subtitle":   subtitle,
		"AgentError": agentErr,
		"Error":      errMsg,
		"Success":    okMsg,
		"CanManage":  s.can(r, "server.manage"),
		"CanRestart": s.can(r, "services.manage"),
	})
}

// dbServerSaveSettings applies the managed settings (saveSettings,
// databases.server.settings, server.manage). Only the fields that differ
// from WhaCP's file are sent; an emptied field that was set removes it.
func (s *Server) dbServerSaveSettings(w http.ResponseWriter, r *http.Request) {
	if s.dbServerDenied(w, r) {
		return
	}
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
	settings := formMap(r.PostForm, "settings[")
	for k, v := range settings {
		settings[k] = strings.TrimSpace(v)
	}
	managed := formMap(r.PostForm, "managed[")
	send := map[string]string{}
	for k, v := range settings {
		if v != managed[k] {
			send[k] = v
		}
	}
	restart := r.FormValue("restart") == "1"
	if len(send) == 0 && !restart {
		sess.Flash("success", "Nothing changed.")
		http.Redirect(w, r, dbServerPage+"/settings", http.StatusSeeOther)
		return
	}
	details := map[string]any{"settings": send, "restart": restart}
	res, err := s.agent.Call(r.Context(), "db.server_settings", map[string]any{"settings": send, "restart": restart}, 200*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			details["error"] = ae.Message
			s.dbServerAudit(r, user, "database.server_settings", audit.Failure, details)
			sess.Flash("error", ae.Message)
		} else {
			s.log.Error("db.server_settings", "error", err)
			sess.Flash("error", "The database server settings could not be saved.")
		}
		http.Redirect(w, r, dbServerPage+"/settings", http.StatusSeeOther)
		return
	}
	s.dbServerAudit(r, user, "database.server_settings", audit.Success, details)
	msg := "Settings saved and applied."
	var out struct {
		Restarted     bool     `json:"restarted"`
		RestartNeeded []string `json:"restart_needed"`
	}
	if err := json.Unmarshal(res, &out); err == nil {
		if out.Restarted {
			msg = "Settings saved; the database server was restarted with them."
		} else if len(out.RestartNeeded) > 0 {
			msg = "Settings saved. " + strings.Join(out.RestartNeeded, ", ") + " take effect after the database server restarts."
		}
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, dbServerPage+"/settings", http.StatusSeeOther)
}

// dbServerRestart runs the checked restart (restart,
// databases.server.restart, services.manage).
func (s *Server) dbServerRestart(w http.ResponseWriter, r *http.Request) {
	if s.dbServerDenied(w, r) {
		return
	}
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
	res, err := s.agent.Call(r.Context(), "db.server_restart", nil, 200*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			s.dbServerAudit(r, user, "database.server_restart", audit.Failure, map[string]any{"error": ae.Message})
			sess.Flash("error", ae.Message)
		} else {
			s.log.Error("db.server_restart", "error", err)
			sess.Flash("error", "The database server could not be restarted.")
		}
		http.Redirect(w, r, dbServerPage, http.StatusSeeOther)
		return
	}
	s.dbServerAudit(r, user, "database.server_restart", audit.Success, map[string]any{})
	msg := "The database server restarted"
	var out struct {
		Seconds int `json:"seconds"`
	}
	if err := json.Unmarshal(res, &out); err == nil && out.Seconds > 0 {
		msg += fmt.Sprintf(" in %d s", out.Seconds)
	}
	sess.Flash("success", msg+" and answers again.")
	http.Redirect(w, r, dbServerPage, http.StatusSeeOther)
}

// dbServerFlush runs FLUSH (flush, databases.server.flush,
// server.manage). Failures are not audited, as in PHP.
func (s *Server) dbServerFlush(w http.ResponseWriter, r *http.Request) {
	if s.dbServerDenied(w, r) {
		return
	}
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
	what := r.FormValue("what")
	_, err := s.agent.Call(r.Context(), "db.server_flush", map[string]any{"what": what}, 60*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			sess.Flash("error", ae.Message)
		} else {
			s.log.Error("db.server_flush", "what", what, "error", err)
			sess.Flash("error", "The flush failed.")
		}
		http.Redirect(w, r, dbServerPage, http.StatusSeeOther)
		return
	}
	s.dbServerAudit(r, user, "database.server_flush", audit.Success, map[string]any{"what": what})
	msg := "Done."
	switch what {
	case "privileges":
		msg = "Privileges reloaded."
	case "logs":
		msg = "Log files reopened."
	case "hosts":
		msg = "Blocked hosts cleared."
	}
	sess.Flash("success", msg)
	http.Redirect(w, r, dbServerPage, http.StatusSeeOther)
}

// dbServerKill ends a connection or its statement (kill,
// databases.server.kill, server.manage). Failures are not audited.
func (s *Server) dbServerKill(w http.ResponseWriter, r *http.Request) {
	if s.dbServerDenied(w, r) {
		return
	}
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
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	queryOnly := r.FormValue("query_only") == "1"
	_, err := s.agent.Call(r.Context(), "db.kill", map[string]any{"id": id, "query_only": queryOnly}, 30*time.Second)
	if err != nil {
		var ae *agent.Error
		if errors.As(err, &ae) {
			sess.Flash("error", ae.Message)
		} else {
			s.log.Error("db.kill", "id", id, "error", err)
			sess.Flash("error", "The connection could not be ended.")
		}
		http.Redirect(w, r, dbServerPage+"/connections", http.StatusSeeOther)
		return
	}
	s.dbServerAudit(r, user, "database.kill", audit.Success, map[string]any{"id": id, "query_only": queryOnly})
	if queryOnly {
		sess.Flash("success", fmt.Sprintf("The statement of connection %d was stopped.", id))
	} else {
		sess.Flash("success", fmt.Sprintf("Connection %d was ended.", id))
	}
	http.Redirect(w, r, dbServerPage+"/connections", http.StatusSeeOther)
}

// dbServerLogs renders the database-server log viewer (logs,
// databases.server.logs, logs.view): the log sources with a MySQL or
// MariaDB id, the reused logs.js UI.
func (s *Server) dbServerLogs(w http.ResponseWriter, r *http.Request) {
	if s.dbServerDenied(w, r) {
		return
	}
	user := userFrom(r.Context())
	var sources []logSource
	var agentErr *agent.Error
	raw, err := s.agent.Call(r.Context(), "logs.sources", nil)
	if err != nil {
		if !errors.As(err, &agentErr) {
			s.log.Error("logs.sources", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	} else if err := json.Unmarshal(raw, &sources); err != nil {
		s.log.Error("logs.sources", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// The database-server sources only (the PHP mysql|mariadb id match).
	filtered := sources[:0]
	for _, src := range sources {
		if dbLogSourceRe.MatchString(src.ID) {
			filtered = append(filtered, src)
		}
	}
	sources = filtered
	initial := ""
	if len(sources) > 0 {
		initial = sources[0].ID
	}
	s.render(w, "db_logs", map[string]any{
		"User":          user,
		"CSRF":          session.CSRFToken(sessionFrom(r.Context())),
		"Groups":        groupLogSources(sources),
		"AgentError":    agentErr,
		"InitialSource": initial,
		"NoSources":     len(sources) == 0 && agentErr == nil,
	})
}

// dbServerAudit records a database-server change (target: the database
// server, as in the PHP 'server' / 'database' pair).
func (s *Server) dbServerAudit(r *http.Request, user *identity.User, action, result string, details map[string]any) {
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &uid, Username: user.Username,
		Action: action, TargetType: "server", TargetID: "database",
		Result: result, IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: details,
	})
}

// formMap reads the form fields named prefix<key>] into a map (the
// settings[...] and managed[...] fields of the settings form).
func formMap(form url.Values, prefix string) map[string]string {
	out := map[string]string{}
	for k := range form {
		if strings.HasPrefix(k, prefix) && strings.HasSuffix(k, "]") {
			key := k[len(prefix) : len(k)-1] // prefix already ends with "["
			if key != "" {
				out[key] = form.Get(k)
			}
		}
	}
	return out
}

// dbServerField is one managed setting of the settings form.
type dbServerField struct {
	Key       string
	Label     string
	Help      string
	Kind      string
	Choices   []string
	Dynamic   bool
	Current   string // WhaCP's drop-in value ("" = the server's own)
	Effective string // the server's effective value, displayed
}

// dbServerGroup is one settings group, in the agent's order.
type dbServerGroup struct {
	Name   string
	Fields []dbServerField
}

// dbServerConn is one processlist row.
type dbServerConn struct {
	ID        int64
	User      string
	Host      string
	DB        string
	Command   string
	State     string
	Time      string
	InfoShown string
	System    bool
}

func dbServerConnFrom(m map[string]any) dbServerConn {
	id, _ := m["id"].(float64)
	t, _ := m["time"].(float64)
	system, _ := m["system"].(bool)
	info, _ := m["info"].(string)
	return dbServerConn{
		ID: int64(id), User: jsonStr(m, "user"), Host: jsonStr(m, "host"),
		DB: jsonStr(m, "db"), Command: jsonStr(m, "command"), State: jsonStr(m, "state"),
		Time: formatDuration(int64(t)), InfoShown: cutText(info, 300), System: system,
	}
}

// dbServerView is the rendered page data (the PHP view's derived values,
// precomputed so the template stays declarative).
type dbServerView struct {
	Section     string
	Running     bool
	Unit        string
	DropIn      string
	ConfigError string
	StatusErr   string

	// Overview stat cards.
	Version    string
	Uptime     string
	ConnValue  string
	ConnSub    string
	QueryValue string
	QuerySub   string
	DataValue  string
	DataSub    string

	// Resources card.
	ConnPct        int
	PoolPct        *int
	PoolSize       string
	HitRate        *float64
	TmpDisk        *float64
	HitV, TmpV     float64 // HitRate / TmpDisk as plain numbers (templates cannot compare or format a pointer)
	OpenTables     string
	TableOpenCache string
	BytesRecv      string
	BytesSent      string
	ComSelect      string
	ComInsert      string
	ComUpdate      string
	ComDelete      string
	LockWaits      string

	// Connection information card.
	Socket    string
	Port      string
	ListensOn string
	DataDir   string
	ErrorLog  string
	TLS       string

	// Settings section.
	Groups []dbServerGroup

	// Connections section.
	Conns []dbServerConn
}

// buildDbServerView mirrors the PHP view's derived values. status is the
// agent's db.server_status data; nil is handled by the caller.
func buildDbServerView(section string, status map[string]any, conns []dbServerConn) *dbServerView {
	v := &dbServerView{Section: section}
	v.Running = status["running"] == true
	v.Unit = jsonStr(status, "unit")
	v.DropIn = jsonStr(status, "drop_in")
	v.ConfigError = jsonStr(status, "config_error")
	v.StatusErr = cutText(jsonStr(status, "error"), 300)
	vars, _ := status["variables"].(map[string]any)
	st, _ := status["status"].(map[string]any)
	managed, _ := status["managed"].(map[string]any)
	num := func(k string) float64 {
		f, _ := strconv.ParseFloat(jsonStr(st, k), 64)
		return f
	}

	uptime := math.Max(1, num("Uptime"))
	v.Version = jsonStr(vars, "version")
	v.Uptime = formatDuration(int64(uptime))
	maxConn := maxConnFromVars(vars)

	v.ConnValue = fmt.Sprintf("%d / %d", int64(num("Threads_connected")), maxConn)
	v.ConnSub = thousands(num("Max_used_connections")) + " · " + thousands(num("Threads_running")) + " running"
	v.QueryValue = decimals(num("Questions")/uptime, 1) + " /s"
	v.QuerySub = thousands(num("Slow_queries")) + " slow · " + thousands(num("Aborted_connects")) + " failed logins"
	bytes, _ := status["bytes"].(float64)
	dbs, _ := status["databases"].(float64)
	users, _ := status["users"].(float64)
	v.DataValue = bytesText(int64(bytes))
	v.DataSub = fmt.Sprintf("%d databases · %d users", int64(dbs), int64(users))

	// Resources.
	v.ConnPct = connPct(num("Threads_connected"), maxConn)
	pages := num("Innodb_buffer_pool_pages_total")
	if pages > 0 {
		p := int(math.Round(100 * (pages - num("Innodb_buffer_pool_pages_free")) / pages))
		v.PoolPct = &p
		poolBytes, _ := strconv.ParseInt(jsonStr(vars, "innodb_buffer_pool_size"), 10, 64)
		v.PoolSize = bytesText(poolBytes)
	}
	if rr := num("Innodb_buffer_pool_read_requests"); rr > 0 {
		h := 100 * (1 - num("Innodb_buffer_pool_reads")/rr)
		v.HitRate, v.HitV = &h, h
	}
	if ctt := num("Created_tmp_tables"); ctt > 0 {
		d := 100 * num("Created_tmp_disk_tables") / ctt
		v.TmpDisk, v.TmpV = &d, d
	}
	v.OpenTables = thousands(num("Open_tables"))
	v.TableOpenCache = jsonStr(vars, "table_open_cache")
	if v.TableOpenCache == "" {
		v.TableOpenCache = "?"
	}
	v.BytesRecv = bytesText(int64(num("Bytes_received")))
	v.BytesSent = bytesText(int64(num("Bytes_sent")))
	v.ComSelect = thousands(num("Com_select"))
	v.ComInsert = thousands(num("Com_insert"))
	v.ComUpdate = thousands(num("Com_update"))
	v.ComDelete = thousands(num("Com_delete"))
	v.LockWaits = thousands(num("Table_locks_waited"))

	// Connection information.
	v.Socket = jsonStr(vars, "socket")
	if jsonStr(vars, "skip_networking") == "ON" {
		v.Port = "none (network off)"
	} else if p := jsonStr(vars, "port"); p != "" {
		v.Port = p
	} else {
		v.Port = "3306"
	}
	if ba := jsonStr(vars, "bind_address"); ba != "" {
		v.ListensOn = ba
	} else {
		v.ListensOn = "all addresses"
	}
	v.DataDir = jsonStr(vars, "datadir")
	if le := jsonStr(vars, "log_error"); le != "" {
		v.ErrorLog = le
	} else {
		v.ErrorLog = "system journal"
	}
	v.TLS = jsonStr(vars, "have_ssl")

	// Settings (the agent's order, grouped).
	charset := managedStr(managed, "character_set_server")
	if charset == "" {
		charset = jsonStr(vars, "character_set_server")
	}
	if charset == "" {
		charset = "utf8mb4"
	}
	collations := map[string][]string{}
	if cm, ok := status["collations"].(map[string]any); ok {
		for cs, list := range cm {
			if l, ok := list.([]any); ok {
				for _, c := range l {
					if s, ok := c.(string); ok {
						collations[cs] = append(collations[cs], s)
					}
				}
			}
		}
	}
	byGroup := map[string]int{}
	if settings, ok := status["settings"].([]any); ok {
		for _, item := range settings {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			key := jsonStr(m, "key")
			group := jsonStr(m, "group")
			choices := []string{}
			if cl, ok := m["choices"].([]any); ok {
				for _, c := range cl {
					if s, ok := c.(string); ok {
						choices = append(choices, s)
					}
				}
			}
			// Bool fields render ON/OFF (the PHP $opts special case); the
			// collation options come from the character set in use.
			if jsonStr(m, "kind") == "bool" {
				choices = []string{"ON", "OFF"}
			}
			if key == "collation_server" {
				choices = collations[charset]
			}
			dynamic, _ := m["dynamic"].(bool)
			f := dbServerField{
				Key: key, Label: jsonStr(m, "label"), Help: jsonStr(m, "help"),
				Kind: jsonStr(m, "kind"), Choices: choices, Dynamic: dynamic,
				Current:   managedStr(managed, key),
				Effective: shownValue(jsonStr(m, "kind"), jsonStr(vars, key)),
			}
			if i, ok := byGroup[group]; ok {
				v.Groups[i].Fields = append(v.Groups[i].Fields, f)
			} else {
				byGroup[group] = len(v.Groups)
				v.Groups = append(v.Groups, dbServerGroup{Name: group, Fields: []dbServerField{f}})
			}
		}
	}
	v.Conns = conns
	return v
}

// maxConnFromVars is (int)($vars['max_connections'] ?? 0).
func maxConnFromVars(vars map[string]any) int64 {
	n, _ := strconv.ParseInt(jsonStr(vars, "max_connections"), 10, 64)
	return n
}

// connPct is the PHP min(100, round(100*connected/maxConn)) or 0.
func connPct(connected float64, maxConn int64) int {
	if maxConn <= 0 {
		return 0
	}
	p := int(math.Round(100 * connected / float64(maxConn)))
	if p > 100 {
		p = 100
	}
	return p
}

// shownValue is the PHP $shown: a numeric size renders as bytes.
func shownValue(kind, v string) string {
	if kind == "size" && v != "" && isDigits(v) {
		n, _ := strconv.ParseInt(v, 10, 64)
		return bytesText(n)
	}
	return v
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// jsonStr reads a string field of a decoded JSON object ("").
func jsonStr(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// managedStr reads a managed-settings value ("" when absent).
func managedStr(managed map[string]any, k string) string {
	s, _ := managed[k].(string)
	return s
}

// formatDuration mirrors helpers.php's format_duration.
func formatDuration(seconds int64) string {
	d := seconds / 86400
	h := seconds % 86400 / 3600
	m := seconds % 3600 / 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// bytesText is helpers.php's format_bytes($n, 0): 1024 steps, no decimals.
func bytesText(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i, v := 0, float64(n)
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + " " + units[i]
}

// thousands mirrors number_format($n, 0) (groups of three). The loop walks
// the digits right-to-left, inserting a comma every three places; the previous
// version re-inserted at the same index and never terminated for n >= 1000.
func thousands(n float64) string {
	neg := n < 0
	i := int64(math.Round(math.Abs(n))) // PHP rounds half away from zero
	s := strconv.FormatInt(i, 10)
	for pos := len(s) - 3; pos > 0; pos -= 3 {
		s = s[:pos] + "," + s[pos:]
	}
	if neg && i != 0 {
		s = "-" + s
	}
	return s
}

// decimals mirrors number_format($n, $d) (groups + d decimals).
func decimals(n float64, d int) string {
	s := strconv.FormatFloat(n, 'f', d, 64)
	// Group the integer digits (after any sign) in threes, keep the decimals.
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	sign := ""
	if strings.HasPrefix(intPart, "-") {
		sign, intPart = "-", intPart[1:]
	}
	for i := len(intPart) - 3; i > 0; i -= 3 {
		intPart = intPart[:i] + "," + intPart[i:]
	}
	return sign + intPart + frac
}

// cutText is PHP's mb_strimwidth($s, 0, $n, '…') for the ASCII case.
func cutText(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// dbLogSourceRe is the PHP preg_match('/mysql|mariadb/i', id) filter.
var dbLogSourceRe = regexp.MustCompile(`(?i)mysql|mariadb`)
