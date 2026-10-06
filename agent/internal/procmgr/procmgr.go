package procmgr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Process is one row of the process list.
type Process struct {
	PID        int      `json:"pid"`
	PPID       int      `json:"ppid"`
	UID        int      `json:"uid"`
	User       string   `json:"user"`
	Name       string   `json:"name"`
	State      string   `json:"state"`
	Nice       int      `json:"nice"`
	CPUPercent float64  `json:"cpu_percent"`
	MemPercent float64  `json:"mem_percent"`
	RSSBytes   uint64   `json:"rss_bytes"`
	Threads    int      `json:"threads"`
	Start      uint64   `json:"start"` // clock ticks after boot (identifies the process with its PID)
	Seconds    int64    `json:"seconds"`
	Command    string   `json:"command"`
	Kernel     bool     `json:"kernel"`
	Exe        string   `json:"exe,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	Listens    []string `json:"listens,omitempty"`
	Suspicious []string `json:"suspicious,omitempty"`
	// Notes are worth knowing but not suspicious (e.g. an update waits
	// for a restart).
	Notes []string `json:"notes,omitempty"`
	// Unit is the systemd service the process belongs to.
	Unit string `json:"unit,omitempty"`
}

type stat struct {
	name, state           string
	ppid, nice, threads   int
	ticks, start, rssPage uint64
}

// parseStat reads /proc/<pid>/stat (fields after the name: state 0, ppid
// 1, utime 11, stime 12, nice 16, num_threads 17, starttime 19, rss 21).
func parseStat(b []byte) (stat, bool) {
	s := string(b)
	open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || closing < open {
		return stat{}, false
	}
	f := strings.Fields(s[closing+1:])
	if len(f) < 22 {
		return stat{}, false
	}
	n := func(i int) uint64 { v, _ := strconv.ParseUint(f[i], 10, 64); return v }
	i := func(i int) int { v, _ := strconv.Atoi(f[i]); return v }
	return stat{name: s[open+1 : closing], state: f[0], ppid: i(1), nice: i(16), threads: i(17), ticks: n(11) + n(12), start: n(19), rssPage: n(21)}, true
}

func readStats(procDir string) map[int]stat {
	out := map[int]stat{}
	entries, _ := os.ReadDir(procDir)
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(procDir, e.Name(), "stat")); err == nil {
			if s, ok := parseStat(b); ok {
				out[pid] = s
			}
		}
		if len(out) >= 20000 {
			break
		}
	}
	return out
}

func memTotal(procDir string) uint64 {
	b, _ := os.ReadFile(filepath.Join(procDir, "meminfo"))
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "MemTotal:"); ok {
			kb, _ := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
			return kb * 1024
		}
	}
	return 0
}

func uptimeTicks(procDir string) uint64 {
	b, _ := os.ReadFile(filepath.Join(procDir, "uptime"))
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return uint64(v * hz)
}

func userNames(env *host.Env) map[int]string {
	out := map[int]string{}
	b, _ := env.Root.ReadFile("/etc/passwd")
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) >= 3 {
			if uid, err := strconv.Atoi(f[2]); err == nil {
				out[uid] = f[0]
			}
		}
	}
	return out
}

const hz = 100.0 // USER_HZ

// privileged runs the agent binary's scan mode in a privileged unit.
func privileged(ctx context.Context, env *host.Env, v any, args ...string) error {
	out, err := env.Runner.RunWith(ctx, "whacp-agent", execx.Opts{Capabilities: []string{"CAP_SYS_PTRACE", "CAP_DAC_READ_SEARCH"}, Timeout: 30 * time.Second}, args...)
	if err != nil {
		return fmt.Errorf("%v: %s", err, host.Output(out))
	}
	return json.Unmarshal(out, v)
}

// listeners maps socket inodes to "proto port" for listening sockets.
func listeners(procDir string) map[uint64]string {
	out := map[uint64]string{}
	for _, f := range []struct{ file, proto string }{{"net/tcp", "tcp"}, {"net/tcp6", "tcp"}, {"net/udp", "udp"}, {"net/udp6", "udp"}} {
		b, _ := os.ReadFile(filepath.Join(procDir, f.file))
		for _, line := range strings.Split(string(b), "\n")[1:] {
			c := strings.Fields(line)
			if len(c) < 10 {
				continue
			}
			// TCP: only LISTEN (0A); UDP: bound sockets (07).
			if (f.proto == "tcp" && c[3] != "0A") || (f.proto == "udp" && c[3] != "07") {
				continue
			}
			_, portHex, _ := strings.Cut(c[1], ":")
			port, _ := strconv.ParseUint(portHex, 16, 32)
			inode, _ := strconv.ParseUint(c[9], 10, 64)
			if inode > 0 {
				out[inode] = fmt.Sprintf("%s %d", f.proto, port)
			}
		}
	}
	return out
}

// Snapshot is the process list.
type Snapshot struct {
	Processes []Process `json:"processes"`
	Users     []string  `json:"users"`
	// Detailed: executables, folders and sockets were read (the
	// privileged scan worked); without them fewer checks run.
	Detailed   bool   `json:"detailed"`
	ScanError  string `json:"scan_error,omitempty"`
	Suspicious int    `json:"suspicious"`
}

// List samples every process twice (interval apart) for its CPU share.
func List(ctx context.Context, env *host.Env, interval time.Duration) Snapshot {
	procDir := env.Cfg.ProcDir
	a := readStats(procDir)
	select {
	case <-ctx.Done():
	case <-time.After(interval):
	}
	b := readStats(procDir)
	secs := interval.Seconds()
	total := memTotal(procDir)
	up := uptimeTicks(procDir)
	page := uint64(os.Getpagesize())
	names := userNames(env)

	snap := Snapshot{Processes: []Process{}, Users: []string{}}
	var scan map[string]Links
	if err := privileged(ctx, env, &scan, ScanFlag); err != nil {
		snap.ScanError = err.Error()
	} else {
		snap.Detailed = true
	}
	listen := listeners(procDir)
	seenUser := map[string]bool{}
	for pid, s := range b {
		p := Process{PID: pid, PPID: s.ppid, Name: s.name, State: s.state, Nice: s.nice, Threads: s.threads, Start: s.start, RSSBytes: s.rssPage * page}
		if old, ok := a[pid]; ok && s.ticks >= old.ticks && old.start == s.start {
			p.CPUPercent = round2(float64(s.ticks-old.ticks) / hz / secs * 100)
		}
		if total > 0 {
			p.MemPercent = round2(float64(p.RSSBytes) / float64(total) * 100)
		}
		if up > s.start {
			p.Seconds = int64(float64(up-s.start) / hz)
		}
		var st syscall.Stat_t
		p.UID = -1
		if syscall.Stat(filepath.Join(procDir, strconv.Itoa(pid)), &st) == nil {
			p.UID = int(st.Uid)
		}
		p.User = names[p.UID]
		if p.User == "" {
			p.User = strconv.Itoa(p.UID)
		}
		p.Unit = unitOf(procDir, pid)
		cmd, _ := os.ReadFile(filepath.Join(procDir, strconv.Itoa(pid), "cmdline"))
		p.Command = strings.TrimSpace(strings.ReplaceAll(string(cmd), "\x00", " "))
		if len(p.Command) > 1000 {
			p.Command = p.Command[:1000] + "…"
		}
		// Kernel threads: children of kthreadd (PID 2) and kthreadd itself.
		p.Kernel = pid == 2 || s.ppid == 2
		if p.Command == "" {
			p.Command = "[" + s.name + "]"
		}
		if l, ok := scan[strconv.Itoa(pid)]; ok {
			p.Exe, p.Cwd = l.Exe, l.Cwd
			for _, in := range l.Sockets {
				if port, ok := listen[in]; ok {
					p.Listens = append(p.Listens, port)
				}
			}
			sort.Strings(p.Listens)
			p.Suspicious = Suspicious(p, l, snap.Detailed)
			if l.Replaced {
				p.Notes = append(p.Notes, "its program was updated after it started; restart it to run the new version")
			}
		} else {
			p.Suspicious = Suspicious(p, Links{}, false)
		}
		if len(p.Suspicious) > 0 {
			snap.Suspicious++
		}
		if !seenUser[p.User] {
			seenUser[p.User] = true
			snap.Users = append(snap.Users, p.User)
		}
		snap.Processes = append(snap.Processes, p)
	}
	sort.Slice(snap.Processes, func(i, j int) bool {
		x, y := snap.Processes[i], snap.Processes[j]
		if x.CPUPercent != y.CPUPercent {
			return x.CPUPercent > y.CPUPercent
		}
		return x.PID < y.PID
	})
	sort.Strings(snap.Users)
	return snap
}

// unitOf is the systemd service (or scope) a process runs in, from its
// cgroup ("0::/system.slice/whacp-greylist.service" → "whacp-greylist.service").
func unitOf(procDir string, pid int) string {
	b, err := os.ReadFile(filepath.Join(procDir, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		segs := strings.Split(parts[2], "/")
		for i := len(segs) - 1; i >= 0; i-- {
			if strings.HasSuffix(segs[i], ".service") || strings.HasSuffix(segs[i], ".scope") {
				return segs[i]
			}
		}
	}
	return ""
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

var (
	// Programs that mine cryptocurrency or are known backdoors on
	// compromised web servers.
	badNameRe = regexp.MustCompile(`(?i)(^|[/ \[])(xmrig|xmr-stak|kdevtmpfsi|kinsing|minerd|cpuminer|ccminer|ethminer|nanominer|nbminer|lolminer|t-rex|phoenixminer|cryptonight|kswapd0|dbused|sysupdate|networkservice|watchbog|tsunami|perlbot)( |$|[\]])`)
	poolRe    = regexp.MustCompile(`(?i)stratum\+(tcp|ssl)://|--donate-level|\bpool\.(minexmr|supportxmr|hashvault)`)
	// Folders anyone may write to: programs there were dropped, not installed.
	tempDirs = []string{"/tmp/", "/var/tmp/", "/dev/shm/", "/run/shm/", "/run/user/"}
	hiddenRe = regexp.MustCompile(`/\.[^/]+/`)
	shells   = map[string]bool{"sh": true, "bash": true, "dash": true, "zsh": true, "ksh": true, "nc": true, "ncat": true, "netcat": true, "socat": true, "perl": true, "python": true, "python3": true, "php": true}
)

