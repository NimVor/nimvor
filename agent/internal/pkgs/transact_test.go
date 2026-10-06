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

	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func init() { retryDelays = []time.Duration{time.Millisecond, time.Millisecond} }

// The outputs reported from real servers, and the other failure kinds.
func TestDiagnose(t *testing.T) {
	for _, c := range []struct {
		name, out string
		cat       Category
		pkg, stg  string
		code      int
	}{
		{"ubuntu 24.04 agent sandbox", "E: seteuid 42 failed - seteuid (1: Operation not permitted)\nReading package lists...\nE: Method gave invalid 400 URI Failure message: Failed to set new user ids - setresuid (1: Operation not permitted)\nE: Method http has died unexpectedly!\nE: Sub-process http returned an error code (112)\n", CatPrivilege, "", "", -1},
		{"rocky dovecot prein", "Running transaction\n  Running scriptlet: dovecot-1:2.3.16-14.el9.x86_64\nerror: %prein(dovecot-1:2.3.16-14.el9.x86_64) scriptlet failed, exit status 1\n\nError in PREIN scriptlet in rpm package dovecot\nError: Transaction failed\n", CatScriptlet, "dovecot", "PREIN", 1},
		{"rocky redis prein, no exit line", "Error in PREIN scriptlet in rpm package redis\nError: Transaction failed\n", CatScriptlet, "redis", "PREIN", -1},
		{"dpkg postinst", "Setting up redis-server (5:7.0.15-1build2) ...\ndpkg: error processing package redis-server (--configure):\n installed redis-server package post-installation script subprocess returned error exit status 1\nErrors were encountered while processing:\n redis-server\nE: Sub-process /usr/bin/dpkg returned an error code (1)\n", CatScriptlet, "redis-server", "post-installation", 1},
		{"apt lock", "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 1234 (unattended-upgr)\nE: Unable to acquire the dpkg frontend lock (/var/lib/dpkg/lock-frontend), is another process using it?\n", CatLocked, "", "", -1},
		{"dpkg interrupted", "E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem.\n", CatInterrupted, "", "", -1},
		{"apt missing", "E: Unable to locate package php8.0-fpm\n", CatNotAvailable, "", "", -1},
		{"dnf missing", "No match for argument: php80-php-fpm\nError: Unable to find a match: php80-php-fpm\n", CatNotAvailable, "", "", -1},
		{"dnf mirror", "Errors during downloading metadata for repository 'appstream':\n  - Curl error (28): Timeout was reached\nError: Failed to download metadata for repo 'appstream'\n", CatRepository, "", "", -1},
		{"apt gpg", "W: GPG error: https://ppa.launchpadcontent.net noble InRelease: The following signatures couldn't be verified because the public key is not available: NO_PUBKEY 4F4EA0AAE5267A6C\n", CatSignature, "", "", -1},
		{"dnf deps", "Error: \n Problem: conflicting requests\n  - nothing provides libfoo.so.1()(64bit) needed by bar-1.0\n", CatDependency, "", "", -1},
		{"rpmdb", "error: rpmdb: BDB0113 Thread/process 2/1 failed: BDB1507 Thread died in Berkeley DB library\nError: rpmdb open failed\n", CatRPMDB, "", "", -1},
		{"disk", "E: You don't have enough free space in /var/cache/apt/archives/.\n", CatDiskFull, "", "", -1},
		{"other", "something odd\n", CatUnknown, "", "", -1},
	} {
		d := Diagnose(c.out)
		if d.Category != c.cat || d.Package != c.pkg || d.Stage != c.stg || d.ExitCode != c.code {
			t.Errorf("%s: %+v", c.name, d)
		}
		if d.Detail == "" || d.Summary() == "" {
			t.Errorf("%s: no detail or summary: %+v", c.name, d)
		}
	}
	if d := Diagnose("E: Unable to locate package php8.0-fpm\nE: Unable to locate package php8.0-cli\n"); strings.Join(d.Missing, ",") != "php8.0-fpm,php8.0-cli" {
		t.Errorf("missing: %v", d.Missing)
	}
	e := &Error{Op: "install", Packages: []string{"dovecot"}, Diagnosis: Diagnose("error: %prein(dovecot-1:2.3.16-14.el9.x86_64) scriptlet failed, exit status 1\nError in PREIN scriptlet in rpm package dovecot\n")}
	if s := e.Error(); !strings.Contains(s, "dovecot: its prein script failed (exit status 1)") || !strings.Contains(s, "[PACKAGE_SCRIPT]") || strings.Contains(s, "dnf install failed") {
		t.Errorf("message: %s", s)
	}
}

