package workers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func TestApplyWritesSandboxedUnitAndScript(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("workers.apply")
	// A launcher left in the state directory by an earlier version.
	os.MkdirAll(filepath.Join(h.Base, "var/lib/whacp-agent/workers"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "var/lib/whacp-agent/workers/7-queue.sh"), []byte("#!/bin/sh\n"), 0o755)
	_, err := a.Handler(context.Background(), json.RawMessage(`{"slug":"7-queue","name":"Queue","domain":"a.test","user":"example","directory":"public_html","command":"php artisan queue:work --sleep=3","env":{"APP_ENV":"production","QUOTE":"it's"},"memory_mb":256,"enabled":true}`))
	if err != nil {
		t.Fatal(err)
	}
	unit := h.Read("etc/systemd/system/whacp-worker-7-queue.service")
	for _, want := range []string{"User=example", "ExecStart=/bin/sh /var/lib/whacp-workers/7-queue.sh", "NoNewPrivileges=yes", "MemoryMax=256M", "Restart=always"} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, "artisan") {
		t.Error("user command must not appear in the unit file")
	}
	script := h.Read("var/lib/whacp-workers/7-queue.sh")
	// The account reads its launcher (group), nobody else can (it holds the
	// worker's environment); the folder cannot be listed.
	if fi, _ := os.Stat(filepath.Join(h.Base, "var/lib/whacp-workers/7-queue.sh")); fi == nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("launcher mode: %v", fi)
	}
	if fi, _ := os.Stat(filepath.Join(h.Base, "var/lib/whacp-workers")); fi == nil || fi.Mode().Perm() != 0o711 {
		t.Errorf("launcher folder mode: %v", fi)
	}
	if h.Exists("var/lib/whacp-agent/workers/7-queue.sh") {
		t.Error("the old launcher was not removed")
	}
	if !strings.Contains(script, `export QUOTE='it'\''s'`) || !strings.Contains(script, "exec php artisan queue:work") {
		t.Errorf("script:\n%s", script)
	}
	for _, c := range []string{"systemctl daemon-reload", "systemctl enable -- whacp-worker-7-queue.service", "systemctl restart -- whacp-worker-7-queue.service"} {
		if !h.Runner.Called(c) {
			t.Errorf("missing %q in %v", c, h.Runner.Calls)
		}
	}
	for _, bad := range []string{
		`{"slug":"x","name":"x","domain":"a.test","user":"root","command":"id","enabled":true}`,
		`{"slug":"X Y","name":"x","domain":"a.test","user":"example","command":"id"}`,
		`{"slug":"x","name":"x","domain":"a.test","user":"example","directory":"../../etc","command":"id"}`,
		`{"slug":"x","name":"x","domain":"a.test","user":"example","command":"id","env":{"bad-key":"v"}}`,
		`{"slug":"x","name":"x","domain":"a.test","user":"example","command":"id","env":{"K":"a\nb"}}`,
	} {
		if _, err := a.Handler(context.Background(), json.RawMessage(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	rm, _ := r.Lookup("workers.remove")
	if _, err := rm.Handler(context.Background(), json.RawMessage(`{"slug":"7-queue"}`)); err != nil {
		t.Fatal(err)
	}
	if h.Exists("etc/systemd/system/whacp-worker-7-queue.service") || h.Exists("var/lib/whacp-workers/7-queue.sh") {
		t.Error("unit or launcher not removed")
	}
}
