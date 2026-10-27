package services

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func writeFile(t *testing.T, base, p, body string, mode os.FileMode) {
	t.Helper()
	os.MkdirAll(filepath.Dir(filepath.Join(base, p)), 0o755)
	if err := os.WriteFile(filepath.Join(base, p), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestSelfUpdate(t *testing.T) {
	h := testhost.New(t)
	sha := strings.Repeat("ab", 20)
	writeFile(t, h.Base, "etc/nimvor/source.json", `{"path":"/srv/nimvor"}`, 0o644)
	writeFile(t, h.Base, "srv/nimvor/.git/HEAD", "ref: refs/heads/main\n", 0o644)
	writeFile(t, h.Base, "srv/nimvor/.git/packed-refs", "# pack-refs\n"+sha+" refs/heads/main\n", 0o644)
	writeFile(t, h.Base, "srv/nimvor/.git/config", "[remote \"origin\"]\n\turl = https://user:secret@github.com/acme/nimvor.git\n[branch \"main\"]\n\tremote = origin\n\tmerge = refs/heads/main\n", 0o644)
	writeFile(t, h.Base, "srv/nimvor/installer/update.sh", "#!/bin/sh\n", 0o755)
	writeFile(t, h.Base, "srv/nimvor/installer/install.sh", "#!/bin/sh\n", 0o755)
	for _, d := range []string{"", "srv", "srv/nimvor"} {
		os.Chmod(filepath.Join(h.Base, d), 0o755)
	}
	s, err := ReadSource(h.Env)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Git || s.Branch != "main" || s.Commit != sha || s.Remote != "https://github.com/acme/nimvor.git" || !s.Updatable {
		t.Fatalf("source: %+v", s)
	}
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("nimvor.update")
	// The fake systemctl says every unit is active: an update "is running".
	if _, err := a.Handler(context.Background(), json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("err = %v", err)
	}
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "systemctl" && len(args) > 0 && args[0] == "is-active" {
			return "inactive", errors.New("exit status 3"), true
		}
		return "", nil, false
	}
	if _, err := a.Handler(context.Background(), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if !h.Runner.Called("systemd-run --unit=nimvor-update.service --description=NimVor update --property=StandardOutput=truncate:/var/log/nimvor-update.log --property=StandardError=append:/var/log/nimvor-update.log --property=TimeoutStopSec=30min --property=KillMode=control-group -- /bin/bash /srv/nimvor/installer/update.sh") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
	// A parent folder others may write to: refused.
	os.Chmod(filepath.Join(h.Base, "srv"), 0o777)
	if s, _ := ReadSource(h.Env); s.Updatable || !strings.Contains(s.Reason, "/srv ") {
		t.Fatalf("writable parent accepted: %+v", s)
	}
	os.Chmod(filepath.Join(h.Base, "srv"), 0o755)
	// Writable by others: the panel does not run it.
	os.Chmod(filepath.Join(h.Base, "srv/nimvor/installer/update.sh"), 0o777)
	if s, _ := ReadSource(h.Env); s.Updatable || !strings.Contains(s.Reason, "other accounts") {
		t.Fatalf("writable script accepted: %+v", s)
	}
}
