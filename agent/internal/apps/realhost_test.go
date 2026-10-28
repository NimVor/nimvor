//go:build realhost

package apps

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// Requires root, PHP 8.3 with mysqli and a local MariaDB, plus either a
// WordPress tarball in NIMVOR_WP_TARBALL (with a .sha1 next to it) or
// NIMVOR_NETWORK=1 to download it from wordpress.org as in production.
func TestRealHostWordPress(t *testing.T) {
	tarball := os.Getenv("NIMVOR_WP_TARBALL")
	network := os.Getenv("NIMVOR_NETWORK") == "1"
	if os.Geteuid() != 0 || (tarball == "" && !network) {
		t.Skip("needs root and NIMVOR_WP_TARBALL or NIMVOR_NETWORK=1")
	}
	src := DefaultSources
	if tarball != "" {
		srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(tarball))))
		defer srv.Close()
		src = Sources{WordPressURL: srv.URL + "/" + filepath.Base(tarball), Client: srv.Client()}
	}

	sitesRoot, _ := os.MkdirTemp("/tmp", "nimvor-wp-")
	os.Chmod(sitesRoot, 0o755)
	t.Cleanup(func() { os.RemoveAll(sitesRoot) })
	exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", "-u", "1778", "nimvorwp").Run()
	t.Cleanup(func() { exec.Command("userdel", "nimvorwp").Run() })
	root := filepath.Join(sitesRoot, "wp.test")
	os.MkdirAll(filepath.Join(root, "public_html"), 0o755)
	exec.Command("chown", "-R", "nimvorwp:nimvorwp", root).Run()
	sql := "CREATE DATABASE IF NOT EXISTS nimvor_wp; CREATE USER IF NOT EXISTS 'nimvor_wp'@'localhost' IDENTIFIED BY 'db-Pa$$''word'; GRANT ALL ON nimvor_wp.* TO 'nimvor_wp'@'localhost';"
	if out, err := exec.Command("mysql", "-e", sql).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	t.Cleanup(func() {
		exec.Command("mysql", "-e", "DROP DATABASE IF EXISTS nimvor_wp; DROP USER IF EXISTS 'nimvor_wp'@'localhost'").Run()
	})

	cfg := config.Default()
	cfg.SitesRoot = sitesRoot
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	env := host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(30*time.Second), jm, log)
	cred, _, err := env.Credential("nimvorwp")
	if err != nil {
		t.Fatal(err)
	}
	req := Request{App: "wordpress", Domain: "wp.test", User: "nimvorwp", Root: root, Path: "public_html", PHPVersion: testhost.PHPVersion(),
		DB: DB{Name: "nimvor_wp", User: "nimvor_wp", Password: "db-Pa$$'word", Host: "localhost"}, URL: "https://wp.test",
		Title: "NimVor test", AdminUser: "owner", AdminEmail: "owner@wp.test", AdminPassword: "a-long-admin-password-1"}
	var buf strings.Builder
	if _, err := Install(context.Background(), env, src, req, cred, &buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	out, _ := exec.Command("mysql", "-N", "-e", "SELECT option_value FROM nimvor_wp.wp_options WHERE option_name = 'siteurl'; SELECT user_login FROM nimvor_wp.wp_users").Output()
	if got := strings.Fields(string(out)); len(got) != 2 || got[0] != "https://wp.test" || got[1] != "owner" {
		t.Fatalf("database = %q\n%s", out, buf.String())
	}
	info, err := os.Stat(filepath.Join(root, "public_html/wp-config.php"))
	if err != nil || info.Mode().Perm()&0o007 != 0 {
		t.Fatalf("wp-config.php must not be world-readable: %v %v", info.Mode(), err)
	}
	st, _ := exec.Command("stat", "-c", "%U", filepath.Join(root, "public_html/wp-login.php")).Output()
	if strings.TrimSpace(string(st)) != "nimvorwp" {
		t.Fatalf("files owned by %s", st)
	}
	// Re-running must not re-install over the existing site.
	if _, err := Install(context.Background(), env, src, req, cred, io.Discard); err == nil {
		t.Fatal("second installation should fail")
	}
}

