package http

import (
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// runtimeVersionRx matches exact Node.js versions (e.g. 20.17.0).
var runtimeVersionRx = regexp.MustCompile(`^\d{1,2}\.\d{1,3}\.\d{1,3}$`)

// runtimesIndex GET /runtimes — lists managed and system Node.js/Python
// runtimes reported by the agent. Mirrors RuntimeController::index
// (runtimes, server.view).
func (s *Server) runtimesIndex(w http.ResponseWriter, r *http.Request) {
	user := userFrom(r.Context())
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"CSRF":       session.CSRFToken(sess),
		"User":       user,
		"Title":      "Runtimes",
		"Status":     nil,
		"AgentError": nil,
		"Success":    "",
		"Error":      "",
		"CanManage":  s.can(r, "server.manage"),
	}
	if sf, ok := sess.PullFlash("success"); ok {
		data["Success"] = sf
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	if raw, err := s.agent.Call(r.Context(), "runtime.status", nil, 60*time.Second); err == nil {
		var st any
		_ = json.Unmarshal(raw, &st)
		data["Status"] = st
	} else {
		data["AgentError"] = err.Error()
	}
	s.render(w, "server_runtimes", data)
}

// runtimesInstall POST /runtimes/install — installs a specific Node.js
// version via the agent. Mirrors RuntimeController::install
// (runtimes.install, server.manage).
func (s *Server) runtimesInstall(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		http.Redirect(w, r, "/runtimes", http.StatusSeeOther)
		return
	}
	version := r.FormValue("version")
	if !runtimeVersionRx.MatchString(version) {
		sess.Flash("error", "Enter an exact Node.js version, e.g. 20.17.0.")
		http.Redirect(w, r, "/runtimes", http.StatusSeeOther)
		return
	}
	raw, err := s.agent.Call(r.Context(), "runtime.install", map[string]any{"kind": "node", "version": version}, 600*time.Second)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/runtimes", http.StatusSeeOther)
		return
	}
	jobID := ""
	if m, _ := raw.(map[string]any); m != nil {
		if j, ok := m["job_id"].(string); ok {
			jobID = j
		}
	}
	uid := user.ID
	label := "Install Node.js " + version
	if jobID != "" {
		_ = hosting.NewJobTracker(s.db).Track(r.Context(), s.currentServerID(r), jobID, label, "server", itoa(s.currentServerID(r)), &uid)
		sess.Flash("job", jobID)
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "runtime.install", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: map[string]any{"kind": "node", "version": version},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Installing Node.js "+version+". You are notified when it is ready.")
	http.Redirect(w, r, "/runtimes", http.StatusSeeOther)
}

// runtimesRemove POST /runtimes/remove — removes a managed Node.js or Python
// runtime via the agent. Mirrors RuntimeController::remove
// (runtimes.remove, server.manage).
func (s *Server) runtimesRemove(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	user := userFrom(r.Context())
	if !hosting.IsAll(user) {
		http.Redirect(w, r, "/runtimes", http.StatusSeeOther)
		return
	}
	kind := r.FormValue("kind")
	version := r.FormValue("version")
	if kind != "node" && kind != "python" {
		sess.Flash("error", "Unknown runtime.")
		http.Redirect(w, r, "/runtimes", http.StatusSeeOther)
		return
	}
	uid := user.ID
	if _, err := s.agent.Call(r.Context(), "runtime.remove", map[string]any{"kind": kind, "version": version}, 120*time.Second); err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "runtime.remove", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
			Result: audit.Failure, Details: map[string]any{"kind": kind, "version": version, "error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/runtimes", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "runtime.remove", TargetType: "server", TargetID: itoa(s.currentServerID(r)),
		Result: audit.Success, Details: map[string]any{"kind": kind, "version": version},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Removed "+kind+" "+version+".")
	http.Redirect(w, r, "/runtimes", http.StatusSeeOther)
}
