package http

// This file ports the server-side Memcached management (MemcachedController):
// the index page (GET /memcached — redis.manage), the settings save
// (POST /memcached/configure, redis.manage), the flush (POST /memcached/flush,
// redis.manage), the install (POST /memcached/install, packages.manage — a
// job) and the restart (POST /memcached/restart, services.manage).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/agent"
	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// memcachedView is the view's $status-derived values (the agent's stats and
// runtime maps are strings).
type memcachedView struct {
	Installed    bool
	Running      bool
	Error        string
	ConfigFile   string
	MemoryMB     int
	MaxConns     int
	Listen       string
	Port         int
	HasStats     bool
	UsedHuman    string
	LimitHuman   string
	Pct          int
	PctHigh      bool
	Version      string
	Uptime       string
	Items        string
	HitRatio     string
	Hits         string
	Misses       string
	Evictions    string
	CurConns     string
	MaxConnsNow  string
	ListenJoined string
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

func memcachedViewOf(status map[string]any) *memcachedView {
	v := &memcachedView{}
	if status == nil {
		return v
	}
	v.Installed, _ = status["installed"].(bool)
	active, _ := status["active"].(bool)
	v.Error, _ = status["error"].(string)
	v.Running = v.Installed && active && v.Error == ""
	v.ConfigFile, _ = status["config_file"].(string)
	cfg, _ := status["settings"].(map[string]any)
	if cfg != nil {
		v.MemoryMB = intOf(cfg["memory_mb"])
		if v.MemoryMB == 0 {
			v.MemoryMB = 64
		}
		v.MaxConns = intOf(cfg["max_conns"])
		if v.MaxConns == 0 {
			v.MaxConns = 1024
		}
		v.Port = intOf(cfg["port"])
		if v.Port == 0 {
			v.Port = 11211
		}
		if l, ok := cfg["listen"].([]any); ok {
			parts := make([]string, 0, len(l))
			for _, x := range l {
				if s, ok := x.(string); ok {
					parts = append(parts, s)
				}
			}
			v.ListenJoined = strings.Join(parts, ", ")
		}
		if v.ListenJoined == "" {
			v.ListenJoined = "127.0.0.1"
		}
		v.Listen = v.ListenJoined
	} else {
		v.Listen = "127.0.0.1"
	}
	st, _ := status["stats"].(map[string]any)
	if len(st) > 0 {
		v.HasStats = true
		used := int64(intOf(st["bytes"]))
		limit := int64(intOf(st["limit_maxbytes"]))
		v.UsedHuman = formatBytes(used)
		v.LimitHuman = formatBytes(limit)
		if limit > 0 {
			pct := float64(used) / float64(limit) * 100
			if pct > 100 {
				pct = 100
			}
			v.Pct = int(pct + 0.5)
			v.PctHigh = pct > 90
		}
		v.Version, _ = st["version"].(string)
		v.Uptime = memcachedUptime(intOf(st["uptime"]))
		v.Items = numberFormat(intOf(st["curr_items"]))
		hits, misses := intOf(st["get_hits"]), intOf(st["get_misses"])
		v.Hits, v.Misses = numberFormat(hits), numberFormat(misses)
		if hits+misses > 0 {
			v.HitRatio = strconv.FormatFloat(float64(hits)/float64(hits+misses)*100, 'f', 1, 64) + " %"
		}
		v.Evictions = numberFormat(intOf(st["evictions"]))
		v.CurConns = strconv.Itoa(intOf(st["curr_connections"]))
		rt, _ := status["runtime"].(map[string]any)
		if mc, ok := rt["maxconns"].(string); ok && mc != "" {
			v.MaxConnsNow = mc
		}
		if v.MaxConnsNow == "" {
			v.MaxConnsNow = strconv.Itoa(v.MaxConns)
		}
	}
	return v
}

// memcachedUptime mirrors the view's "Xd H:i" uptime formatting.
func memcachedUptime(seconds int) string {
	days := seconds / 86400
	rest := seconds % 86400
	return strconv.Itoa(days) + "d " + fmt.Sprintf("%02d:%02d", rest/3600, (rest%3600)/60)
}

// memcachedIndex renders the server's Memcached page (index — redis.manage).
func (s *Server) memcachedIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	sess := sessionFrom(r.Context())
	status, ae := s.memcachedStatusOf(ctx)
	okServices, _ := s.authorizer().Can(ctx, user, "services.manage")
	okPackages, _ := s.authorizer().Can(ctx, user, "packages.manage")
	data := map[string]any{
		"User":       user,
		"CSRF":       session.CSRFToken(sess),
		"AgentError": "",
		"V":          (*memcachedView)(nil),
		"CanManage":  okServices,
		"CanInstall": okPackages,
		"Success":    "",
		"Error":      "",
		"Job":        "",
	}
	if ae != nil {
		data["AgentError"] = ae.Message
	} else {
		data["V"] = memcachedViewOf(status)
	}
	data["Success"], _ = sess.PullFlash("success")
	data["Error"], _ = sess.PullFlash("error")
	data["Job"], _ = sess.PullFlash("job")
	s.render(w, "memcached_index", data)
}

