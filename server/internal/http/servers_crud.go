package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/hosting"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// serversStore POST /servers — adds a new managed remote server.
// Mirrors ServersController::store (servers.store, servers.manage). The real
// token exchange is not yet ported; this records name/url/kind so the page
// works end-to-end and returns an id.
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
	name := r.FormValue("name")
	url := r.FormValue("url")
	if name == "" || url == "" {
		sess.Flash("error", "Name and URL are required.")
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
		return
	}
	id, err := hosting.NewServerRepository(s.db).Add(r.Context(), hosting.ServerInput{Name: name, URL: url, Kind: "remote"})
	if err != nil {
		sess.Flash("error", err.Error())
		http.Redirect(w, r, "/servers", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		Action: "server.add", TargetType: "server", TargetID: strconv.FormatInt(id, 10),
		Result: audit.Success, Details: map[string]any{"name": name, "url": url},
		UserID: &uid, Username: user.Username,
	})
	sess.Flash("success", "Server connected (id "+strconv.FormatInt(id, 10)+"). Switch to it with the selector at the top.")
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
	user := userFrom(r.Context())
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
