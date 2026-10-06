//go:build realhost

package cronmgr

import (
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A cron job runs as the account in the sandbox of commands run as the
// account (no capabilities, NoNewPrivileges, the system read-only), whether
// or not the account has resource limits: root's line runs the launcher
// exactly as cron does, and the job's log is written as the account.
func TestRealHostCronJobSandbox(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	name := "whacpcr" + strconv.Itoa(os.Getpid()%100000)
	home := "/home/" + name
	if out, err := exec.Command("useradd", "-m", "-d", home, "-s", "/usr/sbin/nologin", name).CombinedOutput(); err != nil {
		t.Fatalf("useradd: %v %s", err, out)
	}
	t.Cleanup(func() { exec.Command("userdel", "-r", name).Run() })
	u, _ := user.Lookup(name)
	gid, _ := strconv.Atoi(u.Gid)
	os.MkdirAll(filepath.Join(home, "logs"), 0o755)
	exec.Command("chown", "-R", name+":"+name, home).Run()

	domain := name + ".whacp.test"
	for _, email := range []string{"", "owner@example.test"} {
		jobs := []Job{{ID: 1, Schedule: "* * * * *", Command: "id -un; grep -E '^(CapEff|CapBnd|NoNewPrivs)' /proc/self/status; touch /etc/whacp-cron-escape && echo WROTE-ETC; echo 100%", Enabled: true, Email: email}}
		out, scripts := render(SiteJobs{Domain: domain, User: name, Root: home, Jobs: jobs}, "")
		dir := filepath.Join(ScriptDir, FileName(domain))
		os.MkdirAll(dir, 0o750)
		os.Chown(dir, 0, gid)
		t.Cleanup(func() { os.RemoveAll(dir) })
		for f, body := range scripts {
			mode, owner := os.FileMode(0o640), gid
			if strings.HasSuffix(f, ".run") {
				mode, owner = 0o700, 0
			}
			if err := os.WriteFile(f, []byte(body), mode); err != nil {
				t.Fatal(err)
			}
			os.Chown(f, 0, owner)
		}
		var line string
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, "* * * * * root ") {
				line = strings.TrimPrefix(l, "* * * * * root ")
			}
		}
		os.Remove(filepath.Join(home, "logs/cron-1.log"))
		res, err := exec.Command("/bin/sh", "-c", line).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v %s", line, err, res)
		}
		log, _ := os.ReadFile(filepath.Join(home, "logs/cron-1.log"))
		for _, want := range []string{name + "\n", "CapEff:\t0000000000000000", "CapBnd:\t0000000000000000", "NoNewPrivs:\t1", "100%\n", " exit=0"} {
			if !strings.Contains(string(log), want) {
				t.Errorf("email %q: log lacks %q:\n%s", email, want, log)
			}
		}
		if strings.Contains(string(log), "WROTE-ETC") {
			os.Remove("/etc/whacp-cron-escape")
			t.Errorf("the job wrote to /etc")
		}
		// A mailed job's output reaches cron (which mails it); the others
		// print nothing there.
		if email != "" && !strings.Contains(string(res), "NoNewPrivs:\t1") {
			t.Errorf("mailed job's output not passed to cron: %q", res)
		}
		if email == "" && strings.TrimSpace(string(res)) != "" {
			t.Errorf("unmailed job printed to cron: %q", res)
		}
	}
}
