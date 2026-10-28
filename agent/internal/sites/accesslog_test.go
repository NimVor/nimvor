package sites

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestAccessLogReadableByTheAccountOnly(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root (changes file groups)")
	}
	h := testhost.New(t)
	root := filepath.Join(h.Base, "home/nimvor/a.test")
	logs := filepath.Join(root, "logs")
	os.MkdirAll(logs, 0o750)
	stat := func(p string) (os.FileMode, uint32) {
		info, err := os.Lstat(p)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm(), info.Sys().(*syscall.Stat_t).Gid
	}

	// Created by "nginx -t" under the agent's umask: root:root 0640.
	log := filepath.Join(logs, "access.log")
	os.WriteFile(log, []byte("x\n"), 0o640)
	os.Chmod(log, 0o600)
	if err := AccessLogReadable(h.Env, "/home/nimvor/a.test", h.GID); err != nil {
		t.Fatal(err)
	}
	if mode, gid := stat(log); mode != 0o640 || int(gid) != h.GID {
		t.Fatalf("access log: %o gid %d, want 0640 gid %d", mode, gid, h.GID)
	}
	if info, _ := os.Stat(log); info.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Error("the owner changed")
	}

	// A root file planted as the log (symlink or hard link) keeps its
	// group and mode.
	secret := filepath.Join(h.Base, "etc/secret")
	os.WriteFile(secret, []byte("s"), 0o600)
	os.Remove(log)
	os.Symlink(secret, log)
	if err := AccessLogReadable(h.Env, "/home/nimvor/a.test", h.GID); err != nil {
		t.Log(err) // refusing is fine too
	}
	os.Remove(log)
	if err := os.Link(secret, log); err != nil {
		t.Fatal(err)
	}
	AccessLogReadable(h.Env, "/home/nimvor/a.test", h.GID)
	if mode, gid := stat(secret); mode != 0o600 || gid != 0 {
		t.Fatalf("followed a link: %o gid %d", mode, gid)
	}

	// A logs folder that is a symlink out of the website.
	os.Remove(log)
	os.RemoveAll(logs)
	os.Symlink(filepath.Join(h.Base, "etc"), logs)
	os.Rename(secret, filepath.Join(h.Base, "etc/access.log"))
	AccessLogReadable(h.Env, "/home/nimvor/a.test", h.GID)
	if mode, gid := stat(filepath.Join(h.Base, "etc/access.log")); mode != 0o600 || gid != 0 {
		t.Fatalf("left the website: %o gid %d", mode, gid)
	}
}
