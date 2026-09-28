//go:build realhost

package terminal

import (
	"context"
	"io"
	"log/slog"
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

// A real session: its own systemd unit, as the account, on a terminal the
// account owns, with no_new_privs, ended with the unit when the shell exits.
func TestRealHostTerminalSession(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Log("systemd is not running here: sessions are checked on the systemd test machines")
		return
	}
	sites := "/srv/whacp-termtest"
	os.MkdirAll(sites, 0o755)
	t.Cleanup(func() { os.RemoveAll(sites) })
	const user = "whacptermtest"
	home := filepath.Join(sites, user)
	if out, err := exec.Command("useradd", "-m", "-d", home, "-s", "/usr/sbin/nologin", user).CombinedOutput(); err != nil {
		t.Fatalf("useradd: %s", out)
	}
	t.Cleanup(func() { exec.Command("userdel", user).Run() })

	root := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	cfg.SitesRoot = sites
	os.WriteFile(filepath.Join(cfg.StateDir, "sftp.json"), []byte(`{"users":{"`+user+`":{}},"shells":{"`+user+`":"normal"}}`), 0o600)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(60*time.Second), jm, log)
	m := NewManager(env)
	m.Enabled = true
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()

	res, err := m.Open(OpenParams{User: user, ClientIP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	c, code := dial(t, srv, "https://HOST")
	if code != 101 {
		t.Fatalf("handshake %d", code)
	}
	c.send(opText, []byte(`{"type":"auth","ticket":"`+res["ticket"].(string)+`"}`))
	_, ready := c.until(t, "ready", "")
	id := ready["session_id"].(string)
	c.send(opBinary, []byte("echo user=$(id -un); tty; grep NoNewPrivs /proc/self/status; cat /proc/self/cgroup; echo END-OF-CHECKS\n"))
	out, _ := c.until(t, "", "END-OF-CHECKS\r\n")
	for _, want := range []string{"user=" + user, "/dev/pts/", "NoNewPrivs:\t1", UnitName(id)} {
		if !strings.Contains(out, want) {
			t.Errorf("%q missing in:\n%s", want, out)
		}
	}
	ctx := context.Background()
	if !env.UnitActive(ctx, UnitName(id)) {
		t.Fatal("the session's unit is not running")
	}
	c.send(opBinary, []byte("exit\n"))
	if _, e := c.until(t, "exit", ""); e["reason"] != "The shell exited." {
		t.Fatalf("%v", e)
	}
	deadline := time.Now().Add(10 * time.Second)
	for env.UnitActive(ctx, UnitName(id)) {
		if time.Now().After(deadline) {
			t.Fatal("the unit is still running after the shell exited")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
