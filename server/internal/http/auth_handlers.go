package http

import (
	"bytes"
	"net"
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// showLogin renders the sign-in form, or redirects an already-signed-in user
// to the dashboard. Mirrors AuthController::show.
func (s *Server) showLogin(w http.ResponseWriter, r *http.Request) {
	if userFrom(r.Context()) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	sess := sessionFrom(r.Context())
	errMsg, _ := sess.PullFlash("error")
	oldUser, _ := sess.PullFlash("old_username")
	s.renderLogin(w, sess, errMsg, oldUser, http.StatusOK)
}

// doLogin verifies the CSRF token and the credentials. On success it rotates
// the CSRF token and redirects to the dashboard; on failure it flashes the
// error and redirects back to the form. Mirrors AuthController::login.
func (s *Server) doLogin(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		s.renderLogin(w, sess, "Malformed form submission.", "", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		// Mint a fresh token so the re-rendered form is submittable again.
		s.renderLogin(w, sess, "Your session has expired. Reload the page and try again.", r.FormValue("username"), http.StatusBadRequest)
		return
	}

	username := clip(r.FormValue("username"), 190)
	password := r.FormValue("password")
	msg, err := s.auth.Attempt(r.Context(), sess, username, password, clientIP(r))
	if err != nil {
		s.log.Error("login attempt", "error", err)
		s.renderLogin(w, sess, "Something went wrong. Please try again.", username, http.StatusInternalServerError)
		return
	}
	if msg != "" {
		sess.Flash("error", msg)
		sess.Flash("old_username", username)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// Signed in: rotate the CSRF token (AuthController::login does the same).
	session.RotateCSRF(sess)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// doLogout clears the session. CSRF-protected like any state change.
func (s *Server) doLogout(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	_ = r.ParseForm()
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	s.auth.Logout(sess)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// dashboard renders the authenticated shell.
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"User": userFrom(r.Context()),
		"CSRF": session.CSRFToken(sess),
	}
	s.render(w, "dashboard", data)
}

// renderLogin renders the login page with a fresh CSRF token.
func (s *Server) renderLogin(w http.ResponseWriter, sess *session.Session, errMsg, username string, code int) {
	data := map[string]any{
		"Error":    errMsg,
		"Username": username,
		"CSRF":     session.CSRFToken(sess),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Render into a buffer first so a template error doesn't emit a half page
	// with a 200. The flushWriter writes the session cookie on the first write.
	var buf bytes.Buffer
	if err := s.tpl.Render(&buf, "login", data); err != nil {
		s.log.Error("render login", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes())
}

// render writes a template with a 200, buffering to avoid partial output.
func (s *Server) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.tpl.Render(&buf, name, data); err != nil {
		s.log.Error("render", "template", name, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

// formToken reads the CSRF token from the _token field or X-CSRF-Token header
// (parity with VerifyCsrfToken.php).
func formToken(r *http.Request) string {
	if t := r.Header.Get("X-CSRF-Token"); t != "" {
		return t
	}
	return r.FormValue("_token")
}

func clip(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}

// clientIP returns the remote address without the port. Trusted-proxy handling
// (X-Forwarded-For) is a later concern; for now the direct peer is used.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
