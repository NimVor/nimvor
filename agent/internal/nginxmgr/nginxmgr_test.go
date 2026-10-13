package nginxmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func confRealPath(h *testhost.Host) string {
	return filepath.Join(h.Base, strings.TrimPrefix(filepath.Dir(h.Env.M.NginxAvailable), "/"), "conf.d", TuningFile)
}

func TestTuningAppliesAndRollsBack(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	apply, _ := r.Lookup("nginx.tuning_apply")
	os.MkdirAll(filepath.Dir(confRealPath(h)), 0o755)

	testOK := true
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "nginx" && len(args) > 0 && args[0] == "-t" && !testOK {
			return "nginx: [emerg] invalid value", &os.PathError{Op: "exit", Path: "status 1"}
		}
		return "", nil
	}

	good := `{"client_max_body_mb":128,"keepalive_timeout":65,"client_body_timeout":60,"send_timeout":60,"gzip":true,"hide_version":true}`
	if _, err := apply.Handler(context.Background(), json.RawMessage(good)); err != nil {
		t.Fatal(err)
	}
	conf := confRealPath(h)
	body, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("tuning file not written: %v", err)
	}
	for _, want := range []string{"client_max_body_size 128m;", "keepalive_timeout 65s;", "server_tokens off;", "gzip on;"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("conf lacks %q:\n%s", want, body)
		}
	}

	// A change that makes nginx -t fail is rolled back to the previous file.
	testOK = false
	bad := `{"client_max_body_mb":9999999,"keepalive_timeout":65,"client_body_timeout":60,"send_timeout":60}`
	if _, err := apply.Handler(context.Background(), json.RawMessage(bad)); err == nil {
		t.Fatal("an invalid value was accepted (out of range)")
	}
	// A range-valid change that nginx nonetheless rejects is rolled back: the
	// previous good file remains.
	if _, err := apply.Handler(context.Background(), json.RawMessage(`{"client_max_body_mb":256,"keepalive_timeout":65,"client_body_timeout":60,"send_timeout":60}`)); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("a configuration nginx rejects must roll back: %v", err)
	}
	if body2, _ := os.ReadFile(conf); !strings.Contains(string(body2), "client_max_body_size 128m;") {
		t.Errorf("the previous tuning was not restored on rollback:\n%s", body2)
	}
}

func TestStatusAndTest(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "systemctl" && len(args) >= 2 && args[0] == "is-active" {
			return "active\n", nil
		}
		return "", nil // nginx -t ok
	}
	st, _ := r.Lookup("nginx.status")
	res, err := st.Handler(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]any)
	if m["installed"] != true || m["running"] != true || m["config_ok"] != true {
		t.Fatalf("status: %+v", m)
	}

	tst, _ := r.Lookup("nginx.test")
	res, _ = tst.Handler(context.Background(), json.RawMessage(`{}`))
	if res.(map[string]any)["ok"] != true {
		t.Fatalf("test: %+v", res)
	}

	// Without nginx installed, the management actions are refused.
	h.Runner.Unavailable["nginx"] = true
	if _, err := tst.Handler(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Error("nginx.test should require nginx installed")
	}
}
