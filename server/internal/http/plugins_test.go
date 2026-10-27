package http

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// pluginFixture installs a plugin folder whose program is a Go handler on a
// Unix socket, recording what the panel forwards.
type pluginFixture struct {
	mu     sync.Mutex
	last   *http.Request
	body   string
	events []string
	reply  func(w http.ResponseWriter, r *http.Request)
}

func installPlugin(t *testing.T, s *Server, manifest map[string]any) *pluginFixture {
	t.Helper()
	dir, err := os.MkdirTemp("", "wp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "demo.sock")
	manifest["socket"] = sock
	plug := filepath.Join(dir, "plugins", "demo")
	if err := os.MkdirAll(plug, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(plug, "plugin.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	s.cfg.PluginsDir = filepath.Join(dir, "plugins")

	f := &pluginFixture{reply: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-WhaCP-Title", "Demo page")
		_, _ = w.Write([]byte(`<p id="demo-body">Hello from the plugin</p><form method="post"><input type="hidden" name="_token" value="` + r.Header.Get("X-WhaCP-CSRF") + `"></form>`))
	}}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(strings.Builder)
		_, _ = (&strings.Builder{}).WriteString("")
		b := make([]byte, 4096)
		n, _ := r.Body.Read(b)
		buf.Write(b[:n])
		f.mu.Lock()
		f.last, f.body = r, buf.String()
		if strings.HasPrefix(r.URL.Path, "/_events/") {
			f.events = append(f.events, strings.TrimPrefix(r.URL.Path, "/_events/")+" "+buf.String())
		}
		reply := f.reply
		f.mu.Unlock()
		reply(w, r)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return f
}

func demoManifest() map[string]any {
	return map[string]any{
		"name": "demo", "title": "Demo", "version": "1.0.0", "description": "A demo plugin.",
		"permissions": map[string]string{"plugin.demo.view": "View the demo"},
		"routes": []map[string]string{
			{"method": "GET", "path": "/", "permission": "plugin.demo.view"},
			{"method": "POST", "path": "/save", "permission": "plugin.demo.view"},
			{"method": "GET", "path": "/item/{id}", "permission": "plugin.demo.view"},
		},
		"nav":    []map[string]string{{"label": "Demo", "icon": "puzzle", "path": "/", "permission": "plugin.demo.view"}},
		"events": []string{"website.created"},
	}
}

func TestPluginLifecycleAndProxy(t *testing.T) {
	s, db, _ := rolesEnvServerAgent(t, false)
	f := installPlugin(t, s, demoManifest())
	h := s.Handler()
	admin := loginAsUser(t, h, "admin")

	if page := get(t, h, "/admin/plugins", admin).Body.String(); !pageHas(page, "A demo plugin.") || !pageHas(page, "disabled") {
		t.Fatalf("plugin list: %s", page)
	}
	// Not enabled: no page.
	if rr := get(t, h, "/plugins/demo", admin); rr.Code != http.StatusNotFound {
		t.Errorf("disabled plugin page = %d", rr.Code)
	}

	toggle := func(enable string) {
		t.Helper()
		tok, c := siteFormToken(t, h, admin, "/admin/plugins")
		rr := postForm(h, "/admin/plugins/demo/toggle", url.Values{"_token": {tok}, "enabled": {enable}}, c)
		admin = refreshCookie(c, rr)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("toggle = %d", rr.Code)
		}
	}
	toggle("1")
	var granted int
	_ = db.QueryRow(`SELECT COUNT(*) FROM role_permissions rp JOIN permissions p ON p.id = rp.permission_id JOIN roles r ON r.id = rp.role_id
		WHERE p.name = 'plugin.demo.view' AND r.name = 'admin'`).Scan(&granted)
	if granted != 1 {
		t.Fatalf("administrators granted the plugin permission: %d", granted)
	}

	// The page is the plugin's, in the layout, with the user's identity sent.
	rr := get(t, h, "/plugins/demo", admin)
	body := rr.Body.String()
	if rr.Code != http.StatusOK || !strings.Contains(body, `id="demo-body"`) || !strings.Contains(body, "Demo page") || !strings.Contains(body, "wcp-sidebar") {
		t.Fatalf("plugin page = %d: %s", rr.Code, body)
	}
	if got := f.last.Header.Get("X-WhaCP-User"); got != "admin" {
		t.Errorf("forwarded user = %q", got)
	}
	if !strings.Contains(body, `href="/plugins/demo"`) {
		t.Error("the sidebar entry is missing")
	}
	// A parameterised route; an undeclared one is not forwarded.
	if rr := get(t, h, "/plugins/demo/item/42?x=1", admin); rr.Code != http.StatusOK || f.last.URL.Path != "/item/42" || f.last.URL.RawQuery != "x=1" {
		t.Errorf("item route = %d path %q", rr.Code, f.last.URL.Path)
	}
	f.last = nil
	if rr := get(t, h, "/plugins/demo/secret", admin); rr.Code != http.StatusNotFound || f.last != nil {
		t.Errorf("undeclared route = %d, forwarded=%v", rr.Code, f.last != nil)
	}

	// Another user without the permission is refused before the plugin hears of it.
	f.last = nil
	mgr := loginAsUser(t, h, "mgr")
	if rr := get(t, h, "/plugins/demo", mgr); rr.Code != http.StatusForbidden || f.last != nil {
		t.Errorf("user without permission = %d, forwarded=%v", rr.Code, f.last != nil)
	}

	// POST needs the CSRF token.
	if rr := postForm(h, "/plugins/demo/save", url.Values{"note": {"x"}}, admin); rr.Code != http.StatusBadRequest {
		t.Errorf("POST without token = %d", rr.Code)
	}
	tok, c := siteFormToken(t, h, admin, "/plugins/demo")
	if rr := postForm(h, "/plugins/demo/save", url.Values{"_token": {tok}, "note": {"hello"}}, c); rr.Code != http.StatusOK || !strings.Contains(f.body, "note=hello") {
		t.Errorf("POST with token = %d body %q", rr.Code, f.body)
	}

	// Redirects stay on the panel.
	f.reply = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/plugins/demo")
		w.WriteHeader(302)
	}
	if rr := get(t, h, "/plugins/demo", admin); rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/plugins/demo" {
		t.Errorf("redirect = %d %q", rr.Code, rr.Header().Get("Location"))
	}
	f.reply = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://evil.example/")
		w.WriteHeader(302)
	}
	if rr := get(t, h, "/plugins/demo", admin); rr.Code != http.StatusBadGateway {
		t.Errorf("off-site redirect = %d", rr.Code)
	}

	// Events reach subscribed plugins.
	f.reply = func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }
	if failed := s.pluginManager().Emit(t.Context(), "website.created", map[string]any{"domain": "a.example"}); len(failed) != 0 {
		t.Fatalf("emit failed: %v", failed)
	}
	if len(f.events) != 1 || !strings.Contains(f.events[0], "website.created") || !strings.Contains(f.events[0], "a.example") {
		t.Errorf("events = %v", f.events)
	}
	s.pluginManager().Emit(t.Context(), "website.deleted", map[string]any{})
	if len(f.events) != 1 {
		t.Error("an event the plugin did not subscribe to was delivered")
	}

	// Disabling removes the page and the permission.
	toggle("0")
	if rr := get(t, h, "/plugins/demo", admin); rr.Code != http.StatusNotFound {
		t.Errorf("after disable = %d", rr.Code)
	}
	var left int
	_ = db.QueryRow(`SELECT COUNT(*) FROM permissions WHERE name LIKE 'plugin.demo.%'`).Scan(&left)
	if left != 0 {
		t.Errorf("permissions left: %d", left)
	}
}

