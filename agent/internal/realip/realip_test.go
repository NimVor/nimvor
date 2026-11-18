package realip

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestCleanChecksAndNormalisesNetworks(t *testing.T) {
	got, err := Clean([]string{" 173.245.48.0/20 ", "173.245.48.0/20", "203.0.113.7", "2400:cb00::/32", ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "173.245.48.0/20,203.0.113.7/32,2400:cb00::/32" {
		t.Errorf("got %v", got)
	}
	for _, bad := range []string{"0.0.0.0/0", "::/0", "10.0.0.0/7", "2000::/8", "nonsense", "300.1.1.1/8"} {
		if _, err := Clean([]string{bad}); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
	if _, err := Clean(make([]string, MaxRanges+1)); err == nil {
		t.Error("an oversized list was accepted")
	}
}

func TestDefaultsAreValidAndRender(t *testing.T) {
	c, err := Clean(Defaults)
	if err != nil || len(c) != len(Defaults) {
		t.Fatalf("defaults do not pass their own check: %v %d/%d", err, len(c), len(Defaults))
	}
	out := Render(State{Enabled: true})
	for _, want := range []string{"real_ip_header CF-Connecting-IP;", "real_ip_recursive off;", "set_real_ip_from 173.245.48.0/20;", "set_real_ip_from 2400:cb00::/32;"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "set_real_ip_from 0.0.0.0") {
		t.Error("trusts everything")
	}
	custom := Render(State{Enabled: true, Ranges: []string{"203.0.113.0/24"}})
	if !strings.Contains(custom, "set_real_ip_from 203.0.113.0/24;") || strings.Contains(custom, "173.245.48.0/20") {
		t.Errorf("a custom list replaces the built-in one:\n%s", custom)
	}
}

type fixture struct {
	h *testhost.Host
	r *actions.Registry
}

func setup(t *testing.T) *fixture {
	t.Helper()
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "etc/nginx/sites-available"), 0o755)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return &fixture{h: h, r: r}
}

func (f *fixture) call(t *testing.T, name string, params any) (map[string]any, error) {
	t.Helper()
	a, err := f.r.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(params)
	res, err := a.Handler(context.Background(), b)
	if err != nil {
		return nil, err
	}
	out, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(out, &m)
	return m, nil
}

func (f *fixture) file() string {
	return filepath.Join(f.h.Base, strings.TrimPrefix(Path(f.h.Env), "/"))
}

func TestSwitchOnWritesTheFileAndOffRemovesIt(t *testing.T) {
	f := setup(t)
	st, err := f.call(t, "realip.status", map[string]any{})
	if err != nil || st["enabled"] != false || st["file_present"] != false {
		t.Fatalf("status = %v %v", st, err)
	}
	if _, err := f.call(t, "realip.set", map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(f.file())
	if err != nil || !strings.Contains(string(b), "set_real_ip_from 104.16.0.0/13;") {
		t.Fatalf("file: %v\n%s", err, b)
	}
	st, _ = f.call(t, "realip.status", map[string]any{})
	if st["enabled"] != true || st["file_present"] != true {
		t.Errorf("status after on = %v", st)
	}
	if _, err := f.call(t, "realip.set", map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.file()); err == nil {
		t.Error("the file stayed after switching off")
	}
	// Idempotent.
	if _, err := f.call(t, "realip.set", map[string]any{"enabled": false}); err != nil {
		t.Fatal(err)
	}
}

func TestANewListReplacesTheBuiltInOneAndEmptyGoesBack(t *testing.T) {
	f := setup(t)
	if _, err := f.call(t, "realip.set", map[string]any{"enabled": true, "ranges": []string{"203.0.113.0/24"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(f.file())
	if !strings.Contains(string(b), "203.0.113.0/24") || strings.Contains(string(b), "104.16.0.0/13") {
		t.Errorf("custom list not used:\n%s", b)
	}
	st, _ := f.call(t, "realip.status", map[string]any{})
	if st["custom"] != true {
		t.Errorf("custom = %v", st["custom"])
	}
	if _, err := f.call(t, "realip.set", map[string]any{"enabled": true, "ranges": []string{}}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(f.file())
	if !strings.Contains(string(b), "104.16.0.0/13") {
		t.Errorf("an empty list should go back to Cloudflare's:\n%s", b)
	}
}

func TestAnUnsafeListIsRefused(t *testing.T) {
	f := setup(t)
	_, err := f.call(t, "realip.set", map[string]any{"enabled": true, "ranges": []string{"0.0.0.0/0"}})
	var ae *actions.Error
	if !errors.As(err, &ae) || ae.Code != "invalid_params" {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(f.file()); serr == nil {
		t.Error("a file was written for a refused list")
	}
}

func TestNginxRefusingTheFileRollsBack(t *testing.T) {
	f := setup(t)
	f.h.Fail["nginx -t"] = "nginx: [emerg] \"real_ip_header\" directive is duplicate"
	_, err := f.call(t, "realip.set", map[string]any{"enabled": true})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if _, serr := os.Stat(f.file()); serr == nil {
		t.Error("the refused file was kept")
	}
	st, _ := f.call(t, "realip.status", map[string]any{})
	if st["enabled"] != false {
		t.Errorf("the state says enabled after a rollback: %v", st)
	}
}

func TestExistingRealIPSettingsAreFoundAndNamedInTheError(t *testing.T) {
	f := setup(t)
	os.MkdirAll(filepath.Join(f.h.Base, "etc/nginx/conf.d"), 0o755)
	os.WriteFile(filepath.Join(f.h.Base, "etc/nginx/conf.d/cloudflare.conf"), []byte("# Cloudflare\nset_real_ip_from 103.21.244.0/22;\n  real_ip_header CF-Connecting-IP;\n#real_ip_recursive on;\n"), 0o644)
	os.WriteFile(filepath.Join(f.h.Base, "etc/nginx/nginx.conf"), []byte("http {\n    include conf.d/*.conf;\n}\n"), 0o644)
	st, _ := f.call(t, "realip.status", map[string]any{})
	ex, _ := st["existing"].([]any)
	if len(ex) != 2 || !strings.Contains(ex[0].(string), "cloudflare.conf:2: set_real_ip_from 103.21.244.0/22;") || !strings.Contains(ex[1].(string), "cloudflare.conf:3: real_ip_header CF-Connecting-IP;") {
		t.Fatalf("existing = %v (a commented line is not a setting)", ex)
	}
	f.h.Fail["nginx -t"] = "nginx: [emerg] \"real_ip_header\" directive is duplicate"
	_, err := f.call(t, "realip.set", map[string]any{"enabled": true})
	if err == nil || !strings.Contains(err.Error(), "conf.d/cloudflare.conf:3") {
		t.Fatalf("the error should name the file and line: %v", err)
	}
	// Our own file is never reported as foreign.
	f.h.Fail = map[string]string{}
	if _, err := f.call(t, "realip.set", map[string]any{"enabled": true}); err != nil {
		t.Fatal(err)
	}
	st, _ = f.call(t, "realip.status", map[string]any{})
	for _, e := range st["existing"].([]any) {
		if strings.Contains(e.(string), File) {
			t.Errorf("own file listed: %v", e)
		}
	}
}
