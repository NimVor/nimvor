package pkgs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/testhost"
)

// The state a Rocky 9 server was left in by package installs that ran in
// the old agent's sandbox: install scripts that failed, dependencies that
// were not installed, a duplicate. Repair reinstalls the packages whose
// scripts failed (once), installs the missing dependencies, and leaves the
// duplicate (a removal) to the administrator.
func TestRepairRPM(t *testing.T) {
	h := testhost.New(t)
	rhel(t, h)
	failed := time.Date(2026, 9, 30, 10, 0, 5, 0, time.UTC)
	os.MkdirAll(filepath.Join(h.Base, "var/log"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "var/log/dnf.rpm.log.1"), []byte(
		"2026-09-29T08:00:00+0000 SUBDEBUG Installed: redis-6.2.7-1.el9.x86_64\n"+
			"2026-09-29T08:00:01+0000 INFO Error in POSTIN scriptlet in rpm package redis\n"+
			"2026-09-29T08:00:02+0000 INFO Error in POSTUN scriptlet in rpm package oldthing\n"), 0o644)
	os.WriteFile(filepath.Join(h.Base, "var/log/dnf.rpm.log"), []byte(
		"2026-09-30T10:00:05+0000 INFO Error in POSTIN scriptlet in rpm package php82-php-fpm\n"+
			"2026-09-30T10:00:06+0000 INFO Error in PREIN scriptlet in rpm package mysql-selinux\n"+
			"2026-09-30T10:00:07+0000 INFO Error in POSTIN scriptlet in rpm package dovecot\n"), 0o644)
	installTime := map[string]int64{
		"redis":         failed.Add(-27 * time.Hour).Unix(),
		"php82-php-fpm": failed.Add(-time.Second).Unix(),
		"dovecot":       failed.Add(time.Hour).Unix(), // installed again since: fine
	}
	reinstalled := map[string]bool{}
	h.Respond = func(id string, a []string) (string, error, bool) {
		cmd := id + " " + strings.Join(a, " ")
		switch {
		case strings.HasPrefix(cmd, "dnf -q check"):
			if reinstalled["deps"] {
				return "kernel-tools-5.14.0-570.el9.x86_64 is a duplicate with kernel-tools-5.14.0-503.el9.x86_64\n", errors.New("exit status 1"), true
			}
			return "clamav-1.0.9-1.el9.x86_64 has missing requires of clamav-filesystem = 1.0.9-1.el9\n" +
				"opendkim-2.11.0-0.36.el9.x86_64 has missing requires of libmilter.so.1.0()(64bit)\n" +
				"kernel-tools-5.14.0-570.el9.x86_64 is a duplicate with kernel-tools-5.14.0-503.el9.x86_64\n", errors.New("exit status 1"), true
		case strings.HasPrefix(cmd, "rpm -q --qf %{NAME}\\t%{INSTALLTIME}\\n"):
			var b strings.Builder
			for _, n := range a[4:] {
				if t, ok := installTime[n]; ok {
					fmt.Fprintf(&b, "%s\t%d\n", n, t)
				} else {
					fmt.Fprintf(&b, "package %s is not installed\n", n)
				}
			}
			return b.String(), nil, true
		case strings.HasPrefix(cmd, "dnf install -y -q --setopt=install_weak_deps=False -- "):
			reinstalled["deps"] = true
			return "", nil, true
		case strings.HasPrefix(cmd, "dnf reinstall -y -q -- php82-php-fpm"):
			installTime["php82-php-fpm"] = time.Now().Unix()
			return "", nil, true
		case strings.HasPrefix(cmd, "dnf reinstall -y -q -- redis"):
			return "Installed package redis-6.2.7-1.el9.x86_64 (from appstream) not available.\nError: No packages marked for reinstall.\n", errors.New("exit status 1"), true
		}
		return "", nil, false
	}
	ctx := context.Background()
	hl := CheckHealth(ctx, h.Env)
	kinds := map[string]string{}
	for _, p := range hl.Problems {
		kinds[p.Kind+" "+p.Package] = p.need
	}
	want := []string{"missing_dependency clamav-1.0.9-1.el9.x86_64", "missing_dependency opendkim-2.11.0-0.36.el9.x86_64", "duplicate kernel-tools-5.14.0-570.el9.x86_64", "failed_script php82-php-fpm", "failed_script redis"}
	if !hl.Checked || len(hl.Problems) != len(want) || !hl.Repairable() || !hl.Duplicates() {
		t.Fatalf("health: %+v", hl)
	}
	for _, w := range want {
		if _, ok := kinds[w]; !ok {
			t.Errorf("not found: %s (%v)", w, kinds)
		}
	}
	if kinds["missing_dependency clamav-1.0.9-1.el9.x86_64"] != "clamav-filesystem-1.0.9-1.el9" {
		t.Errorf("exact version: %v", kinds)
	}

	var log strings.Builder
	after, err := Repair(ctx, h.Env, &log, RepairOptions{})
	if err == nil || !strings.Contains(err.Error(), "reinstalling redis") {
		t.Errorf("redis not reported: %v", err)
	}
	if !h.Runner.Called("dnf install -y -q --setopt=install_weak_deps=False -- clamav-filesystem-1.0.9-1.el9 libmilter.so.1.0()(64bit)") ||
		!h.Runner.Called("dnf reinstall -y -q -- php82-php-fpm") {
		t.Errorf("calls: %v\n%s", h.Runner.Calls, log.String())
	}
	for _, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "rpm ") {
			continue
		}
		if strings.Contains(c, "remove") || strings.Contains(c, "mysql-selinux") || strings.Contains(c, "dovecot") || strings.Contains(c, "oldthing") {
			t.Errorf("unexpected call %q", c)
		}
	}
	if !strings.Contains(log.String(), "not done automatically") {
		t.Errorf("duplicate not explained:\n%s", log.String())
	}
	// Left: the duplicate and redis, which is not reinstalled again.
	if len(after.Problems) != 2 || after.Repairable() {
		t.Errorf("after: %+v", after)
	}
	before := len(h.Runner.Calls)
	log.Reset()
	Repair(ctx, h.Env, &log, RepairOptions{})
	for _, c := range h.Runner.Calls[before:] {
		if strings.Contains(c, "reinstall") {
			t.Errorf("reinstalled again: %q", c)
		}
	}
	// Removing duplicates only on confirmation.
	Repair(ctx, h.Env, &log, RepairOptions{RemoveDuplicates: true})
	if !h.Runner.Called("dnf remove -y -q --duplicates") {
		t.Errorf("duplicates not removed on confirmation: %v", h.Runner.Calls)
	}
}

