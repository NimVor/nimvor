package sites

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func pageCacheSpec(ttl int, bypass ...string) Spec {
	s := spec()
	s.Cache = &CachePolicy{DynamicMode: "app", BypassPaths: bypass, PageCache: &PageCache{Enabled: true, TTL: ttl}}
	return s
}

func phpLocation(t *testing.T, conf string) string {
	t.Helper()
	i := strings.Index(conf, "location ~ \\.php$ {")
	if i < 0 {
		t.Fatalf("no PHP location:\n%s", conf)
	}
	j := strings.Index(conf[i:], "\n    }\n")
	return conf[i : i+j]
}

func TestPageCacheRender(t *testing.T) {
	s := pageCacheSpec(300, "/members")
	if err := s.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	conf := RenderNginx(s, policyLayout)
	zone, dir := pageCacheZone(s.Domain), pageCacheDir(s.Domain)
	for _, want := range []string{
		"fastcgi_cache_path " + dir + " levels=1:2 keys_zone=" + zone + ":10m max_size=256m inactive=1d use_temp_path=off;",
		"fastcgi_cache " + zone + ";",
		"fastcgi_cache_valid 200 301 300s;",
		"fastcgi_cache_methods GET HEAD;",
		"fastcgi_cache_lock on;",
		"\"00000\" 0;", // cached only when every flag is clear
		"wp-admin", "my-account", "members",
		"fastcgi_no_cache $" + siteVar(s.Domain, "pcb") + " $nimvor_sf;",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("config lacks %q:\n%s", want, conf)
		}
	}
	if n := strings.Count(conf, "fastcgi_cache_path"); n != 1 {
		t.Errorf("fastcgi_cache_path written %d times", n)
	}
	// add_header in the PHP location would replace the security headers of the whole server block.
	if loc := phpLocation(t, conf); strings.Contains(loc, "add_header") {
		t.Errorf("the PHP location sets headers:\n%s", loc)
	}
	if !strings.Contains(conf, "    add_header X-Page-Cache $upstream_cache_status;\n") || !strings.Contains(conf, "X-Content-Type-Options") {
		t.Errorf("server-level headers:\n%s", conf)
	}
	// The path is matched on the address asked for, not on $uri (which is /index.php after the rewrite).
	if strings.Contains(conf, "map $uri $"+siteVar(s.Domain, "pcp")) || !strings.Contains(conf, "map $request_uri $"+siteVar(s.Domain, "pcp")) {
		t.Errorf("the sensitive-path map must use $request_uri:\n%s", conf)
	}
}

func TestPageCacheOffRendersNothing(t *testing.T) {
	for name, mod := range map[string]func(*Spec){
		"no cache policy": func(s *Spec) { s.Cache = nil },
		"not enabled":     func(s *Spec) { s.Cache = &CachePolicy{PageCache: &PageCache{Enabled: false, TTL: 600}} },
		"static site":     func(s *Spec) { s.Kind, s.PHPVersion = "static", "" },
	} {
		s := pageCacheSpec(600)
		mod(&s)
		if err := s.Normalize(testRoot, testState); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		conf := RenderNginx(s, policyLayout)
		for _, bad := range []string{"fastcgi_cache", "X-Page-Cache"} {
			if strings.Contains(conf, bad) {
				t.Errorf("%s: config contains %q", name, bad)
			}
		}
	}
}

func TestPageCacheValidation(t *testing.T) {
	for _, ttl := range []int{1, 59, 601, 100000} {
		s := pageCacheSpec(ttl)
		if err := s.Normalize(testRoot, testState); err == nil || !strings.Contains(err.Error(), "page_cache") {
			t.Errorf("ttl %d accepted: %v", ttl, err)
		}
	}
	for _, ttl := range PageCacheTTLs {
		s := pageCacheSpec(ttl)
		if err := s.Normalize(testRoot, testState); err != nil {
			t.Errorf("ttl %d refused: %v", ttl, err)
		}
	}
	s := pageCacheSpec(0)
	if err := s.Normalize(testRoot, testState); err != nil || s.Cache.PageCache.TTL != 600 {
		t.Errorf("default ttl: %v %+v", err, s.Cache.PageCache)
	}
}

func TestPageCacheFoldersAreSeparatePerSite(t *testing.T) {
	if pageCacheDir("a.test") == pageCacheDir("b.test") || pageCacheZone("a.test") == pageCacheZone("b.test") {
		t.Fatal("two websites share a cache")
	}
	if !strings.HasPrefix(pageCacheDir("a.test"), PageCacheBase+"/") || strings.Contains(pageCacheDir("a.test"), "a.test") {
		t.Fatalf("folder %s", pageCacheDir("a.test"))
	}
}

