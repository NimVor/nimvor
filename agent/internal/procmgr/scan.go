// Package procmgr is the process manager: every process with its owner,
// priority, CPU and memory use and command line, signals (one process or
// every process of an account), a trace of one process, and processes
// that look suspicious.
//
// The agent reads /proc itself for the list. A process's executable,
// working folder and open files belong to its owner (reading them needs
// CAP_SYS_PTRACE, which the agent does not hold): for those the agent
// runs its own binary in a read-only scan mode (ScanMain) as a privileged
// transient unit with only CAP_SYS_PTRACE and CAP_DAC_READ_SEARCH (see
// execx.Opts.Capabilities), and reads its JSON answer.
package procmgr

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ScanFlag and DetailFlag select the scan modes of the agent binary.
const (
	ScanFlag   = "-proc-scan"
	DetailFlag = "-proc-detail"
)

// Links are what only a privileged reader sees of a process.
type Links struct {
	Exe     string   `json:"exe"`
	Deleted bool     `json:"deleted"`
	Cwd     string   `json:"cwd"`
	FDs     int      `json:"fds"`
	Sockets []uint64 `json:"sockets,omitempty"` // socket inodes
}

// Detail is one process for a trace.
type Detail struct {
	Links
	Root   string            `json:"root"`
	Status map[string]string `json:"status"`
	Files  []string          `json:"files"` // "fd → target"
}

func links(procDir string, pid string) Links {
	l := Links{}
	dir := filepath.Join(procDir, pid)
	if t, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
		l.Exe = strings.TrimSuffix(t, " (deleted)")
		l.Deleted = strings.HasSuffix(t, " (deleted)")
	}
	l.Cwd, _ = os.Readlink(filepath.Join(dir, "cwd"))
	fds, _ := os.ReadDir(filepath.Join(dir, "fd"))
	l.FDs = len(fds)
	for _, fd := range fds {
		t, err := os.Readlink(filepath.Join(dir, "fd", fd.Name()))
		if err != nil {
			continue
		}
		if in, ok := strings.CutPrefix(t, "socket:["); ok {
			if n, err := strconv.ParseUint(strings.TrimSuffix(in, "]"), 10, 64); err == nil && len(l.Sockets) < 256 {
				l.Sockets = append(l.Sockets, n)
			}
		}
	}
	return l
}

// ScanMain writes the links of every process (detailPID 0) or the detail
// of one process as JSON. It only reads.
func ScanMain(procDir string, detailPID int, w io.Writer) error {
	if detailPID > 0 {
		pid := strconv.Itoa(detailPID)
		d := Detail{Links: links(procDir, pid), Status: map[string]string{}}
		d.Root, _ = os.Readlink(filepath.Join(procDir, pid, "root"))
		if b, err := os.ReadFile(filepath.Join(procDir, pid, "status")); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if k, v, ok := strings.Cut(line, ":"); ok {
					switch k {
					case "Name", "State", "PPid", "Uid", "Gid", "Groups", "Threads", "VmRSS", "VmSize", "VmSwap", "voluntary_ctxt_switches", "nonvoluntary_ctxt_switches", "Seccomp", "NoNewPrivs", "CapEff":
						d.Status[k] = strings.Join(strings.Fields(v), " ")
					}
				}
			}
		}
		fds, _ := os.ReadDir(filepath.Join(procDir, pid, "fd"))
		sort.Slice(fds, func(i, j int) bool {
			a, _ := strconv.Atoi(fds[i].Name())
			b, _ := strconv.Atoi(fds[j].Name())
			return a < b
		})
		for _, fd := range fds {
			if len(d.Files) >= 500 {
				break
			}
			if t, err := os.Readlink(filepath.Join(procDir, pid, "fd", fd.Name())); err == nil {
				d.Files = append(d.Files, fd.Name()+" → "+t)
			}
		}
		return json.NewEncoder(w).Encode(d)
	}
	out := map[string]Links{}
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil || !e.IsDir() {
			continue
		}
		out[e.Name()] = links(procDir, e.Name())
		if len(out) >= 20000 {
			break
		}
	}
	return json.NewEncoder(w).Encode(out)
}
