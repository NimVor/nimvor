package execx

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSystemRunnerRejectsUnknownCommands(t *testing.T) {
	r := NewSystemRunner(time.Second)
	for _, id := range []string{"sh", "bash", "/bin/sh", "rm", "wget", "nc"} {
		if _, err := r.Run(context.Background(), id, "-c", "id"); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("Run(%q) err = %v, want ErrNotAllowed", id, err)
		}
	}
}

func TestSystemRunnerRejectsControlCharacters(t *testing.T) {
	r := &SystemRunner{paths: map[string]string{"systemctl": "/bin/true"}}
	if _, err := r.Run(context.Background(), "systemctl", "show\nx"); err == nil {
		t.Fatal("expected invalid argument error")
	}
}

func TestLimitedBuffer(t *testing.T) {
	b := limitedBuffer{limit: 4}
	b.Write([]byte("abc"))
	n, _ := b.Write([]byte("defg"))
	if n != 4 || b.String() != "abcd" {
		t.Fatalf("buffer = %q (n=%d)", b.String(), n)
	}
}

func TestShellOnlyAsUnprivilegedUser(t *testing.T) {
	r := &SystemRunner{paths: map[string]string{"sh": "/bin/sh", "tar": "/usr/bin/tar"}}
	for _, id := range []string{"sh", "tar"} {
		for _, o := range []Opts{{}, {As: &Credential{UID: 0, GID: 0}}, {As: &Credential{UID: 33, GID: 33}}} {
			if _, err := r.RunWith(context.Background(), id, o, "--version"); !errors.Is(err, ErrNotAllowed) {
				t.Errorf("%s as %+v: %v", id, o.As, err)
			}
		}
	}
}

func TestVersionedPHPIDs(t *testing.T) {
	r := &SystemRunner{paths: map[string]string{}}
	if _, err := r.resolve("php8.3; id"); !errors.Is(err, ErrNotAllowed) {
		t.Errorf("injected id: %v", err)
	}
	if _, err := r.resolve("php-fpm8.3"); errors.Is(err, ErrNotAllowed) {
		t.Errorf("php-fpm8.3 should be allowlisted: %v", err)
	}
}

// Every command the agent runs by name must be allowlisted (a missing
// entry makes the feature silently unavailable).
func TestAllowlistHasPlatformTools(t *testing.T) {
	for _, id := range []string{"apt-get", "dpkg-query", "debconf-set-selections", "dnf", "rpm", "needs-restarting", "ufw", "firewall-cmd", "firewall-offline-cmd", "getenforce", "restorecon", "semanage", "setsebool", "getsebool"} {
		if _, ok := defaultCandidates[id]; !ok {
			t.Errorf("%s is not allowlisted", id)
		}
	}
}

// Commands as another account are started by PID 1, confined, and their
// arguments reach the program unchanged (F-16).
func TestTransientArgs(t *testing.T) {
	a := TransientArgs("whacp-run-x.service", "/bin/sh", "/usr/bin/php", "/home/whacp/a.test", []string{"HOME=/home/whacp/a.test"},
		Credential{UID: 1001, GID: 1002}, 90*time.Second, "", "/run/whacp-run/r/out", "/run/whacp-run/r/err", []string{"/usr/local/bin/whacp-agent:/usr/sbin/sendmail.postfix"}, []string{"-r", "echo $HOME, 100%;"})
	s := strings.Join(a, "\n")
	for _, want := range []string{"--uid=1001", "--gid=1002", "--wait", "--property=StandardInput=null",
		"--working-directory=/home/whacp/a.test",
		"--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet=",
		"--property=RuntimeMaxSec=100", "--setenv=HOME=/home/whacp/a.test",
		"--property=BindReadOnlyPaths=/usr/local/bin/whacp-agent:/usr/sbin/sendmail.postfix"} {
		if !strings.Contains(s, want+"\n") && !strings.HasSuffix(s, want) {
			t.Errorf("missing %s in\n%s", want, s)
		}
	}
	// PID 1 opens no pipes: the wrapper does, as the account.
	if strings.Contains(s, "StandardOutput") || strings.Contains(s, "StandardError") {
		t.Errorf("PID 1 would open the output pipes:\n%s", s)
	}
	n := len(a)
	want := []string{"--", "/bin/sh", "-c", strings.ReplaceAll(pipeWrapper, "$", "$$"), "whacp-run", "/dev/null", "/run/whacp-run/r/out", "/run/whacp-run/r/err", "/usr/bin/php", "-r", "echo $$HOME, 100%;"}
	if strings.Join(a[n-len(want):], "\x00") != strings.Join(want, "\x00") {
		t.Errorf("command part: %q", a[n-len(want):])
	}
}
