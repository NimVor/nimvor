//go:build realhost

package hardening

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

func realEnv(t *testing.T) *host.Env {
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	fsys := filesystem.Host()
	env := host.New(cfg, fsys, platform.Detect(fsys), execx.NewSystemRunner(time.Minute), jm, log)
	AllowPaths(env)
	return env
}

func as(user string, args ...string) (string, error) {
	out, err := exec.Command("runuser", append([]string{"-u", user, "--"}, args...)...).CombinedOutput()
	return string(out), err
}

// Requires root and gcc. Restricts the real compilers, compiles as an
// allowed account and fails as another, then opens them again.
func TestRealHostCompilerAccess(t *testing.T) {
	if _, err := exec.LookPath("gcc"); os.Geteuid() != 0 || err != nil {
		t.Skip("needs root and gcc")
	}
	exec.Command("useradd", "-M", "-U", "-s", "/bin/sh", "-u", "1791", "whacpcc").Run()
	exec.Command("useradd", "-M", "-U", "-s", "/bin/sh", "-u", "1792", "whacpnocc").Run()
	t.Cleanup(func() {
		exec.Command("userdel", "whacpcc").Run()
		exec.Command("userdel", "whacpnocc").Run()
	})
	env := realEnv(t)
	ctx := context.Background()
	t.Cleanup(func() { SetCompilers(ctx, env, false, nil); exec.Command("groupdel", CompilerGroup).Run() })
	src := "/tmp/whacp-cc-test.c"
	os.WriteFile(src, []byte("int main(void){return 0;}\n"), 0o644)
	defer os.Remove(src)

	if err := SetCompilers(ctx, env, true, []string{"whacpcc"}); err != nil {
		t.Fatal(err)
	}
	if out, err := as("whacpcc", "gcc", "-o", "/tmp/whacp-cc-ok", src); err != nil {
		t.Fatalf("allowed account cannot compile: %v %s", err, out)
	}
	os.Remove("/tmp/whacp-cc-ok")
	if out, err := as("whacpnocc", "gcc", "-o", "/tmp/whacp-cc-no", src); err == nil {
		t.Fatalf("restricted account compiled: %s", out)
	}
	st := CompilerStatus(env)
	if !st["restricted"].(bool) {
		t.Fatalf("status: %v", st)
	}
	if err := SetCompilers(ctx, env, false, nil); err != nil {
		t.Fatal(err)
	}
	if out, err := as("whacpnocc", "gcc", "-o", "/tmp/whacp-cc-no", src); err != nil {
		t.Fatalf("not opened again: %v %s", err, out)
	}
	os.Remove("/tmp/whacp-cc-no")
}

// Requires root and nft. Loads the generated table: an account gets a
// reset on port 25, root does not; then removes it.
func TestRealHostSMTPRules(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if out, err := exec.Command("nft", "list", "tables").CombinedOutput(); err != nil {
		t.Skipf("nftables not usable here: %s", out)
	}
	env := realEnv(t)
	rules, _ := smtpRules(env)
	f := t.TempDir() + "/r.nft"
	os.WriteFile(f, []byte(rules), 0o600)
	if out, err := exec.Command("nft", "-c", "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("nft refuses the rules: %s\n%s", out, rules)
	}
	if out, err := exec.Command("nft", "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("load: %s", out)
	}
	t.Cleanup(func() { exec.Command("nft", "delete", "table", "inet", "whacp_smtp").Run() })
	// Loading twice replaces the table (the boot unit and a restart).
	if out, err := exec.Command("nft", "-f", f).CombinedOutput(); err != nil {
		t.Fatalf("reload: %s", out)
	}
	// The container may refuse outside connections itself, so the rule's
	// counter tells whether the table rejected the attempt.
	counter := func() string {
		out, _ := exec.Command("nft", "list", "chain", "inet", "whacp_smtp", "output").CombinedOutput()
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, "reject") {
				return strings.TrimSpace(l)
			}
		}
		t.Fatalf("no reject rule: %s", out)
		return ""
	}
	try := func(user, port string) {
		cmd := "timeout 3 bash -c 'exec 3<>/dev/tcp/192.0.2.1/" + port + "' 2>&1; true"
		if user == "root" {
			exec.Command("bash", "-c", cmd).Run()
		} else {
			as(user, "bash", "-c", cmd)
		}
	}
	before := counter()
	try("root", "25")
	if c := counter(); c != before {
		t.Fatalf("root was rejected: %s", c)
	}
	try("nobody", "80")
	if c := counter(); c != before {
		t.Fatalf("port 80 was rejected: %s", c)
	}
	try("nobody", "25")
	if c := counter(); c == before {
		t.Fatalf("nobody was not rejected: %s", c)
	}
}