// memcachedRedirectWith sends the page's flash + redirect (the PHP's
// act() wrapper).
func (s *Server) memcachedRedirectWith(w http.ResponseWriter, r *http.Request, ok bool, msg, job string) {
	sess := sessionFrom(r.Context())
	if ok {
		sess.Flash("success", msg)
	} else {
		sess.Flash("error", msg)
	}
	if job != "" {
		sess.Flash("job", job)
	}
	http.Redirect(w, r, "/memcached", http.StatusSeeOther)
}

// memcachedConfigure saves the settings (configure — redis.manage).
func (s *Server) memcachedConfigure(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		s.memcachedRedirectWith(w, r, false, "Bad request.", "")
		return
	}
	listen := []string{}
	for _, part := range strings.FieldsFunc(r.FormValue("listen"), func(c rune) bool { return c == ',' || c == ' ' || c == '\t' || c == '\n' || c == '\r' }) {
		if part != "" {
			listen = append(listen, part)
		}
	}
	params := map[string]any{
		"memory_mb":                   intOf(r.FormValue("memory_mb")),
		"max_conns":                   intOf(r.FormValue("max_conns")),
		"listen":                      listen,
		"confirm_edit_package_config": r.FormValue("confirm") == "1",
	}
	if params["memory_mb"] == 0 {
		params["memory_mb"] = 64
	}
	if params["max_conns"] == 0 {
		params["max_conns"] = 1024
	}
	if msg := s.memcachedAgentError(r, "memcached.configure", params, 60*time.Second); msg != "" {
		s.memcachedRedirectWith(w, r, false, msg, "")
		return
	}
	s.memcachedAudit(r, user, "memcached.configure", "server", "memcached", params)
	s.memcachedRedirectWith(w, r, true, "Memcached restarted with the new settings.", "")
}

// memcachedFlush removes every item (flush — redis.manage).
func (s *Server) memcachedFlush(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		s.memcachedRedirectWith(w, r, false, "Bad request.", "")
		return
	}
	if strings.ToLower(strings.TrimSpace(r.FormValue("confirm"))) != "flush" {
		s.memcachedRedirectWith(w, r, false, "Type \u0022flush\u0022 to confirm.", "")
		return
	}
	if msg := s.memcachedAgentError(r, "memcached.flush", map[string]any{"confirm": "flush"}, 30*time.Second); msg != "" {
		s.memcachedRedirectWith(w, r, false, msg, "")
		return
	}
	s.memcachedAudit(r, user, "memcached.flush", "server", "memcached", nil)
	s.memcachedRedirectWith(w, r, true, "All Memcached items were removed.", "")
}

// memcachedInstall installs the package as a job (install — packages.manage).
func (s *Server) memcachedInstall(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	ctx := r.Context()
	res, err := s.agent.Call(ctx, "packages.install", map[string]any{"name": "memcached"}, 60*time.Second)
	if err != nil {
		msg := "The WhaCP agent could not be reached."
		var ae *agent.Error
		if errors.As(err, &ae) {
			msg = ae.Message
		}
		s.memcachedRedirectWith(w, r, false, msg, "")
		return
	}
	var out struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		s.memcachedRedirectWith(w, r, false, "The WhaCP agent answered with an unreadable reply.", "")
		return
	}
	_ = hosting.NewJobTracker(s.db).Track(ctx, s.currentServerID(r), out.JobID, "Install memcached", "server", "memcached", &user.ID)
	s.memcachedAudit(r, user, "packages.install", "server", "memcached", map[string]any{"name": "memcached"})
	s.memcachedRedirectWith(w, r, true, "Installing Memcached…", out.JobID)
}

// memcachedRestart restarts the service (restart — services.manage).
func (s *Server) memcachedRestart(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if msg := s.memcachedAgentError(r, "services.control", map[string]any{"unit": "memcached.service", "op": "restart"}, 60*time.Second); msg != "" {
		s.memcachedRedirectWith(w, r, false, msg, "")
		return
	}
	s.memcachedAudit(r, user, "services.restart", "service", "memcached.service", nil)
	s.memcachedRedirectWith(w, r, true, "Memcached restarted.", "")
}

// memcachedAgentError calls the agent and returns "" on success, the
// error message otherwise (the PHP's act() catch).
func (s *Server) memcachedAgentError(r *http.Request, action string, params map[string]any, timeout time.Duration) string {
	_, err := s.agent.Call(r.Context(), action, params, timeout)
	if err == nil {
		return ""
	}
	var ae *agent.Error
	if errors.As(err, &ae) {
		return ae.Message
	}
	s.log.Error(action, "error", err)
	return "The WhaCP agent could not be reached."
}

func (s *Server) memcachedAudit(r *http.Request, user *identity.User, action, targetType, targetID string, details map[string]any) {
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID: &uid, Username: user.Username,
		Action: action, TargetType: targetType, TargetID: targetID,
		Result: audit.Success, IP: clientIP(r), UserAgent: r.UserAgent(),
		Details: details,
	})
}