// Suspicious lists why a process looks like it does not belong (empty
// when nothing stands out). detailed: l was read by the privileged scan.
func Suspicious(p Process, l Links, detailed bool) []string {
	var why []string
	if p.Kernel {
		return nil
	}
	if badNameRe.MatchString(p.Name) || badNameRe.MatchString(p.Command) {
		why = append(why, "named like a known cryptocurrency miner or backdoor")
	}
	if poolRe.MatchString(p.Command) {
		why = append(why, "connects to a mining pool")
	}
	// A user process that calls itself a kernel thread ("[kworker/0:1]").
	if strings.HasPrefix(p.Command, "[") && strings.HasSuffix(p.Command, "]") && (detailed && l.Exe != "") {
		why = append(why, "pretends to be a kernel thread")
	}
	if detailed && l.Deleted && !l.Replaced {
		why = append(why, "its program was deleted from disk after it started")
	}
	for _, d := range tempDirs {
		if strings.HasPrefix(l.Exe, d) {
			why = append(why, "runs a program from "+strings.TrimSuffix(d, "/")+" (anyone can write there)")
			break
		}
	}
	if l.Exe != "" && hiddenRe.MatchString(l.Exe) && !strings.HasPrefix(l.Exe, "/usr/") && !strings.HasPrefix(l.Exe, "/opt/") && !strings.Contains(l.Exe, "/.local/") && !strings.Contains(l.Exe, "/.nvm/") && !strings.Contains(l.Exe, "/.cache/") {
		why = append(why, "runs a program from a hidden folder")
	}
	if script := scriptArg(p.Command); script != "" {
		for _, d := range tempDirs {
			if strings.HasPrefix(script, d) {
				why = append(why, "runs a script from a temporary folder ("+script+")")
				break
			}
		}
	}
	// Website accounts do not run network servers; a shell or interpreter
	// listening on a port is how bind shells look.
	if p.UID >= 1000 && p.UID != 65534 && len(p.Listens) > 0 {
		base := filepath.Base(l.Exe)
		if shells[base] || strings.HasPrefix(base, "python") || strings.HasPrefix(base, "perl") {
			why = append(why, "a shell or script interpreter of a website account listens on "+strings.Join(p.Listens, ", "))
		}
	}
	return why
}

