// Command probe runs tar as another account exactly like the agent does
// (execx.SystemRunner.RunWith with a Credential). Used by sandbox-probe.sh.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
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
	for _, l := range splitLines(string(b)) {
		if len(l) > 3 && (l[:3] == "Cap" || l[:6] == "NoNewP" || l[:7] == "Seccomp") {
			fmt.Println(l)
		}
	}
	c := exec.Command("/usr/bin/tar", "-cf", "-", ".")
	c.Dir = u.HomeDir
	c.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + u.HomeDir}
	c.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, Setsid: true}
	out, err := c.Output()
	fmt.Printf("RESULT err=%v bytes=%d\n", err, len(out))
	if err != nil {
		os.Exit(1)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
