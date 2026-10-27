package monitoring

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Disk is usage for one mount point.
type Disk struct {
	Mount
	TotalBytes  uint64  `json:"total_bytes"`
	UsedBytes   uint64  `json:"used_bytes"`
	FreeBytes   uint64  `json:"free_bytes"`
	UsedPercent float64 `json:"used_percent"`
}

// NetRate is throughput for one interface.
type NetRate struct {
	NetCounters
	RxBytesPerSec float64 `json:"rx_bytes_per_sec"`
	TxBytesPerSec float64 `json:"tx_bytes_per_sec"`
}

// Snapshot is one metrics sample.
type Snapshot struct {
	Timestamp     time.Time `json:"timestamp"`
	CPUPercent    float64   `json:"cpu_percent"`
	Memory        Memory    `json:"memory"`
	Load          Load      `json:"load"`
	UptimeSeconds float64   `json:"uptime_seconds"`
	Disks         []Disk    `json:"disks"`
	Network       []NetRate `json:"network"`
}

// Point is a compact history entry for charts.
type Point struct {
	T     int64   `json:"t"`
	CPU   float64 `json:"cpu"`
	Mem   float64 `json:"mem"`
	Disk  float64 `json:"disk"`
	RxBps float64 `json:"rx_bps"`
	TxBps float64 `json:"tx_bps"`
	Load1 float64 `json:"load1"`
}

// Collector samples metrics periodically and keeps a bounded history.
type Collector struct {
	procDir  string
	interval time.Duration
	capacity int
	log      *slog.Logger
	statfs   func(path string, st *syscall.Statfs_t) error

	mu      sync.RWMutex
	latest  *Snapshot
	history []Point
	prevCPU *CPUTimes
	prevNet map[string]NetCounters
	prevAt  time.Time
}

// NewCollector returns a Collector reading procfs at procDir.
func NewCollector(procDir string, interval time.Duration, capacity int, log *slog.Logger) *Collector {
	return &Collector{
		procDir:  procDir,
		interval: interval,
		capacity: capacity,
		log:      log,
		statfs:   syscall.Statfs,
		prevNet:  map[string]NetCounters{},
	}
}

// Run samples until ctx is cancelled.
func (c *Collector) Run(ctx context.Context) {
	c.Sample()
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Sample()
		}
	}
}

// Sample takes one measurement and records it.
func (c *Collector) Sample() {
	now := time.Now().UTC()
	snap := Snapshot{Timestamp: now, Disks: []Disk{}, Network: []NetRate{}}

	c.mu.Lock()
	defer c.mu.Unlock()

	if data, err := readProc(c.procDir, "stat"); err == nil {
		if cur, err := ParseCPUStat(data); err == nil {
			if c.prevCPU != nil {
				snap.CPUPercent = round2(CPUPercent(*c.prevCPU, cur))
			}
			c.prevCPU = &cur
		}
	} else {
		c.log.Debug("read /proc/stat", "error", err)
	}
	if data, err := readProc(c.procDir, "meminfo"); err == nil {
		if m, err := ParseMeminfo(data); err == nil {
			snap.Memory = m
		}
	}
	if data, err := readProc(c.procDir, "loadavg"); err == nil {
		if l, err := ParseLoadavg(data); err == nil {
			snap.Load = l
		}
	}
	if data, err := readProc(c.procDir, "uptime"); err == nil {
		if u, err := ParseUptime(data); err == nil {
			snap.UptimeSeconds = u
		}
	}
	if data, err := readProc(c.procDir, "mounts"); err == nil {
		for _, m := range ParseMounts(data) {
			var st syscall.Statfs_t
			if err := c.statfs(m.MountPoint, &st); err != nil {
				continue
			}
			bsize := uint64(st.Bsize)
			total := st.Blocks * bsize
			free := st.Bavail * bsize
			used := total - st.Bfree*bsize
			d := Disk{Mount: m, TotalBytes: total, FreeBytes: free, UsedBytes: used}
			if used+free > 0 {
				d.UsedPercent = round2(float64(used) / float64(used+free) * 100)
			}
			snap.Disks = append(snap.Disks, d)
		}
	}
	elapsed := now.Sub(c.prevAt).Seconds()
	if data, err := readProc(c.procDir, "net/dev"); err == nil {
		cur := map[string]NetCounters{}
		for _, n := range ParseNetDev(data) {
			cur[n.Interface] = n
			r := NetRate{NetCounters: n}
			if prev, ok := c.prevNet[n.Interface]; ok && elapsed > 0 && n.RxBytes >= prev.RxBytes && n.TxBytes >= prev.TxBytes {
				r.RxBytesPerSec = round2(float64(n.RxBytes-prev.RxBytes) / elapsed)
				r.TxBytesPerSec = round2(float64(n.TxBytes-prev.TxBytes) / elapsed)
			}
			snap.Network = append(snap.Network, r)
		}
		c.prevNet = cur
	}
	c.prevAt = now
	c.latest = &snap

	p := Point{T: now.Unix(), CPU: snap.CPUPercent, Mem: snap.Memory.UsedPercent, Load1: snap.Load.Load1}
	for _, d := range snap.Disks {
		if d.MountPoint == "/" {
			p.Disk = d.UsedPercent
		}
	}
	for _, n := range snap.Network {
		p.RxBps += n.RxBytesPerSec
		p.TxBps += n.TxBytesPerSec
	}
	c.history = append(c.history, p)
	if len(c.history) > c.capacity {
		c.history = c.history[len(c.history)-c.capacity:]
	}
}

