package apps

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/sites"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

// stagedTarGz builds a tar.gz with a single top folder "pkg/" holding the
// given files (keyed by path under pkg/).
func stagedTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "pkg/", Mode: 0o755, Typeflag: tar.TypeDir})
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: "pkg/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestStagedInstallVerifiesAndUnpacks(t *testing.T) {
	tarball := stagedTarGz(t, map[string]string{"index.php": "<?php // app", "sub/app.php": "<?php // sub"})
	sum := sha256.Sum256(tarball)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sum") {
			io.WriteString(w, hex.EncodeToString(sum[:])+"  testapp.tar.gz\n")
			return
		}
		w.Write(tarball)
	}))
	t.Cleanup(srv.Close)

	Catalog["testapp"] = AppDef{Name: "Test App", Format: "targz", Strip: 1, SumURL: "x", SumAlgo: "sha256", Finish: "/install/"}
	t.Cleanup(func() { delete(Catalog, "testapp") })

	h, _ := setup(t)
	req := request()
	req.App = "testapp"
	req.Root = "/home/whacp/shop.test"
	src := Sources{Client: srv.Client(), CatalogBase: srv.URL}
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	res, err := Install(context.Background(), h.Env, src, req, cred, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res["staged"] != true || res["finish_url"] != "https://shop.test/install/" || res["document_root"] != "public_html" {
		t.Fatalf("res = %+v", res)
	}
	root := filepath.Join(h.Base, "home/whacp/shop.test/public_html")
	for _, f := range []string{"index.php", "sub/app.php"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("missing extracted file %s: %v", f, err)
		}
	}
	// WhaCP's placeholder page was replaced.
	if b, _ := os.ReadFile(filepath.Join(root, "index.html")); sites.PlaceholderIndex(b, "shop.test") {
		t.Error("placeholder page left behind")
	}
	if !strings.Contains(log.String(), "Checksum verified") {
		t.Error("checksum was not verified")
	}
}

func TestStagedChecksumMismatchStops(t *testing.T) {
	tarball := stagedTarGz(t, map[string]string{"index.php": "x"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sum") {
			io.WriteString(w, strings.Repeat("0", 64)+"  testapp.tar.gz\n")
			return
		}
		w.Write(tarball)
	}))
	t.Cleanup(srv.Close)
	Catalog["testapp"] = AppDef{Name: "Test App", Format: "targz", Strip: 1, SumURL: "x", SumAlgo: "sha256", Finish: "/install/"}
	t.Cleanup(func() { delete(Catalog, "testapp") })
	h, _ := setup(t)
	req := request()
	req.App = "testapp"
	req.Root = "/home/whacp/shop.test"
	cred, _, _ := h.Env.Credential("example")
	if _, err := Install(context.Background(), h.Env, Sources{Client: srv.Client(), CatalogBase: srv.URL}, req, cred, io.Discard); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a checksum mismatch must stop the install: %v", err)
	}
}

func TestExtractionGuards(t *testing.T) {
	// stripPath drops the top folder; the folder itself yields nothing.
	if rel, ok := stripPath("pkg/sub/x.php", 1); !ok || rel != "sub/x.php" {
		t.Errorf("stripPath = %q %v", rel, ok)
	}
	if _, ok := stripPath("pkg/", 1); ok {
		t.Error("the stripped folder itself should be skipped")
	}
	// safeJoin refuses anything escaping the destination.
	for _, bad := range []string{"../evil", "../../etc/passwd", "/etc/passwd", "a/../../evil"} {
		if _, ok := safeJoin("/srv/app", bad); ok {
			t.Errorf("safeJoin accepted an escape: %s", bad)
		}
	}
	if p, ok := safeJoin("/srv/app", "sub/x.php"); !ok || p != "/srv/app/sub/x.php" {
		t.Errorf("safeJoin good path: %q %v", p, ok)
	}
}

func serve(t *testing.T, body []byte, sum string) Sources {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha1") {
			io.WriteString(w, sum+"\n")
			return
		}
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return Sources{WordPressURL: srv.URL + "/latest.tar.gz", Client: srv.Client()}
}

func request() Request {
	return Request{App: "wordpress", Domain: "shop.test", User: "example", Path: "public_html", PHPVersion: "8.3",
		DB: DB{Name: "shop_wp", User: "shop_wp", Password: "db-Pa$$'word"}, URL: "https://shop.test",
		Title: "Shop", AdminUser: "owner", AdminEmail: "owner@shop.test", AdminPassword: "a-long-admin-password-1"}
}

func setup(t *testing.T) (*testhost.Host, *actions.Registry) {
	h := testhost.New(t)
	docroot := filepath.Join(h.Base, "home/whacp/shop.test/public_html")
	os.MkdirAll(docroot, 0o755)
	os.WriteFile(filepath.Join(docroot, "index.html"), []byte(sites.DefaultIndex("shop.test")), 0o644)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	return h, r
}