// scriptArg is the script an interpreter or shell runs ("perl /tmp/x.pl"
// → "/tmp/x.pl"); "" for other programs and for -c/-e command strings.
func scriptArg(cmd string) string {
	f := strings.Fields(cmd)
	if len(f) < 2 {
		return ""
	}
	base := filepath.Base(f[0])
	if !shells[base] && !strings.HasPrefix(base, "python") && !strings.HasPrefix(base, "perl") && !strings.HasPrefix(base, "php") {
		return ""
	}
	for _, a := range f[1:] {
		switch {
		case a == "-c" || a == "-e" || a == "-r" || a == "-m":
			return ""
		case strings.HasPrefix(a, "-"):
			continue
		}
		return a
	}
	return ""
}

// Signals the panel may send.
var Signals = map[string]syscall.Signal{"TERM": syscall.SIGTERM, "KILL": syscall.SIGKILL, "HUP": syscall.SIGHUP, "STOP": syscall.SIGSTOP, "CONT": syscall.SIGCONT}

// kill is syscall.Kill (replaced in tests).
var kill = syscall.Kill

// protected says why a process may not be signalled ("" when it may).
func protected(pid int, s stat) string {
	switch {
	case pid <= 1:
		return "PID 1 (systemd) cannot be signalled from WhaCP."
	case pid == 2 || s.ppid == 2:
		return "Kernel threads cannot be signalled."
	case pid == os.Getpid():
		return "This is the WhaCP agent itself; restart it on the Services page."
	}
	return ""
}