// Latest returns the most recent snapshot (sampling now if none exists).
func (c *Collector) Latest() Snapshot {
	c.mu.RLock()
	l := c.latest
	c.mu.RUnlock()
	if l == nil {
		c.Sample()
		c.mu.RLock()
		l = c.latest
		c.mu.RUnlock()
	}
	return *l
}

// History returns a copy of the recorded points.
func (c *Collector) History() []Point {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Point, len(c.history))
	copy(out, c.history)
	return out
}

// SystemInfo is static-ish host information.
type SystemInfo struct {
	Hostname      string        `json:"hostname"`
	Kernel        string        `json:"kernel"`
	Architecture  string        `json:"architecture"`
	CPU           CPUInfo       `json:"cpu"`
	UptimeSeconds float64       `json:"uptime_seconds"`
	BootTime      time.Time     `json:"boot_time"`
	Timezone      string        `json:"timezone"`
	Interfaces    []NetworkIfce `json:"interfaces"`
}

// NetworkIfce describes a network interface.
type NetworkIfce struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac"`
	Up        bool     `json:"up"`
	Addresses []string `json:"addresses"`
}

// ReadSystemInfo gathers host information. etcRoot is the root used to
// resolve /etc/timezone and /etc/localtime.
func ReadSystemInfo(procDir, etcRoot string) SystemInfo {
	si := SystemInfo{Interfaces: []NetworkIfce{}}
	si.Hostname = currentHostname(etcRoot)
	if b, err := readProc(procDir, "sys/kernel/osrelease"); err == nil {
		si.Kernel = strings.TrimSpace(string(b))
	}
	si.Architecture = runtime.GOARCH
	if b, err := readProc(procDir, "cpuinfo"); err == nil {
		si.CPU = ParseCPUInfo(b)
	}
	if b, err := readProc(procDir, "uptime"); err == nil {
		if u, err := ParseUptime(b); err == nil {
			si.UptimeSeconds = u
			si.BootTime = time.Now().Add(-time.Duration(u * float64(time.Second))).UTC().Truncate(time.Second)
		}
	}
	si.Timezone = readTimezone(etcRoot)
	if ifs, err := net.Interfaces(); err == nil {
		for _, i := range ifs {
			if i.Flags&net.FlagLoopback != 0 {
				continue
			}
			n := NetworkIfce{Name: i.Name, MAC: i.HardwareAddr.String(), Up: i.Flags&net.FlagUp != 0, Addresses: []string{}}
			if addrs, err := i.Addrs(); err == nil {
				for _, a := range addrs {
					n.Addresses = append(n.Addresses, a.String())
				}
			}
			si.Interfaces = append(si.Interfaces, n)
		}
	}
	return si
}

func readTimezone(root string) string {
	if b, err := os.ReadFile(filepath.Join(root, "etc/timezone")); err == nil {
		if tz := strings.TrimSpace(string(b)); tz != "" {
			return tz
		}
	}
	if target, err := os.Readlink(filepath.Join(root, "etc/localtime")); err == nil {
		if _, tz, ok := strings.Cut(target, "zoneinfo/"); ok {
			return tz
		}
	}
	return "UTC"
}

// currentHostname is the machine's host name. The agent runs with
// ProtectHostname=yes (its own UTS namespace), so os.Hostname() keeps the name
// the machine had when the agent started and never shows a change made from
// the panel; /etc/hostname, which the change writes, does.
func currentHostname(etcRoot string) string {
	if b, err := os.ReadFile(filepath.Join(etcRoot, "etc/hostname")); err == nil {
		if name := strings.TrimSpace(string(b)); name != "" && !strings.ContainsAny(name, " \t\r\n") {
			return name
		}
	}
	name, _ := os.Hostname()
	return name
}
