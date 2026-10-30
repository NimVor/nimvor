package execx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	// Any executable file will do: the policy is checked before it is run,
	// and the host need not have ClamAV, Perl or tar installed.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := &SystemRunner{paths: map[string]string{"sh": exe, "tar": exe, "perl": exe, "clamscan": exe}}
	for _, id := range []string{"sh", "perl", "tar", "clamscan"} {
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
	a := TransientArgs("nimvor-run-x.service", "/bin/sh", "/usr/bin/php", "/home/nimvor/a.test", []string{"HOME=/home/nimvor/a.test"},
		Credential{UID: 1001, GID: 1002}, 90*time.Second, "", "/run/nimvor-run/r/out", "/run/nimvor-run/r/err", []string{"/usr/local/bin/nimvor-agent:/usr/sbin/sendmail.postfix"}, []string{"-r", "echo $HOME, 100%;"})
	s := strings.Join(a, "\n")
	for _, want := range []string{"--uid=1001", "--gid=1002", "--wait", "--property=StandardInput=null",
		"--working-directory=/home/nimvor/a.test",
		"--property=NoNewPrivileges=yes", "--property=CapabilityBoundingSet=",
		"--property=RuntimeMaxSec=100", "--setenv=HOME=/home/nimvor/a.test",
		"--property=BindReadOnlyPaths=/usr/local/bin/nimvor-agent:/usr/sbin/sendmail.postfix"} {
		if !strings.Contains(s, want+"\n") && !strings.HasSuffix(s, want) {
			t.Errorf("missing %s in\n%s", want, s)
		}
	}
	// PID 1 opens no pipes: the wrapper does, as the account.
	if strings.Contains(s, "StandardOutput") || strings.Contains(s, "StandardError") {
		t.Errorf("PID 1 would open the output pipes:\n%s", s)
	}
	n := len(a)
	want := []string{"--", "/bin/sh", "-c", strings.ReplaceAll(pipeWrapper, "$", "$$"), "nimvor-run", "/dev/null", "/run/nimvor-run/r/out", "/run/nimvor-run/r/err", "/usr/bin/php", "-r", "echo $$HOME, 100%;"}
	if strings.Join(a[n-len(want):], "\x00") != strings.Join(want, "\x00") {
		t.Errorf("command part: %q", a[n-len(want):])
	}
}

// Commands given capabilities run as root in a unit whose bounding set is
// exactly those, without the account sandbox's mount namespace; only the
// quota commands may, only with the allowed capabilities.
func TestPrivilegedCommands(t *testing.T) {
	a := TransientArgs("nimvor-run-x.service", "/bin/sh", "/usr/sbin/setquota", "/", nil,
		Credential{Home: "/root", Capabilities: []string{"CAP_SYS_ADMIN"}}, time.Minute, "", "/o", "/e", []string{"/x:/y"}, []string{"-u", "web1", "0", "1024", "0", "0", "/"})
	s := strings.Join(a, "\n") + "\n"
	for _, want := range []string{"--uid=0\n", "--property=NoNewPrivileges=yes\n", "--property=CapabilityBoundingSet=CAP_SYS_ADMIN\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	for _, gone := range []string{"ProtectSystem", "BindReadOnlyPaths", "CapabilityBoundingSet=\n"} {
		if strings.Contains(s, gone) {
			t.Errorf("%q in a privileged unit:\n%s", gone, s)
		}
	}
	caps := []string{"CAP_SYS_ADMIN"}
	for _, c := range []struct {
		id string
		o  Opts
		ok bool
	}{
		{"setquota", Opts{Capabilities: caps}, true},
		{"quotacheck", Opts{Capabilities: []string{"CAP_SYS_ADMIN", "CAP_DAC_READ_SEARCH"}}, true},
		{"sh", Opts{Capabilities: caps}, false},
		{"apt-get", Opts{Capabilities: caps}, false},
		{"setquota", Opts{Capabilities: []string{"CAP_SYS_MODULE"}}, false},
		{"setquota", Opts{Capabilities: caps, As: &Credential{UID: 1001, GID: 1001}}, false},
	} {
		err := checkArgs(c.id, c.o, nil)
		if (err == nil) != c.ok {
			t.Errorf("%s %v: %v", c.id, c.o.Capabilities, err)
		}
	}
}

// The process manager's privileged commands take exactly their own
// arguments.
func TestProcessManagerPrivilegedArgs(t *testing.T) {
	ptrace := Opts{Capabilities: []string{"CAP_SYS_PTRACE", "CAP_DAC_READ_SEARCH"}}
	for _, c := range []struct {
		id   string
		args []string
		ok   bool
	}{
		{"nimvor-agent", []string{"-proc-scan"}, true},
		{"nimvor-agent", []string{"-proc-detail", "4242"}, true},
		{"nimvor-agent", []string{"-proc-detail", "0"}, false},
		{"nimvor-agent", []string{"-proc-detail", "1;id"}, false},
		{"nimvor-agent", []string{"-config", "/tmp/x.json"}, false},
		{"nimvor-agent", nil, false},
		{"strace", []string{"-f", "-tt", "-qq", "-s", "200", "-p", "4242"}, true},
		{"strace", []string{"-f", "-tt", "-qq", "-s", "200", "-o", "/etc/passwd", "-p", "4242"}, false},
		{"strace", []string{"-f", "-tt", "-qq", "-s", "200", "-p", "4242", "sh"}, false},
		{"strace", []string{"sh", "-c", "id"}, false},
	} {
		err := checkArgs(c.id, ptrace, c.args)
		if (err == nil) != c.ok {
			t.Errorf("%s %v: %v", c.id, c.args, err)
		}
	}
}

// A program installed after the agent started is found without a restart,
// and one removed is reported missing.
func TestAllowlistedCommandInstalledLater(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "firewall-cmd")
	saved := defaultCandidates["firewall-cmd"]
	defaultCandidates["firewall-cmd"] = []string{bin}
	defer func() { defaultCandidates["firewall-cmd"] = saved }()
	r := NewSystemRunner(time.Second)
	if r.Available("firewall-cmd") {
		t.Fatal("available before it is installed")
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !r.Available("firewall-cmd") {
		t.Fatal("not found after it was installed")
	}
	os.Remove(bin)
	if r.Available("firewall-cmd") {
		t.Fatal("still available after it was removed")
	}
}

// "postfix check" switches to the postfix user, which the agent's own process cannot do on Ubuntu 24.04:
// it always runs through PID 1 with the capabilities it needs, and nothing else about postfix does.
func TestPostfixCheckRunsWithCapabilities(t *testing.T) {
	caps := autoCapabilities("postfix", []string{"check"})
	if len(caps) == 0 {
		t.Fatal("postfix check gets no capabilities")
	}
	if err := checkArgs("postfix", Opts{Capabilities: caps}, []string{"check"}); err != nil {
		t.Fatalf("not accepted: %v", err)
	}
	for _, c := range caps {
		if !PrivilegedCapabilities[c] {
			t.Errorf("capability %s not allowed", c)
		}
	}
	for _, args := range [][]string{{"reload"}, {"start"}, {"check", "-v"}, {}, {"set-permissions"}} {
		if autoCapabilities("postfix", args) != nil {
			t.Errorf("postfix %v must not get capabilities", args)
		}
		if err := checkArgs("postfix", Opts{Capabilities: postfixCheckCaps}, args); err == nil {
			t.Errorf("postfix %v accepted with capabilities", args)
		}
	}
	if autoCapabilities("postmap", []string{"check"}) != nil {
		t.Error("another command got capabilities")
	}
}
