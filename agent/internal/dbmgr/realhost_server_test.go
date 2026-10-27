//go:build realhost

package dbmgr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

func TestRealHostDatabaseServerManager(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	ctx := context.Background()
	c := Client{Env: env}
	call := func(name, params string) (map[string]any, error) {
		a, err := r.Lookup(name)
		if err != nil {
			t.Fatalf("no action %s", name)
		}
		res, err := a.Handler(ctx, json.RawMessage(params))
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(res)
		var m map[string]any
		json.Unmarshal(b, &m)
		return m, nil
	}
	do := func(name, params string) map[string]any {
		t.Helper()
		m, err := call(name, params)
		if err != nil {
			t.Fatalf("%s %s: %v", name, params, err)
		}
		return m
	}
	path, ok := c.dropInPath()
	if !ok {
		t.Fatal("no option file directory")
	}
	orig, _ := c.variables(ctx, []string{"max_connections", "slow_query_log", "long_query_time", "character_set_server", "collation_server"})
	t.Cleanup(func() {
		call("db.server_settings", `{"settings":{"max_connections":"","slow_query_log":"","long_query_time":"","character_set_server":"","collation_server":"","skip_name_resolve":""}}`)
		os.Remove(path)
		c.Query(ctx, "SET GLOBAL max_connections = "+orig["max_connections"]+"; SET GLOBAL slow_query_log = "+orig["slow_query_log"]+"; SET GLOBAL long_query_time = "+orig["long_query_time"]+"; SET GLOBAL character_set_server = "+Quote(orig["character_set_server"])+"; SET GLOBAL collation_server = "+Quote(orig["collation_server"])+";")
	})

	st := do("db.server_status", `{}`)
	if st["running"] != true || st["config_error"] != nil || st["variables"].(map[string]any)["version"] == "" || st["status"].(map[string]any)["Uptime"] == nil {
		t.Fatalf("status = %+v", st)
	}
	if _, ok := st["collations"].(map[string]any)["utf8mb4"]; !ok {
		t.Fatalf("collations = %+v", st["collations"])
	}

	// Dynamic settings are applied at once; a static one waits for a restart.
	res := do("db.server_settings", `{"settings":{"max_connections":"222","slow_query_log":"on","long_query_time":"1.5","character_set_server":"utf8mb4","collation_server":"utf8mb4_unicode_ci","skip_name_resolve":"on"}}`)
	if rn := res["restart_needed"].([]any); len(rn) != 1 || rn[0] != "skip_name_resolve" {
		t.Fatalf("settings = %+v", res)
	}
	v, _ := c.variables(ctx, []string{"max_connections", "slow_query_log", "long_query_time", "collation_server"})
	if v["max_connections"] != "222" || v["slow_query_log"] != "ON" || !strings.HasPrefix(v["long_query_time"], "1.5") || v["collation_server"] != "utf8mb4_unicode_ci" {
		t.Fatalf("variables = %v", v)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "max_connections = 222") || !strings.Contains(string(b), "skip_name_resolve = ON") {
		t.Fatalf("drop-in:\n%s", b)
	}
	// The server program accepts the file.
	if err := c.ValidateConfig(ctx); err != nil {
		t.Fatal(err)
	}
	// Invalid input is refused before anything is written.
	for _, bad := range []string{`{"settings":{"collation_server":"latin1_swedish_ci"}}`, `{"settings":{"max_connections":"5"}}`, `{"settings":{"bind_address":"0.0.0.0"}}`} {
		if _, err := call("db.server_settings", bad); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	if b2, _ := os.ReadFile(path); string(b2) != string(b) {
		t.Fatal("refused settings changed the file")
	}
	// A file the server rejects is rolled back: simulate with an unknown
	// option in another file, then try to save.
	bogus := strings.TrimSuffix(path, DropInName) + "zz-nimvor-test-bogus.cnf"
	os.WriteFile(bogus, []byte("[mysqld]\nnimvor_no_such_option = 1\n"), 0o644)
	if _, err := call("db.server_settings", `{"settings":{"max_connections":"223"}}`); err == nil || !strings.Contains(err.Error()+detailsOf(err), "nimvor_no_such_option") {
		os.Remove(bogus)
		t.Fatalf("invalid configuration accepted: %v", err)
	}
	os.Remove(bogus)
	if b3, _ := os.ReadFile(path); string(b3) != string(b) {
		t.Fatal("the drop-in was not rolled back")
	}
	if v, _ := c.variables(ctx, []string{"max_connections"}); v["max_connections"] != "222" {
		t.Fatalf("max_connections changed despite the failure: %v", v)
	}

	// Connections: a sleeping client is listed and can be ended.
	cmd := exec.Command("mariadb", "--protocol=socket", "-e", "SELECT SLEEP(60)")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var id float64
	for i := 0; i < 50 && id == 0; i++ {
		time.Sleep(100 * time.Millisecond)
		for _, p := range do("db.processlist", `{}`)["connections"].([]any) {
			if pm := p.(map[string]any); strings.Contains(pm["info"].(string), "SLEEP(60)") {
				id = pm["id"].(float64)
			}
		}
	}
	if id == 0 {
		t.Fatal("sleeping connection not listed")
	}
	do("db.kill", `{"id":`+jsonNum(id)+`}`)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("killed connection still running")
	}
	for _, p := range do("db.processlist", `{}`)["connections"].([]any) {
		if pm := p.(map[string]any); pm["system"] == true {
			if _, err := call("db.kill", `{"id":`+jsonNum(pm["id"].(float64))+`}`); err == nil {
				t.Fatal("a server thread was killed")
			}
			break
		}
	}
	do("db.server_flush", `{"what":"privileges"}`)
	do("db.server_flush", `{"what":"hosts"}`)

	// With systemd: a checked restart applies the static setting.
	if out, err := exec.Command("systemctl", "is-system-running").Output(); err == nil || strings.TrimSpace(string(out)) == "degraded" {
		do("db.server_restart", `{}`)
		if v, _ := c.variables(ctx, []string{"skip_name_resolve", "max_connections"}); v["skip_name_resolve"] != "ON" || v["max_connections"] != "222" {
			t.Fatalf("after restart: %v", v)
		}
	}
}

func jsonNum(f float64) string { b, _ := json.Marshal(int64(f)); return string(b) }

func detailsOf(err error) string {
	if ae, ok := err.(*actions.Error); ok {
		return ae.Details
	}
	return ""
}
