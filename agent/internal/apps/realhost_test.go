//go:build realhost

package apps

import (
	"context"
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

	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Requires root, PHP 8.3 with mysqli, a local MariaDB and a WordPress
// tarball in WHACP_WP_TARBALL (with a .sha1 next to it).
func TestRealHostWordPress(t *testing.T) {
	tarball := os.Getenv("WHACP_WP_TARBALL")
	if os.Geteuid() != 0 || tarball == "" {
		t.Skip("needs root and WHACP_WP_TARBALL")
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(tarball))))
	defer srv.Close()
	src := Sources{WordPressURL: srv.URL + "/" + filepath.Base(tarball), Client: srv.Client()}

	sitesRoot, _ := os.MkdirTemp("/tmp", "whacp-wp-")
	os.Chmod(sitesRoot, 0o755)
	t.Cleanup(func() { os.RemoveAll(sitesRoot) })
	exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", "-u", "1778", "whacpwp").Run()
	t.Cleanup(func() { exec.Command("userdel", "whacpwp").Run() })
	root := filepath.Join(sitesRoot, "wp.test")
	os.MkdirAll(filepath.Join(root, "public_html"), 0o755)
	exec.Command("chown", "-R", "whacpwp:whacpwp", root).Run()
	sql := "CREATE DATABASE IF NOT EXISTS whacp_wp; CREATE USER IF NOT EXISTS 'whacp_wp'@'localhost' IDENTIFIED BY 'db-Pa$$''word'; GRANT ALL ON whacp_wp.* TO 'whacp_wp'@'localhost';"
	if out, err := exec.Command("mysql", "-e", sql).CombinedOutput(); err != nil {
		t.Fatalf("%s", out)
	}
	t.Cleanup(func() {
		exec.Command("mysql", "-e", "DROP DATABASE IF EXISTS whacp_wp; DROP USER IF EXISTS 'whacp_wp'@'localhost'").Run()
	})

	cfg := config.Default()
	cfg.SitesRoot = sitesRoot
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	env := host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(30*time.Second), jm, log)
	cred, _, err := env.Credential("whacpwp")
	if err != nil {
		t.Fatal(err)
	}
	req := Request{App: "wordpress", Domain: "wp.test", User: "whacpwp", Root: root, Path: "public_html", PHPVersion: "8.3",
		DB: DB{Name: "whacp_wp", User: "whacp_wp", Password: "db-Pa$$'word", Host: "localhost"}, URL: "https://wp.test",
		Title: "WhaCP test", AdminUser: "owner", AdminEmail: "owner@wp.test", AdminPassword: "a-long-admin-password-1"}
	var buf strings.Builder
	if _, err := Install(context.Background(), env, src, req, cred, &buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	out, _ := exec.Command("mysql", "-N", "-e", "SELECT option_value FROM whacp_wp.wp_options WHERE option_name = 'siteurl'; SELECT user_login FROM whacp_wp.wp_users").Output()
	if got := strings.Fields(string(out)); len(got) != 2 || got[0] != "https://wp.test" || got[1] != "owner" {
		t.Fatalf("database = %q\n%s", out, buf.String())
	}
	info, err := os.Stat(filepath.Join(root, "public_html/wp-config.php"))
	if err != nil || info.Mode().Perm()&0o007 != 0 {
		t.Fatalf("wp-config.php must not be world-readable: %v %v", info.Mode(), err)
	}
	st, _ := exec.Command("stat", "-c", "%U", filepath.Join(root, "public_html/wp-login.php")).Output()
	if strings.TrimSpace(string(st)) != "whacpwp" {
		t.Fatalf("files owned by %s", st)
	}
	// Re-running must not re-install over the existing site.
	if _, err := Install(context.Background(), env, src, req, cred, io.Discard); err == nil {
		t.Fatal("second installation should fail")
	}
}
