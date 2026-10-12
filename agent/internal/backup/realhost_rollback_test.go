//go:build realhost

package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
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

// A website-file restore that fails part-way (a corrupt or truncated
// archive, an interrupted extraction) must not leave the site half
// restored: the safety backup of the previous files is put back
// automatically (security item 152). This builds a real backup, corrupts
// its archive so that the real tar overwrites one file and then errors, and
// checks that after the failed restore the previous files are back.
// Requires root and tar.
func TestRealHostSiteRestoreRollback(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("needs tar")
	}
	const user = "whacp-roll"
	exec.Command("useradd", "-M", "-U", "-s", "/usr/sbin/nologin", user).Run()
	t.Cleanup(func() { exec.Command("userdel", user).Run() })
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.BackupDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsroot := filesystem.Host()
	env := host.New(cfg, fsroot, platform.Detect(fsroot), execx.NewSystemRunner(time.Minute), jm, log)
	s := Service{Env: env}
	ctx := context.Background()
	var out bytes.Buffer

	// The website tree, owned by the unprivileged account and reachable by
	// it (tar runs as that account, not root).
	sitesRoot, err := os.MkdirTemp("/tmp", "whacp-roll-")
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(sitesRoot, 0o711)
	t.Cleanup(func() { os.RemoveAll(sitesRoot) })
	root := filepath.Join(sitesRoot, "roll.test")
	docroot := filepath.Join(root, "public_html")
	os.MkdirAll(docroot, 0o755)
	index := filepath.Join(docroot, "index.html")
	keep := filepath.Join(docroot, "keep.txt")
	os.WriteFile(index, []byte("CURRENT-GOOD"), 0o644)
	os.WriteFile(keep, []byte("KEEP"), 0o644)
	exec.Command("chown", "-R", user+":"+user, root).Run()

	cred, _, err := env.Credential(user)
	if err != nil {
		t.Fatal(err)
	}

	// A valid backup of the current files.
	g, err := s.CreateSite(ctx, SiteRequest{Domain: "roll.test", User: user, Root: root, Files: true, Encrypt: false}, cred, &out)
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out.String())
	}

	// Corrupt that backup's files archive so the real tar extracts one file
	// (overwriting index.html) and then hits garbage and fails. A single
	// gzip member whose decompressed bytes are one complete tar entry
	// followed by random bytes: tar writes index.html, then errors on the
	// trailing garbage.
	gm, gdir, err := s.Load("roll.test", g.ID)
	if err != nil {
		t.Fatal(err)
	}
	var fi int = -1
	for i, f := range gm.Files {
		if f.Kind == "files" {
			fi = i
		}
	}
	if fi < 0 {
		t.Fatal("no files entry in the backup")
	}
	var tbuf bytes.Buffer
	tw := tar.NewWriter(&tbuf)
	body := []byte("CORRUPTED-BY-RESTORE")
	tw.WriteHeader(&tar.Header{Name: "./public_html/index.html", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write(body)
	tw.Flush() // one complete entry, but do NOT Close (no end-of-archive)
	tbuf.Write(bytes.Repeat([]byte("\x7fnot-a-tar-header"), 256))
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(tbuf.Bytes())
	zw.Close()
	corrupt := gz.Bytes()
	archivePath := filepath.Join(gdir, gm.Files[fi].Name)
	if err := os.WriteFile(archivePath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(corrupt)
	gm.Files[fi].SHA256 = hex.EncodeToString(sum[:])
	gm.Files[fi].Size = int64(len(corrupt))
	mb, _ := json.Marshal(gm)
	if err := os.WriteFile(filepath.Join(gdir, "manifest.json"), mb, 0o600); err != nil {
		t.Fatal(err)
	}

	// Restore the corrupt backup with a safety backup. It must fail and roll
	// back to the previous files.
	out.Reset()
	_, err = s.Restore(ctx, RestoreRequest{ID: g.ID, Domain: "roll.test", User: user, Root: root, Files: true, Safety: true}, cred, &out)
	if err == nil {
		t.Fatalf("the corrupt restore unexpectedly succeeded\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "previous files were restored from the safety backup") {
		t.Fatalf("the restore did not roll back: %v\n%s", err, out.String())
	}

	// The previous files are back, not the half-written corrupt content.
	if b, _ := os.ReadFile(index); string(b) != "CURRENT-GOOD" {
		t.Errorf("index.html was left half-restored: %q", b)
	}
	if b, _ := os.ReadFile(keep); string(b) != "KEEP" {
		t.Errorf("keep.txt changed: %q", b)
	}
}