func rhel(t *testing.T, h *testhost.Host) {
	os.WriteFile(filepath.Join(h.Base, "etc/os-release"), []byte("ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\nVERSION_ID=\"9.8\"\n"), 0o644)
	h.Env.Platform = platform.Detect(h.Env.Root)
}

// Transient failures are retried (with recovery), permanent ones are not.
func TestTransactRecovery(t *testing.T) {
	h := testhost.New(t)
	ubuntu := h.Env
	fails := 0
	configured := false
	h.Respond = func(id string, a []string) (string, error, bool) {
		if id == "dpkg" && strings.Join(a, " ") == "--configure -a" {
			configured = true
		}
		if id == "apt-get" && len(a) > 0 && a[0] == "install" && fails > 0 {
			fails--
			return "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 99 (apt-get)\n", errors.New("exit status 100"), true
		}
		if id == "apt-get" && len(a) > 0 && a[0] == "install" && strings.Contains(strings.Join(a, " "), "interrupted-pkg") && !configured {
			return "E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem.\n", errors.New("exit status 100"), true
		}
		if id == "apt-get" && len(a) > 0 && a[0] == "install" && strings.Contains(strings.Join(a, " "), "broken-pkg") {
			return "dpkg: error processing package broken-pkg (--configure):\n installed broken-pkg package post-installation script subprocess returned error exit status 2\n", errors.New("exit status 100"), true
		}
		return "", nil, false
	}
	var log strings.Builder
	fails = 2
	if err := InstallNative(context.Background(), ubuntu, &log, []string{"redis-server"}); err != nil {
		t.Fatalf("lock held twice, then free: %v\n%s", err, log.String())
	}
	if !strings.Contains(log.String(), "Automatic recovery: waited") || !strings.Contains(log.String(), "succeeded on attempt 3") {
		t.Errorf("log:\n%s", log.String())
	}
	log.Reset()
	if err := InstallNative(context.Background(), ubuntu, &log, []string{"interrupted-pkg"}); err != nil {
		t.Fatalf("interrupted dpkg not finished: %v\n%s", err, log.String())
	}
	log.Reset()
	before := len(h.Runner.Calls)
	err := InstallNative(context.Background(), ubuntu, &log, []string{"broken-pkg"})
	var pe *Error
	if !errors.As(err, &pe) || pe.Diagnosis.Category != CatScriptlet || pe.Attempts != 1 || pe.Diagnosis.Package != "broken-pkg" {
		t.Fatalf("package script failure: %v %+v", err, pe)
	}
	installs := 0
	for _, c := range h.Runner.Calls[before:] {
		if strings.HasPrefix(c, "apt-get install") {
			installs++
		}
	}
	if installs != 1 || !strings.Contains(log.String(), "Relevant output:") || !strings.Contains(log.String(), "post-installation script subprocess returned error exit status 2") {
		t.Errorf("retried a permanent failure (%d installs) or hid the output:\n%s", installs, log.String())
	}
	// Never: forced options or signature bypasses.
	for _, c := range h.Runner.Calls {
		for _, bad := range []string{"--force-all", "--allow-unauthenticated", "--nogpgcheck", "--nodeps", "--force-yes"} {
			if strings.Contains(c, bad) {
				t.Errorf("unsafe option in %q", c)
			}
		}
	}
}

// A package the repositories do not have is reported before any
// transaction, naming it.
func TestNotAvailableUbuntu(t *testing.T) {
	h := testhost.New(t)
	h.Respond = func(id string, a []string) (string, error, bool) {
		if id == "apt-cache" && len(a) > 1 && a[0] == "policy" {
			return "N: Unable to locate package php8.0-fpm\nphp8.0-cli:\n  Installed: (none)\n  Candidate: (none)\nnginx:\n  Installed: (none)\n  Candidate: 1.24.0-2ubuntu7\n", nil, true
		}
		return "", nil, false
	}
	var log strings.Builder
	err := InstallNative(context.Background(), h.Env, &log, []string{"php8.0-fpm", "php8.0-cli", "nginx"})
	var pe *Error
	if !errors.As(err, &pe) || pe.Diagnosis.Category != CatNotAvailable || strings.Join(pe.Diagnosis.Missing, ",") != "php8.0-fpm,php8.0-cli" {
		t.Fatalf("%v", err)
	}
	if h.Runner.Called("apt-get install -y -q --no-install-recommends -o Dpkg::Options::=--force-confold -o DPkg::Lock::Timeout=300 -- php8.0-fpm php8.0-cli nginx") {
		t.Error("installed although packages are missing")
	}
}

