package monitoring

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Live statistics: two samples a short interval apart give per-core CPU
// use, interface rates and the busiest processes. Processes are named by
// their program and user only: command lines can carry passwords.

// CoreUse is one logical CPU's use.
type CoreUse struct {
	CPU     string  `json:"cpu"`
	Percent float64 `json:"percent"`
}

// CPUDetails describes the processor.
type CPUDetails struct {
	Model        string    `json:"model"`
	Architecture string    `json:"architecture"`
	Sockets      int       `json:"sockets"`
	Cores        int       `json:"cores"`   // physical cores
	Threads      int       `json:"threads"` // logical CPUs
	MHz          float64   `json:"mhz,omitempty"`
	MaxMHz       float64   `json:"max_mhz,omitempty"`
	Percent      float64   `json:"percent"`
	PerCore      []CoreUse `json:"per_core"`
}

// Interface is one network interface's counters and current rates.
type Interface struct {
	Name          string  `json:"name"`
	RxBytes       uint64  `json:"rx_bytes"`
	TxBytes       uint64  `json:"tx_bytes"`
	RxPackets     uint64  `json:"rx_packets"`
	TxPackets     uint64  `json:"tx_packets"`
	RxErrors      uint64  `json:"rx_errors"`
	TxErrors      uint64  `json:"tx_errors"`
	RxDropped     uint64  `json:"rx_dropped"`
	TxDropped     uint64  `json:"tx_dropped"`
	RxBytesPerSec float64 `json:"rx_bytes_per_sec"`
	TxBytesPerSec float64 `json:"tx_bytes_per_sec"`
}

// Process is one process's use.
type Process struct {
	PID        int     `json:"pid"`
	Name       string  `json:"name"`
	User       string  `json:"user"`
	State      string  `json:"state"`
	CPUPercent float64 `json:"cpu_percent"`
	RSSBytes   uint64  `json:"rss_bytes"`
	MemPercent float64 `json:"mem_percent"`
	Threads    int     `json:"threads"`
}

// Live is one live statistics result.
type Live struct {
	Timestamp     time.Time   `json:"timestamp"`
	CPU           CPUDetails  `json:"cpu"`
	Memory        Memory      `json:"memory"`
	Load          Load        `json:"load"`
	UptimeSeconds float64     `json:"uptime_seconds"`
	Processes     int         `json:"processes"`
	Threads       int         `json:"threads"`
	Running       int         `json:"running"`
	Zombies       int         `json:"zombies"`
	Interfaces    []Interface `json:"interfaces"`
	Disks         []Disk      `json:"disks"`
	TopCPU        []Process   `json:"top_cpu"`
	TopMemory     []Process   `json:"top_memory"`
}

type procSample struct {
	name, state string
	uid         int
	ticks       uint64
	rssPages    uint64
	threads     int
}

// parseCPULines returns the times of "cpu" and every "cpuN" line.
func parseCPULines(data []byte) map[string]CPUTimes {
	out := map[string]CPUTimes{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		name, _, _ := strings.Cut(line, " ")
		if t, err := ParseCPUStat([]byte("cpu " + strings.TrimSpace(strings.TrimPrefix(line, name)) + "\n")); err == nil {
			out[name] = t
		}
	}
	return out
}

// parseNetDevFull reads all counters of /proc/net/dev.
func parseNetDevFull(data []byte) map[string]Interface {
	out := map[string]Interface{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if len(f) < 16 || name == "lo" {
			continue
		}
		n := func(i int) uint64 { v, _ := strconv.ParseUint(f[i], 10, 64); return v }
		out[name] = Interface{Name: name, RxBytes: n(0), RxPackets: n(1), RxErrors: n(2), RxDropped: n(3), TxBytes: n(8), TxPackets: n(9), TxErrors: n(10), TxDropped: n(11)}
	}
	return out
}

// parsePIDStat reads /proc/<pid>/stat.
func parsePIDStat(data []byte) (procSample, bool) {
	s := string(data)
	open, close := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || close < open {
		return procSample{}, false
	}
	f := strings.Fields(s[close+1:])
	// Fields after the name: state(0) ... utime(11) stime(12) ... num_threads(17) ... rss(21)
	if len(f) < 22 {
		return procSample{}, false
	}
	u, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	th, _ := strconv.Atoi(f[17])
	rss, _ := strconv.ParseUint(f[21], 10, 64)
	return procSample{name: s[open+1 : close], state: f[0], ticks: u + st, threads: th, rssPages: rss}, true
}

func sampleProcs(procDir string) map[int]procSample {
	out := map[int]procSample{}
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procDir, e.Name(), "stat"))
		if err != nil {
			continue
		}
		p, ok := parsePIDStat(b)
		if !ok {
			continue
		}
		var st syscall.Stat_t
		if syscall.Stat(filepath.Join(procDir, e.Name()), &st) == nil {
			p.uid = int(st.Uid)
		}
		out[pid] = p
		if len(out) >= 20000 {
			break
		}
	}
	return out
}

