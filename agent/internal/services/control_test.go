package services

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// Rocky Linux 9 report: starting nimvor-opendkim.service before email was
// set up failed with systemd's "Unit … not found". It is refused with a
// clear message, and systemctl start is not run.
func TestControlRefusesMissingUnit(t *testing.T) {
	h := testhost.New(t)
	h.Respond = func(id string, a []string) (string, error, bool) {
		if id == "systemctl" && len(a) > 0 && a[0] == "show" {
			return "Id=nimvor-opendkim.service\nLoadState=not-found\nActiveState=inactive\n", nil, true
		}
		return "", nil, false
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("services.control")
	_, err := a.Handler(context.Background(), json.RawMessage(`{"unit":"nimvor-opendkim.service","op":"start"}`))
	if err == nil || !strings.Contains(err.Error(), "not set up yet") {
		t.Fatalf("err = %v", err)
	}
	if h.Runner.Called("systemctl start -- nimvor-opendkim.service") {
		t.Fatal("started a unit that does not exist")
	}
}

// Restarting Nginx from the panel cut the connection carrying its own answer ("Failed to fetch"): the
// configuration is tested first, then the answer goes out and the restart follows a moment later.
func TestNginxRestartIsDeferredAfterItsTest(t *testing.T) {
	old := restartDelay
	restartDelay = 20 * time.Millisecond
	defer func() { restartDelay = old }()
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("services.control")
	res, err := a.Handler(context.Background(), json.RawMessage(`{"unit":"nginx.service","op":"restart"}`))
	if err != nil {
		t.Fatal(err)
	}
	m, _ := res.(map[string]any)
	if m["scheduled"] != true {
		t.Fatalf("res = %v", res)
	}
	if !h.Runner.Called("nginx -t") {
		t.Fatal("the configuration was not tested before answering")
	}
	if h.Runner.Called("systemctl restart nginx.service") || h.Runner.Called("systemctl restart -- nginx.service") {
		t.Fatal("restarted before the answer went out")
	}
	time.Sleep(300 * time.Millisecond)
	if !h.Runner.Called("systemctl restart nginx.service") && !h.Runner.Called("systemctl restart -- nginx.service") {
		t.Fatalf("Nginx was never restarted: %v", h.Runner.Calls)
	}
}

func TestNginxRestartWithABadConfigurationIsRefusedAtOnce(t *testing.T) {
	h := testhost.New(t)
	h.Fail["nginx -t"] = "nginx: [emerg] unexpected end of file"
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("services.control")
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"unit":"nginx.service","op":"restart"}`)); err == nil {
		t.Fatal("a configuration that does not pass its test was restarted")
	}
	time.Sleep(100 * time.Millisecond)
	if h.Runner.Called("systemctl restart nginx.service") || h.Runner.Called("systemctl restart -- nginx.service") {
		t.Fatal("restarted although the test failed")
	}
}

func TestNginxReloadStaysImmediate(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("services.control")
	res, err := a.Handler(context.Background(), json.RawMessage(`{"unit":"nginx.service","op":"reload"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := res.(map[string]any); m["scheduled"] == true {
		t.Fatalf("a reload (graceful, keeps connections) should not be deferred: %v", res)
	}
}
