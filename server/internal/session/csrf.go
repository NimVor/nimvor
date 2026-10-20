package session

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
)

// csrfKey is the session key holding the synchronizer token. It matches the
// PHP Csrf class (Security\Csrf): one 64-hex-char token per session, compared
// in constant time, accepted from the "_token" field or the X-CSRF-Token
// header by the verifying middleware.
const csrfKey = "_csrf_token"

// CSRFToken returns the session's CSRF token, minting one (and storing it) if
// absent or malformed. Mirrors Csrf::token().
func CSRFToken(s *Session) string {
	tok := s.Get(csrfKey)
	if len(tok) != 64 {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic("csrf: entropy source failed: " + err.Error())
		}
		tok = hex.EncodeToString(b[:])
		s.Set(csrfKey, tok)
	}
	return tok
}

// ValidateCSRF reports whether submitted matches the session token in constant
// time. Mirrors Csrf::validate(): empty or missing tokens never validate.
func ValidateCSRF(s *Session, submitted string) bool {
	expected := s.Get(csrfKey)
	if expected == "" || submitted == "" || len(expected) != len(submitted) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(submitted)) == 1
}

// RotateCSRF drops the current token so the next CSRFToken call mints a new
// one. Mirrors Csrf::rotate(); called after login / privilege change.
func RotateCSRF(s *Session) { s.Remove(csrfKey) }