// Requires root, network access (Packagist and GitHub), Composer, PHP 8.3
// with pdo_mysql, pdo_sqlite, mbstring, xml, curl and zip, and a local
// MariaDB. Enabled with NIMVOR_NETWORK=1.
func TestRealHostLaravel(t *testing.T) {
	if os.Geteuid() != 0 || os.Getenv("NIMVOR_NETWORK") != "1" {
		t.Skip("needs root and NIMVOR_NETWORK=1")
	}
	sitesRoot, _ := os.MkdirTemp("/tmp", "nimvor-lv-")
	os.Chmod(sitesRoot, 0o755)
	t.Cleanup(func() { os.RemoveAll(sitesRoot) })
	exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", "-u", "1779", "nimvorlv").Run()
	t.Cleanup(func() { exec.Command("userdel", "nimvorlv").Run() })
	root := filepath.Join(sitesRoot, "lv.test")
	os.MkdirAll(filepath.Join(root, "tmp"), 0o755)
	exec.Command("chown", "-R", "nimvorlv:nimvorlv", root).Run()
	sql := "CREATE DATABASE IF NOT EXISTS nimvor_lv; CREATE USER IF NOT EXISTS 'nimvor_lv'@'localhost' IDENTIFIED BY 'lv-Pa55word'; GRANT ALL ON nimvor_lv.* TO 'nimvor_lv'@'localhost';"
	if out, err := exec.Command("mysql", "-e", sql).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	t.Cleanup(func() {
		exec.Command("mysql", "-e", "DROP DATABASE IF EXISTS nimvor_lv; DROP USER IF EXISTS 'nimvor_lv'@'localhost'").Run()
	})
	cfg := config.Default()
	cfg.SitesRoot = sitesRoot
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	env := host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(30*time.Minute), jm, log)
	cred, _, err := env.Credential("nimvorlv")
	if err != nil {
		t.Fatal(err)
	}
	req := Request{App: "laravel", Domain: "lv.test", User: "nimvorlv", Root: root, Path: "app", PHPVersion: testhost.PHPVersion(),
		DB: DB{Name: "nimvor_lv", User: "nimvor_lv", Password: "lv-Pa55word", Host: "localhost"}, URL: "https://lv.test", Migrate: true}
	var buf strings.Builder
	res, err := Install(context.Background(), env, DefaultSources, req, cred, &buf)
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if res["document_root"] != "app/public" {
		t.Errorf("result %v", res)
	}
	envFile, _ := os.ReadFile(filepath.Join(root, "app/.env"))
	for _, want := range []string{"APP_URL=https://lv.test", "DB_CONNECTION=mysql", "DB_DATABASE=nimvor_lv"} {
		if !strings.Contains(string(envFile), want) {
			t.Errorf(".env lacks %q", want)
		}
	}
	out, _ := exec.Command("mysql", "-N", "-e", "SELECT COUNT(*) FROM nimvor_lv.migrations").Output()
	if n := strings.TrimSpace(string(out)); n == "" || n == "0" {
		t.Errorf("migrations did not run: %q", out)
	}
	st, _ := exec.Command("stat", "-c", "%U", filepath.Join(root, "app/artisan")).Output()
	if strings.TrimSpace(string(st)) != "nimvorlv" {
		t.Fatalf("files owned by %s", st)
	}
}

// Downloads every archive in the catalog the way the installer does, unpacks
// it with the catalog's Strip, and checks the web root and the web installer
// path exist, so a vendor moving a file or changing an archive layout is
// found here instead of by a customer. Needs NIMVOR_NETWORK=1.
func TestRealHostCatalogDownloads(t *testing.T) {
	if os.Getenv("NIMVOR_NETWORK") != "1" {
		t.Skip("needs NIMVOR_NETWORK=1")
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	fetch := func(url string) (string, error) {
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("User-Agent", userAgent)
		res, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP %d", res.StatusCode)
		}
		f, err := os.CreateTemp(t.TempDir(), "dl-*")
		if err != nil {
			return "", err
		}
		defer f.Close()
		if n, err := io.Copy(f, io.LimitReader(res.Body, maxDownload+1)); err != nil || n > maxDownload {
			return "", fmt.Errorf("failed or larger than %d bytes", maxDownload)
		}
		return f.Name(), nil
	}
	for slug, def := range Catalog {
		if def.URL == "" && def.GitHub == "" {
			continue
		}
		slug, def := slug, def
		if def.Unverified != "" {
			t.Logf("UNVERIFIED %s: %s", slug, def.Unverified)
			continue
		}
		t.Run(slug, func(t *testing.T) {
			url, err := ResolveURL(context.Background(), client, def)
			if err != nil {
				t.Errorf("%s: %v", slug, err)
				return
			}
			file, err := fetch(url)
			if err != nil {
				t.Errorf("%s: download %s: %v", slug, url, err)
				return
			}
			dest := t.TempDir()
			if err := extractArchive(def.Format, file, dest, def.Strip, os.Getuid(), os.Getgid()); err != nil {
				t.Errorf("%s: unpack (strip %d): %v", slug, def.Strip, err)
				return
			}
			docroot := filepath.Join(dest, def.DocRoot)
			if entries, err := os.ReadDir(docroot); err != nil || len(entries) == 0 {
				t.Errorf("%s: web root %q missing or empty after unpacking", slug, def.DocRoot)
				return
			}
			if rel := strings.Trim(def.Finish, "/"); rel != "" && !strings.HasPrefix(rel, "index.php/") {
				if _, err := os.Stat(filepath.Join(docroot, rel)); err != nil {
					t.Errorf("%s: installer path %s not found in the web root", slug, def.Finish)
				}
			}
		})
	}
	for name, p := range WPPlugins {
		file, err := fetch(p.URL)
		if err != nil {
			t.Errorf("plugin %s: %v", name, err)
			continue
		}
		dest := t.TempDir()
		if err := extractArchive("zip", file, dest, 0, os.Getuid(), os.Getgid()); err != nil {
			t.Errorf("plugin %s: %v", name, err)
			continue
		}
		if _, err := os.Stat(filepath.Join(dest, p.Main)); err != nil {
			t.Errorf("plugin %s: %s missing from the archive", name, p.Main)
		}
	}
}
