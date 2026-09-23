//go:build realhost

package backup

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Requires root, useradd and a local MariaDB reachable over its socket.
func TestRealHostBackupRestore(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	sites, _ := os.MkdirTemp("/tmp", "whacp-sites-")
	t.Cleanup(func() { os.RemoveAll(sites) })
	os.Chmod(sites, 0o755)
	exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", "-u", "1777", "whacprtbk").Run()
	t.Cleanup(func() { exec.Command("userdel", "whacprtbk").Run() })
	root := filepath.Join(sites, "rt.test")
	os.MkdirAll(filepath.Join(root, "public_html/sub"), 0o750)
	os.MkdirAll(filepath.Join(root, "logs"), 0o750)
	os.WriteFile(filepath.Join(root, "public_html/index.php"), []byte("<?php echo 1;"), 0o640)
	os.WriteFile(filepath.Join(root, "logs/access.log"), []byte("log"), 0o640)
	os.Symlink("public_html", filepath.Join(root, "current"))
	exec.Command("chown", "-R", "whacprtbk:whacprtbk", root).Run()

	cfg := config.Default()
	cfg.SitesRoot = sites
	cfg.StateDir = t.TempDir()
	cfg.BackupDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	env := host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(30*time.Second), jm, log)
	s := Service{Env: env}
	cred, _, err := env.Credential("whacprtbk")
	if err != nil {
		t.Fatal(err)
	}
	mysql := func(sql string) {
		if out, err := exec.Command("mysql", "-e", sql).CombinedOutput(); err != nil {
			t.Fatalf("%s: %s", sql, out)
		}
	}
	mysql("CREATE DATABASE IF NOT EXISTS whacp_bk; CREATE TABLE IF NOT EXISTS whacp_bk.t (v TEXT); DELETE FROM whacp_bk.t; INSERT INTO whacp_bk.t VALUES ('héllo');")
	t.Cleanup(func() { exec.Command("mysql", "-e", "DROP DATABASE IF EXISTS whacp_bk; DROP DATABASE IF EXISTS whacp_bk2").Run() })

	var buf bytes.Buffer
	m, err := s.CreateSite(context.Background(), SiteRequest{Domain: "rt.test", User: "whacprtbk", Root: root, Files: true, Databases: []string{"whacp_bk"}, Encrypt: true}, cred, &buf)
	if err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	// Change things, then restore.
	os.Remove(filepath.Join(root, "public_html/index.php"))
	mysql("CREATE DATABASE whacp_bk2")
	if _, err := s.Restore(context.Background(), RestoreRequest{Domain: "rt.test", User: "whacprtbk", Root: root, ID: m.ID, Files: true, Databases: map[string]string{"whacp_bk": "whacp_bk2"}, Safety: true}, cred, &buf); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	b, err := os.ReadFile(filepath.Join(root, "public_html/index.php"))
	if string(b) != "<?php echo 1;" {
		t.Fatalf("file not restored: %q %v\n%s", b, err, buf.String())
	}
	if l, _ := os.Readlink(filepath.Join(root, "current")); l != "public_html" {
		t.Errorf("symlink = %q", l)
	}
	out, _ := exec.Command("mysql", "-N", "-e", "SELECT v FROM whacp_bk2.t").Output()
	if string(bytes.TrimSpace(out)) != "héllo" {
		t.Fatalf("db restore = %q", out)
	}
	if list, _ := s.List("rt.test"); len(list) != 2 {
		t.Errorf("expected the backup plus a safety backup, got %d", len(list))
	}
}
