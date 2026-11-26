package dbtools

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/apps"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "pma/", Mode: 0o755, Typeflag: tar.TypeDir})
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: "pma/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

const panelSiteText = "server {\n    listen 8443 ssl;\n    server_name _;\n    client_max_body_size 256m;\n    location / {\n        proxy_pass http://127.0.0.1:8090;\n    }\n}\n"

func setup(t *testing.T) (*testhost.Host, env) {
	t.Helper()
	h := testhost.New(t)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "php8.3" && len(args) == 1 && args[0] == "-m" {
			return "[PHP Modules]\nmysqli\nmbstring\ndom\nxml\nsession\njson\nctype\ncurl\n", nil, true
		}
		return "", nil, false
	}
	os.MkdirAll(filepath.Join(h.Base, "etc/nginx/sites-available"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/nginx/sites-available/nimvor-panel.conf"), []byte(panelSiteText), 0o644)
	return h, env{h.Env}
}

func serveTools(t *testing.T) {
	t.Helper()
	pma := tarball(t, map[string]string{"index.php": "<?php // pma", "libraries/x.php": "<?php"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sum") {
			sum := sha256.Sum256(pma)
			io.WriteString(w, hex.EncodeToString(sum[:])+"  phpMyAdmin-latest-all-languages.tar.gz\n")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/phpmyadmin") {
			w.Write(pma)
			return
		}
		io.WriteString(w, "<?php // adminer")
	}))
	old := apps.DefaultSources
	apps.DefaultSources.CatalogBase = srv.URL
	apps.DefaultSources.Client = srv.Client()
	t.Cleanup(func() { apps.DefaultSources = old; srv.Close() })
}

func TestInstallAndRemove(t *testing.T) {
	h, e := setup(t)
	serveTools(t)
	old := probe
	probed := ""
	probe = func(_ context.Context, url string) error { probed = url; return nil }
	t.Cleanup(func() { probe = old })

	pma, _ := toolByID("phpmyadmin")
	if _, err := e.install(context.Background(), pma, io.Discard); err != nil {
		t.Fatalf("%v %#v", err, err)
	}
	if probed != "https://127.0.0.1:8443/dbtools/phpmyadmin/" {
		t.Errorf("probed %q", probed)
	}
	if !h.Exists("var/lib/nimvor-dbtools/www/dbtools/phpmyadmin/index.php") || !h.Exists("var/lib/nimvor-dbtools/www/dbtools/phpmyadmin/tmp/.keep") {
		t.Fatal("phpMyAdmin was not unpacked")
	}
	cfg := h.Read("var/lib/nimvor-dbtools/www/dbtools/phpmyadmin/config.inc.php")
	if strings.Contains(cfg, "{{") || !strings.Contains(cfg, "AllowArbitraryServer'] = false") {
		t.Errorf("config:\n%s", cfg)
	}
	if st, err := os.Stat(filepath.Join(h.Base, "var/lib/nimvor-dbtools/www/dbtools/phpmyadmin/config.inc.php")); err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("config mode: %v %v", st, err)
	}
	pool := h.Read("etc/php/8.3/fpm/pool.d/nimvor-dbtools.conf")
	for _, want := range []string{"[nimvor-dbtools]", "user = nimvor-dbtools", "listen = /run/php/nimvor-dbtools.sock", "open_basedir", "disable_functions"} {
		if !strings.Contains(pool, want) {
			t.Errorf("pool lacks %q:\n%s", want, pool)
		}
	}
	snip := h.Read("etc/nginx/nimvor-dbtools.d/dbtools.conf")
	for _, want := range []string{"location ^~ /dbtools/ {", "root /var/lib/nimvor-dbtools/www;", "^/dbtools/(phpmyadmin)/[A-Za-z0-9_]+\\.php$", "fastcgi_pass unix:/run/php/nimvor-dbtools.sock;", "absolute_redirect off;"} {
		if !strings.Contains(snip, want) {
			t.Errorf("snippet lacks %q:\n%s", want, snip)
		}
	}
	site := h.Read("etc/nginx/sites-available/nimvor-panel.conf")
	if strings.Count(site, includeLine) != 1 || !strings.Contains(site, "client_max_body_size 256m;\n    "+includeLine) {
		t.Errorf("panel site:\n%s", site)
	}

	// A second tool joins the first; the include line is not added twice.
	adm, _ := toolByID("adminer")
	if _, err := e.install(context.Background(), adm, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Read("etc/nginx/nimvor-dbtools.d/dbtools.conf"), "(adminer|phpmyadmin)") || strings.Count(h.Read("etc/nginx/sites-available/nimvor-panel.conf"), includeLine) != 1 {
		t.Error("second tool not added cleanly")
	}
	if got := h.Read("var/lib/nimvor-dbtools/www/dbtools/adminer/adminer.php"); got != "<?php // adminer" {
		t.Errorf("adminer.php = %q", got)
	}
	if !strings.Contains(h.Read("var/lib/nimvor-dbtools/www/dbtools/adminer/index.php"), "'localhost'") {
		t.Error("adminer's front page does not fix the server")
	}
	if s := e.status(); len(s["tools"].([]map[string]any)) != 2 || s["panel_https"] != true {
		t.Errorf("status = %v", s)
	}

	// Removing one keeps the other; removing the last takes the pool and snippet.
	if _, err := e.remove(context.Background(), pma); err != nil {
		t.Fatal(err)
	}
	if h.Exists("var/lib/nimvor-dbtools/www/dbtools/phpmyadmin") || !strings.Contains(h.Read("etc/nginx/nimvor-dbtools.d/dbtools.conf"), "(adminer)") {
		t.Error("phpMyAdmin not removed cleanly")
	}
	if _, err := e.remove(context.Background(), adm); err != nil {
		t.Fatal(err)
	}
	if h.Exists("etc/nginx/nimvor-dbtools.d/dbtools.conf") || h.Exists("etc/php/8.3/fpm/pool.d/nimvor-dbtools.conf") {
		t.Error("pool or snippet left behind")
	}
}

