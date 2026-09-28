package monitoring

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestParsePIDStatAndNet(t *testing.T) {
	p, ok := parsePIDStat([]byte("1234 (php-fpm: pool (x)) S 1 1 1 0 -1 4194560 100 0 0 0 250 50 0 0 20 0 3 0 100 1000000 2560 18446744073709551615"))
	if !ok || p.name != "php-fpm: pool (x)" || p.state != "S" || p.ticks != 300 || p.threads != 3 || p.rssPages != 2560 {
		t.Fatalf("stat: %+v %v", p, ok)
	}
	if _, ok := parsePIDStat([]byte("garbage")); ok {
		t.Fatal("garbage parsed")
	}
	n := parseNetDevFull([]byte("Inter-|   Receive |  Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n    lo: 100 1 0 0 0 0 0 0 100 1 0 0 0 0 0 0\n  eth0: 5000 40 1 2 0 0 0 0 7000 50 3 4 0 0 0 0\n"))
	if len(n) != 1 || n["eth0"] != (Interface{Name: "eth0", RxBytes: 5000, RxPackets: 40, RxErrors: 1, RxDropped: 2, TxBytes: 7000, TxPackets: 50, TxErrors: 3, TxDropped: 4}) {
		t.Fatalf("net: %+v", n)
	}
	c := parseCPULines([]byte("cpu  10 0 10 80 0 0 0 0 0 0\ncpu0 5 0 5 40 0 0 0 0 0 0\ncpu1 5 0 5 40 0 0 0 0 0 0\nintr 1\n"))
	if len(c) != 3 || c["cpu1"].Total != 50 {
		t.Fatalf("cpu lines: %+v", c)
	}
}

// Against this machine's real /proc: plausible values.
func TestSampleLiveOnThisHost(t *testing.T) {
	if _, err := os.Stat("/proc/stat"); err != nil {
		t.Skip("no /proc")
	}
	l := SampleLive(context.Background(), "/proc", "/sys", "/", 200*time.Millisecond, nil)
	if l.CPU.Threads < 1 || len(l.CPU.PerCore) != l.CPU.Threads || l.CPU.Cores < 1 || l.CPU.Architecture == "" {
		t.Fatalf("cpu: %+v", l.CPU)
	}
	if l.Memory.TotalBytes == 0 || l.Processes < 1 || len(l.TopMemory) == 0 || l.TopMemory[0].RSSBytes == 0 {
		t.Fatalf("memory/processes: %+v %d %+v", l.Memory, l.Processes, l.TopMemory)
	}
	for _, p := range l.TopMemory {
		if p.User == "" || p.Name == "" {
			t.Fatalf("process without name/user: %+v", p)
		}
	}
}
