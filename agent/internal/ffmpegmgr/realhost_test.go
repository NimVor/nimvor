//go:build realhost

package ffmpegmgr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
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

// FFmpeg limited to the whacp-ffmpeg group on a real system: a member runs
// it, another account cannot; the limit survives a package reinstall.
func TestRealHostFFmpegAccess(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := os.Stat("/usr/bin/ffmpeg"); err != nil {
		t.Skip("needs FFmpeg installed")
	}
	fsys := filesystem.Host()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	env := host.New(cfg, fsys, platform.Detect(fsys), execx.NewSystemRunner(2*time.Minute), jm, log)
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, cfg.StateDir+"/**")
	r := actions.NewRegistry(true)
	Register(r, env)
	call := func(name string, params any) map[string]any {
		t.Helper()
		a, _ := r.Lookup(name)
		b, _ := json.Marshal(params)
		res, err := a.Handler(context.Background(), b)
		if err != nil {
			t.Fatalf("%s: %+v", name, err)
		}
		out, _ := json.Marshal(res)
		var m map[string]any
		json.Unmarshal(out, &m)
		return m
	}
	for _, u := range []string{"wcpffyes", "wcpffno"} {
		exec.Command("useradd", "--create-home", "--shell", "/bin/sh", u).Run()
	}
	t.Cleanup(func() {
		for _, p := range Binaries {
			exec.Command("dpkg-statoverride", "--remove", p).Run()
			os.Chown(p, 0, 0)
			os.Chmod(p, 0o755)
		}
		exec.Command("groupdel", Group).Run()
		for _, u := range []string{"wcpffyes", "wcpffno"} {
			exec.Command("userdel", "--remove", u).Run()
		}
	})
	runs := func(user string) bool {
		out, err := exec.Command("runuser", "-u", user, "--", "/usr/bin/ffmpeg", "-hide_banner", "-version").CombinedOutput()
		if err != nil && !strings.Contains(string(out), "Permission denied") {
			t.Fatalf("%s: unexpected failure: %v %s", user, err, out)
		}
		return err == nil
	}
	if !runs("wcpffno") {
		t.Fatal("FFmpeg not usable before WhaCP limits it")
	}
	res := call("ffmpeg.access", map[string]any{"users": []string{"wcpffyes"}})
	if res["restricted"] != true || strings.Join(toStrings(res["members"]), ",") != "wcpffyes" || !strings.HasPrefix(res["version"].(string), "ffmpeg version") {
		t.Fatalf("access: %v", res)
	}
	if !runs("wcpffyes") || runs("wcpffno") {
		t.Fatal("the member must run FFmpeg and the other account must not")
	}
	// Package reinstall: dpkg keeps the override; on RHEL the agent applies it again.
	if env.Platform.PackageManager() == "apt" {
		if out, err := exec.Command("dpkg-statoverride", "--list", "/usr/bin/ffmpeg").Output(); err != nil || strings.TrimSpace(string(out)) != "root whacp-ffmpeg 750 /usr/bin/ffmpeg" {
			t.Fatalf("statoverride: %q %v", out, err)
		}
		if out, err := exec.Command("apt-get", "install", "-y", "-q", "--reinstall", "--no-install-recommends", "ffmpeg").CombinedOutput(); err != nil {
			t.Logf("reinstall not possible here (%v): %s", err, out)
		}
	} else {
		exec.Command("dnf", "-y", "-q", "reinstall", "ffmpeg-free").Run()
		if err := Reapply(context.Background(), env); err != nil {
			t.Fatal(err)
		}
	}
	if runs("wcpffno") || !runs("wcpffyes") {
		t.Fatal("the limit did not survive the package reinstall")
	}
	res = call("ffmpeg.access", map[string]any{"users": []string{}})
	if strings.Join(toStrings(res["removed"]), ",") != "wcpffyes" || runs("wcpffyes") {
		t.Fatalf("removal: %v", res)
	}
	if _, err := call2(r, "ffmpeg.access", map[string]any{"users": []string{"root"}}); err == nil {
		t.Fatal("a system account was accepted")
	}
}

func call2(r *actions.Registry, name string, params any) (any, error) {
	a, _ := r.Lookup(name)
	b, _ := json.Marshal(params)
	return a.Handler(context.Background(), b)
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}
