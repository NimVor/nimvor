package http

// TopbarController port (panel/app/Http/Controllers/TopbarController.php):
// admin-only top-bar server tools: load-average JSON poll, restart all
// services, and WhaCP self-update page/start. The update-available check
// is stubbed (no UpdateCheckService in Go yet); the agent endpoints
// (services.restart_all, whacp.update_status, whacp.update) are wired
// through so the admin UI works end-to-end when the agent supports them.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// topbarStatus GET /topbar/status — admin-only JSON (load average).
func (s *Server) topbarStatus(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		writeJSON(w, http.StatusForbidden, map[string]any{"success": false})
		return
	}
	raw, err := s.agent.Call(r.Context(), "agent.status", nil, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": agentErrMap(err)})
		return
	}
	var st map[string]any
	_ = json.Unmarshal(raw, &st)
	var load any
	if data, ok := st["data"].(map[string]any); ok {
		load = data["load"]
	} else {
		load = st["load"]
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "load": load, "update": false})
}

// topbarRestartAll POST /services/restart-all — restart every managed service.
func (s *Server) topbarRestartAll(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	back := safeReferer(r, "/")
	if !hosting.IsAll(user) {
		topbarFlashErr(w, r, "Only server administrators can restart every service.", back)
		return
	}
	raw, err := s.agent.Call(r.Context(), "services.restart_all", nil, 120*time.Second)
	if err != nil {
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{Action: "services.restart_all", TargetType: "server", TargetID: itoa(s.currentServerID(r)), Result: audit.Failure, Details: map[string]any{"error": err.Error()}, UserID: &uid, Username: user.Username})
		topbarFlashErr(w, r, agentMessage(err), back)
		return
	}
	var env map[string]any
	_ = json.Unmarshal(raw, &env)
	jobID, _ := env["job_id"].(string)
	services, _ := env["services"].([]any)
	if jobID == "" {
		if data, ok := env["data"].(map[string]any); ok {
			jobID, _ = data["job_id"].(string)
			if s2, ok := data["services"].([]any); ok {
				services = s2
			}
		}
	}
	if jobID != "" {
		uid := user.ID
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, "Restart all services", "server", itoa(s.currentServerID(r)), &uid)
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{Action: "services.restart_all", TargetType: "server", TargetID: itoa(s.currentServerID(r)), Result: audit.Success, Details: map[string]any{"services": services}, UserID: &uid, Username: user.Username})
	sess := sessionFrom(r.Context())
	if jobID != "" {
		sess.Flash("job", jobID)
	}
	sess.Flash("success", "Restarting all services; the panel restarts last, so this page may not answer for a few seconds.")
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// topbarUpdate GET /whacp/update — admin-only update page.
func (s *Server) topbarUpdate(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		s.renderError(w, http.StatusForbidden, "Access denied", "Updating WhaCP is for server administrators.")
		return
	}
	var status any
	var agentErr string
	raw, err := s.agent.Call(r.Context(), "whacp.update_status", nil, 60*time.Second)
	if err != nil {
		agentErr = agentMessage(err)
	} else {
		_ = json.Unmarshal(raw, &status)
	}
	if wantsJSON(r) {
		if agentErr != "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"success": false, "error": agentErr})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "status": status})
		return
	}
	sess := sessionFrom(r.Context())
	st := agentObject(raw)
	short := func(sha string) string {
		if len(sha) >= 7 {
			return sha[:7]
		}
		return "—"
	}
	val := func(k string) string {
		if v := jstr(st, k); v != "" {
			return v
		}
		return "—"
	}
	s.render(w, "server_update", map[string]any{
		"User": user, "CSRF": session.CSRFToken(sess), "Title": "WhaCP updates",
		"Success": pullFlashKey(sess, "success"), "Error": pullFlashKey(sess, "error"),
		"HasStatus": st != nil, "AgentError": agentErr, "Version": PanelVersion,
		"Commit": short(jstr(st, "commit")), "Branch": val("branch"), "Path": val("path"),
		"Updatable": jbool(st, "updatable"), "Running": jbool(st, "running"), "Reason": jstr(st, "reason"),
		"Command": jstr(st, "command"), "Log": jstr(st, "log"), "LastResult": jstr(st, "last_result"),
	})
}

// topbarUpdateCheck POST /whacp/update/check — stub: refresh cached status.
func (s *Server) topbarUpdateCheck(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		topbarFlashErr(w, r, "Updating WhaCP is for server administrators.", "/whacp/update")
		return
	}
	sess := sessionFrom(r.Context())
	sess.Flash("error", "Looking up the newest version is not available in this panel yet. The installed commit is shown on this page; compare it with the repository, or update over SSH.")
	http.Redirect(w, r, "/whacp/update", http.StatusSeeOther)
}

// topbarUpdateStart POST /whacp/update — kick off self-update.
func (s *Server) topbarUpdateStart(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		topbarFlashErr(w, r, "Updating WhaCP is for server administrators.", "/whacp/update")
		return
	}
	_, err := s.agent.Call(r.Context(), "whacp.update", nil, 30*time.Second)
	if err != nil {
		uid := user.ID
		s.audit.Record(r.Context(), audit.Entry{Action: "whacp.update", TargetType: "server", TargetID: itoa(s.currentServerID(r)), Result: audit.Failure, Details: map[string]any{"error": err.Error()}, UserID: &uid, Username: user.Username})
		topbarFlashErr(w, r, agentMessage(err), "/whacp/update")
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{Action: "whacp.update", TargetType: "server", TargetID: itoa(s.currentServerID(r)), Result: audit.Success, UserID: &uid, Username: user.Username})
	sess := sessionFrom(r.Context())
	sess.Flash("success", "The update is running. The panel restarts during it; this page follows its progress.")
	http.Redirect(w, r, "/whacp/update", http.StatusSeeOther)
}

func topbarFlashErr(w http.ResponseWriter, r *http.Request, msg, to string) {
	sess := sessionFrom(r.Context())
	sess.Flash("error", msg)
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func safeReferer(r *http.Request, fallback string) string {
	ref := r.Header.Get("Referer")
	if ref == "" {
		return fallback
	}
	// Only accept relative (same-origin) paths; ignore absolute URLs.
	if strings.HasPrefix(ref, "/") && !strings.HasPrefix(ref, "//") {
		return ref
	}
	return fallback
}

// itoa formats any integer width (server ids are int64, counts are int).
func itoa[T ~int | ~int32 | ~int64](n T) string {
	return strconv.FormatInt(int64(n), 10)
}

// agentObject decodes an agent action's result payload (agent.Client.Call
// returns the raw JSON "data") into a generic object. It returns nil when the
// payload is empty or not a JSON object, so callers can keep the PHP-style
// "if it is an object, read these keys" shape.
func agentObject(raw json.RawMessage) map[string]any {
	var m map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}
