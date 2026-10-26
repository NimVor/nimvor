package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// serversStore POST /servers — refuses, honestly: no remote agent connection yet.
// ServersController::store tests the agent over pinned TLS and stores the
// encrypted token; that client is not ported yet (servers.store, servers.manage).
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
	if !hosting.IsAll(userFrom(r.Context())) {
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
		return
	}
	// This panel talks to its own agent only: there is no pinned-TLS client for
	// remote agents yet, so a row stored here (with no token or pin, as this
	// handler used to do) could never be reached or used.
	sess.Flash("error", "Connecting other servers is not available in this panel yet. It manages this server only.")
	http.Redirect(w, r, "/servers", http.StatusSeeOther)
}

// serversCheck POST /servers/{id}/check — verifies the agent answers for a
// managed server. Mirrors ServersController::check (servers.check,
// servers.manage).
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
	ok, err := hosting.NewServerRepository(s.db).Check(r.Context(), id)
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
		return
	}
	if ok {
		sess.Flash("success", "The agent answered.")
	} else {
		sess.Flash("error", "The agent did not answer; see the error in the list.")
	}
	http.Redirect(w, r, "/servers", http.StatusSeeOther)
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
		if errors.Is(err, hosting.ErrServerSelf) {
			sess.Flash("error", "The local server cannot be removed.")
		} else {
			sess.Flash("error", err.Error())
		}
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{
		Action: "server.remove", TargetType: "server", TargetID: strconv.FormatInt(id, 10),
		Result: audit.Success, UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Server removed from WhaCP. Nothing was changed on it.")
	http.Redirect(w, r, "/servers", http.StatusSeeOther)
}

// serversSwitch POST /servers/switch — a no-op stub: WhaCP runs against a
// single local server. Mirrors ServersController::switch so existing forms
// that POST to it do not 404.
func (s *Server) serversSwitch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
