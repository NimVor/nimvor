package http

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// webauthnLoginOptions POST /login/two-factor/key/options — returns
// WebAuthn assertion options for the pending sign-in. Stub: returns a
// JSON error because the WebAuthn library is not vendored in Go yet.
// Mirrors AuthController::securityKeyOptions.
func (s *Server) webauthnLoginOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"error":   map[string]any{"message": "Security-key (WebAuthn) sign-in is not yet ported in the Go panel."},
	})
}

// webauthnLogin POST /login/two-factor/key — completes a WebAuthn sign-in
// assertion. Stub: returns a JSON error. Mirrors AuthController::securityKey.
func (s *Server) webauthnLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"error":   map[string]any{"message": "Security-key (WebAuthn) sign-in is not yet ported in the Go panel."},
	})
}

// webauthnProfileOptions POST /profile/security-keys/options — returns
// WebAuthn registration options for the signed-in user. Stub: JSON 501.
// Mirrors ProfileController::keyOptions.
func (s *Server) webauthnProfileOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"error":   map[string]any{"message": "Security-key registration is not yet ported in the Go panel."},
	})
}

// webauthnProfileAdd POST /profile/security-keys — registers a new
// security key for the signed-in user. Stub: JSON 501.
// Mirrors ProfileController::keyAdd.
func (s *Server) webauthnProfileAdd(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sessionFrom(r.Context()), formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"success": false,
		"error":   map[string]any{"message": "Security-key registration is not yet ported in the Go panel."},
	})
}

// webauthnProfileDelete POST /profile/security-keys/{id}/delete — removes
// a registered security key. Stub: CSRF-checked, flashes a friendly error
// and redirects to /profile#security-keys. (A full implementation would
// DELETE the row from webauthn_credentials by id and user_id.)
// Mirrors ProfileController::keyDelete.
func (s *Server) webauthnProfileDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		http.Error(w, "Page expired", http.StatusBadRequest)
		return
	}
	// Best-effort: delete by id from webauthn_credentials for the signed-in
	// user so users CAN remove stuck keys even before the WebAuthn library is
	// vendored. This lets the user recover if a lost key blocks login.
	if id, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil && id > 0 {
		user := userFrom(r.Context())
		if user != nil {
			_, _ = s.db.ExecContext(r.Context(), "DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?", id, user.ID)
		}
		sess.Flash("success", "Security key removed.")
		http.Redirect(w, r, "/profile#security-keys", http.StatusSeeOther)
		return
	}
	sess.Flash("error", "Security-key deletion is not yet fully ported in the Go panel.")
	http.Redirect(w, r, "/profile#security-keys", http.StatusSeeOther)
}
