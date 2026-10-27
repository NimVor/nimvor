package http

import (
	"context"
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
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// serversStore POST /servers — connects a remote server: the agent must
// answer over TLS presenting exactly the pinned key before anything is
// stored; the token is stored encrypted (ServersController::store).
func (s *Server) serversStore(w http.ResponseWriter, r *http.Request) {
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
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
		return
	}
	ctx := r.Context()
	repo := hosting.NewServerRepository(s.db)
	var errs []string
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" || utf8.RuneCountInString(name) > 60 {
		errs = append(errs, "Enter a name (up to 60 characters).")
	} else if taken, _ := repo.NameTaken(ctx, name); taken {
		errs = append(errs, "A server with this name exists.")
	}
	url := strings.TrimRight(strings.TrimSpace(r.PostFormValue("url")), "/")
	if !agent.ValidRemoteURL(url) {
		errs = append(errs, "Enter the agent address, e.g. https://203.0.113.10:8443")
	}
	pin := strings.TrimSpace(r.PostFormValue("pin"))
	if !agent.ValidPin(pin) {
		errs = append(errs, `Paste the pin printed by "whacp-agent -pin" on that server (sha256//…).`)
	}
	token := strings.TrimSpace(r.PostFormValue("token"))
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		errs = append(errs, "Paste the agent token (/etc/whacp/agent.token on that server).")
	}
	if len(errs) > 0 {
		sess.Flash("error", strings.Join(errs, " "))
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
		return
	}
	uid := user.ID
	fail := func(msg string, details map[string]any) {
		s.audit.Record(ctx, audit.Entry{Action: "server.add", TargetType: "server", TargetID: name, Result: audit.Failure,
			Details: details, UserID: &uid, Username: user.Username, IP: clientIP(r), UserAgent: r.UserAgent()})
		sess.Flash("error", msg)
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
	}
	// Probe before storing anything.
	client := agent.NewRemote(url, pin, token, 15*time.Second)
	health, err := client.Health(ctx)
	if err != nil {
		fail(agentMessage(err), map[string]any{"url": url, "error": err.Error()})
		return
	}
	version := agentVersion(health)
	hostname := ""
	if info, err := client.Call(ctx, "system.info", nil, 15*time.Second); err == nil {
		if sys, _ := agentObject(info)["system"].(map[string]any); sys != nil {
			hostname, _ = sys["hostname"].(string)
		}
	} else {
		fail(agentMessage(err), map[string]any{"url": url, "error": err.Error()})
		return
	}
	enc, err := s.crypto.Encrypt([]byte(token))
	if err != nil {
		s.log.Error("encrypt agent token", "error", err)
		fail("The token could not be stored.", map[string]any{"url": url})
		return
	}
	id, err := repo.AddRemote(ctx, name, url, pin, enc, version)
	if err != nil {
		s.log.Error("add server", "error", err)
		fail("The server could not be saved.", map[string]any{"url": url})
		return
	}
	s.audit.Record(ctx, audit.Entry{Action: "server.add", TargetType: "server", TargetID: strconv.FormatInt(id, 10), Result: audit.Success,
		Details: map[string]any{"name": name, "url": url, "pin": pin, "hostname": hostname},
		UserID:  &uid, Username: user.Username, IP: clientIP(r), UserAgent: r.UserAgent()})
	sess.Flash("success", "Server connected (id "+strconv.FormatInt(id, 10)+"). Switch to it with the selector at the top.")
	http.Redirect(w, r, "/servers", http.StatusSeeOther)
}

// agentVersion reads the version out of a health reply.
func agentVersion(raw json.RawMessage) string {
	m := agentObject(raw)
	if v, ok := m["version"].(string); ok {
		return v
	}
	return ""
}

// releaseOf drops a version's "v" and its -/+ suffix.
func releaseOf(v string) string {
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	return v
}

