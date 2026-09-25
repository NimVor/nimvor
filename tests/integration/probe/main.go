// Command probe runs tar as another account exactly like the agent does
// (execx.SystemRunner.RunWith with a Credential), then with each
// credential step alone, to show which one the sandbox refuses. Used by
// sandbox-probe.sh.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	u, err := user.Lookup(os.Args[1])
	if err != nil {
		fmt.Println("lookup:", err)
		os.Exit(2)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	b, _ := os.ReadFile("/proc/self/status")
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "Cap") || strings.HasPrefix(l, "NoNewPrivs") || strings.HasPrefix(l, "Seccomp") {
			fmt.Println(l)
		}
	}
	try := func(name string, attr *syscall.SysProcAttr, dir string) bool {
		args := []string{"--version"}
		if name == "agent" {
			args = []string{"-cf", "-", "."}
		}
		c := exec.Command("/usr/bin/tar", args...)
		c.Dir = dir
		c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + u.HomeDir}
		c.SysProcAttr = attr
		out, err := c.Output()
		fmt.Printf("RESULT %s err=%v bytes=%d\n", name, err, len(out))
		return err == nil
	}
	ok := try("agent", &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, Setsid: true}, u.HomeDir)
	try("no-setsid", &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}, u.HomeDir)
	try("no-setgroups", &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), NoSetGroups: true}}, u.HomeDir)
	try("gid-only", &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 0, Gid: uint32(gid)}}, "/")
	try("uid-only", &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: 0}}, "/")
	try("dir-root", &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}, "/")
	for _, bin := range []string{"/usr/bin/true", "/usr/bin/id"} {
		c := exec.Command(bin)
		c.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
		fmt.Printf("RESULT exec-%s err=%v\n", bin, c.Run())
	}
	for _, args := range [][]string{
		{"--reuid=" + u.Uid, "--regid=" + u.Gid, "--clear-groups", "/usr/bin/tar", "--version"},
		{"--regid=" + u.Gid, "--clear-groups", "/usr/bin/tar", "--version"},
		{"--reuid=" + u.Uid, "/usr/bin/tar", "--version"},
		{"--reuid=" + u.Uid, "--regid=" + u.Gid, "--clear-groups", "--inh-caps=-all", "/usr/bin/tar", "--version"},
	} {
		out, err := exec.Command("/usr/bin/setpriv", args...).CombinedOutput()
		fmt.Printf("RESULT setpriv %v err=%v out=%.70q\n", args[:len(args)-2], err, out)
	}
	// Architectural alternative: PID 1 switches to the account in a
	// transient unit; the caller never changes its own credentials.
	out, err := exec.Command("/usr/bin/systemd-run", "--quiet", "--pipe", "--wait", "--collect", "--service-type=exec",
		"--uid="+u.Uid, "--gid="+u.Gid, "-p", "NoNewPrivileges=yes", "-p", "CapabilityBoundingSet=",
		"/usr/bin/id").CombinedOutput()
	fmt.Printf("RESULT systemd-run err=%v out=%.80q\n", err, out)
	if !ok {
		os.Exit(1)
	}
}
