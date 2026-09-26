package modules

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

// Actions that change or reload the web server configuration wait for the
// web configuration lock, so two of them never validate each other's
// half-written files.
func TestWebConfigChangesAreSerialised(t *testing.T) {
	for _, c := range []struct{ action, params string }{
		{"sites.apply", `{"domain":"a.test","user":"atest","create_user":true,"kind":"static","enabled":true}`},
		{"sites.delete", `{"domain":"a.test"}`},
		{"services.control", `{"unit":"nginx.service","op":"reload"}`},
		{"php.set_enabled", `{"version":"8.3","enabled":true}`},
	} {
		t.Run(c.action, func(t *testing.T) {
			h := testhost.New(t)
			r := actions.NewRegistry(true)
			for _, reg := range All() {
				reg(r, h.Env)
			}
			a, err := r.Lookup(c.action)
			if err != nil {
				t.Fatal(err)
			}
			unlock := h.Env.LockWebConfig()
			done := make(chan struct{})
			go func() {
				defer close(done)
				a.Handler(context.Background(), json.RawMessage(c.params))
			}()
			select {
			case <-done:
				unlock()
				t.Fatal("ran while another web configuration change held the lock")
			case <-time.After(150 * time.Millisecond):
			}
			if n := len(h.Runner.Calls); n != 0 {
				unlock()
				t.Fatalf("ran commands while waiting: %v", h.Runner.Calls)
			}
			unlock()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("did not continue after the lock was released")
			}
		})
	}
}
