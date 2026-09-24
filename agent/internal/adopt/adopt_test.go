package adopt

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/sites"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func setup(t *testing.T) (*testhost.Host, Service) {
	h := testhost.New(t)
	pub := filepath.Join(h.Base, "var/www/shop.example.com/public")
	os.MkdirAll(filepath.Join(pub, "css"), 0o755)
	os.WriteFile(filepath.Join(pub, "index.php"), []byte("<?php echo 'shop';"), 0o4755) // setuid must be dropped
	os.WriteFile(filepath.Join(pub, "css/app.css"), []byte("body{}"), 0o666)            // world-write must be dropped
	os.Symlink("css/app.css", filepath.Join(pub, "style.css"))                          // stays inside: copied
	os.Symlink("/etc/shadow", filepath.Join(pub, "secret"))                             // escapes: skipped
	os.WriteFile(filepath.Join(h.Base, "var/www/shop.example.com/.env"), []byte("APP_NAME=Shop\nDB_DATABASE=shop_live\n"), 0o640)
	return h, Service{Env: h.Env, Sites: &sites.Manager{Env: h.Env}}
}

func shopRequest() Request {
	return Request{Spec: sites.Spec{Domain: "shop.example.com", User: "shopimport", Kind: "php", PHPVersion: "8.3", Enabled: true}, Source: "/var/www/shop.example.com/public"}
}

func TestPreview(t *testing.T) {
	_, s := setup(t)
	p, err := s.Plan(context.Background(), shopRequest())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Blockers) != 0 {
		t.Fatalf("blockers: %v", p.Blockers)
	}
	if len(p.Steps) != 1 || p.Steps[0].Action != "unlink" || p.Steps[0].Path != "/etc/nginx/sites-enabled/shop.example.com" {
		t.Fatalf("steps = %+v", p.Steps)
	}
	if p.Files != 2 || p.Database != "shop_live" || len(p.Skipped) != 1 || p.Skipped[0] != "secret" {
		t.Fatalf("preview = %+v", p)
	}
	if !strings.Contains(p.NewConfig, "server_name shop.example.com") || !strings.Contains(p.OldConfig, "fastcgi_pass 127.0.0.1:9084") {
		t.Error("configs missing from the preview")
	}

	// Blocked: a different document root, a system directory, a file that
	// also serves other names.
	req := shopRequest()
	req.Source = "/etc"
	if p, _ := s.Plan(context.Background(), req); len(p.Blockers) == 0 {
		t.Error("/etc accepted as a source")
	}
	req = shopRequest()
	req.Spec.Domain, req.Source = "example.com", "/var/www/example.com/public"
	if p, _ := s.Plan(context.Background(), req); !strings.Contains(strings.Join(p.Blockers, " "), "also serves") {
		t.Errorf("expected a blocker for other names, got %v", p.Blockers)
	}
}

func TestRunAndUndo(t *testing.T) {
	h, s := setup(t)
	ctx := context.Background()
	res, err := s.Run(ctx, shopRequest(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res["files"].(int64) != 2 {
		t.Errorf("res = %+v", res)
	}
	if h.Exists("etc/nginx/sites-enabled/shop.example.com") {
		t.Fatal("old site still enabled")
	}
	if !h.Exists("etc/nginx/sites-available/shop.example.com") {
		t.Fatal("the old configuration file must be kept")
	}
	if !h.Exists("etc/nginx/sites-enabled/whacp-shop.example.com.conf") {
		t.Fatal("new site not enabled")
	}
	docroot := filepath.Join(h.Base, "home/whacp/shop.example.com/public_html")
	info, err := os.Stat(filepath.Join(docroot, "index.php"))
	if err != nil || info.Mode()&os.ModeSetuid != 0 {
		t.Fatalf("index.php: %v %v", info, err)
	}
	if info, _ := os.Stat(filepath.Join(docroot, "css/app.css")); info.Mode().Perm()&0o002 != 0 {
		t.Error("world-writable bit kept")
	}
	if l, err := os.Readlink(filepath.Join(docroot, "style.css")); err != nil || l != "css/app.css" {
		t.Errorf("internal link: %q %v", l, err)
	}
	if _, err := os.Lstat(filepath.Join(docroot, "secret")); err == nil {
		t.Error("a link escaping the website was copied")
	}
	if h.Exists("home/whacp/shop.example.com/public_html/index.html") {
		t.Error("placeholder page left behind")
	}
	if !h.Exists("var/www/shop.example.com/public/index.php") {
		t.Error("the old files must be left in place")
	}

	// A second import of the same site is blocked.
	if p, _ := s.Plan(ctx, shopRequest()); len(p.Blockers) == 0 {
		t.Error("re-import not blocked")
	}

	spec := shopRequest().Spec
	if err := s.Undo(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if !h.Exists("etc/nginx/sites-enabled/shop.example.com") || h.Exists("etc/nginx/sites-enabled/whacp-shop.example.com.conf") {
		t.Fatal("undo did not switch back")
	}
	if l, _ := os.Readlink(filepath.Join(h.Base, "etc/nginx/sites-enabled/shop.example.com")); l != "/etc/nginx/sites-available/shop.example.com" {
		t.Errorf("restored link = %q", l)
	}
}

func TestFailedSwitchRestoresOldSite(t *testing.T) {
	h, s := setup(t)
	// nginx -t passes for the precheck and the disabled site, then fails.
	h.Fail["nginx -t"] = "emerg: something"
	h.FailFrom["nginx -t"] = 4
	if _, err := s.Run(context.Background(), shopRequest(), io.Discard); err == nil || !strings.Contains(err.Error(), "old configuration was restored") {
		t.Fatalf("err = %v", err)
	}
	if !h.Exists("etc/nginx/sites-enabled/shop.example.com") {
		t.Fatal("old site not re-enabled after a failed switch")
	}
}
