package http

import (
	"net/http"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// verifyIpForm GET /login/verify-ip — IP-verification page for sign-ins
// from an unknown address (AuthController::verifyIpForm). The LoginIpVerifier
// email-code flow is not ported to Go yet; this handler renders a page that
// tells the user the flow isn't available and offers a link back to /login
// so they can try again. The POST handler redirects to /login as well.
func (s *Server) verifyIpForm(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
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
// Stub: clears any pending state and redirects to /login with a flash
// message because LoginIpVerifier and IP-verification email delivery are
// not yet ported. Mirrors AuthController::verifyIp.
func (s *Server) verifyIp(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if r.FormValue("cancel") == "1" {
		sess.Flash("error", "Sign-in cancelled.")
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	sess.Flash("error", "IP verification by email code is not yet ported in the Go panel. Sign in from a known IP address or use another factor.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
