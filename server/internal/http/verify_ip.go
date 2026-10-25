package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// verifyIpForm GET /login/verify-ip — IP-verification page for sign-ins
// from an unknown address. If there is no pending IP-verification sign-in,
// redirects back to /login (AuthController::verifyIpForm).
func (s *Server) verifyIpForm(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if !s.auth.PendingIpVerification(sess) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	data := map[string]any{
		"CSRF":  session.CSRFToken(sess),
		"Error": "",
	}
	if ef, ok := sess.PullFlash("error"); ok {
		data["Error"] = ef
	}
	s.render(w, "auth_verify_ip", data)
}

// verifyIp POST /login/verify-ip — handles the emailed verification code.
// Mirrors AuthController::verifyIp. On cancel, drops the pending state.
// Otherwise passes the code to auth.CompleteIpVerification; a 6-digit match
// signs the session in and redirects to /.
func (s *Server) verifyIp(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.FormValue("cancel") == "1" {
		s.auth.CancelIpVerification(sess)
		sess.Flash("error", "Sign-in cancelled.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")
	ip := clientIP(r)
	msg, err := s.auth.CompleteIpVerification(r.Context(), sess, code, ip)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if msg != "" {
		sess.Flash("error", msg)
		if s.auth.PendingIpVerification(sess) {
			http.Redirect(w, r, "/login/verify-ip", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		}
		return
	}
	session.RotateCSRF(sess)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
