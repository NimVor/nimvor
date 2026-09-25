// Package testhost builds a writable copy of a fixture server for tests of
// modifying actions, with a FakeRunner that simulates system tools.
package testhost

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
)

// Host is a test server.
type Host struct {
	Base   string
	Env    *host.Env
	Runner *execx.FakeRunner
	// Fail makes calls whose key has this prefix fail.
	Fail map[string]string
	// FailFrom delays a Fail entry until the n-th matching call (1-based).
	FailFrom map[string]int
	counts   map[string]int
	// Respond, when set, answers calls first (ok=false falls through).
	Respond func(id string, args []string) (out string, err error, ok bool)
	// NextUID is assigned to accounts created with useradd.
	NextUID int
	UID     int
	GID     int
}

// New copies the ubuntu-host fixture into a temp dir.
func New(t *testing.T) *Host {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(file), "../../tests/fixtures/ubuntu-host")
	base := t.TempDir()
	if err := copyTree(src, base); err != nil {
		t.Fatal(err)
	}
	h := &Host{Base: base, Fail: map[string]string{}, FailFrom: map[string]int{}, counts: map[string]int{}, NextUID: 1500, UID: 1500, GID: 1500}
	webUID, webGID := 33, 33
	if os.Geteuid() != 0 {
		// Unprivileged test runs can only chown to themselves.
		h.UID, h.GID = os.Getuid(), os.Getgid()
		h.NextUID = h.UID
		webUID, webGID = h.UID, h.GID
	}
	passwd := fmt.Sprintf("root:x:0:0:root:/root:/bin/bash\nwww-data:x:%d:%d:www-data:/var/www:/usr/sbin/nologin\nexample:x:%d:%d::/var/www/example.com:/usr/sbin/nologin\n", webUID, webGID, h.UID, h.GID)
	os.WriteFile(filepath.Join(base, "etc/passwd"), []byte(passwd), 0o644)
	for _, d := range []string{"run/php", "var/lib/whacp-agent", "home/whacp", "var/backups/whacp", "etc/cron.d", "etc/systemd/system", "etc/ssh/sshd_config.d", "etc/fail2ban/jail.d", "etc/letsencrypt/renewal-hooks/deploy", "var/www"} {
		os.MkdirAll(filepath.Join(base, d), 0o755)
	}
	h.Runner = &execx.FakeRunner{Handler: h.handle, Unavailable: map[string]bool{}}
	root, _ := filesystem.NewRoot(base)
	cfg := config.Default()
	cfg.RootDir = base
	cfg.AllowWriteActions = true
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 2, time.Minute, log)
	// Registered after TempDir, so it runs first: background jobs a test
	// started (archives, restores) finish before their folders are removed.
	t.Cleanup(jm.Drain)
	h.Env = host.New(cfg, root, platform.Detect(root), h.Runner, jm, log)
	h.Env.Writer.Owners = agentfs.SelfOwners()
	h.Env.Now = func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) }
	return h
}

func (h *Host) handle(id string, o execx.Opts, args []string) (string, error) {
	key := strings.TrimSpace(id + " " + strings.Join(args, " "))
	for prefix, msg := range h.Fail {
		if strings.HasPrefix(key, prefix) {
			h.counts[prefix]++
			if h.counts[prefix] >= h.FailFrom[prefix] {
				return msg, fmt.Errorf("exit status 1")
			}
		}
	}
	if h.Respond != nil {
		if out, err, ok := h.Respond(id, args); ok {
			return out, err
		}
	}
	switch {
	case id == "tar" && strings.Contains(key, " -cf - "):
		return "TAR-DATA:" + args[1], nil
	case id == "mysqldump":
		return "-- dump of " + args[len(args)-1] + "\n", nil
	case key == "nginx -v":
		return "nginx version: nginx/1.24.0 (Ubuntu)", nil
	case id == "systemctl" && len(args) > 0 && args[0] == "is-active":
		return "active", nil
	case id == "useradd":
		name := args[len(args)-1]
		line := fmt.Sprintf("%s:x:%d:%d::/home/whacp:/usr/sbin/nologin\n", name, h.NextUID, h.NextUID)
		f, _ := os.OpenFile(filepath.Join(h.Base, "etc/passwd"), os.O_APPEND|os.O_WRONLY, 0o644)
		f.WriteString(line)
		f.Close()
		return "", nil
	case id == "userdel":
		name := args[len(args)-1]
		b, _ := os.ReadFile(filepath.Join(h.Base, "etc/passwd"))
		var keep []string
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if !strings.HasPrefix(l, name+":") {
				keep = append(keep, l)
			}
		}
		os.WriteFile(filepath.Join(h.Base, "etc/passwd"), []byte(strings.Join(keep, "\n")+"\n"), 0o644)
		return "", nil
	}
	return "", nil
}

// Read returns a file from the test host ("<missing>" when absent).
func (h *Host) Read(p string) string {
	b, err := os.ReadFile(filepath.Join(h.Base, p))
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

// Exists reports whether a host path exists (without following links).
func (h *Host) Exists(p string) bool {
	_, err := os.Lstat(filepath.Join(h.Base, p))
	return err == nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			l, _ := os.Readlink(p)
			return os.Symlink(l, target)
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		default:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(target, b, info.Mode().Perm())
		}
	})
}
