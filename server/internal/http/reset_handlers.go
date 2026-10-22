package http

import (
	"bytes"
	"net/http"
	"regexp"
	"strings"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// resetTokenPattern mirrors PasswordReset's token shape (the controller uses
// the same check when building the redirect after a rejected reset).
var resetTokenPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// showForgot renders the password-reset request form (minimal layout,
// PasswordResetController::forgot).
func (s *Server) showForgot(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	available, err := s.resets.Available(r.Context())
	if err != nil {
		s.log.Error("forgot password", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	errMsg, _ := sess.PullFlash("error")
	s.renderAuthPage(w, "forgot", map[string]any{
		"Available": available,
		"Error":     errMsg,
		"CSRF":      session.CSRFToken(sess),
	}, http.StatusOK)
}

// doForgot asks for a reset link. The public answer is the same whether or
// not the account exists (anti-enumeration): success flash, then /login
// (PasswordResetController::send).
func (s *Server) doForgot(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	if err := s.resets.Request(r.Context(), clip(r.FormValue("identifier"), 190), clientIP(r)); err != nil {
		s.log.Error("forgot password", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	sess.Flash("success", "If that account exists and has an email address, a link to reset the password is on its way. It is valid for 30 minutes.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// showReset renders the new-password form for a token; an invalid token is a
// 404 with the same page body (PasswordResetController::show).
func (s *Server) showReset(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	token := r.PathValue("token")
	user, err := s.resets.UserFor(r.Context(), token)
	if err != nil {
		s.log.Error("reset password", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	errMsg, _ := sess.PullFlash("error")
	code := http.StatusOK
	if user == nil {
		code = http.StatusNotFound
	}
	s.renderAuthPage(w, "reset", map[string]any{
		"Valid": user != nil,
		"Token": token,
		"Error": errMsg,
		"CSRF":  session.CSRFToken(sess),
	}, code)
}

// doReset verifies the token and the new password and applies it
// (PasswordResetController::reset). Field errors re-render the form via the
// error flash, keeping the (valid or not) token in the redirect target
// exactly like the PHP controller.
func (s *Server) doReset(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	token := r.PathValue("token")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	fields, err := s.resets.Reset(r.Context(), token, r.FormValue("password"), r.FormValue("password_confirmation"))
	if err != nil {
		s.log.Error("reset password", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if len(fields) > 0 {
		msgs := make([]string, 0, len(fields))
		for _, k := range []string{"token", "password", "password_confirmation"} {
			if m, ok := fields[k]; ok {
				msgs = append(msgs, m)
			}
		}
		sess.Flash("error", strings.Join(msgs, " "))
		loc := "/password/reset/"
		if resetTokenPattern.MatchString(token) {
			loc += token
		}
		http.Redirect(w, r, loc, http.StatusSeeOther)
		return
	}
	sess.Flash("success", "Your password was changed. Sign in with the new password.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// renderAuthPage renders a minimal-layout page (no authenticated shell),
// buffering first so the status code can be set before any bytes are sent.
func (s *Server) renderAuthPage(w http.ResponseWriter, name string, data map[string]any, code int) {
	var buf bytes.Buffer
	if err := s.tpl.Render(&buf, name, data); err != nil {
		s.log.Error("render", "template", name, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write(buf.Bytes())
}