func TestFailedNginxTestRollsBack(t *testing.T) {
	h, e := setup(t)
	serveTools(t)
	pma, _ := toolByID("phpmyadmin")
	h.Fail["nginx -t"] = "nginx: [emerg] test failure"
	h.FailFrom["nginx -t"] = 2 // the precheck passes
	if _, err := e.install(context.Background(), pma, io.Discard); err == nil {
		t.Fatal("an invalid Nginx configuration was accepted")
	}
	for _, p := range []string{"etc/nginx/nimvor-dbtools.d/dbtools.conf", "etc/php/8.3/fpm/pool.d/nimvor-dbtools.conf", "var/lib/nimvor-dbtools/www/dbtools/phpmyadmin"} {
		if h.Exists(p) {
			t.Errorf("%s left after a failed change", p)
		}
	}
	if strings.Contains(h.Read("etc/nginx/sites-available/nimvor-panel.conf"), includeLine) {
		t.Error("the panel site was changed")
	}
}

func TestWithInclude(t *testing.T) {
	if _, _, err := withInclude("server {\n}\n"); err == nil {
		t.Error("a site without client_max_body_size should be refused")
	}
	out, changed, err := withInclude(panelSiteText)
	if err != nil || !changed {
		t.Fatal(err)
	}
	again, changed, _ := withInclude(out)
	if changed || again != out {
		t.Error("the include line was added twice")
	}
}

func TestMissingPHPModulesStopTheInstall(t *testing.T) {
	h, e := setup(t)
	serveTools(t)
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "php8.3" && len(args) == 1 && args[0] == "-m" {
			return "[PHP Modules]\nmbstring\n", nil, true
		}
		return "", nil, false
	}
	pma, _ := toolByID("phpmyadmin")
	if _, err := e.install(context.Background(), pma, io.Discard); err == nil || !strings.Contains(err.Error(), "mysqli") {
		t.Fatalf("err = %v", err)
	}
}