// serversCheck POST /servers/{id}/check — asks the agent for its health and
// records the state; a reachable agent of another release is usable but
// flagged (ServerService::check).
func (s *Server) serversCheck(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	if _, _, err := hosting.NewServerRepository(s.db).Server(r.Context(), id); err != nil {
		sess.Flash("error", "That server does not exist.")
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
		return
	}
	if state, _ := s.checkServer(withServer(r.Context(), id), id); state == "online" || state == "version_mismatch" {
		sess.Flash("success", "The agent answered.")
	} else {
		sess.Flash("error", "The agent did not answer; see the error in the list.")
	}
	http.Redirect(w, r, "/servers", http.StatusSeeOther)
}

// checkServer asks a server's agent for its health and records the state; a
// reachable agent of another release is usable but flagged
// (ServerService::check). ctx must select the server. It returns the state
// and, for a failure or mismatch, the note.
func (s *Server) checkServer(ctx context.Context, id int64) (state, note string) {
	repo := hosting.NewServerRepository(s.db)
	health, err := s.agentHealth(ctx)
	if err != nil {
		code := ""
		var ae *agent.Error
		if errors.As(err, &ae) {
			code = ae.Code
		}
		state, note = hosting.FailureState(code), agentMessage(err)
		_ = repo.RecordCheck(ctx, id, state, note, "", false)
		return state, note
	}
	version := agentVersion(health)
	state = "online"
	if PanelVersion != "" && version != "" && releaseOf(version) != releaseOf(PanelVersion) {
		state = "version_mismatch"
		note = "Agent " + version + ", panel " + PanelVersion + ": upgrade both together."
	}
	_ = repo.RecordCheck(ctx, id, state, note, version, true)
	return state, note
}

// serversDestroy POST /servers/{id}/delete — removes a managed server.
// Mirrors ServersController::destroy (servers.delete, servers.manage).
func (s *Server) serversDestroy(w http.ResponseWriter, r *http.Request) {
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
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	uid := user.ID
	if err := hosting.NewServerRepository(s.db).Remove(r.Context(), id); err != nil {
		s.audit.Record(r.Context(), audit.Entry{
			Action: "server.remove", TargetType: "server", TargetID: strconv.FormatInt(id, 10),
			Result: audit.Failure, Details: map[string]any{"error": err.Error()},
			UserID: &uid, Username: user.Username,
		})
		var re *hosting.ServerRemoveError
		if errors.Is(err, hosting.ErrServerSelf) {
			sess.Flash("error", "The local server cannot be removed.")
		} else if errors.Is(err, hosting.ErrServerNotFound) {
			sess.Flash("error", "That server does not exist.")
		} else if errors.As(err, &re) {
			sess.Flash("error", re.Msg)
		} else {
			sess.Flash("error", err.Error())
		}
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
		return
	}
	if rt, ok := s.agent.(*agentRouter); ok {
		rt.forget(id)
	}
	if sess.Get(serverSessionKey) == strconv.FormatInt(id, 10) {
		sess.Set(serverSessionKey, "1")
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "server.remove", TargetType: "server", TargetID: strconv.FormatInt(id, 10),
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Server removed from WhaCP. Nothing was changed on it.")
	http.Redirect(w, r, "/servers", http.StatusSeeOther)
}

// serversSwitch POST /servers/switch — selects the server the session works
// on, among those the user may use (ServersController::switch).
func (s *Server) serversSwitch(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	id, err := strconv.ParseInt(r.PostFormValue("server_id"), 10, 64)
	if err != nil || id <= 0 {
		id = 1
	}
	allowed := false
	for _, c := range s.visibleServers(r.Context(), userFrom(r.Context())) {
		if c.ID == id {
			allowed = true
		}
	}
	if !allowed {
		sess.Flash("error", "That server does not exist.")
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	sess.Set(serverSessionKey, strconv.FormatInt(id, 10))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// agentHealth asks the selected server's agent for its health.
func (s *Server) agentHealth(ctx context.Context) (json.RawMessage, error) {
	if rt, ok := s.agent.(*agentRouter); ok {
		return rt.Health(ctx)
	}
	return nil, errors.New("no agent")
}
