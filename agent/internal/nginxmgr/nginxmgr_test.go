package nginxmgr

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/testhost"
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

// Debian and Ubuntu's nginx.conf already sets "gzip on;" (and older releases keepalive_timeout) in the
// http block, and nginx refuses a directive set twice there. The tuning comments those lines out, only
// in the http block itself, in the same change; a rollback puts them back.
func TestTuningReleasesDirectivesNginxConfAlreadySets(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	apply, _ := r.Lookup("nginx.tuning_apply")
	dir := filepath.Dir(confRealPath(h))
	os.MkdirAll(dir, 0o755)
	main := filepath.Join(filepath.Dir(dir), "nginx.conf")
	orig := "user www-data;\nevents {\n\tworker_connections 768;\n}\nhttp {\n\tsendfile on;\n\tkeepalive_timeout 65;\n\t# server_tokens off;\n\tgzip on;\n\tgzip_types text/plain\n\t\ttext/css;\n\tinclude /etc/nginx/conf.d/*.conf;\n\tserver {\n\t\tgzip off;\n\t\tlisten 80;\n\t}\n}\n"
	if err := os.WriteFile(main, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	good := `{"client_max_body_mb":64,"keepalive_timeout":30,"client_body_timeout":60,"send_timeout":60,"gzip":true,"hide_version":false}`
	if _, err := apply.Handler(context.Background(), json.RawMessage(good)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(main)
	got := string(b)
	for _, gone := range []string{"\n\tkeepalive_timeout 65;", "\n\tgzip on;", "\n\tgzip_types text/plain", "\n\t\ttext/css;"} {
		if strings.Contains(got, gone) {
			t.Errorf("still active in nginx.conf: %q\n%s", gone, got)
		}
	}
	for _, kept := range []string{"\t\tgzip off;\n", "\tsendfile on;\n", "\tinclude /etc/nginx/conf.d/*.conf;\n", "\t# server_tokens off;\n"} {
		if !strings.Contains(got, kept) {
			t.Errorf("changed although it is not the http block's own setting: %q\n%s", kept, got)
		}
	}
	// Applying again changes nothing more.
	if again, changed := releaseTunedDirectives(got); changed || again != got {
		t.Errorf("second pass changed the file:\n%s", again)
	}
	// nginx rejects the result: both files are back as they were.
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "nginx" && len(args) > 0 && args[0] == "-t" {
			return "nginx: [emerg] boom", &os.PathError{Op: "exit", Path: "status 1"}
		}
		return "", nil
	}
	os.WriteFile(main, []byte(orig), 0o644)
	if _, err := apply.Handler(context.Background(), json.RawMessage(good)); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected rollback, got %v", err)
	}
	if b, _ := os.ReadFile(main); string(b) != orig {
		t.Errorf("nginx.conf not restored:\n%s", b)
	}
}