// Rocky Linux: missing PHP packages bring Remi, other missing packages
// EPEL with CRB; only what is missing triggers it.
func TestRHELRepositoryPreparation(t *testing.T) {
	h := testhost.New(t)
	rhel(t, h)
	repos := map[string]bool{"baseos": true, "appstream": true}
	h.Respond = func(id string, a []string) (string, error, bool) {
		cmd := strings.Join(a, " ")
		switch {
		case id == "rpm":
			return "", errors.New("exit status 1"), true // nothing installed
		case id == "dnf" && strings.HasPrefix(cmd, "-q repolist"):
			var b strings.Builder
			b.WriteString("repo id   repo name\n")
			for r := range repos {
				fmt.Fprintf(&b, "%s  %s\n", r, r)
			}
			return b.String(), nil, true
		case id == "dnf" && strings.HasPrefix(cmd, "-q list --available --installed"):
			var b strings.Builder
			for _, p := range a[5:] {
				if (strings.HasPrefix(p, "php80-") && repos["remi-safe"]) || (p == "pure-ftpd" && repos["epel"]) || (p == "rspamd" && repos["rspamd"]) || p == "dovecot" {
					fmt.Fprintf(&b, "%s.x86_64  1.0-1.el9  repo\n", p)
				}
			}
			if b.Len() == 0 {
				return "Error: No matching Packages to list\n", errors.New("exit status 1"), true
			}
			return b.String(), nil, true
		case id == "dnf" && strings.Contains(cmd, "install -y -q -- epel-release"):
			repos["epel"] = true
		case id == "dnf" && strings.Contains(cmd, "remi-release-9.rpm"):
			repos["remi"] = true
		case id == "dnf" && strings.HasPrefix(cmd, "config-manager --set-enabled "):
			repos[a[2]] = true
		case id == "dnf" && strings.HasPrefix(cmd, "config-manager --add-repo ") && strings.HasSuffix(cmd, "/rspamd.repo"):
			repos["rspamd"] = true
		}
		return "", nil, false
	}
	var log strings.Builder
	if err := InstallNative(context.Background(), h.Env, &log, []string{"dovecot"}); err != nil {
		t.Fatal(err)
	}
	if h.Runner.Called("dnf install -y -q -- epel-release") || h.Runner.Called("dnf config-manager --set-enabled crb") {
		t.Error("enabled repositories for a package the base repositories have")
	}
	if err := InstallNative(context.Background(), h.Env, &log, []string{"php80-php-fpm", "php80-php-cli"}); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !h.Runner.Called("dnf install -y -q -- https://rpms.remirepo.net/enterprise/remi-release-9.rpm") || !h.Runner.Called("dnf config-manager --set-enabled remi-safe") || repos["epel"] {
		t.Errorf("Remi (only) expected; calls:\n%s", strings.Join(h.Runner.Calls, "\n"))
	}
	if err := InstallNative(context.Background(), h.Env, &log, []string{"pure-ftpd"}); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !h.Runner.Called("dnf install -y -q -- epel-release") || !h.Runner.Called("dnf config-manager --set-enabled crb") {
		t.Errorf("EPEL and CRB expected; calls:\n%s", strings.Join(h.Runner.Calls, "\n"))
	}
	// Rspamd comes from its project's repository.
	if err := InstallNative(context.Background(), h.Env, &log, []string{"rspamd"}); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !h.Runner.Called("dnf config-manager --add-repo https://rspamd.com/rpm-stable/centos-9/rspamd.repo") {
		t.Errorf("Rspamd repository not added; calls:\n%s", strings.Join(h.Runner.Calls, "\n"))
	}
	// Still missing after that: a precise answer, nothing installed.
	err := InstallNative(context.Background(), h.Env, &log, []string{"clamav-unofficial"})
	var pe *Error
	if !errors.As(err, &pe) || pe.Diagnosis.Category != CatNotAvailable || !strings.Contains(pe.Diagnosis.Detail, "Enabled repositories:") || !strings.Contains(pe.Diagnosis.Detail, "epel") {
		t.Fatalf("%v %+v", err, pe)
	}
	if h.Runner.Called("dnf install -y -q --setopt=install_weak_deps=False -- clamav-unofficial") {
		t.Error("tried to install a missing package")
	}
}

