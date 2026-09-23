package monitoring

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestParseCPUStatAndPercent(t *testing.T) {
	a, err := ParseCPUStat([]byte("cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 1 2 3 4\n"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := ParseCPUStat([]byte("cpu  150 0 150 800 100 0 0 0 0 0\n"))
	if got := CPUPercent(a, b); got != 50 {
		t.Fatalf("CPUPercent = %v, want 50", got)
	}
	if CPUPercent(b, a) != 0 {
		t.Fatal("counter reset must yield 0")
	}
}

func TestParseMeminfo(t *testing.T) {
	m, err := ParseMeminfo([]byte("MemTotal:  1000 kB\nMemFree: 100 kB\nMemAvailable: 250 kB\nSwapTotal: 100 kB\nSwapFree: 40 kB\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalBytes != 1024000 || m.UsedBytes != 768000 || m.UsedPercent != 75 || m.SwapUsedBytes != 61440 {
		t.Fatalf("mem = %+v", m)
	}
	if _, err := ParseMeminfo([]byte("garbage")); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseLoadUptimeNet(t *testing.T) {
	l, err := ParseLoadavg([]byte("0.52 0.40 0.31 2/451 12345\n"))
	if err != nil || l.Load1 != 0.52 || l.Total != 451 {
		t.Fatalf("load = %+v %v", l, err)
	}
	u, _ := ParseUptime([]byte("3600.50 7000.00\n"))
	if u != 3600.5 {
		t.Fatalf("uptime = %v", u)
	}
	nd := ParseNetDev([]byte(`Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999 1 0 0 0 0 0 0 999 1 0 0 0 0 0 0
  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0
`))
	if len(nd) != 1 || nd[0].Interface != "eth0" || nd[0].RxBytes != 1000 || nd[0].TxBytes != 2000 {
		t.Fatalf("netdev = %+v", nd)
	}
}

func TestParseMounts(t *testing.T) {
	ms := ParseMounts([]byte("/dev/sda1 / ext4 rw 0 0\nproc /proc proc rw 0 0\n/dev/sda1 / ext4 rw 0 0\n/dev/loop0 /snap/core squashfs ro 0 0\n/dev/sdb1 /mnt/my\\040disk xfs rw 0 0\n"))
	if len(ms) != 2 || ms[1].MountPoint != "/mnt/my disk" {
		t.Fatalf("mounts = %+v", ms)
	}
}

func TestParseCPUInfo(t *testing.T) {
	ci := ParseCPUInfo([]byte("processor\t: 0\nmodel name\t: Test CPU\nprocessor\t: 1\nmodel name\t: Test CPU\n"))
	if ci.Cores != 2 || ci.Model != "Test CPU" {
		t.Fatalf("cpuinfo = %+v", ci)
	}
}

func TestCollectorWithFakeProc(t *testing.T) {
	proc := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(proc, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(content), 0o644)
	}
	write("stat", "cpu  100 0 100 700 100 0 0 0 0 0\n")
	write("meminfo", "MemTotal: 1000 kB\nMemAvailable: 500 kB\n")
	write("loadavg", "1.00 0.50 0.25 1/100 1\n")
	write("uptime", "100.0 100.0\n")
	write("mounts", "/dev/sda1 / ext4 rw 0 0\n")
	write("net/dev", "h\nh\n eth0: 1000 0 0 0 0 0 0 0 1000 0 0 0 0 0 0 0\n")

	c := NewCollector(proc, time.Second, 3, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.statfs = func(_ string, st *syscall.Statfs_t) error {
		st.Bsize, st.Blocks, st.Bfree, st.Bavail = 1024, 100, 40, 30
		return nil
	}
	c.Sample()
	write("stat", "cpu  200 0 100 800 100 0 0 0 0 0\n")
	write("net/dev", "h\nh\n eth0: 3000 0 0 0 0 0 0 0 1000 0 0 0 0 0 0 0\n")
	c.mu.Lock()
	c.prevAt = c.prevAt.Add(-2 * time.Second)
	c.mu.Unlock()
	c.Sample()

	s := c.Latest()
	if s.CPUPercent != 50 || s.Memory.UsedPercent != 50 || s.Load.Load1 != 1 {
		t.Fatalf("snapshot = %+v", s)
	}
	if len(s.Disks) != 1 || s.Disks[0].UsedBytes != 60*1024 || s.Disks[0].UsedPercent != 66.67 {
		t.Fatalf("disk = %+v", s.Disks)
	}
	if len(s.Network) != 1 || s.Network[0].RxBytesPerSec < 900 || s.Network[0].RxBytesPerSec > 1100 {
		t.Fatalf("net = %+v", s.Network)
	}
	c.Sample()
	c.Sample()
	if h := c.History(); len(h) != 3 {
		t.Fatalf("history length = %d, want capacity 3", len(h))
	}
}
