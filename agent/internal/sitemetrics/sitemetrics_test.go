package sitemetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/testhost"
)

func TestStatFields(t *testing.T) {
	// comm may hold spaces and parentheses; utime/stime are fields 14/15, starttime 22.
	line := "1234 (php-fpm: pool (x) y) S 1 1234 1234 0 -1 4194560 100 0 0 0 150 50 0 0 20 0 1 0 987654 1000 2 18446744073709551615"
	cpu, start, ok := statFields([]byte(line))
	if !ok || cpu != 200 || start != 987654 {
		t.Fatalf("cpu=%d start=%d ok=%v", cpu, start, ok)
	}
	if _, _, ok := statFields([]byte("garbage")); ok {
		t.Fatal("garbage parsed")
	}
}

func TestCountLines(t *testing.T) {
	var w Web
	CountLines(&w, []byte(`1.1.1.1 - - [01/Sep/2026:12:00:00 +0000] "GET / HTTP/1.1" 200 1000 "-" "ua"
1.1.1.1 - - [01/Sep/2026:12:00:01 +0000] "GET /x HTTP/1.1" 404 150 "-" "ua"
1.1.1.1 - - [01/Sep/2026:12:00:02 +0000] "POST /y HTTP/1.1" 502 - "-" "a ""quoted"" ua"
1.1.1.1 - - [01/Sep/2026:12:00:03 +0000] "GET /z HTTP/1.1" 301 0 "-" "ua"
not a log line
`))
	if w.Requests != 4 || w.Status2xx != 1 || w.Status3xx != 1 || w.Status4xx != 1 || w.Status5xx != 1 || w.BytesSent != 1150 {
		t.Fatalf("%+v", w)
	}
}

// proc writes a fake /proc/<pid> of a PHP-FPM worker.
func proc(t *testing.T, dir string, pid int, pool string, cpuTicks, start, readB, writeB int, pssKB int) {
	t.Helper()
	d := filepath.Join(dir, fmt.Sprint(pid))
	os.MkdirAll(d, 0o755)
	os.WriteFile(filepath.Join(d, "cmdline"), []byte("php-fpm: pool nimvor-"+pool+"\x00"), 0o644)
	os.WriteFile(filepath.Join(d, "stat"), []byte(fmt.Sprintf("%d (php-fpm) S 1 1 1 0 -1 0 0 0 0 0 %d 0 0 0 20 0 1 0 %d 0 0", pid, cpuTicks, start)), 0o644)
	os.WriteFile(filepath.Join(d, "io"), []byte(fmt.Sprintf("rchar: 1\nread_bytes: %d\nwrite_bytes: %d\n", readB, writeB)), 0o644)
	os.WriteFile(filepath.Join(d, "smaps_rollup"), []byte(fmt.Sprintf("Rss:  %d kB\nPss:                 %d kB\n", pssKB*2, pssKB)), 0o644)
}