func userNames(etcRoot string) map[int]string {
	out := map[int]string{}
	b, err := os.ReadFile(filepath.Join(etcRoot, "etc/passwd"))
	if err != nil {
		return out
	}
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

// cpuDetails reads model, topology and frequency from /proc/cpuinfo and sysfs.
func cpuDetails(procDir, sysDir string) CPUDetails {
	d := CPUDetails{Architecture: runtime.GOARCH}
	b, _ := readProc(procDir, "cpuinfo")
	cores := map[string]bool{}
	sockets := map[string]bool{}
	var mhz float64
	var mhzN int
	phys, core := "", ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			if phys != "" || core != "" {
				cores[phys+"/"+core] = true
			}
			phys, core = "", ""
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			d.Threads++
		case "model name", "Model", "cpu model":
			if d.Model == "" {
				d.Model = v
			}
		case "physical id":
			phys = v
			sockets[v] = true
		case "core id":
			core = v
		case "cpu MHz":
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				mhz += f
				mhzN++
			}
		}
	}
	if phys != "" || core != "" {
		cores[phys+"/"+core] = true
	}
	d.Sockets, d.Cores = len(sockets), len(cores)
	if d.Cores == 0 {
		d.Cores = d.Threads
	}
	if d.Sockets == 0 && d.Threads > 0 {
		d.Sockets = 1
	}
	if mhzN > 0 {
		d.MHz = round2(mhz / float64(mhzN))
	}
	if b, err := os.ReadFile(filepath.Join(sysDir, "devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq")); err == nil {
		if khz, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64); err == nil {
			d.MaxMHz = round2(khz / 1000)
		}
	}
	return d
}

// SampleLive takes two samples interval apart. statfs measures disks.
func SampleLive(ctx context.Context, procDir, sysDir, etcRoot string, interval time.Duration, disks func() []Disk) Live {
	statA, _ := readProc(procDir, "stat")
	netA, _ := readProc(procDir, "net/dev")
	procsA := sampleProcs(procDir)
	start := time.Now()
	select {
	case <-ctx.Done():
	case <-time.After(interval):
	}
	statB, _ := readProc(procDir, "stat")
	netB, _ := readProc(procDir, "net/dev")
	procsB := sampleProcs(procDir)
	secs := time.Since(start).Seconds()
	if secs <= 0 {
		secs = interval.Seconds()
	}

	live := Live{Timestamp: time.Now().UTC(), CPU: cpuDetails(procDir, sysDir), Interfaces: []Interface{}, TopCPU: []Process{}, TopMemory: []Process{}}
	cpuA, cpuB := parseCPULines(statA), parseCPULines(statB)
	live.CPU.Percent = CPUPercent(cpuA["cpu"], cpuB["cpu"])
	var names []string
	for n := range cpuB {
		if n != "cpu" {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		a, _ := strconv.Atoi(strings.TrimPrefix(names[i], "cpu"))
		b, _ := strconv.Atoi(strings.TrimPrefix(names[j], "cpu"))
		return a < b
	})
	for _, n := range names {
		live.CPU.PerCore = append(live.CPU.PerCore, CoreUse{CPU: n, Percent: CPUPercent(cpuA[n], cpuB[n])})
	}
	if b, err := readProc(procDir, "meminfo"); err == nil {
		live.Memory, _ = ParseMeminfo(b)
	}
	if b, err := readProc(procDir, "loadavg"); err == nil {
		live.Load, _ = ParseLoadavg(b)
	}
	if b, err := readProc(procDir, "uptime"); err == nil {
		live.UptimeSeconds, _ = ParseUptime(b)
	}
	ia, ib := parseNetDevFull(netA), parseNetDevFull(netB)
	for name, cur := range ib {
		if prev, ok := ia[name]; ok {
			if cur.RxBytes >= prev.RxBytes {
				cur.RxBytesPerSec = round2(float64(cur.RxBytes-prev.RxBytes) / secs)
			}
			if cur.TxBytes >= prev.TxBytes {
				cur.TxBytesPerSec = round2(float64(cur.TxBytes-prev.TxBytes) / secs)
			}
		}
		live.Interfaces = append(live.Interfaces, cur)
	}
	sort.Slice(live.Interfaces, func(i, j int) bool { return live.Interfaces[i].Name < live.Interfaces[j].Name })
	if disks != nil {
		live.Disks = disks()
	}

	// Processes: CPU share of one CPU over the interval (like top).
	hz := 100.0 // USER_HZ on Linux
	page := uint64(os.Getpagesize())
	users := userNames(etcRoot)
	var all []Process
	for pid, b := range procsB {
		live.Processes++
		live.Threads += b.threads
		switch b.state {
		case "R":
			live.Running++
		case "Z":
			live.Zombies++
		}
		p := Process{PID: pid, Name: b.name, State: b.state, Threads: b.threads, RSSBytes: b.rssPages * page}
		if a, ok := procsA[pid]; ok && b.ticks >= a.ticks {
			p.CPUPercent = round2(float64(b.ticks-a.ticks) / hz / secs * 100)
		}
		if live.Memory.TotalBytes > 0 {
			p.MemPercent = round2(float64(p.RSSBytes) / float64(live.Memory.TotalBytes) * 100)
		}
		p.User = users[b.uid]
		if p.User == "" {
			p.User = strconv.Itoa(b.uid)
		}
		all = append(all, p)
	}
	top := func(less func(a, b Process) bool) []Process {
		cp := append([]Process(nil), all...)
		sort.Slice(cp, func(i, j int) bool {
			if less(cp[i], cp[j]) != less(cp[j], cp[i]) {
				return less(cp[i], cp[j])
			}
			return cp[i].PID < cp[j].PID
		})
		if len(cp) > 15 {
			cp = cp[:15]
		}
		if cp == nil {
			cp = []Process{}
		}
		return cp
	}
	live.TopCPU = top(func(a, b Process) bool { return a.CPUPercent > b.CPUPercent })
	live.TopMemory = top(func(a, b Process) bool { return a.RSSBytes > b.RSSBytes })
	return live
}
