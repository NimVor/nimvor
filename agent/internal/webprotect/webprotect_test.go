package webprotect

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

type fixture struct {
	h *testhost.Host
	r *actions.Registry
}

const vhost = "# Managed by NimVor.\nserver {\n    listen 80;\n    server_name shop.test;\n    root /home/nimvor/shop.test/public_html;\n" +
	"    add_header X-Content-Type-Options \"nosniff\" always;\n    location / {\n        try_files $uri $uri/ /index.php?$query_string;\n    }\n}\n"

func setup(t *testing.T) *fixture {
	t.Helper()
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "etc/nginx/sites-available"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/nginx/sites-available/nimvor-shop.test.conf"), []byte(vhost), 0o644)
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

func code(err error) string {
	var ae *actions.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func TestPasswordProtection(t *testing.T) {
	f := setup(t)
	res, err := f.call(t, "protect.password", map[string]any{"domain": "shop.test", "path": "/", "enabled": true, "user": "boss", "password": "S3cret-pass"})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(res); strings.Contains(string(b), "$6$") || strings.Contains(string(b), "S3cret") {
		t.Fatalf("status shows the password or its hash: %s", b)
	}
	if !strings.Contains(f.h.Read("etc/nginx/sites-available/nimvor-shop.test.conf"), "include /etc/nginx/nimvor-protect/shop.test.con[f];") {
		t.Fatalf("include not added:\n%s", f.h.Read("etc/nginx/sites-available/nimvor-shop.test.conf"))
	}
	conf := f.h.Read("etc/nginx/nimvor-protect/shop.test.conf")
	for _, want := range []string{"set $nimvor_auth \"Restricted area\";", "auth_basic $nimvor_auth;", "auth_basic_user_file $nimvor_auth_file;", "acme-challenge", "nimvor-protect/htpasswd/shop.test-0"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("conf lacks %q:\n%s", want, conf)
		}
	}
	pw := f.h.Read("etc/nginx/nimvor-protect/htpasswd/shop.test-0")
	if !strings.HasPrefix(pw, "boss:$6$") || strings.Contains(pw, "S3cret") {
		t.Fatalf("password file: %q", pw)
	}
	if fi, _ := os.Stat(filepath.Join(f.h.Base, "etc/nginx/nimvor-protect/htpasswd/shop.test-0")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", fi.Mode())
	}
	// A second path: its own file, the longest path last (it wins).
	f.call(t, "protect.password", map[string]any{"domain": "shop.test", "path": "blog/wp-admin", "enabled": true, "user": "ed", "password": "An0ther-pass"})
	conf = f.h.Read("etc/nginx/nimvor-protect/shop.test.conf")
	if !strings.Contains(conf, "if ($uri ~ \"^/blog/wp-admin/\")") || strings.Index(conf, "shop.test-0;") > strings.Index(conf, "shop.test-1;") {
		t.Fatalf("second path:\n%s", conf)
	}
	// Off again: files removed, the include stays (it matches nothing).
	f.call(t, "protect.password", map[string]any{"domain": "shop.test", "path": "/", "enabled": false})
	f.call(t, "protect.password", map[string]any{"domain": "shop.test", "path": "/blog/wp-admin/", "enabled": false})
	if f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") || f.h.Exists("etc/nginx/nimvor-protect/htpasswd/shop.test-0") {
		t.Fatal("files left after turning protection off")
	}
}

func TestInputIsValidated(t *testing.T) {
	f := setup(t)
	for _, p := range []map[string]any{
		{"domain": "shop.test", "path": "/a b/", "enabled": true, "user": "boss", "password": "S3cret-pass"},
		{"domain": "shop.test", "path": "/../etc/", "enabled": true, "user": "boss", "password": "S3cret-pass"},
		{"domain": "shop.test", "path": "/x\";}/", "enabled": true, "user": "boss", "password": "S3cret-pass"},
		{"domain": "shop.test", "path": "/", "enabled": true, "user": "bo:ss", "password": "S3cret-pass"},
		{"domain": "shop.test", "path": "/", "enabled": true, "user": "boss", "password": "short"},
		{"domain": "shop.test", "path": "/", "enabled": true, "user": "boss", "password": "S3cret-pass", "realm": "a\";deny all;#"},
	} {
		if _, err := f.call(t, "protect.password", p); code(err) != "invalid_params" {
			t.Errorf("%v: %v", p, err)
		}
	}
	for _, p := range []map[string]any{
		{"domain": "shop.test", "enabled": true, "allowed": []string{"evil.test; deny all"}},
		{"domain": "shop.test", "enabled": true, "extensions": []string{"php"}},
		{"domain": "shop.test", "enabled": true, "extensions": []string{"png|.*"}},
	} {
		if _, err := f.call(t, "protect.hotlink", p); code(err) != "invalid_params" {
			t.Errorf("%v: %v", p, err)
		}
	}
	if f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") {
		t.Fatal("written despite invalid input")
	}
}

func TestHotlinkProtection(t *testing.T) {
	f := setup(t)
	if _, err := f.call(t, "protect.hotlink", map[string]any{"domain": "shop.test", "enabled": true, "allowed": []string{"*.Partner.test", "cdn.example.com"}}); err != nil {
		t.Fatal(err)
	}
	conf := f.h.Read("etc/nginx/nimvor-protect/shop.test.conf")
	if !strings.Contains(conf, "valid_referers none blocked server_names *.partner.test cdn.example.com;") || !strings.Contains(conf, "(?!.*/\\.)") || !strings.Contains(conf, "jpg|jpeg|png") {
		t.Fatalf("conf:\n%s", conf)
	}
	f.call(t, "protect.hotlink", map[string]any{"domain": "shop.test", "enabled": false})
	if f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") {
		t.Fatal("left after turning off")
	}
}

// Nginx refusing the result: everything as before.
func TestRolledBackWhenNginxRefuses(t *testing.T) {
	f := setup(t)
	f.h.Fail["nginx -t"] = "nginx: [emerg] unknown directive"
	if _, err := f.call(t, "protect.hotlink", map[string]any{"domain": "shop.test", "enabled": true}); err == nil {
		t.Fatal("accepted")
	}
	if f.h.Exists("etc/nginx/nimvor-protect/shop.test.conf") || f.h.Read("etc/nginx/sites-available/nimvor-shop.test.conf") != vhost {
		t.Fatal("not rolled back")
	}
	if len(Load(f.h.Env).Sites) != 0 {
		t.Fatal("state kept")
	}
}

func TestWordPressRules(t *testing.T) {
	f := setup(t)
	if _, err := Update(context.Background(), f.h.Env, "shop.test", func(s *Site) error {
		s.WordPress = map[string][]string{"/blog/": Rules}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	conf := f.h.Read("etc/nginx/nimvor-protect/shop.test.conf")
	for _, want := range []string{"location = /blog/xmlrpc.php", "^/blog/wp-content/uploads/.*\\.(php", "^/blog/wp-includes/(?!js/tinymce/wp-tinymce\\.php$", "author=[0-9]", "wp-config\\.php", "sqlmap"} {
		if !strings.Contains(conf, want) {
			t.Errorf("lacks %q", want)
		}
	}
	if paths := Remove(f.h.Env, "shop.test"); len(paths) != 1 {
		t.Fatalf("remove: %v", paths)
	}
}
