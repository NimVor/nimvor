package nginx

import (
	"context"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

const fixture = "../../tests/fixtures/ubuntu-host"

func discover(t *testing.T) Result {
	t.Helper()
	root, err := filesystem.NewRoot(mustAbs(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	p := platform.Detect(root)
	runner := &execx.FakeRunner{Outputs: map[string]string{"nginx -v": "nginx version: nginx/1.24.0 (Ubuntu)\n"}}
	return Discoverer{Root: root, Paths: p.Paths(), Runner: runner}.Discover(context.Background())
}

func TestDiscoverFixture(t *testing.T) {
	res := discover(t)
	if !res.Installed || res.Version != "1.24.0" {
		t.Fatalf("installed=%v version=%q", res.Installed, res.Version)
	}
	byName := map[string][]VHost{}
	for _, v := range res.VHosts {
		key := "_"
		if len(v.ServerNames) > 0 {
			key = v.ServerNames[0]
		}
		byName[key] = append(byName[key], v)
	}
	ex := byName["example.com"]
	if len(ex) != 2 {
		t.Fatalf("example.com vhosts = %d, want 2 (%+v)", len(ex), res.VHosts)
	}
	var tls VHost
	for _, v := range ex {
		if v.SSL {
			tls = v
		} else if !v.RedirectHTTPS {
			t.Error("port 80 block of example.com should redirect to https")
		}
	}
	if tls.Root != "/var/www/example.com/public" || tls.FastCGIPass != "unix:/run/php/php8.3-fpm-example.sock" || !tls.HTTP2 {
		t.Errorf("unexpected TLS vhost: %+v", tls)
	}
	if tls.File != "/etc/nginx/sites-available/example.com" {
		t.Errorf("vhost file should be the resolved symlink target, got %q", tls.File)
	}
	if !tls.Enabled {
		t.Error("example.com should be enabled")
	}
	if api := byName["api.example.com"]; len(api) != 1 || api[0].ProxyPass != "http://127.0.0.1:3000" {
		t.Errorf("api vhost = %+v", api)
	}
	if st := byName["status.local"]; len(st) != 1 {
		t.Errorf("conf.d vhost not discovered")
	}
	old := byName["old.example.com"]
	if len(old) != 1 || old[0].Enabled {
		t.Errorf("old.example.com should be discovered as disabled: %+v", old)
	}
	if def := byName["_"]; len(def) != 1 || !def[0].DefaultServer {
		t.Errorf("default server not detected: %+v", def)
	}
	// Debian's modules-enabled links to the package's load_module stubs
	// in /usr/share/nginx/modules-available: followed without warnings.
	if len(res.Warnings) != 0 {
		t.Errorf("warnings: %v", res.Warnings)
	}
}

func TestIncludeOutsideAllowedRootsIsSkipped(t *testing.T) {
	root, _ := filesystem.NewRoot(mustAbs(t, fixture))
	l := &loader{root: root, prefix: "/etc/nginx", allowed: []string{"/etc/nginx"}, seen: map[string]bool{}}
	dirs := l.load("/etc/passwd", 0)
	if dirs != nil || len(l.warnings) == 0 {
		t.Fatal("expected /etc/passwd to be refused as an nginx include")
	}
}
