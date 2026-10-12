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

func TestApplyRunsSeveralCopies(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	apply, _ := r.Lookup("workers.apply")
	base := `"slug":"q","name":"Queue","domain":"a.test","user":"example","command":"php artisan queue:work","enabled":true`

	// procs > MaxProcs is rejected.
	if _, err := apply.Handler(context.Background(), json.RawMessage(`{`+base+`,"procs":21}`)); err == nil {
		t.Fatal("accepted procs over the maximum")
	}

	// Three copies: the primary keeps the plain unit name, the extras are
	// siblings, each carrying its own instance number.
	if _, err := apply.Handler(context.Background(), json.RawMessage(`{`+base+`,"procs":3}`)); err != nil {
		t.Fatal(err)
	}
	for n, name := range []string{"whacp-worker-q.service", "whacp-worker-q-2.service", "whacp-worker-q-3.service"} {
		path := "etc/systemd/system/" + name
		if !h.Exists(path) {
			t.Fatalf("copy %d unit missing: %s", n+1, name)
		}
		u := h.Read(path)
		if !strings.Contains(u, "ExecStart=/bin/sh /var/lib/whacp-workers/q.sh") {
			t.Errorf("%s does not run the shared launcher:\n%s", name, u)
		}
		wantInstance := "WHACP_WORKER_INSTANCE=" + itoa(n+1)
		if !strings.Contains(u, wantInstance) {
			t.Errorf("%s lacks %q", name, wantInstance)
		}
		for _, verb := range []string{"enable", "restart"} {
			if !h.Runner.Called("systemctl " + verb + " -- " + name) {
				t.Errorf("copy %s not %sed", name, verb)
			}
		}
	}
	if !strings.Contains(h.Read("etc/systemd/system/whacp-worker-q-2.service"), "copy 2") {
		t.Error("extra copy is not labelled")
	}

	// Aggregated status over all copies: systemd reports each copy; the
	// worker is one entry with the copy counts and summed memory.
	h.Respond = func(id string, a []string) (string, error, bool) {
		if id != "systemctl" || len(a) == 0 || a[0] != "show" {
			return "", nil, false
		}
		var b strings.Builder
		for _, u := range []string{"whacp-worker-q.service", "whacp-worker-q-2.service", "whacp-worker-q-3.service"} {
			b.WriteString("Id=" + u + "\nLoadState=loaded\nActiveState=active\nSubState=running\nMainPID=10\nMemoryCurrent=10485760\nActiveEnterTimestamp=Tue 2026-09-22 10:11:12 UTC\n\n")
		}
		return b.String(), nil, true
	}
	st, _ := r.Lookup("workers.status")
	res, err := st.Handler(context.Background(), json.RawMessage(`{"slugs":["q"]}`))
	if err != nil {
		t.Fatal(err)
	}
	rows := res.([]map[string]any)
	if len(rows) != 1 {
		t.Fatalf("status rows: %+v", rows)
	}
	row := rows[0]
	if row["unit"] != "whacp-worker-q.service" || row["procs_total"].(int) != 3 || row["procs_running"].(int) != 3 || row["running"] != true {
		t.Fatalf("aggregated status: %+v", row)
	}
	if m, ok := row["memory_bytes"].(uint64); !ok || m != 3*10485760 {
		t.Fatalf("memory not summed: %+v", row["memory_bytes"])
	}
	h.Respond = nil

	// Scaling down to one copy stops, disables and removes the extras; the
	// primary stays.
	h.Runner.Calls = nil
	if _, err := apply.Handler(context.Background(), json.RawMessage(`{`+base+`,"procs":1}`)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"whacp-worker-q-2.service", "whacp-worker-q-3.service"} {
		if h.Exists("etc/systemd/system/" + name) {
			t.Errorf("%s was not removed when scaling down", name)
		}
		if !h.Runner.Called("systemctl stop -- "+name) || !h.Runner.Called("systemctl disable -- "+name) {
			t.Errorf("%s not stopped/disabled when scaling down", name)
		}
	}
	if !h.Exists("etc/systemd/system/whacp-worker-q.service") {
		t.Error("the primary copy was removed when scaling down")
	}

	// Remove takes every copy; recreate three first.
	if _, err := apply.Handler(context.Background(), json.RawMessage(`{`+base+`,"procs":3}`)); err != nil {
		t.Fatal(err)
	}
	rm, _ := r.Lookup("workers.remove")
	if _, err := rm.Handler(context.Background(), json.RawMessage(`{"slug":"q"}`)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"whacp-worker-q.service", "whacp-worker-q-2.service", "whacp-worker-q-3.service"} {
		if h.Exists("etc/systemd/system/" + name) {
			t.Errorf("%s not removed", name)
		}
	}
	if h.Exists("var/lib/whacp-workers/q.sh") {
		t.Error("launcher not removed")
	}
}

func itoa(n int) string { return string(rune('0' + n)) }
