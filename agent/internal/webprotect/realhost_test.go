//go:build realhost

package webprotect

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/shacrypt"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// The rendered rules in a real Nginx: password protection (with the
// certificate path left open), hotlink protection that never serves hidden
// files, and the WordPress rules.
func TestRealHostNginxEnforcesProtection(t *testing.T) {
	nginx, err := exec.LookPath("nginx")
	if err != nil || os.Geteuid() != 0 {
		t.Skip("needs root and nginx")
	}
	h := testhost.New(t)
	e := h.Env
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	docroot := filepath.Join(dir, "www")
	for p, body := range map[string]string{
		"index.html": "home", "wp/index.html": "wp", "wp/xmlrpc.php": "x", "wp/readme.html": "readme", "wp/wp-config.php": "cfg",
		"wp/wp-content/uploads/shell.php": "x", "wp/wp-content/uploads/a.png": "PNG", "wp/wp-includes/load.php": "x",
		"wp/wp-includes/js/tinymce/wp-tinymce.php": "ok", "wp/.hidden.png": "secret", "img.png": "PNG",
		".well-known/acme-challenge/tok": "acme", "private/index.html": "private",
	} {
		os.MkdirAll(filepath.Join(docroot, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(docroot, p), []byte(body), 0o644)
	}
	pwFile := filepath.Join(dir, "pw")
	os.WriteFile(pwFile, []byte("boss:"+shacrypt.HashPassword("S3cret-pass")+"\n"), 0o644)
	s := &Site{
		Password:  []Protected{{Path: "/private/", Realm: "Private", User: "boss", Hash: "x"}},
		Hotlink:   &Hotlink{Allowed: []string{"partner.test"}},
		WordPress: map[string][]string{"/wp/": Rules},
	}
	conf := strings.ReplaceAll(Render(e, "shop.test", s), passwdPath(e, "shop.test", 0), pwFile)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	os.WriteFile(filepath.Join(dir, "site.conf"), []byte(conf), 0o644)
	main := fmt.Sprintf(`daemon off; user root; pid %[1]s/nginx.pid; error_log %[1]s/error.log;
events {}
http {
    access_log off; client_body_temp_path %[1]s; proxy_temp_path %[1]s; fastcgi_temp_path %[1]s; uwsgi_temp_path %[1]s; scgi_temp_path %[1]s;
    server {
        listen 127.0.0.1:%[2]d;
        server_name shop.test;
        root %[3]s;
        add_header X-Content-Type-Options "nosniff" always;
        include %[1]s/site.conf;
        location ^~ /.well-known/acme-challenge/ { try_files $uri =404; }
        location ~ /\.(?!well-known) { deny all; }
        location / { try_files $uri $uri/ =404; }
        location ~ \.php$ { return 200 "php ran"; }
    }
}
`, dir, port, docroot)
	os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(main), 0o644)
	if out, err := exec.Command(nginx, "-t", "-p", dir, "-c", filepath.Join(dir, "nginx.conf")).CombinedOutput(); err != nil {
		t.Fatalf("nginx -t: %v\n%s\n%s", err, out, conf)
	}
	cmd := exec.Command(nginx, "-p", dir, "-c", filepath.Join(dir, "nginx.conf"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	get := func(path string, hdr map[string]string, user, pass string) int {
		req, _ := http.NewRequest("GET", base+path, nil)
		req.Host = "shop.test"
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	for _, c := range []struct {
		path       string
		hdr        map[string]string
		user, pass string
		want       int
	}{
		{"/", nil, "", "", 200},
		{"/private/", nil, "", "", 401},
		{"/private/", nil, "boss", "wrong-pass", 401},
		{"/private/", nil, "boss", "S3cret-pass", 200},
		{"/.well-known/acme-challenge/tok", nil, "", "", 200},
		{"/wp/", nil, "", "", 200},
		{"/wp/xmlrpc.php", nil, "", "", 403},
		{"/wp/readme.html", nil, "", "", 403},
		{"/wp/wp-config.php", nil, "", "", 403},
		{"/wp/wp-content/uploads/shell.php", nil, "", "", 403},
		{"/wp/wp-includes/load.php", nil, "", "", 403},
		{"/wp/wp-includes/js/tinymce/wp-tinymce.php", nil, "", "", 200},
		{"/wp/?author=1", nil, "", "", 403},
		{"/wp/", map[string]string{"User-Agent": "sqlmap/1.7"}, "", "", 403},
		{"/img.png", nil, "", "", 200},
		{"/img.png", map[string]string{"Referer": "http://shop.test/page"}, "", "", 200},
		{"/img.png", map[string]string{"Referer": "https://partner.test/x"}, "", "", 200},
		{"/img.png", map[string]string{"Referer": "https://evil.test/x"}, "", "", 403},
		{"/wp/.hidden.png", nil, "", "", 403},
	} {
		if got := get(c.path, c.hdr, c.user, c.pass); got != c.want {
			t.Errorf("GET %s %v: %d, want %d", c.path, c.hdr, got, c.want)
		}
	}
}

// The traffic limits in a real Nginx: a rule trips after its rate and burst
// with the chosen status, only for its URL, per address (or per address and
// browser), and certificate renewal is never limited.
func TestRealHostNginxTrafficLimits(t *testing.T) {
	nginx, err := exec.LookPath("nginx")
	if err != nil || os.Geteuid() != 0 {
		t.Skip("needs root and nginx")
	}
	h := testhost.New(t)
	e := h.Env
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	docroot := filepath.Join(dir, "www")
	for p, body := range map[string]string{"index.html": "home", "login.php": "login", "api/a.html": "a", "api/b.html": "b", ".well-known/acme-challenge/tok": "acme"} {
		os.MkdirAll(filepath.Join(docroot, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(docroot, p), []byte(body), 0o644)
	}
	site := &Site{LimitStatus: 503, Limits: []Limit{
		// 1 request per minute and 2 more at once: the 4th within the minute trips.
		{Name: "login", Match: "exact", Path: "/login.php", Rate: 1, Unit: "m", Burst: 2, Enabled: true},
		// Per address and browser, a whole folder.
		{Name: "api", Match: "prefix", Path: "/api/", Rate: 1, Unit: "m", Burst: 1, PerUA: true, Enabled: true},
		// The whole website, tight, to prove certificate renewal is exempt.
		{Name: "all", Match: "prefix", Path: "/", Rate: 1, Unit: "m", Burst: 0, Enabled: true},
	}}
	st := State{Sites: map[string]*Site{"shop.test": site}}
	os.WriteFile(filepath.Join(dir, "limits.conf"), []byte(renderLimitsHTTP(st)), 0o644)
	os.WriteFile(filepath.Join(dir, "site.conf"), []byte(Render(e, "shop.test", site)), 0o644)
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	main := fmt.Sprintf(`daemon off; user root; pid %[1]s/nginx.pid; error_log %[1]s/error.log;
events {}
http {
    access_log off; client_body_temp_path %[1]s; proxy_temp_path %[1]s; fastcgi_temp_path %[1]s; uwsgi_temp_path %[1]s; scgi_temp_path %[1]s;
    include %[1]s/limits.conf;
    server {
        listen 127.0.0.1:%[2]d;
        server_name shop.test;
        root %[3]s;
        include %[1]s/site.conf;
        location / { try_files $uri $uri/ =404; }
    }
}
`, dir, port, docroot)
	os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(main), 0o644)
	if out, err := exec.Command(nginx, "-t", "-p", dir, "-c", filepath.Join(dir, "nginx.conf")).CombinedOutput(); err != nil {
		t.Fatalf("nginx -t: %v\n%s\n%s\n%s", err, out, renderLimitsHTTP(st), Render(e, "shop.test", site))
	}
	cmd := exec.Command(nginx, "-p", dir, "-c", filepath.Join(dir, "nginx.conf"))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	get := func(path, ua string) int {
		req, _ := http.NewRequest("GET", base+path, nil)
		req.Host = "shop.test"
		req.Header.Set("User-Agent", ua)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	// The whole-website rule: the first request passes, the second trips
	// (rate 1 per minute, no burst) with the chosen status...
	if got := get("/index.html", "a"); got != 200 {
		t.Fatalf("first request: %d", got)
	}
	if got := get("/index.html", "a"); got != 503 {
		t.Errorf("second request to the limited website: %d, want 503", got)
	}
	// ...but certificate renewal is never limited.
	for i := 0; i < 5; i++ {
		if got := get("/.well-known/acme-challenge/tok", "a"); got != 200 {
			t.Errorf("acme request %d: %d, want 200", i, got)
		}
	}
	// A separate website would have its own counters; here, only the login
	// rule is looked at: it is keyed by address, so its own counter runs on.
	// (The whole-website rule has already tripped for this address, so the
	// login rule is switched off to look at the others.)
	site.Limits = site.Limits[:2]
	os.WriteFile(filepath.Join(dir, "limits.conf"), []byte(renderLimitsHTTP(st)), 0o644)
	os.WriteFile(filepath.Join(dir, "site.conf"), []byte(Render(e, "shop.test", site)), 0o644)
	if out, err := exec.Command(nginx, "-s", "reload", "-p", dir, "-c", filepath.Join(dir, "nginx.conf")).CombinedOutput(); err != nil {
		t.Fatalf("reload: %v %s", err, out)
	}
	time.Sleep(500 * time.Millisecond)
	for i, want := range []int{200, 200, 200, 503} {
		if got := get("/login.php", "a"); got != want {
			t.Errorf("login.php request %d: %d, want %d", i+1, got, want)
		}
	}
	if got := get("/index.html", "a"); got != 200 {
		t.Errorf("a URL without a rule is limited: %d", got)
	}
	// The folder rule counts an address together with its browser.
	for i, want := range []int{200, 200, 503} {
		if got := get("/api/a.html", "agent-one"); got != want {
			t.Errorf("api request %d (agent-one): %d, want %d", i+1, got, want)
		}
	}
	if got := get("/api/b.html", "agent-two"); got != 200 {
		t.Errorf("another browser shares the counter: %d", got)
	}
}

// The access lists in a real Nginx: addresses (the most specific network
// wins), browsers and URLs, an allow entry beating any deny entry, the
// server-wide list combined with a website's own, and certificate renewal
// never refused.
func TestRealHostNginxAccessLists(t *testing.T) {
	nginx, err := exec.LookPath("nginx")
	if err != nil || os.Geteuid() != 0 {
		t.Skip("needs root and nginx")
	}
	h := testhost.New(t)
	e := h.Env
	docroot := ""
	run := func(st State, status int) func(path, ua string) int {
		dir := t.TempDir()
		os.Chmod(dir, 0o755)
		docroot = filepath.Join(dir, "www")
		for _, p := range []string{"index.html", "private/x.html", "private/ok.html", "open/x.html", ".well-known/acme-challenge/tok"} {
			os.MkdirAll(filepath.Join(docroot, filepath.Dir(p)), 0o755)
			os.WriteFile(filepath.Join(docroot, p), []byte("x"), 0o644)
		}
		site := st.Sites["shop.test"]
		os.WriteFile(filepath.Join(dir, "http.conf"), []byte(renderAccessHTTP(st)+renderLimitsHTTP(st)), 0o644)
		os.WriteFile(filepath.Join(dir, "site.conf"), []byte(renderFor(e, "shop.test", site, st)), 0o644)
		l, _ := net.Listen("tcp", "127.0.0.1:0")
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		main := fmt.Sprintf(`daemon off; user root; pid %[1]s/nginx.pid; error_log %[1]s/error.log;
events {}
http {
    access_log off; client_body_temp_path %[1]s; proxy_temp_path %[1]s; fastcgi_temp_path %[1]s; uwsgi_temp_path %[1]s; scgi_temp_path %[1]s;
    include %[1]s/http.conf;
    server {
        listen 127.0.0.1:%[2]d;
        server_name shop.test;
        root %[3]s;
        include %[1]s/site.conf;
        location / { try_files $uri $uri/ =404; }
    }
}
`, dir, port, docroot)
		os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(main), 0o644)
		if out, err := exec.Command(nginx, "-t", "-p", dir, "-c", filepath.Join(dir, "nginx.conf")).CombinedOutput(); err != nil {
			t.Fatalf("nginx -t: %v\n%s\n%s\n%s", err, out, renderAccessHTTP(st), renderFor(e, "shop.test", site, st))
		}
		cmd := exec.Command(nginx, "-p", dir, "-c", filepath.Join(dir, "nginx.conf"))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
		for i := 0; i < 50; i++ {
			if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
				c.Close()
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		return func(path, ua string) int {
			req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d%s", port, path), nil)
			req.Host = "shop.test"
			req.Header.Set("User-Agent", ua)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				if status == 444 {
					return 444
				}
				t.Fatalf("%s: %v", path, err)
			}
			res.Body.Close()
			return res.StatusCode
		}
	}
	check := func(get func(string, string) int, name, path, ua string, want int) {
		t.Helper()
		if got := get(path, ua); got != want {
			t.Errorf("%s: GET %s as %q = %d, want %d", name, path, ua, got, want)
		}
	}

	// Addresses: a whole network denied, one address in it allowed.
	st := State{Sites: map[string]*Site{"shop.test": {AccessStatus: 404, Access: []Access{{Kind: KindIP, Action: ActionDeny, Value: "127.0.0.0/8"}}}}}
	get := run(st, 404)
	check(get, "ip deny", "/index.html", "a", 404)
	check(get, "ip deny: certificates", "/.well-known/acme-challenge/tok", "a", 200)
	st.Sites["shop.test"].Access = append(st.Sites["shop.test"].Access, Access{Kind: KindIP, Action: ActionAllow, Value: "127.0.0.1"})
	get = run(st, 404)
	check(get, "ip allow in a denied network", "/index.html", "a", 200)

	// Browsers and URLs, with a server-wide browser entry.
	st = State{
		Global: []Access{{Kind: KindUA, Action: ActionDeny, Value: "scrapy"}},
		Sites: map[string]*Site{"shop.test": {Access: []Access{
			{Kind: KindUA, Action: ActionDeny, Value: "BadBot"},
			{Kind: KindURL, Action: ActionDeny, Value: "/private/"},
			{Kind: KindURL, Action: ActionAllow, Value: "/private/ok.html"},
			{Kind: KindURL, Action: ActionAllow, Value: "/open/"},
		}}},
	}
	get = run(st, 403)
	check(get, "plain visitor", "/index.html", "Mozilla/5.0", 200)
	check(get, "denied browser", "/index.html", "xx badbot/2.0", 403)
	check(get, "server-wide browser", "/index.html", "Scrapy/2", 403)
	check(get, "denied URL", "/private/x.html", "Mozilla/5.0", 403)
	check(get, "allowed URL inside a denied one", "/private/ok.html", "Mozilla/5.0", 200)
	check(get, "allow beats a denied browser", "/private/ok.html", "BadBot", 200)
	check(get, "allow beats the server-wide list", "/open/x.html", "scrapy", 200)
	check(get, "certificates", "/.well-known/acme-challenge/tok", "scrapy", 200)

	// An allow-listed address is not counted by a traffic limit (and one that
	// is not listed is).
	limited := &Site{Limits: []Limit{{Name: "all", Match: "prefix", Path: "/", Rate: 1, Unit: "m", Enabled: true}}}
	st = State{Sites: map[string]*Site{"shop.test": limited}}
	get = run(st, 403)
	check(get, "limit, first", "/index.html", "a", 200)
	check(get, "limit, second", "/index.html", "a", 429)
	limited.Access = []Access{{Kind: KindIP, Action: ActionAllow, Value: "127.0.0.1"}}
	get = run(st, 403)
	for i := 0; i < 4; i++ {
		check(get, "limit with an allowed address", "/index.html", "a", 200)
	}

	// Countries: a denied one, an "allow only" one (private addresses are
	// always served) and an allowed address beating a denied country.
	countries := func(mode, from, to string) State {
		site := &Site{AccessStatus: 404, Countries: &Countries{Mode: mode, Codes: []string{"XX"}}}
		ps, err := RangePrefixes(from, to)
		if err != nil {
			t.Fatal(err)
		}
		return State{Sites: map[string]*Site{"shop.test": site}, Geo: map[string][]string{"XX": ps}}
	}
	st = countries(CountryDeny, "127.0.0.0", "127.255.255.255")
	get = run(st, 404)
	check(get, "denied country", "/index.html", "a", 404)
	check(get, "denied country: certificates", "/.well-known/acme-challenge/tok", "a", 200)
	st.Sites["shop.test"].Access = []Access{{Kind: KindIP, Action: ActionAllow, Value: "127.0.0.1"}}
	get = run(st, 404)
	check(get, "allowed address in a denied country", "/index.html", "a", 200)
	st = countries(CountryAllowOnly, "203.0.113.0", "203.0.113.255")
	get = run(st, 404)
	check(get, "allow only another country: private address", "/index.html", "a", 200)
	st = countries(CountryAllowOnly, "127.0.0.0", "127.255.255.255")
	get = run(st, 404)
	check(get, "allow only this country", "/index.html", "a", 200)

	// Only the server-wide list: a website with nothing of its own.
	st = State{Global: []Access{{Kind: KindUA, Action: ActionDeny, Value: "scrapy"}}, Sites: map[string]*Site{}}
	get = run(st, 403)
	check(get, "server-wide only", "/index.html", "scrapy", 403)
	check(get, "server-wide only, others", "/index.html", "firefox", 200)
	_ = docroot
}