func TestWordPressInstall(t *testing.T) {
	body := []byte("fake tarball")
	sum := sha1.Sum(body)
	src := serve(t, body, hex.EncodeToString(sum[:]))
	h, _ := setup(t)
	req := request()
	req.Root = "/home/whacp/shop.test"
	cred, _, _ := h.Env.Credential("example")
	var log strings.Builder
	res, err := Install(context.Background(), h.Env, src, req, cred, &log)
	if err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if res["login_url"] != "https://shop.test/wp-admin/" {
		t.Errorf("res = %+v", res)
	}
	var tarCall, phpCall int = -1, -1
	for i, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "tar ") {
			tarCall = i
		}
		if strings.HasPrefix(c, "php8.3 ") {
			phpCall = i
		}
		if strings.Contains(c, "a-long-admin-password-1") || strings.Contains(c, "db-Pa$$") {
			t.Errorf("secret in argv: %s", c)
		}
		if o := h.Runner.Opts[i]; o.As == nil || o.As.UID == 0 {
			t.Errorf("%s did not run as the website account", c)
		}
	}
	if tarCall < 0 || phpCall < tarCall {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	joined := strings.Join(h.Runner.Stdins, "\n")
	if !strings.Contains(joined, "fake tarball") || !strings.Contains(joined, `define('DB_PASSWORD', 'db-Pa$$\'word');`) || !strings.Contains(joined, "wp_install(") {
		t.Fatalf("stdins = %q", joined)
	}
	if !strings.Contains(strings.Join(h.Runner.Opts[phpCall].Env, " "), "WHACP_ADMIN_PASSWORD=a-long-admin-password-1") {
		t.Error("admin password not passed through the environment")
	}
}

func TestChecksumMismatchStopsBeforeExtracting(t *testing.T) {
	src := serve(t, []byte("tampered"), strings.Repeat("0", 40))
	h, _ := setup(t)
	req := request()
	req.Root = "/home/whacp/shop.test"
	cred, _, _ := h.Env.Credential("example")
	if _, err := Install(context.Background(), h.Env, src, req, cred, io.Discard); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v", err)
	}
	for _, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "tar ") {
			t.Fatal("extracted a download that failed its checksum")
		}
	}
}

func TestActionValidation(t *testing.T) {
	h, r := setup(t)
	// Jobs started here stop at the PHP check, before any download.
	h.Fail["php8.3 -r"] = "mysqli"
	a, _ := r.Lookup("apps.install")
	call := func(mut func(*Request)) error {
		req := request()
		mut(&req)
		b, _ := json.Marshal(req)
		_, err := a.Handler(context.Background(), b)
		return err
	}
	for name, mut := range map[string]func(*Request){
		"app":       func(r *Request) { r.App = "magento" },
		"path":      func(r *Request) { r.Path = "../../etc" },
		"logs":      func(r *Request) { r.Path = "logs" },
		"db host":   func(r *Request) { r.DB.Host = "10.0.0.1" },
		"url":       func(r *Request) { r.URL = "javascript:alert(1)" },
		"weak pass": func(r *Request) { r.AdminPassword = "short" },
		"root user": func(r *Request) { r.User = "root" },
	} {
		if err := call(mut); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A folder with real content is refused; the placeholder page is not content.
	if err := call(func(*Request) {}); err != nil {
		t.Fatalf("placeholder-only folder refused: %v", err)
	}
	os.WriteFile(filepath.Join(h.Base, "home/whacp/shop.test/public_html/app.php"), []byte("<?php"), 0o644)
	if err := call(func(*Request) {}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("non-empty folder: %v", err)
	}
}

func TestMissingPHPExtensionStopsEarly(t *testing.T) {
	src := serve(t, []byte("x"), strings.Repeat("0", 40))
	h, _ := setup(t)
	h.Fail["php8.3 -r"] = "mysqli"
	req := request()
	req.Root = "/home/whacp/shop.test"
	cred, _, _ := h.Env.Credential("example")
	_, err := Install(context.Background(), h.Env, src, req, cred, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "missing extensions: mysqli") {
		t.Fatalf("err = %v", err)
	}
	if len(h.Runner.Calls) != 1 {
		t.Fatalf("nothing else may run: %v", h.Runner.Calls)
	}
}

func TestLaravelInstall(t *testing.T) {
	h, _ := setup(t)
	src := Sources{} // Laravel downloads nothing itself
	os.MkdirAll(filepath.Join(h.Base, "usr/local/bin"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "usr/local/bin/composer"), []byte("#!/usr/bin/env php"), 0o755)
	req := request()
	req.App, req.Path, req.Migrate = "laravel", "app", true
	req.Root = "/home/whacp/shop.test"
	cred, _, _ := h.Env.Credential("example")
	res, err := Install(context.Background(), h.Env, src, req, cred, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if res["document_root"] != "app/public" {
		t.Errorf("res = %+v", res)
	}
	var composer, migrate bool
	for i, c := range h.Runner.Calls {
		if strings.Contains(c, "'/usr/bin/php8.3' /usr/local/bin/composer create-project --prefer-dist --no-progress laravel/laravel 'app'") {
			composer = true
		}
		if c == "php8.3 artisan migrate --force" {
			migrate = true
		}
		if o := h.Runner.Opts[i]; o.As == nil || o.As.UID == 0 {
			t.Errorf("%s did not run as the website account", c)
		}
		if strings.Contains(c, "db-Pa$$") {
			t.Errorf("database password in argv: %s", c)
		}
	}
	if !composer || !migrate {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	if !strings.Contains(strings.Join(h.Runner.Stdins, "\n"), "DB_PASSWORD") {
		t.Error(".env script not run")
	}
}