// dpkg: half-configured packages are configured; missing dependencies are
// installed with apt-get install -f only when that removes nothing.
func TestRepairDpkg(t *testing.T) {
	h := testhost.New(t)
	configured := false
	remove := true
	h.Respond = func(id string, a []string) (string, error, bool) {
		cmd := id + " " + strings.Join(a, " ")
		switch {
		case cmd == "dpkg --audit" && !configured:
			return "The following packages are only half configured, probably due to problems\nconfiguring them the first time.  The configuration should be retried using\ndpkg --configure <package> or the configure menu option in dselect:\n redis-server         Persistent key-value database with network interface\n", errors.New("exit status 1"), true
		case cmd == "dpkg --configure -a":
			configured = true
			return "", nil, true
		case cmd == "apt-get -s check":
			return "E: Unmet dependencies. Try 'apt --fix-broken install' with no packages (or specify a solution).\n", errors.New("exit status 100"), true
		case strings.HasPrefix(cmd, "apt-get -s install -f"):
			if remove {
				return "Remv phpmyadmin [4:5.2.1]\n", nil, true
			}
			return "Inst libfoo1 (1.0)\n", nil, true
		}
		return "", nil, false
	}
	var log strings.Builder
	_, err := Repair(context.Background(), h.Env, &log, RepairOptions{})
	if !configured || err == nil || !strings.Contains(err.Error(), "phpmyadmin") {
		t.Fatalf("configured=%v err=%v\n%s", configured, err, log.String())
	}
	for _, c := range h.Runner.Calls {
		if strings.HasPrefix(c, "apt-get install -f") {
			t.Errorf("ran a fix that removes packages: %q", c)
		}
	}
	remove = false
	Repair(context.Background(), h.Env, &log, RepairOptions{})
	if !h.Runner.Called("apt-get install -f -y -q --no-install-recommends -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=300") {
		t.Errorf("calls: %v", h.Runner.Calls)
	}
}
