package session

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mchkhawarshahzad/whacp/server/internal/crypto"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	var key [32]byte
	for i := range key {
		key[i] = byte(i)
	}
	return NewManager(crypto.NewWithKey(key), "whacp_session", true)
}

// roundTrip saves s onto a response, then loads a fresh session from the
// resulting cookie (simulating the next request).
func roundTrip(t *testing.T, m *Manager, s *Session) *Session {
	t.Helper()
	rr := httptest.NewRecorder()
	if err := m.Save(rr, s); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/", nil)
	for _, c := range rr.Result().Cookies() {
		req.AddCookie(c)
	}
	return m.Load(req)
}

func TestValuesPersistAcrossRequests(t *testing.T) {
	m := testManager(t)
	s := m.Load(httptest.NewRequest("GET", "/", nil))
	s.Set("user_id", "42")
	next := roundTrip(t, m, s)
	if got := next.Get("user_id"); got != "42" {
		t.Fatalf("user_id = %q, want 42", got)
	}
}

func TestCookieIsHardened(t *testing.T) {
	m := testManager(t)
	s := m.Load(httptest.NewRequest("GET", "/", nil))
	s.Set("k", "v")
	rr := httptest.NewRecorder()
	if err := m.Save(rr, s); err != nil {
		t.Fatal(err)
	}
	c := rr.Result().Cookies()[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Fatalf("cookie not hardened: %+v", c)
	}
	// The sealed value must not leak the plaintext.
	if contains(c.Value, "user") || c.Value == "" {
		t.Fatalf("cookie value looks unsealed: %q", c.Value)
	}
}

func TestTamperedCookieYieldsEmptySession(t *testing.T) {
	m := testManager(t)
	s := m.Load(httptest.NewRequest("GET", "/", nil))
	s.Set("user_id", "7")
	rr := httptest.NewRecorder()
	_ = m.Save(rr, s)
	c := rr.Result().Cookies()[0]
	b := []byte(c.Value)
	b[len(b)-3] ^= 0x01
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "whacp_session", Value: string(b)})
	if got := m.Load(req).Get("user_id"); got != "" {
		t.Fatalf("tampered cookie exposed value %q", got)
	}
}

func TestFlashIsOneShot(t *testing.T) {
	m := testManager(t)
	s := m.Load(httptest.NewRequest("GET", "/", nil))
	s.Flash("status", "saved")
	// Not readable on the same request.
	if _, ok := s.PullFlash("status"); ok {
		t.Fatal("flash readable on the same request")
	}
	next := roundTrip(t, m, s)
	v, ok := next.PullFlash("status")
	if !ok || v != "saved" {
		t.Fatalf("flash = %q,%v want saved,true", v, ok)
	}
	// Consumed: a further round trip must not carry it.
	after := roundTrip(t, m, next)
	if _, ok := after.PullFlash("status"); ok {
		t.Fatal("flash survived consumption")
	}
}

func TestInvalidateClearsCookie(t *testing.T) {
	m := testManager(t)
	s := m.Load(httptest.NewRequest("GET", "/", nil))
	s.Set("user_id", "9")
	s.Invalidate()
	rr := httptest.NewRecorder()
	_ = m.Save(rr, s)
	c := rr.Result().Cookies()[0]
	if c.MaxAge >= 0 {
		t.Fatalf("invalidate did not delete cookie: MaxAge=%d", c.MaxAge)
	}
}

func TestCSRFLifecycle(t *testing.T) {
	m := testManager(t)
	s := m.Load(httptest.NewRequest("GET", "/", nil))
	tok := CSRFToken(s)
	if len(tok) != 64 {
		t.Fatalf("token length = %d", len(tok))
	}
	if CSRFToken(s) != tok {
		t.Fatal("token not stable within a session")
	}
	if !ValidateCSRF(s, tok) {
		t.Fatal("valid token rejected")
	}
	if ValidateCSRF(s, "") || ValidateCSRF(s, "wrong") || ValidateCSRF(s, tok[:63]+"0") {
		t.Fatal("invalid token accepted")
	}
	// Survives a round trip.
	next := roundTrip(t, m, s)
	if !ValidateCSRF(next, tok) {
		t.Fatal("token lost across requests")
	}
	RotateCSRF(next)
	if ValidateCSRF(next, tok) {
		t.Fatal("rotated token still validates")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
