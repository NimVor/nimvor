//go:build realhost

package security

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
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

// Requires root, ufw, openssh-server and a running fail2ban server.
// Changes the host's firewall and SSH drop-in, and restores them.
func TestRealHostSecurity(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	root := filesystem.Host()
	cfg := config.Default()
	cfg.StateDir = t.TempDir()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	jm, _ := jobs.NewManager(t.TempDir(), 1, time.Minute, log)
	env := host.New(cfg, root, platform.Detect(root), execx.NewSystemRunner(30*time.Second), jm, log)
	s := NewService(env)
	ctx := context.Background()
	ConfirmWindow = time.Hour

	st, err := Firewall(ctx, env)
	if err != nil || !st.Installed || !st.SSHOpen {
		t.Fatalf("status %+v %v (needs a firewall rule allowing SSH)", st, err)
	}
	p, err := s.AddRule(ctx, RuleInput{Action: "allow", Port: "8443", Proto: "tcp", From: "192.0.2.0/24", Comment: "rt"})
	if err != nil {
		t.Fatal(err)
	}
	has8443 := func() bool {
		st, err := Firewall(ctx, env)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range st.Rules {
			if strings.Contains(r.Spec, "8443") {
				return true
			}
		}
		return false
	}
	if !has8443() {
		t.Fatal("rule not added")
	}
	if _, err := s.AddRule(ctx, RuleInput{Action: "allow", Port: "8443", Proto: "tcp", From: "192.0.2.0/24"}); err == nil {
		t.Fatal("second change accepted while pending")
	}
	if err := s.guard.Undo(p.ID); err != nil {
		t.Fatal(err)
	}
	if has8443() {
		t.Fatal("rule not rolled back")
	}
	// Duplicate detection against ufw's own ordering.
	p, err = s.AddRule(ctx, RuleInput{Action: "allow", Port: "8443", Proto: "tcp", From: "192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	s.guard.Confirm(p.ID)
	if _, err := s.AddRule(ctx, RuleInput{Action: "allow", Port: "8443", Proto: "tcp", From: "192.0.2.0/24"}); err == nil {
		t.Fatal("duplicate rule accepted")
	}
	st, _ = Firewall(ctx, env)
	for _, r := range st.Rules {
		if strings.Contains(r.Spec, "8443") {
			p, err = s.DeleteRule(ctx, r.Spec)
			if err != nil {
				t.Fatal(err)
			}
			s.guard.Confirm(p.ID)
		}
	}

	// SSH: writes the drop-in, validates, verifies the effective value.
	t.Cleanup(func() { os.Remove(env.M.SSHDropIn) })
	eff, _ := SSHEffective(ctx, env)
	p, err = s.ConfigureSSH(ctx, SSHSettings{Port: 22, PermitRootLogin: "prohibit-password", PasswordAuthentication: true, MaxAuthTries: 3})
	if err != nil && !strings.Contains(err.Error(), "reload") {
		t.Fatal(err)
	}
	if err == nil {
		eff2, _ := SSHEffective(ctx, env)
		if eff2["maxauthtries"] != "3" {
			t.Fatalf("effective maxauthtries = %s (was %s)", eff2["maxauthtries"], eff["maxauthtries"])
		}
		s.guard.Undo(p.ID)
		if _, err := os.Stat(env.M.SSHDropIn); err == nil {
			t.Fatal("drop-in not removed by undo")
		}
	}

	// fail2ban: real --test and reload.
	t.Cleanup(func() { os.Remove(env.M.Fail2banJail); exec.Command("fail2ban-client", "reload").Run() })
	if err := s.ConfigureFail2ban(ctx, Fail2banSettings{BanMinutes: 30, FindMinutes: 10, MaxRetry: 4}); err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigureFail2ban(ctx, Fail2banSettings{Jails: []string{"nonexistent"}, BanMinutes: 30, FindMinutes: 10, MaxRetry: 4}); err == nil {
		t.Fatal("unknown jail accepted")
	}
	checks := Audit(ctx, env)
	if len(checks) < 4 {
		t.Fatalf("audit = %+v", checks)
	}
}
