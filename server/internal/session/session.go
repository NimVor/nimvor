// Package session implements the panel's session as a sealed cookie.
// No session files are kept on disk: the session payload is JSON, sealed with
// NaCl secretbox under the panel key (internal/crypto), so it is tamper-proof
// and readable only by the panel. Cookie attributes (HttpOnly, SameSite=Lax,
// Secure in production) are strict and production-ready.
package session

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
)

// DefaultCookieName is the session cookie name.
const DefaultCookieName = "whacp_session"

// DefaultMaxAge is the session idle timeout — 24 hours.
const DefaultMaxAge = 24 * time.Hour

// Manager seals and opens sessions.
type Manager struct {
	crypto     *crypto.Crypto
	cookieName string
	secure     bool
	maxAge     time.Duration
}

// NewManager builds a session Manager. secure sets the cookie Secure flag
// (true behind TLS in production).
func NewManager(c *crypto.Crypto, cookieName string, secure bool) *Manager {
	if cookieName == "" {
		cookieName = DefaultCookieName
	}
	return &Manager{crypto: c, cookieName: cookieName, secure: secure, maxAge: DefaultMaxAge}
}

// payload is the wire form of a session.
type payload struct {
	Data     map[string]string `json:"d,omitempty"`
	Flash    map[string]string `json:"f,omitempty"`
	IssuedAt int64             `json:"iat"`
}

// Session is the per-request session. Values and the next-request flash are
// mutated by handlers; flash carries values flashed on the previous request.
type Session struct {
	Values   map[string]string // persistent values (user id, csrf token, epoch)
	flash    map[string]string // flashed on the previous request (read this one)
	newFlash map[string]string // flashed this request (persisted for the next)
	issuedAt int64
	dirty    bool
}

// Load decodes the session cookie. A missing, unreadable, tampered or expired
// cookie yields a fresh empty session (never an error) — the request simply
// proceeds unauthenticated.
func (m *Manager) Load(r *http.Request) *Session {
	s := &Session{
		Values:   map[string]string{},
		flash:    map[string]string{},
		newFlash: map[string]string{},
		issuedAt: time.Now().Unix(),
	}
	c, err := r.Cookie(m.cookieName)
	if err != nil || c.Value == "" {
		return s
	}
	raw, err := m.crypto.Decrypt(c.Value)
	if err != nil {
		return s
	}
	var p payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return s
	}
	if p.IssuedAt > 0 && time.Since(time.Unix(p.IssuedAt, 0)) > m.maxAge {
		return s // expired: treat as empty
	}
	if p.Data != nil {
		s.Values = p.Data
	}
	if p.Flash != nil {
		s.flash = p.Flash
	}
	s.issuedAt = p.IssuedAt
	return s
}

// Save writes the session cookie when the session changed, or clears it when
// the session was invalidated. It must be called before the response body is
// written.
func (m *Manager) Save(w http.ResponseWriter, s *Session) error {
	if !s.dirty {
		return nil
	}
	// An invalidated session (no values, no new flash) clears the cookie.
	if len(s.Values) == 0 && len(s.newFlash) == 0 {
		http.SetCookie(w, m.cookie("", -1))
		return nil
	}
	p := payload{Data: s.Values, Flash: s.newFlash, IssuedAt: s.issuedAt}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	sealed, err := m.crypto.Encrypt(raw)
	if err != nil {
		return err
	}
	http.SetCookie(w, m.cookie(sealed, 0))
	return nil
}

// cookie builds a Set-Cookie with the hardened attributes. maxAge 0 means a
// session cookie (expires when the browser closes), <0 deletes it.
func (m *Manager) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     m.cookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	}
}

// Get returns a stored value (empty string if absent).
func (s *Session) Get(key string) string { return s.Values[key] }

// Has reports whether key is set.
func (s *Session) Has(key string) bool { _, ok := s.Values[key]; return ok }

// Set stores a value and marks the session dirty.
func (s *Session) Set(key, value string) {
	s.Values[key] = value
	s.dirty = true
}

// Remove deletes a value.
func (s *Session) Remove(key string) {
	if _, ok := s.Values[key]; ok {
		delete(s.Values, key)
		s.dirty = true
	}
}

// Flash stores a value for the next request only.
func (s *Session) Flash(key, value string) {
	s.newFlash[key] = value
	s.dirty = true
}

// PullFlash reads (and consumes) a value flashed on the previous request.
func (s *Session) PullFlash(key string) (string, bool) {
	v, ok := s.flash[key]
	if ok {
		delete(s.flash, key)
	}
	return v, ok
}

// Regenerate issues a fresh session identity after a privilege change (login).
// With sealed-cookie sessions there is no server-side id to rotate, so this
// resets the issued-at clock and marks the session dirty, forcing a new sealed
// cookie (new nonce) to be written.
func (s *Session) Regenerate() {
	s.issuedAt = time.Now().Unix()
	s.dirty = true
}

// Invalidate clears all session data (logout). Save then deletes the cookie.
func (s *Session) Invalidate() {
	s.Values = map[string]string{}
	s.flash = map[string]string{}
	s.newFlash = map[string]string{}
	s.dirty = true
}
