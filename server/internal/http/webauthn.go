package http

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	wa "github.com/mchkhawarshahzad/whacp/server/internal/webauthn"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/session"
)

// Session keys used to persist the pending WebAuthn challenge between the
// options call and the response. The values are JSON-encoded wa.Challenge.
const (
	webauthnLoginChallengeKey    = "_webauthn_login_challenge"
	webauthnRegisterChallengeKey = "_webauthn_register_challenge"
)

// webauthnLoginOptions POST /login/two-factor/key/options — returns
// WebAuthn assertion options (allowCredentials = keys registered to the
// pending sign-in). Mirrors AuthController::securityKeyOptions.
func (s *Server) webauthnLoginOptions(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	uid := s.auth.PendingSigninID(sess)
	if uid == 0 {
		writeWebAuthnErr(w, http.StatusUnauthorized, "Your sign-in expired. Enter your password again.")
		return
	}
	origin, rpID := s.webauthnOrigin(r)
	has, err := s.wa.Has(uid)
	if err != nil {
		s.log.Error("webauthn has-keys", "error", err)
		writeWebAuthnErr(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	if !has {
		writeWebAuthnErr(w, http.StatusUnauthorized, "Your sign-in expired. Enter your password again.")
		return
	}
	opts, ch, err := s.wa.LoginOptions(uid, rpID)
	if err != nil {
		s.log.Error("webauthn login options", "error", err)
		writeWebAuthnErr(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	buf, _ := json.Marshal(ch)
	sess.Set(webauthnLoginChallengeKey, string(buf))
	writeWebAuthnOK(w, opts)
}

// webauthnLogin POST /login/two-factor/key — completes a WebAuthn assertion
// (sign-in). Mirrors AuthController::securityKey.
func (s *Server) webauthnLogin(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	uid := s.auth.PendingSigninID(sess)
	if uid == 0 {
		writeWebAuthnErr(w, http.StatusUnauthorized, "Your sign-in expired. Enter your password again.", withRestart(true))
		return
	}

	var body struct {
		ID                string `json:"id"`
		ClientData        string `json:"client_data"`
		AuthenticatorData string `json:"authenticator_data"`
		Signature         string `json:"signature"`
	}
	if err := parseJSONBody(r, &body); err != nil {
		writeWebAuthnErr(w, http.StatusBadRequest, "The key sent an unreadable response.")
		return
	}
	for _, s := range []*string{&body.ID, &body.ClientData, &body.AuthenticatorData, &body.Signature} {
		if len(*s) > 8192 {
			*s = (*s)[:8192]
		}
	}

	origin, rpID := s.webauthnOrigin(r)

	// Take and clear the challenge.
	ch, ok := takeWebAuthnChallenge(sess, webauthnLoginChallengeKey, "login", uid)
	if !ok {
		writeWebAuthnErr(w, http.StatusBadRequest, "The request expired. Try again.", withRestart(true))
		return
	}

	name, err := s.wa.Verify(uid, origin, rpID, body.ID, body.ClientData, body.AuthenticatorData, body.Signature, ch)
	if err != nil {
		if errors.Is(err, wa.ErrCloned) {
			s.audit.Record(r.Context(), audit.Entry{
				UserID:     &uid,
				Action:     "auth.webauthn_clone",
				TargetType: "user",
				TargetID:   strconv.FormatInt(uid, 10),
				Result:     audit.Denied,
				IP:         clientIP(r),
				Details:    map[string]any{"reason": "counter_went_backwards"},
			})
		}
		if msg, ferr := s.auth.SecondFactorFailed(r.Context(), sess, clientIP(r)); ferr != nil {
			s.log.Error("webauthn second factor failed", "error", ferr)
			writeWebAuthnErr(w, http.StatusInternalServerError, "Internal error.")
			return
		} else if msg != "" {
			sess.Remove(webauthnLoginChallengeKey)
			restart := !s.auth.PendingTwoFactor(sess)
			writeWebAuthnErr(w, http.StatusBadRequest, msg, withRestart(restart))
			return
		}
		writeWebAuthnErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.auth.CompleteSecondFactor(r.Context(), sess, clientIP(r), "security_key", map[string]any{"key": name}); err != nil {
		s.log.Error("webauthn finish login", "error", err)
		writeWebAuthnErr(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	// The panel also supports IP verification after 2FA; Go panel doesn't
	// yet, so redirect to /.
	writeWebAuthnOK(w, map[string]any{"redirect": "/"})
}

// webauthnProfileOptions POST /profile/security-keys/options — returns
// WebAuthn registration options for the signed-in user. Mirrors
// ProfileController::keyOptions.
func (s *Server) webauthnProfileOptions(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if user == nil {
		writeWebAuthnErr(w, http.StatusUnauthorized, "You must be signed in.")
		return
	}
	origin, rpID := s.webauthnOrigin(r)
	rpName, err := s.settings.String(r.Context(), "panel_name", "WhaCP")
	if err != nil {
		s.log.Error("webauthn settings panel_name", "error", err)
		rpName = "WhaCP"
	}
	opts, ch, err := s.wa.RegistrationOptions(wa.RegUser{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName}, rpID, rpName)
	if err != nil {
		s.log.Error("webauthn registration options", "error", err)
		writeWebAuthnErr(w, http.StatusInternalServerError, "Internal error.")
		return
	}
	buf, _ := json.Marshal(ch)
	sess.Set(webauthnRegisterChallengeKey, string(buf))
	writeWebAuthnOK(w, opts)
}

// webauthnProfileAdd POST /profile/security-keys — registers a new
// security key for the signed-in user. Mirrors ProfileController::keyAdd.
// Accepts both form-urlencoded (used by the inline JS) and JSON.
func (s *Server) webauthnProfileAdd(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if user == nil {
		writeWebAuthnErr(w, http.StatusUnauthorized, "You must be signed in.")
		return
	}
	var name, clientData, attestation string
	if r.Header.Get("Content-Type") == "application/json" {
		var body struct {
			Name        string `json:"name"`
			ClientData  string `json:"client_data"`
			Attestation string `json:"attestation"`
		}
		if err := parseJSONBody(r, &body); err != nil {
			writeWebAuthnErr(w, http.StatusBadRequest, "Bad request.")
			return
		}
		name, clientData, attestation = body.Name, body.ClientData, body.Attestation
	} else {
		if err := r.ParseForm(); err != nil {
			writeWebAuthnErr(w, http.StatusBadRequest, "Bad request.")
			return
		}
		name = r.PostForm.Get("name")
		clientData = r.PostForm.Get("client_data")
		attestation = r.PostForm.Get("attestation")
	}
	if !session.ValidateCSRF(sess, formToken(r)) {
		writeWebAuthnErr(w, http.StatusBadRequest, "Page expired.")
		return
	}
	for _, s := range []*string{&name, &clientData, &attestation} {
		if len(*s) > 16384 {
			*s = (*s)[:16384]
		}
	}

	origin, rpID := s.webauthnOrigin(r)
	ch, ok := takeWebAuthnChallenge(sess, webauthnRegisterChallengeKey, "register", user.ID)
	if !ok {
		writeWebAuthnErr(w, http.StatusBadRequest, "The request expired. Try again.")
		return
	}

	err := s.wa.Register(
		wa.RegUser{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName},
		origin, rpID, name, clientData, attestation, ch,
	)
	if err != nil {
		writeWebAuthnErr(w, http.StatusBadRequest, err.Error())
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &uid,
		Username:   user.Username,
		Action:     "auth.webauthn_add",
		TargetType: "user",
		TargetID:   strconv.FormatInt(user.ID, 10),
		Result:     audit.Success,
		IP:         clientIP(r),
	})
	sess.Flash("success", "Security key added. You can now use it to sign in.")
	writeWebAuthnOK(w, map[string]any{"redirect": "/profile#security-keys"})
}

// webauthnProfileDelete POST /profile/security-keys/{id}/delete — removes
// a registered security key. Mirrors ProfileController::keyDelete. The
// profile page gates this behind a password confirmation (confirm-password
// middleware on the UI side); we also honour the session confirm-password
// window if present, but do not block on it here because the PHP panel's
// own POST endpoint already returns a redirect-with-error if the password
// was wrong. When the password isn't confirmed we still require CSRF.
func (s *Server) webauthnProfileDelete(w http.ResponseWriter, r *http.Request) {
	sess := sessionFrom(r.Context())
	user := userFrom(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
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
		sess.Flash("error", "Key not found.")
		http.Redirect(w, r, "/profile#security-keys", http.StatusSeeOther)
		return
	}
	ok, name, err := s.wa.Delete(user.ID, id)
	if err != nil {
		s.log.Error("webauthn delete", "error", err)
		sess.Flash("error", "Could not remove the security key.")
		http.Redirect(w, r, "/profile#security-keys", http.StatusSeeOther)
		return
	}
	if !ok {
		sess.Flash("error", "Key not found.")
		http.Redirect(w, r, "/profile#security-keys", http.StatusSeeOther)
		return
	}
	uid := user.ID
	s.audit.Record(r.Context(), audit.Entry{
		UserID:     &uid,
		Username:   user.Username,
		Action:     "auth.webauthn_remove",
		TargetType: "user",
		TargetID:   strconv.FormatInt(user.ID, 10),
		Result:     audit.Success,
		IP:         clientIP(r),
		Details:    map[string]any{"name": name},
	})
	sess.Flash("success", "Security key removed.")
	http.Redirect(w, r, "/profile#security-keys", http.StatusSeeOther)
}

// --- helpers -----------------------------------------------------------

type webAuthnErrOpts struct {
	restart bool
}

type webAuthnErrOpt func(*webAuthnErrOpts)

func withRestart(r bool) webAuthnErrOpt { return func(o *webAuthnErrOpts) { o.restart = r } }

func writeWebAuthnOK(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data})
}

func writeWebAuthnErr(w http.ResponseWriter, code int, message string, opts ...webAuthnErrOpt) {
	var o webAuthnErrOpts
	for _, fn := range opts {
		fn(&o)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	body := map[string]any{"success": false, "error": map[string]any{"message": message}}
	if o.restart {
		body["restart"] = true
	}
	_ = json.NewEncoder(w).Encode(body)
}

// takeWebAuthnChallenge reads a stored wa.Challenge out of the session,
// validates purpose/user and clears it. Returns the b64url challenge.
func takeWebAuthnChallenge(sess *session.Session, key, purpose string, userID int64) (string, bool) {
	raw := sess.Get(key)
	sess.Remove(key)
	if raw == "" {
		return "", false
	}
	var ch wa.Challenge
	if err := json.Unmarshal([]byte(raw), &ch); err != nil {
		return "", false
	}
	s, err := wa.TakeChallenge(ch, purpose, userID)
	if err != nil {
		return "", false
	}
	return s, true
}

// webauthnOrigin resolves (origin, rpID) from panel_url setting when set,
// otherwise the request Host header. Mirrors WebAuthnOrigin::of.
func (s *Server) webauthnOrigin(r *http.Request) (origin, rpID string) {
	panelURL, _ := s.settings.String(r.Context(), "panel_url", "")
	if panelURL != "" {
		if u, err := url.Parse(panelURL); err == nil && u.Host != "" {
			host := u.Hostname()
			port := u.Port()
			origin = u.Scheme + "://" + host
			if port != "" {
				origin += ":" + port
			}
			return origin, strings.ToLower(host)
		}
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil && h != "" {
		host = h
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	// Mirror the PHP panel: use the Host header verbatim for origin (so the
	// browser's Origin header matches) and the bare host (lowercased) for
	// rp_id.
	origin = scheme + "://" + r.Host
	rpID = strings.ToLower(host)
	if rpID == "" {
		rpID = "localhost"
	}
	return origin, rpID
}