func TestPluginProblemsAndUnavailable(t *testing.T) {
	s, _, _ := rolesEnvServerAgent(t, false)
	installPlugin(t, s, demoManifest())
	h := s.Handler()
	admin := loginAsUser(t, h, "admin")
	// A group-writable file makes the plugin unusable.
	if err := os.Chmod(filepath.Join(s.cfg.PluginsDir, "demo", "plugin.json"), 0o664); err != nil {
		t.Fatal(err)
	}
	if page := get(t, h, "/admin/plugins", admin).Body.String(); !pageHas(page, "Writable by group or others: plugin.json") {
		t.Fatalf("problem not shown: %s", page)
	}
	tok, c := siteFormToken(t, h, admin, "/admin/plugins")
	postForm(h, "/admin/plugins/demo/toggle", url.Values{"_token": {tok}, "enabled": {"1"}}, c)
	if rr := get(t, h, "/plugins/demo", c); rr.Code != http.StatusNotFound {
		t.Errorf("plugin with problems served a page: %d", rr.Code)
	}

	// A plugin that is not running: a clear error, not a crash.
	_ = os.Chmod(filepath.Join(s.cfg.PluginsDir, "demo", "plugin.json"), 0o644)
	tok, c = siteFormToken(t, h, c, "/admin/plugins")
	postForm(h, "/admin/plugins/demo/toggle", url.Values{"_token": {tok}, "enabled": {"1"}}, c)
	var m map[string]any
	raw, _ := os.ReadFile(filepath.Join(s.cfg.PluginsDir, "demo", "plugin.json"))
	_ = json.Unmarshal(raw, &m)
	_ = os.Remove(m["socket"].(string))
	rr := get(t, h, "/plugins/demo", c)
	if rr.Code != http.StatusBadGateway || !pageHas(rr.Body.String(), "is not running") {
		t.Errorf("plugin down = %d", rr.Code)
	}
	_ = httptest.NewRecorder
}