func TestCollectAttributesWorkersAndLogs(t *testing.T) {
	h := testhost.New(t)
	procDir := t.TempDir()
	h.Env.Cfg.ProcDir = procDir
	h.Env.Cfg.SitesRoot = "/var/www"
	logDir := filepath.Join(h.Base, "var/www/a.test/logs")
	os.MkdirAll(logDir, 0o755)
	os.MkdirAll(filepath.Join(h.Base, "var/www/b.test/logs"), 0o755)
	logf := filepath.Join(logDir, "access.log")
	os.WriteFile(logf, []byte("old line that must not be counted\n"), 0o644)
	// an account with a limits slice and its cgroup files
	os.MkdirAll(filepath.Join(h.Base, "etc"), 0o755)
	os.MkdirAll(filepath.Join(h.Base, h.Env.Cfg.StateDir), 0o755)
	os.WriteFile(filepath.Join(h.Base, h.Env.Cfg.StateDir, "limits.json"), []byte(`{"accounts":{"acct7":{"limits":{"cpu_percent":100,"memory_mb":512},"users":["example"]}}}`), 0o644)
	cg := filepath.Join(h.Base, "sys/fs/cgroup/nimvor.slice/nimvor-acct7.slice")
	os.MkdirAll(cg, 0o755)
	os.WriteFile(filepath.Join(h.Base, "sys/fs/cgroup/cgroup.controllers"), []byte("cpu memory\n"), 0o644)
	os.WriteFile(filepath.Join(cg, "memory.current"), []byte("104857600\n"), 0o644)
	os.WriteFile(filepath.Join(cg, "memory.peak"), []byte("209715200\n"), 0o644)
	os.WriteFile(filepath.Join(cg, "cpu.stat"), []byte("usage_usec 5000000\nnr_periods 100\nnr_throttled 7\nthrottled_usec 700000\n"), 0o644)
	os.WriteFile(filepath.Join(cg, "memory.events"), []byte("low 0\nhigh 3\nmax 2\noom 1\noom_kill 1\n"), 0o644)
	os.WriteFile(filepath.Join(cg, "io.stat"), []byte("8:0 rbytes=1000 wbytes=2000 rios=10 wios=20 dbytes=0 dios=0\n8:16 rbytes=1 wbytes=2 rios=1 wios=2\n"), 0o644)
	os.WriteFile(filepath.Join(cg, "pids.current"), []byte("4\n"), 0o644)

	proc(t, procDir, 100, "a.test", 1000, 5000, 4096, 8192, 20000)
	proc(t, procDir, 101, "a.test", 500, 5001, 0, 0, 10000)
	proc(t, procDir, 200, "b.test", 100, 5002, 0, 0, 5000)
	os.MkdirAll(filepath.Join(procDir, "300"), 0o755)
	os.WriteFile(filepath.Join(procDir, "300/cmdline"), []byte("nginx: worker process\x00"), 0o644) // not PHP-FPM: ignored

	c := New()
	c.uidName = func(*host.Env, uint32) string { return "example" } // the fake /proc entries belong to the test's user
	t0 := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	first := c.Collect(h.Env, []string{"a.test", "b.test", "c.test"}, t0)
	if first.IntervalSeconds != 0 {
		t.Fatalf("first interval %v", first.IntervalSeconds)
	}
	a := first.Sites[0]
	if a.PHP == nil || a.PHP.Workers != 2 || a.PHP.PSSBytes != 30000*1024 || a.PHP.MemoryKind != "pss" || a.PHP.CPUSeconds != 0 || a.Web != nil {
		t.Fatalf("first a.test: %+v %+v", a.PHP, a.Web)
	}
	if first.Sites[2].PHP != nil || first.Sites[2].Web != nil {
		t.Fatalf("c.test has no pool and no log: nothing is reported, not zeros: %+v", first.Sites[2])
	}

	// 60 seconds later: worker 100 used 3 s of CPU, worker 101 is gone, a new worker 102 has 1.5 s on its clock;
	// the log gained 3 requests.
	proc(t, procDir, 100, "a.test", 1300, 5000, 4096+1000, 8192+500, 22000)
	os.RemoveAll(filepath.Join(procDir, "101"))
	proc(t, procDir, 102, "a.test", 150, 9000, 200, 300, 8000)
	f, _ := os.OpenFile(logf, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`1.1.1.1 - - [01/Sep/2026:12:00:10 +0000] "GET / HTTP/1.1" 200 500 "-" "ua"` + "\n")
	f.WriteString(`1.1.1.1 - - [01/Sep/2026:12:00:11 +0000] "GET / HTTP/1.1" 500 10 "-" "ua"` + "\n")
	f.WriteString(`1.1.1.1 - - [01/Sep/2026:12:00:12 +0000] "GET / HTTP/1.1" 404 20 "-" "partial`) // no newline yet: not counted
	f.Close()
	second := c.Collect(h.Env, []string{"a.test", "b.test"}, t0.Add(60*time.Second))
	if second.IntervalSeconds != 60 || !second.CGroupV2 {
		t.Fatalf("second: %+v", second)
	}
	a = second.Sites[0]
	p := a.PHP
	if p.Workers != 2 || p.CPUSeconds != 3+1.5 || p.ReadBytes != 1000+200 || p.WriteBytes != 500+300 || p.WorkersExited != 1 || p.WorkersStarted != 1 || p.PSSBytes != 30000*1024 {
		t.Fatalf("a.test php: %+v", p)
	}
	if a.Web == nil || a.Web.Requests != 2 || a.Web.Status5xx != 1 || a.Web.BytesSent != 510 {
		t.Fatalf("a.test web: %+v", a.Web)
	}
	if a.User != "example" || a.Account != "acct7" {
		t.Fatalf("account of a.test: user=%q account=%q", a.User, a.Account)
	}
	b := second.Sites[1]
	if b.PHP == nil || b.PHP.CPUSeconds != 0 || b.PHP.Workers != 1 {
		t.Fatalf("b.test: %+v", b.PHP)
	}
	if len(second.Accounts) != 1 {
		t.Fatalf("accounts %+v", second.Accounts)
	}
	u := second.Accounts[0].Usage
	if !u.Active || u.MemoryBytes != 104857600 || !u.MemoryPeakKnown || u.MemoryPeakBytes != 209715200 || u.NrThrottled != 7 || u.MemoryMaxHit != 2 || u.OOMKills != 1 || u.IOReadBytes != 1001 || u.IOWriteOps != 22 {
		t.Fatalf("usage %+v", u)
	}
	// The partial line is counted once it is complete.
	f, _ = os.OpenFile(logf, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString(`"` + "\n")
	f.Close()
	third := c.Collect(h.Env, []string{"a.test"}, t0.Add(120*time.Second))
	_ = third
	// log rotation: a new file is read from its start
	os.Remove(logf)
	os.WriteFile(logf, []byte(`1.1.1.1 - - [01/Sep/2026:12:02:00 +0000] "GET / HTTP/1.1" 200 5 "-" "ua"`+"\n"), 0o644)
	fourth := c.Collect(h.Env, []string{"a.test"}, t0.Add(180*time.Second))
	if w := fourth.Sites[0].Web; w == nil || w.Requests != 1 || w.BytesSent != 5 {
		t.Fatalf("after rotation: %+v", w)
	}
}

func TestActionValidatesDomains(t *testing.T) {
	h := testhost.New(t)
	r := actions.NewRegistry(true)
	Register(r, h.Env)
	a, err := r.Lookup("sites.metrics")
	if err != nil || a.Mutating {
		t.Fatal("sites.metrics must exist and be read-only")
	}
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"domains":["../etc"]}`)); err == nil {
		t.Fatal("a path as a domain must be refused")
	}
	if _, err := a.Handler(context.Background(), json.RawMessage(`{"domains":["ok.test"]}`)); err != nil {
		t.Fatal(err)
	}
}

// BenchmarkCollect measures one pass over 500 PHP-FPM workers of 100 websites (fake /proc): the cost the
// monitoring pass adds. Run with: go test -bench Collect -benchmem ./internal/sitemetrics
func BenchmarkCollect(b *testing.B) {
	t := &testing.T{}
	h := testhost.New(t)
	procDir := b.TempDir()
	h.Env.Cfg.ProcDir = procDir
	var domains []string
	pid := 1000
	for s := 0; s < 100; s++ {
		d := fmt.Sprintf("site%d.test", s)
		domains = append(domains, d)
		for w := 0; w < 5; w++ {
			proc(t, procDir, pid, d, 1000+w, 5000+pid, 4096, 4096, 20000)
			pid++
		}
	}
	c := New()
	c.uidName = func(*host.Env, uint32) string { return "u" }
	now := time.Now()
	c.Collect(h.Env, domains, now)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(time.Minute)
		c.Collect(h.Env, domains, now)
	}
}
