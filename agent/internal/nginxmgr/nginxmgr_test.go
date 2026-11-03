package nginxmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestBrotliLinesOnlyWhenRequested(t *testing.T) {
	base := Tuning{ClientMaxBodyMB: 128, KeepaliveTimeout: 65, ClientBodyTimeout: 60, SendTimeout: 60, Gzip: true}
	if strings.Contains(base.conf(), "brotli") {
		t.Fatalf("brotli written although it is off:\n%s", base.conf())
	}
	base.Brotli = true
	conf := base.conf()
	for _, want := range []string{"brotli on;", "brotli_comp_level 5;", "brotli_types text/plain text/css", "gzip on;"} {
		if !strings.Contains(conf, want) {
			t.Errorf("conf lacks %q:\n%s", want, conf)
		}
	}
}

const brotliBody = `{"client_max_body_mb":128,"keepalive_timeout":65,"client_body_timeout":60,"send_timeout":60,"gzip":true,"brotli":true}`

func TestBrotliNeedsTheModule(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	apply, _ := r.Lookup("nginx.tuning_apply")
	os.MkdirAll(filepath.Dir(confRealPath(h)), 0o755)

	// No module: refused before anything is written, with what to do.
	if _, err := apply.Handler(context.Background(), json.RawMessage(brotliBody)); err == nil || !strings.Contains(err.Error(), "Brotli module") || !strings.Contains(err.Error(), "Packages") {
		t.Fatalf("brotli without the module: %v", err)
	}
	if _, err := os.Stat(confRealPath(h)); err == nil {
		t.Fatal("a configuration was written although the module is missing")
	}

	// A module built into Nginx counts.
	h.Runner.Handler = func(id string, o execx.Opts, args []string) (string, error) {
		if id == "nginx" && len(args) > 0 && args[0] == "-V" {
			return "nginx version: nginx/1.24.0\nconfigure arguments: --with-http_v2_module --add-module=/build/ngx_brotli\n", nil
		}
		return "", nil
	}
	if _, err := apply.Handler(context.Background(), json.RawMessage(brotliBody)); err != nil {
		t.Fatalf("brotli with the module: %v", err)
	}
	if b, _ := os.ReadFile(confRealPath(h)); !strings.Contains(string(b), "brotli on;") {
		t.Fatalf("brotli not written:\n%s", b)
	}
	// Turning it off removes the lines again (so a later removal of the module cannot break Nginx).
	off := strings.Replace(brotliBody, `"brotli":true`, `"brotli":false`, 1)
	if _, err := apply.Handler(context.Background(), json.RawMessage(off)); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(confRealPath(h)); strings.Contains(string(b), "brotli") {
		t.Fatalf("brotli still written:\n%s", b)
	}
}

// With the real Brotli module: a client that accepts br gets Brotli, one that only accepts gzip gets gzip, and
// a small or non-text response is left alone.
func TestBrotliWithARealNginx(t *testing.T) {
	bin, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx is not installed")
	}
	var mod string
	for _, d := range []string{"/usr/lib/nginx/modules", "/usr/lib64/nginx/modules", "/usr/share/nginx/modules"} {
		if m, _ := filepath.Glob(filepath.Join(d, "ngx_http_brotli_filter_module.so")); len(m) > 0 {
			mod = m[0]
		}
	}
	if mod == "" {
		t.Skip("the Nginx Brotli module is not installed")
	}
	dir, err := os.MkdirTemp("", "nimvor-brotli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Chmod(dir, 0o755)
	os.MkdirAll(filepath.Join(dir, "docroot"), 0o755)
	os.MkdirAll(filepath.Join(dir, "tmp"), 0o755)
	text := strings.Repeat("NimVor compresses text responses. ", 200)
	os.WriteFile(filepath.Join(dir, "docroot", "page.css"), []byte(text), 0o644)
	os.WriteFile(filepath.Join(dir, "docroot", "image.png"), []byte(text), 0o644) // not a compressed type
	tuning := Tuning{ClientMaxBodyMB: 8, KeepaliveTimeout: 5, ClientBodyTimeout: 5, SendTimeout: 5, Gzip: true, Brotli: true, HideVersion: true}
	os.WriteFile(filepath.Join(dir, "tuning.conf"), []byte(tuning.conf()), 0o644)
	pl, _ := net.Listen("tcp", "127.0.0.1:0")
	port := pl.Addr().(*net.TCPAddr).Port
	pl.Close()
	conf := fmt.Sprintf("load_module %s;\npid %s/nginx.pid;\nerror_log %s/error.log;\nevents { worker_connections 16; }\nhttp {\n    access_log off;\n    include %s/mime.types;\n    client_body_temp_path %s/tmp/b;\n    fastcgi_temp_path %s/tmp/f;\n    proxy_temp_path %s/tmp/p;\n    uwsgi_temp_path %s/tmp/u;\n    scgi_temp_path %s/tmp/s;\n    include %s/tuning.conf;\n    server {\n        listen 127.0.0.1:%d;\n        root %s/docroot;\n    }\n}\n",
		mod, dir, dir, dir, dir, dir, dir, dir, dir, dir, port, dir)
	os.WriteFile(filepath.Join(dir, "mime.types"), []byte("types { text/css css; image/png png; }\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(conf), 0o644)
	if out, err := exec.Command(bin, "-t", "-p", dir, "-c", filepath.Join(dir, "nginx.conf")).CombinedOutput(); err != nil {
		t.Fatalf("nginx -t: %v\n%s\n%s", err, out, conf)
	}
	cmd := exec.Command(bin, "-p", dir, "-c", filepath.Join(dir, "nginx.conf"), "-g", "daemon off;")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	get := func(path, accept string) (string, int) {
		req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
		req.Header.Set("Accept-Encoding", accept)
		tr := &http.Transport{DisableCompression: true}
		res, err := (&http.Client{Transport: tr}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.Header.Get("Content-Encoding"), len(b)
	}
	if enc, n := get("/page.css", "br, gzip"); enc != "br" || n >= len(text)/4 {
		t.Errorf("br, gzip: encoding %q, %d bytes of %d", enc, n, len(text))
	}
	if enc, _ := get("/page.css", "gzip"); enc != "gzip" {
		t.Errorf("gzip only: encoding %q", enc)
	}
	if enc, n := get("/page.css", "identity"); enc != "" || n != len(text) {
		t.Errorf("identity: encoding %q, %d bytes", enc, n)
	}
	if enc, _ := get("/image.png", "br, gzip"); enc != "" {
		t.Errorf("a non-text type was compressed: %q", enc)
	}
}
