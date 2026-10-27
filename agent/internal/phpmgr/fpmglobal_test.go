package phpmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestFPMGlobal(t *testing.T) {
	g := FPMGlobal{EmergencyRestartThreshold: 10, EmergencyRestartInterval: 60, ProcessControlTimeout: 10, ProcessMax: 128, LogLevel: "warning"}
	body := g.Render("8.3")
	if !strings.Contains(body, "[global]\nemergency_restart_threshold = 10\nemergency_restart_interval = 60s\nprocess_control_timeout = 10s\nprocess.max = 128\nlog_level = warning\n") {
		t.Fatalf("render:\n%s", body)
	}
	if ParseFPMGlobal(body) != g {
		t.Errorf("parse: %+v", ParseFPMGlobal(body))
	}
	if (FPMGlobal{}).Render("8.3") != "" {
		t.Error("empty settings rendered")
	}
	for _, bad := range []FPMGlobal{{EmergencyRestartThreshold: 5}, {ProcessMax: -1}, {ProcessControlTimeout: 4000}, {LogLevel: "loud"}, {LogLevel: "notice\ninclude=/tmp/x"}} {
		if bad.check() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}

	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "etc/php/8.3/fpm/pool.d"), 0o755)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	params := map[string]any{"version": "8.3", "emergency_restart_threshold": 10, "emergency_restart_interval_seconds": 60, "log_level": "warning"}
	if _, err := call(t, r, "php.fpm_set", params); err != nil {
		t.Fatal(err)
	}
	if got := h.Read("etc/php/8.3/fpm/pool.d/whacp-global.conf"); !strings.Contains(got, "[global]\nemergency_restart_threshold = 10\nemergency_restart_interval = 60s\nlog_level = warning\n") {
		t.Fatalf("file:\n%s", got)
	}
	h.Fail["php-fpm8.3 -t"] = "ERROR: failed"
	params["log_level"] = "debug"
	if _, err := call(t, r, "php.fpm_set", params); err == nil {
		t.Fatal("rejected settings accepted")
	}
	delete(h.Fail, "php-fpm8.3 -t")
	if !strings.Contains(h.Read("etc/php/8.3/fpm/pool.d/whacp-global.conf"), "log_level = warning") {
		t.Error("previous settings not restored")
	}
	if _, err := call(t, r, "php.fpm_set", map[string]any{"version": "8.3"}); err != nil || h.Exists("etc/php/8.3/fpm/pool.d/whacp-global.conf") {
		t.Errorf("clearing: %v", err)
	}
}