// Signal sends sig to pid, after checking that pid is still the process
// the panel listed (its start time), so a reused PID is never hit.
func Signal(env *host.Env, pid int, start uint64, sig string) error {
	s, ok := Signals[sig]
	if !ok {
		return actions.InvalidParams("signal: TERM, KILL, HUP, STOP or CONT")
	}
	b, err := os.ReadFile(filepath.Join(env.Cfg.ProcDir, strconv.Itoa(pid), "stat"))
	if err != nil {
		return actions.NotFound("The process has ended.")
	}
	st, ok := parseStat(b)
	if !ok || (start != 0 && st.start != start) {
		return actions.NotFound("The process has ended (its number now belongs to another process).")
	}
	if why := protected(pid, st); why != "" {
		return actions.Conflict(why)
	}
	if err := kill(pid, s); err != nil && !errors.Is(err, syscall.ESRCH) {
		return actions.Failed("The signal could not be sent.", err.Error())
	}
	return nil
}

func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "processes.list", Description: "Every process: owner, priority, CPU and memory use, command line, executable and listening ports, and why a process looks suspicious (miners, deleted or temporary programs, fake kernel threads, shells listening on a port).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return List(ctx, env, 500*time.Millisecond), nil
	}})
	r.Register(actions.Action{Name: "processes.trace", Description: "One process in detail: executable, folders, open files, status, and (when strace is installed) its system calls for five seconds.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			PID   int    `json:"pid"`
			Start uint64 `json:"start"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(filepath.Join(env.Cfg.ProcDir, strconv.Itoa(in.PID), "stat"))
		st, ok := parseStat(b)
		if in.PID <= 0 || err != nil || !ok || (in.Start != 0 && st.start != in.Start) {
			return nil, actions.NotFound("The process has ended.")
		}
		res := map[string]any{"pid": in.PID, "name": st.name, "strace_available": env.Runner.Available("strace")}
		var d Detail
		if err := privileged(ctx, env, &d, DetailFlag, strconv.Itoa(in.PID)); err != nil {
			res["detail_error"] = err.Error()
		} else {
			res["detail"] = d
		}
		if env.Runner.Available("strace") && protected(in.PID, st) == "" {
			tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			out, _ := env.Runner.RunWith(tctx, "strace", execx.Opts{Capabilities: []string{"CAP_SYS_PTRACE"}, Timeout: 5 * time.Second}, "-f", "-tt", "-qq", "-s", "200", "-p", strconv.Itoa(in.PID))
			cancel()
			res["strace"] = host.Output(out)
		}
		return res, nil
	}})
	r.Register(actions.Action{Name: "processes.signal", Mutating: true, Description: "Send a signal (TERM, KILL, HUP, STOP, CONT) to one process; start (from processes.list) makes sure it is still the same process.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			PID    int    `json:"pid"`
			Start  uint64 `json:"start"`
			Signal string `json:"signal"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if in.PID <= 0 || in.Start == 0 {
			return nil, actions.InvalidParams("pid and start are required")
		}
		if err := Signal(env, in.PID, in.Start, in.Signal); err != nil {
			return nil, err
		}
		return map[string]any{"pid": in.PID, "signal": in.Signal}, nil
	}})
	r.Register(actions.Action{Name: "processes.kill_user", Mutating: true, Description: "Send a signal (TERM or KILL) to every process of one account (never root or WhaCP's own account).", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			User   string `json:"user"`
			Signal string `json:"signal"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		if in.Signal != "TERM" && in.Signal != "KILL" {
			return nil, actions.InvalidParams("signal: TERM or KILL")
		}
		u, err := env.LookupUser(in.User)
		if err != nil {
			return nil, actions.NotFound("No account named " + in.User + ".")
		}
		if u.UID == 0 || in.User == "whacp" {
			return nil, actions.Conflict("The processes of " + in.User + " cannot be ended from WhaCP: the server or the panel would stop working.")
		}
		sent, failed := 0, 0
		for pid, s := range readStats(env.Cfg.ProcDir) {
			var fi syscall.Stat_t
			if syscall.Stat(filepath.Join(env.Cfg.ProcDir, strconv.Itoa(pid)), &fi) != nil || int(fi.Uid) != u.UID {
				continue
			}
			if Signal(env, pid, s.start, in.Signal) == nil {
				sent++
			} else {
				failed++
			}
		}
		return map[string]any{"user": in.User, "signalled": sent, "failed": failed}, nil
	}})
}
