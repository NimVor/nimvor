package clientip

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func req(remote, xff string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = remote
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestFromRequest(t *testing.T) {
	nets, bad := Parse("127.0.0.1, ::1")
	if len(bad) != 0 {
		t.Fatal(bad)
	}
	SetTrusted(nets)
	cases := []struct{ remote, xff, want string }{
		{"127.0.0.1:5000", "203.0.113.9", "203.0.113.9"},
		{"127.0.0.1:5000", "198.51.100.1, 203.0.113.9", "203.0.113.9"}, // right-most untrusted hop
		{"127.0.0.1:5000", "203.0.113.9, 127.0.0.1", "203.0.113.9"},    // trusted hops skipped
		{"203.0.113.50:5000", "10.9.9.9", "203.0.113.50"},              // untrusted peer cannot spoof
		{"127.0.0.1:5000", "", "127.0.0.1"},                            // no header
		{"127.0.0.1:5000", "not-an-ip", "127.0.0.1"},                   // malformed header ignored
		{"[::1]:5000", "2001:db8::1", "2001:db8::1"},
	}
	for _, c := range cases {
		if got := FromRequest(req(c.remote, c.xff)); got != c.want {
			t.Errorf("remote=%s xff=%q: got %s want %s", c.remote, c.xff, got, c.want)
		}
	}
}
