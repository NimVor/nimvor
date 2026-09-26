//go:build realhost

package workers

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// A worker created through the agent starts under systemd as the website
// account with the installed layout (state directory 0700 root), is
// confined, can unpack a tar archive (Ubuntu 26.04: tar uses openat2), and
// its launcher is readable by that account only.
func TestRealHostWorker(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Fatal("systemd is not running: workers are systemd services")
	}
	const sites, name, domain, slug = "/srv/whacp-rt-workers", "rtworker", "rt-worker.test", "9-rt"
	home := filepath.Join(sites, domain)
	state := "/var/lib/whacp-rt-agent"
	_, hadDir := os.Stat(host.WorkersDir)
	removeUser := func() {
		exec.Command("systemctl", "stop", Unit(slug)).Run()
		exec.Command("pkill", "-KILL", "-u", name).Run()
		exec.Command("userdel", "-f", name).Run()
	}
	removeUser()
	os.RemoveAll(sites)
	if out, err := exec.Command("useradd", "--home-dir", home, "--no-create-home", "--shell", "/usr/sbin/nologin", "--user-group", "--uid", "2891", name).CombinedOutput(); err != nil {
		t.Fatalf("useradd: %s", out)
	}
	os.MkdirAll(filepath.Join(home, "public_html"), 0o750)
	os.Chmod(sites, 0o755)
	exec.Command("chown", "-R", name+":"+name, home).Run()
	os.Chmod(home, 0o710)
	os.MkdirAll(state, 0o700)
	os.Chmod(state, 0o700)
	t.Cleanup(func() {
		removeUser()
		os.RemoveAll(sites)
		os.RemoveAll(state)
		os.Remove(filepath.Join(host.WorkersDir, slug+".sh"))
		if hadDir != nil {
			os.Remove(host.WorkersDir)
		}
		os.Remove(filepath.Join("/etc/systemd/system", Unit(slug)))
		exec.Command("systemctl", "daemon-reload").Run()
	})

	// An archive with nested folders, owned by the account.
	var arc bytes.Buffer
	tw := tar.NewWriter(&arc)
	tw.WriteHeader(&tar.Header{Name: "d/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: "d/e/", Typeflag: tar.TypeDir, Mode: 0o755})
	tw.WriteHeader(&tar.Header{Name: "d/e/f.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 6})
	tw.Write([]byte("hello\n"))
	tw.Close()
	dir := filepath.Join(home, "public_html")
	os.WriteFile(filepath.Join(dir, "a.tar"), arc.Bytes(), 0o644)
	exec.Command("chown", name+":"+name, filepath.Join(dir, "a.tar")).Run()

	cfg := config.Default()
	cfg.SitesRoot = sites
	cfg.StateDir = state
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	root := filesystem.Host()
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	r := actions.NewRegistry(true)
	Register(r, env)
	do := func(action string, params any) {
		t.Helper()
		a, _ := r.Lookup(action)
		b, _ := json.Marshal(params)
		if _, err := a.Handler(context.Background(), b); err != nil {
			t.Fatalf("%s: %+v", action, err)
		}
	}

	cmd := `sh -c 'mkdir -p out; tar -xf a.tar -C out 2>tar.err; echo $? >tar.rc; grep -E "^(Uid|Gid|CapEff|NoNewPrivs):" /proc/self/status >status.txt; touch done'`
	do("workers.apply", map[string]any{"slug": slug, "name": "RT", "domain": domain, "user": name, "directory": "public_html", "command": cmd, "restart": "no", "enabled": true})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "done")); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	read := func(f string) string { b, _ := os.ReadFile(filepath.Join(dir, f)); return string(b) }
	if _, err := os.Stat(filepath.Join(dir, "done")); err != nil {
		j, _ := exec.Command("journalctl", "--no-pager", "-n", "30", "-u", Unit(slug)).CombinedOutput()
		t.Fatalf("the worker did not run:\n%s", j)
	}
	if rc := strings.TrimSpace(read("tar.rc")); rc != "0" || read("out/d/e/f.txt") != "hello\n" {
		t.Errorf("tar in the worker: exit %s: %s", rc, read("tar.err"))
	}
	uid := strconv.Itoa(2891)
	status := read("status.txt")
	for _, want := range []string{"Uid:\t" + uid + "\t" + uid, "CapEff:\t0000000000000000", "NoNewPrivs:\t1"} {
		if !strings.Contains(status, want) {
			t.Errorf("worker status lacks %q:\n%s", want, status)
		}
	}

	// The launcher: the account's group only; other accounts cannot read it.
	launcher := filepath.Join(host.WorkersDir, slug+".sh")
	if fi, err := os.Stat(launcher); err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("launcher: %v %v", fi, err)
	}
	if out, err := exec.Command("setpriv", "--reuid=65534", "--regid=65534", "--clear-groups", "cat", launcher).CombinedOutput(); err == nil {
		t.Errorf("another account read the launcher:\n%s", out)
	}

	// For the record: tar under RestrictSUIDSGID (the setting workers no
	// longer use) on this system.
	os.RemoveAll(filepath.Join(dir, "out2"))
	out, err := exec.Command("systemd-run", "--quiet", "--wait", "--collect", "--uid="+uid, "--gid="+uid, "--working-directory="+dir,
		"--property=RestrictSUIDSGID=yes", "--", "sh", "-c", "mkdir -p out2 && tar -xf a.tar -C out2 2>tar2.err").CombinedOutput()
	t.Logf("tar under RestrictSUIDSGID=yes: err=%v %s %s", err, out, read("tar2.err"))

	a, _ := r.Lookup("workers.remove")
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"slug":"`+slug+`"}`)); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{launcher, filepath.Join("/etc/systemd/system", Unit(slug))} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s left after removal", p)
		}
	}
}
