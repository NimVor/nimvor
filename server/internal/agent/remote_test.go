package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRemoteClientPinsTheKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/v1/actions/system.forbidden" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"error":{"code":"forbidden","message":"no"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"ok":true}}`))
	}))
	defer srv.Close()
	pin := PinOf(srv.Certificate())
	if !ValidPin(pin) {
		t.Fatalf("pin %q is not valid", pin)
	}
	url := "https://" + srv.Listener.Addr().String()
	if !ValidRemoteURL(url) {
		t.Fatalf("url %q is not valid", url)
	}
	ctx := context.Background()

	c := NewRemote(url, pin, "tok", 5*time.Second)
	raw, err := c.Call(ctx, "system.info", nil)
	if err != nil || string(raw) != `{"ok":true}` {
		t.Fatalf("Call = %s, %v", raw, err)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}

	// Another key: refused in the handshake, the token never sent.
	gotAuth = ""
	wrong := "sha256//" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	_, err = NewRemote(url, wrong, "tok", 5*time.Second).Call(ctx, "system.info", nil)
	var ae *Error
	if !errors.As(err, &ae) || ae.Code != "agent_pin_mismatch" || gotAuth != "" {
		t.Fatalf("pin mismatch = %v (auth sent: %q)", err, gotAuth)
	}

	// The agent refusing this panel.
	_, err = c.Call(ctx, "system.forbidden", nil)
	if !errors.As(err, &ae) || ae.Code != "agent_forbidden" {
		t.Errorf("forbidden = %v", err)
	}

	// Nothing listening; a bad configuration.
	srv.Close()
	if _, err = c.Call(ctx, "system.info", nil); !errors.As(err, &ae) || ae.Code != "agent_unavailable" {
		t.Errorf("closed = %v", err)
	}
	if _, err = NewRemote("http://x", "bad", "t", time.Second).Call(ctx, "system.info", nil); !errors.As(err, &ae) || ae.Code != "agent_unavailable" {
		t.Errorf("bad config = %v", err)
	}
}
