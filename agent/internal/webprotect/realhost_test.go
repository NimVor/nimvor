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
