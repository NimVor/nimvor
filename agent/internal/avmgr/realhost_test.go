//go:build realhost

package avmgr

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

// Requires root and the clamav package. Scans a website with the real
// clamscan as the website's account, with a test signature (the MD5 of a
// file), a file the account cannot read, and quarantine on.
func TestRealHostScan(t *testing.T) {
	if _, err := exec.LookPath("clamscan"); os.Geteuid() != 0 || err != nil {
		t.Skip("needs root and ClamAV")
	}
	sitesRoot, _ := os.MkdirTemp("/tmp", "whacp-av-")
	os.Chmod(sitesRoot, 0o755)
	t.Cleanup(func() { os.RemoveAll(sitesRoot) })
	exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", "-u", "1782", "whacpav").Run()
	t.Cleanup(func() { exec.Command("userdel", "whacpav").Run() })
	root := filepath.Join(sitesRoot, "av.test")
	os.MkdirAll(filepath.Join(root, "public_html/up"), 0o755)
	bad := []byte("<?php /* whacp test malware */ eval($_POST['x']);\n")
	os.WriteFile(filepath.Join(root, "public_html/up/shell.php"), bad, 0o644)
	os.WriteFile(filepath.Join(root, "public_html/index.php"), []byte("<?php echo 'hi';\n"), 0o644)
	exec.Command("chown", "-R", "whacpav:whacpav", root).Run()
	// Not readable by the account: counted as an error, the scan goes on.
	os.MkdirAll(filepath.Join(root, "logs"), 0o755)
	os.WriteFile(filepath.Join(root, "logs/secret.log"), []byte("root only\n"), 0o600)

	cfg := config.Default()
	cfg.SitesRoot = sitesRoot
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsys := filesystem.Host()
	env := host.New(cfg, fsys, platform.Detect(fsys), execx.NewSystemRunner(time.Minute), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	scan := func(quarantine bool) (jobs.Job, string) {
		a, _ := r.Lookup("av.scan")
		res, err := a.Handler(context.Background(), json.RawMessage(fmt.Sprintf(`{"domain":"av.test","user":"whacpav","root":%q,"quarantine":%v}`, root, quarantine)))
		if err != nil {
			t.Fatal(err)
		}
		id := res.(map[string]any)["job_id"].(string)
		j, _ := jm.Wait(context.Background(), id)
		out, _, _ := jm.Log(id, 0)
		return j, string(out)
	}

	dbs, _ := filepath.Glob("/var/lib/clamav/*.c[lv]d")
	if len(dbs) == 0 {
		// A fresh install before freshclam's first download.
		if j, out := scan(false); j.Status != "failed" || !strings.Contains(j.Error, "no virus signatures") {
			t.Fatalf("without signatures: %s %s\n%s", j.Status, j.Error, out)
		}
	}
	sum := md5.Sum(bad)
	sig := "/var/lib/clamav/whacp-realhost-test.hdb"
	if err := os.WriteFile(sig, []byte(fmt.Sprintf("%s:%d:WhaCP.Test.Webshell\n", hex.EncodeToString(sum[:]), len(bad))), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(sig) })

	// Only an unreadable file (exit status 2): the scan still completes.
	os.Rename(filepath.Join(root, "public_html/up/shell.php"), filepath.Join(sitesRoot, "shell.php"))
	j, out := scan(true)
	if j.Status != "succeeded" {
		t.Fatalf("clean scan with an unreadable file: %s %s\n%s", j.Status, j.Error, out)
	}
	b, _ := json.Marshal(j.Result)
	var res Result
	json.Unmarshal(b, &res)
	if len(res.Infected) != 0 || res.Errors < 1 || res.Scanned < 1 {
		t.Fatalf("clean: %+v", res)
	}
	os.Rename(filepath.Join(sitesRoot, "shell.php"), filepath.Join(root, "public_html/up/shell.php"))

	j, out = scan(true)
	if j.Status != "succeeded" {
		t.Fatalf("scan %s: %s\n%s", j.Status, j.Error, out)
	}
	b, _ = json.Marshal(j.Result)
	json.Unmarshal(b, &res)
	if len(res.Infected) != 1 || res.Infected[0].Path != "public_html/up/shell.php" || !strings.HasPrefix(res.Infected[0].Signature, "WhaCP.Test.Webshell") || !res.Infected[0].Quarantined {
		t.Fatalf("infected: %+v\n%s", res, out)
	}
	if _, err := os.Lstat(filepath.Join(root, "public_html/up/shell.php")); !os.IsNotExist(err) {
		t.Error("still in place")
	}
	moved, _ := filepath.Glob(filepath.Join(root, QuarantineDir, "*", "public_html/up/shell.php"))
	if len(moved) != 1 {
		t.Fatalf("quarantine: %v", moved)
	}
	if info, _ := os.Stat(moved[0]); info.Mode().Perm() != 0 {
		t.Errorf("mode %v", info.Mode())
	}
	// The next scan skips the quarantine folder.
	j, _ = scan(true)
	b, _ = json.Marshal(j.Result)
	json.Unmarshal(b, &res)
	if j.Status != "succeeded" || len(res.Infected) != 0 {
		t.Fatalf("rescan: %s %+v", j.Status, res)
	}
}
