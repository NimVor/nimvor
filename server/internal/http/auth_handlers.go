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
	msg, pending, err := s.auth.Attempt(r.Context(), sess, username, password, clientIP(r))
	if err != nil {
		s.log.Error("login attempt", "error", err)
		s.renderLogin(w, sess, "Something went wrong. Please try again.", username, http.StatusInternalServerError)
		return
	}
	if pending {
		// Password accepted, second factor next (AuthController::login).
		http.Redirect(w, r, "/login/two-factor", http.StatusSeeOther)
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

// showTwoFactor renders the second-factor step of a sign-in. Mirrors
// AuthController::twoFactor.
func (s *Server) showTwoFactor(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if !s.auth.PendingTwoFactor(sess) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	totp, key := s.auth.PendingMethods(r.Context(), sess)
	errMsg, _ := sess.PullFlash("error")
	s.renderTwoFactor(w, sess, s.auth.PendingUsername(sess), errMsg, totp, key, http.StatusOK)
}

// doTwoFactor verifies the second factor (or cancels the pending sign-in).
// Mirrors AuthController::verifyTwoFactor.
func (s *Server) doTwoFactor(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		s.renderTwoFactor(w, sess, "", "Malformed form submission.", true, false, http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		s.renderTwoFactor(w, sess, "", "Your session has expired. Reload the page and try again.", true, false, http.StatusBadRequest)
		return
	}
	if r.FormValue("cancel") == "1" {
		s.auth.CancelTwoFactor(sess)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	code := clip(r.FormValue("code"), 40) // mb_substr($code, 0, 40) in the PHP controller
	ip := clientIP(r)
	msg, err := s.auth.CompleteTwoFactor(r.Context(), sess, code, ip)
	if err != nil {
		s.log.Error("two-factor verify", "error", err)
		s.renderTwoFactor(w, sess, "", "Something went wrong. Please try again.", true, false, http.StatusInternalServerError)
		return
	}
	if msg != "" {
		sess.Flash("error", msg)
		// Still waiting for the factor -> stay on this step; otherwise the
		// sign-in expired and the user starts again (AuthController::verifyTwoFactor).
		if s.auth.PendingTwoFactor(sess) {
			http.Redirect(w, r, "/login/two-factor", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	// Signed in: rotate the CSRF token.
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
	// The web terminals of this sign-in end with it (best effort).
	s.terminalCloseOwn(r.Context(), sess)
	s.auth.Logout(r.Context(), sess, userFrom(r.Context()))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// dashboard renders the authenticated shell with a permission-gated nav.
func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	data := map[string]any{
		"User": userFrom(r.Context()),
		"CSRF": session.CSRFToken(sess),
		"Nav":  s.navFor(r),
	}
	s.render(w, "dashboard", data)
}

// navFor computes which nav links the signed-in user may see. Each entry is
// gated by the same permission as its route, so the menu reflects RBAC + plan
// features (a withheld permission hides its link) — the hiding is a
// consequence of the server-side gate, not the gate itself.
func (s *Server) navFor(r *http.Request) map[string]bool {
	perms := map[string]string{
		"websites":      "websites.view",
		"databases":     "databases.view",
		"dns":           "dns.manage",
		"mail":          "mail.manage",
		"users":         "users.view",
		"roles":         "users.view",
		"plans":         "plans.manage",
		"servers":       "servers.manage",
		"notifications": "notifications.view",
		"audit":         "audit.view",
	}
	out := make(map[string]bool, len(perms)+1)
	for key, perm := range perms {
		out[key] = s.can(r, perm)
	}
	out["profile"] = true // always available to an authenticated user
	return out
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

// renderTwoFactor renders the second-factor page. totp/key mirror
// AuthService::pendingMethods (the key button would be rendered only when a
// WebAuthn flow exists; the Go panel offers the authenticator code).
func (s *Server) renderTwoFactor(w http.ResponseWriter, sess *session.Session, username, errMsg string, totp, key bool, code int) {
	data := map[string]any{
		"Error":    errMsg,
		"Username": username,
		"TOTP":     totp,
		"Key":      key,
		"CSRF":     session.CSRFToken(sess),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var buf bytes.Buffer
	if err := s.tpl.Render(&buf, "two_factor", data); err != nil {
		s.log.Error("render two-factor", "error", err)
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
