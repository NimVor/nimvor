// Package monitoring collects lightweight host metrics from procfs.
package monitoring

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// CPUTimes is an aggregate line from /proc/stat.
type CPUTimes struct {
	Idle  uint64
	Total uint64
}

// ParseCPUStat extracts the aggregate "cpu" line from /proc/stat content.
func ParseCPUStat(data []byte) (CPUTimes, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		var vals []uint64
		for _, f := range fields[1:] {
			v, err := strconv.ParseUint(f, 10, 64)
			if err != nil {
				return CPUTimes{}, fmt.Errorf("parse /proc/stat: %w", err)
			}
			vals = append(vals, v)
		}
		var total uint64
		// guest and guest_nice (fields 9,10) are already included in user/nice.
		for i, v := range vals {
			if i >= 8 {
				break
			}
			total += v
		}
		idle := vals[3]
		if len(vals) > 4 {
			idle += vals[4] // iowait
		}
		return CPUTimes{Idle: idle, Total: total}, nil
	}
	return CPUTimes{}, errors.New("cpu line not found in /proc/stat")
}

// CPUPercent computes utilisation between two samples.
func CPUPercent(prev, cur CPUTimes) float64 {
	dt := float64(cur.Total - prev.Total)
	if cur.Total <= prev.Total || dt == 0 {
		return 0
	}
	di := float64(cur.Idle - prev.Idle)
	p := (1 - di/dt) * 100
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return p
}

// Memory holds /proc/meminfo values in bytes.
type Memory struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	UsedPercent    float64 `json:"used_percent"`
	SwapTotalBytes uint64  `json:"swap_total_bytes"`
	SwapUsedBytes  uint64  `json:"swap_used_bytes"`
}

// ParseMeminfo parses /proc/meminfo content.
func ParseMeminfo(data []byte) (Memory, error) {
	vals := map[string]uint64{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) == 0 {
			continue
		}
		v, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		if len(f) > 1 && f[1] == "kB" {
			v *= 1024
		}
		vals[k] = v
	}
	total, ok := vals["MemTotal"]
	if !ok || total == 0 {
		return Memory{}, errors.New("MemTotal missing from /proc/meminfo")
	}
	avail, ok := vals["MemAvailable"]
	if !ok {
		avail = vals["MemFree"] + vals["Buffers"] + vals["Cached"]
	}
	if avail > total {
		avail = total
	}
	m := Memory{
		TotalBytes:     total,
		AvailableBytes: avail,
		UsedBytes:      total - avail,
		SwapTotalBytes: vals["SwapTotal"],
	}
	if vals["SwapTotal"] >= vals["SwapFree"] {
		m.SwapUsedBytes = vals["SwapTotal"] - vals["SwapFree"]
	}
	m.UsedPercent = round2(float64(m.UsedBytes) / float64(total) * 100)
	return m, nil
}

// Load is the system load average.
type Load struct {
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
	Procs  int     `json:"procs_running"`
	Total  int     `json:"procs_total"`
}

// ParseLoadavg parses /proc/loadavg content.
func ParseLoadavg(data []byte) (Load, error) {
	f := strings.Fields(string(data))
	if len(f) < 4 {
		return Load{}, errors.New("malformed /proc/loadavg")
	}
	var l Load
	var err error
	if l.Load1, err = strconv.ParseFloat(f[0], 64); err != nil {
		return Load{}, err
	}
	if l.Load5, err = strconv.ParseFloat(f[1], 64); err != nil {
		return Load{}, err
	}
	if l.Load15, err = strconv.ParseFloat(f[2], 64); err != nil {
		return Load{}, err
	}
	if run, tot, ok := strings.Cut(f[3], "/"); ok {
		l.Procs, _ = strconv.Atoi(run)
		l.Total, _ = strconv.Atoi(tot)
	}
	return l, nil
}

// ParseUptime returns seconds of uptime from /proc/uptime content.
func ParseUptime(data []byte) (float64, error) {
	f := strings.Fields(string(data))
	if len(f) < 1 {
		return 0, errors.New("malformed /proc/uptime")
	}
	return strconv.ParseFloat(f[0], 64)
}

// NetCounters are cumulative byte counters for one interface.
type NetCounters struct {
	Interface string `json:"interface"`
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
}

// ParseNetDev parses /proc/net/dev, skipping the loopback interface.
func ParseNetDev(data []byte) []NetCounters {
	var out []NetCounters
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" || name == "" {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 9 {
			continue
		}
		rx, err1 := strconv.ParseUint(f[0], 10, 64)
		tx, err2 := strconv.ParseUint(f[8], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, NetCounters{Interface: name, RxBytes: rx, TxBytes: tx})
	}
	return out
}

// CPUInfo describes the processor.
type CPUInfo struct {
	Model string `json:"model"`
	Cores int    `json:"cores"`
}

// ParseCPUInfo parses /proc/cpuinfo content.
func ParseCPUInfo(data []byte) CPUInfo {
	var ci CPUInfo
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "processor":
			ci.Cores++
		case "model name", "Model", "cpu model":
			if ci.Model == "" {
				ci.Model = v
			}
		}
	}
	return ci
}

// Mount is a filesystem from /proc/mounts worth reporting.
type Mount struct {
	Device     string `json:"device"`
	MountPoint string `json:"mount_point"`
	FSType     string `json:"fs_type"`
}

var realFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true, "zfs": true,
	"f2fs": true, "jfs": true, "reiserfs": true, "vfat": true, "overlay": true,
}

// ParseMounts returns real (disk-backed) mounts, one per mount point.
func ParseMounts(data []byte) []Mount {
	seen := map[string]bool{}
	var out []Mount
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || !realFS[f[2]] {
			continue
		}
		mp := unescapeMount(f[1])
		if seen[mp] || strings.HasPrefix(mp, "/snap/") || strings.HasPrefix(mp, "/var/lib/docker/") {
			continue
		}
		seen[mp] = true
		out = append(out, Mount{Device: f[0], MountPoint: mp, FSType: f[2]})
	}
	return out
}

func unescapeMount(s string) string {
	r := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return r.Replace(s)
}

func readProc(procDir, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(procDir, name))
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
