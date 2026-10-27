package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// Rocky Linux 9 report: starting whacp-opendkim.service before email was
// set up failed with systemd's "Unit … not found". It is refused with a
// clear message, and systemctl start is not run.
func TestControlRefusesMissingUnit(t *testing.T) {
	h := testhost.New(t)
	h.Respond = func(id string, a []string) (string, error, bool) {
		if id == "systemctl" && len(a) > 0 && a[0] == "show" {
			return "Id=whacp-opendkim.service\nLoadState=not-found\nActiveState=inactive\n", nil, true
		}
		return "", nil, false
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("services.control")
	_, err := a.Handler(context.Background(), json.RawMessage(`{"unit":"whacp-opendkim.service","op":"start"}`))
	if err == nil || !strings.Contains(err.Error(), "not set up yet") {
		t.Fatalf("err = %v", err)
	}
	if h.Runner.Called("systemctl start -- whacp-opendkim.service") {
		t.Fatal("started a unit that does not exist")
	}
}