// An EPEL package whose libraries live in CRB (opendkim: libmilter): CRB is
// enabled and the install tried again; still unresolvable, the repository
// providing the library is named.
func TestDependencyEnablesCRB(t *testing.T) {
	h := testhost.New(t)
	rhel(t, h)
	repos := map[string]bool{"baseos": true, "appstream": true, "epel": true}
	h.Respond = func(id string, a []string) (string, error, bool) {
		cmd := strings.Join(a, " ")
		switch {
		case id == "rpm" && strings.Contains(cmd, "epel-release"):
			return "epel-release\t9-10.el9\n", nil, true
		case id == "rpm":
			return "", errors.New("exit status 1"), true
		case id == "dnf" && strings.HasPrefix(cmd, "-q repolist"):
			var b strings.Builder
			for r := range repos {
				fmt.Fprintf(&b, "%s  %s\n", r, r)
			}
			return b.String(), nil, true
		case id == "dnf" && strings.HasPrefix(cmd, "-q list --available --installed"):
			return "opendkim.x86_64  2.11.0-0.36.el9  epel\n", nil, true
		case id == "dnf" && strings.HasPrefix(cmd, "config-manager --set-enabled "):
			repos[a[2]] = true
		case id == "dnf" && strings.Contains(cmd, "-- opendkim") && !repos["crb"]:
			return "Error: \n Problem: conflicting requests\n  - nothing provides libmilter.so.1.0()(64bit) needed by opendkim-2.11.0-0.36.el9.x86_64 from epel\n", errors.New("exit status 1"), true
		}
		return "", nil, false
	}
	var log strings.Builder
	if err := InstallNative(context.Background(), h.Env, &log, []string{"opendkim"}); err != nil {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if !h.Runner.Called("dnf config-manager --set-enabled crb") || !strings.Contains(log.String(), "enabled the repositories EPEL's packages depend on") {
		t.Errorf("CRB not enabled; log:\n%s", log.String())
	}

	// CRB already on and still nothing: the provider is named.
	h2 := testhost.New(t)
	rhel(t, h2)
	h2.Respond = func(id string, a []string) (string, error, bool) {
		cmd := strings.Join(a, " ")
		switch {
		case id == "rpm":
			return "epel-release\t9-10.el9\ndnf-plugins-core\t4\n", nil, true
		case id == "dnf" && strings.HasPrefix(cmd, "-q repolist"):
			return "baseos x\nappstream x\nepel x\ncrb x\n", nil, true
		case id == "dnf" && strings.HasPrefix(cmd, "-q list --available --installed"):
			return "opendkim.x86_64  2.11.0-0.36.el9  epel\n", nil, true
		case id == "dnf" && strings.Contains(cmd, "repoquery --enablerepo=*"):
			return "sendmail-milter from devel\n", nil, true
		case id == "dnf" && strings.Contains(cmd, "-- opendkim"):
			return " Problem: conflicting requests\n  - nothing provides libmilter.so.1.0()(64bit) needed by opendkim-2.11.0-0.36.el9.x86_64 from epel\n", errors.New("exit status 1"), true
		}
		return "", nil, false
	}
	log.Reset()
	err := InstallNative(context.Background(), h2.Env, &log, []string{"opendkim"})
	var pe *Error
	if !errors.As(err, &pe) || pe.Diagnosis.Category != CatDependency || !strings.Contains(pe.Diagnosis.Detail, "libmilter.so.1.0()(64bit) is provided by: sendmail-milter from devel") {
		t.Fatalf("%v\n%s", err, log.String())
	}
	if pe.Attempts != 1 {
		t.Errorf("retried an unchanged dependency problem: %d attempts", pe.Attempts)
	}
}