func TestApplyCreatesTheCacheFolderAndTurningItOffRemovesIt(t *testing.T) {
	h := testhost.New(t)
	m := &Manager{Env: h.Env}
	s := pageCacheSpec(600)
	if _, err := m.Apply(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(h.Base, pageCacheDir(s.Domain))
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("cache folder: %v %v", info, err)
	}
	if !strings.Contains(h.Read("etc/nginx/sites-available/nimvor-new.test.conf"), "fastcgi_cache_path") {
		t.Fatal("no cache in the configuration")
	}
	os.WriteFile(filepath.Join(dir, "entry"), []byte("cached"), 0o600)

	// Purge empties the folder and keeps it.
	if _, err := m.handlePageCachePurge(context.Background(), []byte(`{"domain":"new.test"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "entry")); err == nil {
		t.Fatal("purge left the entry")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("purge removed the folder")
	}
	// A purge of a site that never cached is not an error; a bad domain is refused.
	if _, err := m.handlePageCachePurge(context.Background(), []byte(`{"domain":"never.test"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := m.handlePageCachePurge(context.Background(), []byte(`{"domain":"../../etc"}`)); err == nil {
		t.Fatal("a path was accepted as a domain")
	}

	// Turned off: the configuration loses it and the cached pages are removed.
	off := spec()
	off.Cache = &CachePolicy{DynamicMode: "app", StaticTTL: 3600}
	if _, err := m.Apply(context.Background(), off); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h.Read("etc/nginx/sites-available/nimvor-new.test.conf"), "fastcgi_cache") {
		t.Fatal("the cache stayed in the configuration")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the cached pages stayed on disk")
	}
}

// --- against a real Nginx ------------------------------------------------------------------------------------------

type backend struct {
	hits   atomic.Int64
	handle func(w http.ResponseWriter, r *http.Request)
}

func (b *backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.hits.Add(1)
	if b.handle != nil {
		b.handle(w, r)
		return
	}
	fmt.Fprintf(w, "page %s hit %d", r.URL.RequestURI(), b.hits.Load())
}

// startNginx runs a real Nginx with the generated configuration in front of a FastCGI backend and returns its address.
func startNginx(t *testing.T, s Spec, be *backend) string {
	t.Helper()
	bin, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx is not installed")
	}
	dir, err := os.MkdirTemp("", "nimvor-ngx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	os.Chmod(dir, 0o755)
	for _, d := range []string{"logs", "docroot", "tmp"} {
		os.MkdirAll(filepath.Join(dir, d), 0o755)
	}
	// Real PHP files, as a WordPress site has them (try_files answers 404 for a script that does not exist).
	os.MkdirAll(filepath.Join(dir, "docroot", "wp-admin"), 0o755)
	for _, f := range []string{"index.php", "wp-login.php", "wp-admin/options.php"} {
		os.WriteFile(filepath.Join(dir, "docroot", f), []byte("<?php"), 0o644)
	}
	sock := filepath.Join(dir, "php.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(sock, 0o777)
	go fcgi.Serve(ln, be)
	t.Cleanup(func() { ln.Close() })

	pl, _ := net.Listen("tcp", "127.0.0.1:0")
	port := pl.Addr().(*net.TCPAddr).Port
	pl.Close()
	s.DocumentRoot = filepath.Join(dir, "docroot")
	l := Layout{Socket: sock, LogDir: filepath.Join(dir, "logs"), ACMERoot: filepath.Join(dir, "tmp")}
	vhost := strings.Replace(RenderNginx(s, l), "listen 80;", fmt.Sprintf("listen 127.0.0.1:%d;", port), 1)
	// The cache folder is the test's own, not the server's.
	vhost = strings.ReplaceAll(vhost, pageCacheDir(s.Domain), filepath.Join(dir, "cache"))
	os.WriteFile(filepath.Join(dir, "flags.conf"), []byte(RenderCacheFlags()), 0o644)
	os.WriteFile(filepath.Join(dir, "vhost.conf"), []byte(vhost), 0o644)
	os.WriteFile(filepath.Join(dir, "fastcgi_params"), []byte("fastcgi_param REQUEST_METHOD $request_method;\nfastcgi_param QUERY_STRING $query_string;\nfastcgi_param REQUEST_URI $request_uri;\nfastcgi_param CONTENT_LENGTH $content_length;\nfastcgi_param CONTENT_TYPE $content_type;\nfastcgi_param SERVER_PROTOCOL $server_protocol;\n"), 0o644)
	conf := fmt.Sprintf("pid %s/nginx.pid;\nerror_log %s/logs/main-error.log;\nevents { worker_connections 64; }\nhttp {\n    access_log off;\n    client_body_temp_path %s/tmp/b;\n    fastcgi_temp_path %s/tmp/f;\n    proxy_temp_path %s/tmp/p;\n    uwsgi_temp_path %s/tmp/u;\n    scgi_temp_path %s/tmp/s;\n    include %s/flags.conf;\n    include %s/vhost.conf;\n}\n",
		dir, dir, dir, dir, dir, dir, dir, dir, dir)
	os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(conf), 0o644)
	if out, err := exec.Command(bin, "-t", "-p", dir, "-c", filepath.Join(dir, "nginx.conf")).CombinedOutput(); err != nil {
		t.Fatalf("nginx -t: %v\n%s\n%s", err, out, vhost)
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
			return addr
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs, _ := os.ReadFile(filepath.Join(dir, "logs/main-error.log"))
	t.Fatalf("nginx did not start:\n%s", logs)
	return ""
}

type reply struct {
	status int
	header http.Header
	body   string
}

func get(t *testing.T, addr, host, method, path string, hdr map[string]string) reply {
	t.Helper()
	req, _ := http.NewRequest(method, "http://"+addr+path, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return reply{res.StatusCode, res.Header, string(b)}
}

// The cache serves a plain page from memory and never a personal or sensitive one.
func TestPageCacheBehaviourWithARealNginx(t *testing.T) {
	be := &backend{}
	s := pageCacheSpec(600, "/members")
	s.SecurityHeaders = true
	if err := s.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	addr := startNginx(t, s, be)
	host := s.Domain
	status := func(r reply) string { return r.header.Get("X-Page-Cache") }

	// A plain page: the first request reaches PHP, the next ones do not.
	r1 := get(t, addr, host, "GET", "/about", nil)
	r2 := get(t, addr, host, "GET", "/about", nil)
	r3 := get(t, addr, host, "HEAD", "/about", nil)
	if r1.status != 200 || status(r1) != "MISS" || status(r2) != "HIT" || r2.body != r1.body {
		t.Fatalf("plain page: %d %s / %s %q", r1.status, status(r1), status(r2), r2.body)
	}
	_ = r3
	if be.hits.Load() != 2 { // the GET, then the HEAD (a separate entry)
		t.Fatalf("PHP reached %d times for a cached GET and a HEAD", be.hits.Load())
	}
	// The server's security headers are still sent on a cached page.
	if r2.header.Get("X-Content-Type-Options") != "nosniff" || r2.header.Get("X-Frame-Options") != "SAMEORIGIN" {
		t.Errorf("security headers lost on a cached page: %v", r2.header)
	}
	// The pretty front-controller address is cached too (index.php answers it).
	if r := get(t, addr, host, "GET", "/blog/post-1/", nil); status(r) != "MISS" {
		t.Errorf("pretty address: %s", status(r))
	}
	if r := get(t, addr, host, "GET", "/blog/post-1/", nil); status(r) != "HIT" {
		t.Errorf("pretty address second request: %s", status(r))
	}

	// Never cached: each of these reaches PHP every time and is reported as BYPASS.
	never := []struct {
		name, path string
		hdr        map[string]string
		method     string
	}{
		{"session cookie", "/about", map[string]string{"Cookie": "PHPSESSID=abc"}, "GET"},
		{"sign-in cookie", "/about", map[string]string{"Cookie": "wordpress_logged_in_x=1"}, "GET"},
		{"Authorization", "/about", map[string]string{"Authorization": "Bearer x"}, "GET"},
		{"query string", "/about?x=1", nil, "GET"},
		{"encoded address", "/%61bout", nil, "GET"},
		{"administration", "/wp-admin/options.php", nil, "GET"},
		{"WooCommerce account", "/my-account/orders/", nil, "GET"},
		{"checkout", "/checkout/", nil, "GET"},
		{"sign-in page", "/wp-login.php", nil, "GET"},
		{"API", "/wp-json/wp/v2/posts", nil, "GET"},
		{"listed path", "/members/area", nil, "GET"},
		{"POST", "/about", nil, "POST"},
	}
	for _, c := range never {
		before := be.hits.Load()
		get(t, addr, host, c.method, c.path, c.hdr)
		get(t, addr, host, c.method, c.path, c.hdr)
		if got := be.hits.Load() - before; got != 2 {
			t.Errorf("%s: PHP reached %d times for two requests (it was cached)", c.name, got)
		}
	}

	// A response that sets a cookie or says it is private is not stored.
	be.handle = func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sets-cookie":
			http.SetCookie(w, &http.Cookie{Name: "visit", Value: "1"})
		case "/private":
			w.Header().Set("Cache-Control", "private, max-age=600")
		case "/no-store":
			w.Header().Set("Cache-Control", "no-store")
		case "/error":
			w.WriteHeader(500)
		}
		fmt.Fprintf(w, "x")
	}
	for _, p := range []string{"/sets-cookie", "/private", "/no-store", "/error"} {
		before := be.hits.Load()
		get(t, addr, host, "GET", p, nil)
		get(t, addr, host, "GET", p, nil)
		if got := be.hits.Load() - before; got != 2 {
			t.Errorf("%s: PHP reached %d times for two requests (the response was cached)", p, got)
		}
	}
}

func TestPageCacheOnOneSiteDoesNotServeAnotherSite(t *testing.T) {
	be := &backend{handle: func(w http.ResponseWriter, r *http.Request) { fmt.Fprintf(w, "site=%s", r.Host) }}
	s := pageCacheSpec(600)
	s.Aliases = []string{"www.new.test"}
	if err := s.Normalize(testRoot, testState); err != nil {
		t.Fatal(err)
	}
	addr := startNginx(t, s, be)
	a := get(t, addr, "new.test", "GET", "/same", nil)
	b := get(t, addr, "www.new.test", "GET", "/same", nil)
	if a.header.Get("X-Page-Cache") != "MISS" || b.header.Get("X-Page-Cache") != "MISS" || a.body == b.body {
		t.Fatalf("the host name is not part of the cache key: %q %q", a.body, b.body)
	}
}
