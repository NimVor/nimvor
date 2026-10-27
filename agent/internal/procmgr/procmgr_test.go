package procmgr

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestSuspicious(t *testing.T) {
	cases := []struct {
		p    Process
		l    Links
		want string
	}{
		{Process{Name: "xmrig", Command: "./xmrig -o x"}, Links{}, "miner"},
		{Process{Name: "sh", Command: "sh -c curl x | sh -s stratum+tcp://pool:3333"}, Links{}, "mining pool"},
		{Process{Name: "kworker", Command: "[kworker/0:1]"}, Links{Exe: "/var/tmp/.x/k"}, "kernel thread"},
		{Process{Name: "httpd", Command: "httpd"}, Links{Exe: "/usr/sbin/httpd", Deleted: true}, "deleted"},
		{Process{Name: "a", Command: "./a"}, Links{Exe: "/dev/shm/a"}, "/dev/shm"},
		{Process{Name: "b", Command: "b"}, Links{Exe: "/home/x/.cache2/b"}, "hidden folder"},
		{Process{Name: "perl", Command: "perl /tmp/bot.pl"}, Links{Exe: "/usr/bin/perl"}, "script from a temporary folder"},
		{Process{Name: "python3", Command: "/usr/bin/python3 -u /dev/shm/x.py"}, Links{Exe: "/usr/bin/python3.12"}, "/dev/shm/x.py"},
		{Process{UID: 1500, Name: "python3", Command: "python3 -c x", Listens: []string{"tcp 4444"}}, Links{Exe: "/usr/bin/python3.12"}, "listens on tcp 4444"},
	}
	for _, c := range cases {
		got := strings.Join(Suspicious(c.p, c.l, true), "; ")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.p.Command, got, c.want)
		}
	}
	// Ordinary processes are not flagged.
	for _, c := range []struct {
		p Process
		l Links
	}{
		{Process{Name: "nginx", Command: "nginx: worker process", UID: 33}, Links{Exe: "/usr/sbin/nginx"}},
		{Process{Name: "kworker/0:1", Command: "[kworker/0:1]", Kernel: true}, Links{}},
		{Process{Name: "node", Command: "node app.js", UID: 1500, Listens: []string{"tcp 3000"}}, Links{Exe: "/home/x/.nvm/versions/node/bin/node"}},
		{Process{Name: "php-fpm", Command: "php-fpm: pool example", UID: 1500}, Links{Exe: "/usr/sbin/php-fpm8.3"}},
		{Process{Name: "bash", Command: "/bin/bash -c cd /srv && cat > /tmp/notes.txt"}, Links{Exe: "/usr/bin/bash"}},
		// Updated in place (NimVor's own services after an update, packages).
		{Process{Name: "nimvor-agent", Command: "/usr/local/bin/nimvor-agent greylist-policy", UID: 62198}, Links{Exe: "/usr/local/bin/nimvor-agent", Deleted: true, Replaced: true}},
	} {
		if got := Suspicious(c.p, c.l, true); len(got) > 0 {
			t.Errorf("%s flagged: %v", c.p.Command, got)
		}
	}
}

func TestSignalChecksIdentityAndProtection(t *testing.T) {
	h := testhost.New(t)
	var sent []string
	kill = func(pid int, s syscall.Signal) error {
		sent = append(sent, strconv.Itoa(pid)+":"+s.String())
		return nil
	}
	defer func() { kill = syscall.Kill }()
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, _ := r.Lookup("processes.signal")
	self := os.Getpid()
	b, _ := os.ReadFile("/proc/self/stat")
	st, _ := parseStat(b)
	call := func(pid int, start uint64, sig string) error {
		raw, _ := json.Marshal(map[string]any{"pid": pid, "start": start, "signal": sig})
		_, err := a.Handler(context.Background(), raw)
		return err
	}
	if err := call(self, st.start, "KILL"); err == nil || !strings.Contains(err.Error(), "agent itself") {
		t.Fatalf("self: %v", err)
	}
	if err := call(1, 1, "TERM"); err == nil {
		t.Fatal("PID 1 accepted")
	}
	if err := call(self, st.start+1, "TERM"); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("reused pid: %v", err)
	}
	if err := call(self, st.start, "USR1"); err == nil {
		t.Fatal("unknown signal accepted")
	}
	if len(sent) > 0 {
		t.Fatalf("signals sent: %v", sent)
	}
	// The parent of this test is an ordinary process.
	pb, _ := os.ReadFile("/proc/" + strconv.Itoa(os.Getppid()) + "/stat")
	pst, _ := parseStat(pb)
	if err := call(os.Getppid(), pst.start, "TERM"); err != nil || len(sent) != 1 {
		t.Fatalf("parent: %v %v", err, sent)
	}
}

func TestListUsesPrivilegedScan(t *testing.T) {
	h := testhost.New(t)
	h.Env.Cfg.ProcDir = "/proc"
	self := strconv.Itoa(os.Getpid())
	h.Respond = func(id string, args []string) (string, error, bool) {
		if id == "nimvor-agent" && len(args) == 1 && args[0] == ScanFlag {
			return `{"` + self + `":{"exe":"/dev/shm/x","cwd":"/","fds":3}}`, nil, true
		}
		return "", nil, false
	}
	snap := List(context.Background(), h.Env, 10*time.Millisecond)
	if !snap.Detailed || len(snap.Processes) == 0 {
		t.Fatalf("snapshot: detailed=%v n=%d err=%s", snap.Detailed, len(snap.Processes), snap.ScanError)
	}
	found := false
	for _, p := range snap.Processes {
		if strconv.Itoa(p.PID) == self {
			found = p.Exe == "/dev/shm/x" && len(p.Suspicious) > 0
		}
	}
	if !found || snap.Suspicious == 0 {
		t.Fatal("the scan's executable was not used")
	}
	if !h.Runner.Called("nimvor-agent -proc-scan") {
		t.Fatalf("calls: %v", h.Runner.Calls)
	}
}

func TestUnitOf(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(dir+"/42", 0o755)
	os.WriteFile(dir+"/42/cgroup", []byte("0::/system.slice/nimvor-greylist.service\n"), 0o644)
	os.MkdirAll(dir+"/43", 0o755)
	os.WriteFile(dir+"/43/cgroup", []byte("0::/user.slice/user-0.slice/session-3.scope\n"), 0o644)
	if u := unitOf(dir, 42); u != "nimvor-greylist.service" {
		t.Fatalf("unit = %q", u)
	}
	if u := unitOf(dir, 43); u != "session-3.scope" {
		t.Fatalf("scope = %q", u)
	}
}

// A program replaced on disk (an update) is "replaced"; one removed for
// good is only "deleted".
func TestReplacedProgram(t *testing.T) {
	dir := t.TempDir()
	prog := dir + "/prog"
	os.WriteFile(prog, []byte("new"), 0o755)
	os.MkdirAll(dir+"/proc/7", 0o755)
	os.Symlink(prog+" (deleted)", dir+"/proc/7/exe")
	if l := links(dir+"/proc", "7"); !l.Deleted || !l.Replaced || l.Exe != prog {
		t.Fatalf("replaced: %+v", l)
	}
	os.Remove(prog)
	if l := links(dir+"/proc", "7"); !l.Deleted || l.Replaced {
		t.Fatalf("removed: %+v", l)
	}
}
