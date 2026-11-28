// Package sitemetrics measures what each website uses, from sources that can
// be attributed to a website:
//
//   - PHP-FPM workers: every pool is named nimvor-<domain> and its workers
//     show it in their command line ("php-fpm: pool nimvor-example.com"), so
//     the workers' CPU time, memory (PSS) and disk bytes (/proc/<pid>) belong
//     to that website. This is sampled: a worker that starts and exits
//     between two samples is not seen, which is counted and reported.
//   - The website's own Nginx access log: requests, status classes and bytes
//     sent, counted from where the previous call stopped.
//   - The cgroup v2 slice of the hosting account (the exact, kernel-kept
//     counters for everything the account runs, in the plan's limits). It is
//     per account, not per website: with several websites in one account the
//     slice is shared and is reported as such.
//
// Nothing here is estimated. A source that is missing reports nothing for
// that field (never a zero). The only state kept between calls is the
// previous sample's counters, in memory; the first call after a start of the
// agent reports no rates.
package sitemetrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/limits"
	"github.com/NimVor/nimvor/agent/internal/sites"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

const (
	// clockTicks is USER_HZ, the unit of /proc/<pid>/stat times (always 100 on Linux).
	clockTicks = 100
	poolPrefix = "php-fpm: pool nimvor-"
	// maxLogRead bounds the bytes read from one access log per call; a log
	// that grew more is skipped to its end and flagged.
	maxLogRead = 16 << 20
	maxDomains = 2000
)

// Web is the requests a website served since the previous call.
type Web struct {
	Requests  int64 `json:"requests"`
	Status2xx int64 `json:"status_2xx"`
	Status3xx int64 `json:"status_3xx"`
	Status4xx int64 `json:"status_4xx"`
	Status5xx int64 `json:"status_5xx"`
	BytesSent int64 `json:"bytes_sent"` // response body bytes (the log's size field)
	Skipped   bool  `json:"log_skipped,omitempty"`
}

// PHP is a website's PHP-FPM workers.
type PHP struct {
	Workers        int     `json:"workers"`
	PSSBytes       int64   `json:"memory_bytes"`    // proportional set size (shared pages split fairly); RSS where PSS is unreadable
	MemoryKind     string  `json:"memory_kind"`     // "pss" or "rss"
	CPUSeconds     float64 `json:"cpu_seconds"`     // CPU time used by the workers since the previous call
	ReadBytes      int64   `json:"read_bytes"`      // disk bytes the workers read since the previous call
	WriteBytes     int64   `json:"write_bytes"`     // disk bytes the workers wrote since the previous call
	WorkersExited  int     `json:"workers_exited"`  // workers seen last time and gone now: their last interval is not counted
	WorkersStarted int     `json:"workers_started"` // workers new since the previous call
	// FPM is the pool's own status page, when an administrator switched it on.
	FPM *FPM `json:"fpm,omitempty"`
}

// Site is one website's sample. Absent parts were not measured.
type Site struct {
	Domain  string `json:"domain"`
	User    string `json:"user,omitempty"`
	Account string `json:"account,omitempty"` // the limits account whose slice the website runs in
	PHP     *PHP   `json:"php,omitempty"`
	Web     *Web   `json:"web,omitempty"`
}

// Account is a limits account's cgroup counters (cumulative, from the kernel).
type Account struct {
	Account string        `json:"account"`
	Users   []string      `json:"users"`
	Limits  limits.Limits `json:"limits"`
	Usage   limits.Usage  `json:"usage"`
}

// Result is one call's answer.
type Result struct {
	TakenAt         int64     `json:"taken_at"`
	IntervalSeconds float64   `json:"interval_seconds"` // since the previous call; 0 on the first (no rates yet)
	CGroupV2        bool      `json:"cgroup_v2"`
	Sites           []Site    `json:"sites"`
	Accounts        []Account `json:"accounts"`
}

type procState struct {
	cpuTicks   uint64
	rb, wb     uint64
	startTicks uint64
}

type logState struct {
	inode uint64
	off   int64
}

// Collector keeps the previous sample's counters.
type Collector struct {
	mu     sync.Mutex
	prevAt time.Time
	procs  map[string]procState   // pool|pid|starttime
	logs   map[string]logState    // domain
	fpm    map[string]fpmCounters // domain
	// uidName names a user id (the host's /etc/passwd unless a test replaces it).
	uidName func(e *host.Env, uid uint32) string
}

func New() *Collector {
	return &Collector{procs: map[string]procState{}, logs: map[string]logState{}, fpm: map[string]fpmCounters{}, uidName: uidToName}
}

var statusLine = regexp.MustCompile(`" (\d{3}) (\d+|-)(?: |$)`)

// CountLines adds the lines of an access log in the combined format to w.
func CountLines(w *Web, data []byte) {
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		m := statusLine.FindSubmatch(line)
		if m == nil {
			continue
		}
		w.Requests++
		switch m[1][0] {
		case '2':
			w.Status2xx++
		case '3':
			w.Status3xx++
		case '4':
			w.Status4xx++
		case '5':
			w.Status5xx++
		}
		if n, err := strconv.ParseInt(string(m[2]), 10, 64); err == nil {
			w.BytesSent += n
		}
	}
}

// statFields returns utime+stime (ticks) and starttime of /proc/<pid>/stat
// content ("pid (comm) state ppid ... utime stime ... starttime ...").
func statFields(b []byte) (cpu, start uint64, ok bool) {
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0, 0, false
	}
	f := strings.Fields(s[i+1:]) // f[0] is the state: field 3 of stat
	if len(f) < 20 {
		return 0, 0, false
	}
	ut, e1 := strconv.ParseUint(f[11], 10, 64) // field 14
	st, e2 := strconv.ParseUint(f[12], 10, 64) // field 15
	sT, e3 := strconv.ParseUint(f[19], 10, 64) // field 22
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, 0, false
	}
	return ut + st, sT, true
}

func kvBytes(b []byte, key string) (uint64, bool) {
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == key {
			if n, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64); err == nil {
				return n * 1024, true
			}
		}
	}
	return 0, false
}

func ioBytes(b []byte) (r, w uint64) {
	for _, line := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		switch k {
		case "read_bytes":
			r = n
		case "write_bytes":
			w = n
		}
	}
	return
}

type worker struct {
	pool, user string
	pss        int64
	kind       string
	state      procState
	key        string
}

// scanWorkers lists the PHP-FPM workers under procDir, by website.
func scanWorkers(procDir string, uidName func(uint32) string) []worker {
	ents, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	var out []worker
	for _, e := range ents {
		pid := e.Name()
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		dir := filepath.Join(procDir, pid)
		cmd, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if err != nil {
			continue
		}
		line := strings.TrimRight(strings.ReplaceAll(string(cmd), "\x00", " "), " ")
		if !strings.HasPrefix(line, poolPrefix) {
			continue
		}
		domain := strings.TrimPrefix(line, "php-fpm: pool nimvor-")
		if domain == "" || strings.ContainsAny(domain, " /") {
			continue
		}
		sb, err := os.ReadFile(filepath.Join(dir, "stat"))
		if err != nil {
			continue
		}
		cpu, start, ok := statFields(sb)
		if !ok {
			continue
		}
		w := worker{pool: domain, kind: "rss", state: procState{cpuTicks: cpu, startTicks: start}, key: domain + "|" + pid + "|" + strconv.FormatUint(start, 10)}
		if b, err := os.ReadFile(filepath.Join(dir, "smaps_rollup")); err == nil {
			if n, ok := kvBytes(b, "Pss"); ok {
				w.pss, w.kind = int64(n), "pss"
			}
		}
		if w.kind == "rss" {
			if b, err := os.ReadFile(filepath.Join(dir, "status")); err == nil {
				if n, ok := kvBytes(b, "VmRSS"); ok {
					w.pss = int64(n)
				}
			}
		}
		if b, err := os.ReadFile(filepath.Join(dir, "io")); err == nil {
			w.state.rb, w.state.wb = ioBytes(b)
		}
		if st, err := os.Stat(dir); err == nil && uidName != nil {
			if uid, ok := ownerUID(st); ok {
				w.user = uidName(uid)
			}
		}
		out = append(out, w)
	}
	return out
}

// readLog counts the lines added to a website's access log since last time.
func (c *Collector) readLog(domain, path string) (*Web, bool) {
	f, err := os.Open(path)
	if err != nil {
		delete(c.logs, domain)
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	ino := inode(info)
	size := info.Size()
	prev, had := c.logs[domain]
	if !had { // first sight: start at the end, never backfill from an old log
		c.logs[domain] = logState{inode: ino, off: size}
		return nil, true
	}
	if prev.inode != ino || size < prev.off { // rotated or truncated: read the new file from its start
		prev = logState{inode: ino}
	}
	w := &Web{}
	start := prev.off
	if size-start > maxLogRead {
		w.Skipped = true
		start = size
	}
	if size > start {
		buf := make([]byte, size-start)
		n, err := f.ReadAt(buf, start)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, true
		}
		buf = buf[:n]
		// Count whole lines only; a partial last line waits for the next call.
		if i := bytes.LastIndexByte(buf, '\n'); i >= 0 {
			CountLines(w, buf[:i+1])
			start += int64(i + 1)
		} else {
			start = prev.off
		}
		prev.off = start
	} else {
		prev.off = size
	}
	c.logs[domain] = logState{inode: ino, off: prev.off}
	return w, true
}

// Collect samples the given websites.
func (c *Collector) Collect(e *host.Env, domains []string, now time.Time) Result {
	c.mu.Lock()
	defer c.mu.Unlock()
	res := Result{TakenAt: now.Unix(), CGroupV2: e.Root.Exists("/sys/fs/cgroup/cgroup.controllers")}
	interval := 0.0
	if !c.prevAt.IsZero() {
		interval = now.Sub(c.prevAt).Seconds()
	}
	if interval < 0 || interval > 3600 { // a clock jump or a long gap: no rates this time
		interval = 0
	}
	res.IntervalSeconds = interval
	byUser := map[string]string{} // system user -> account
	st := limits.LoadState(e)
	for acct, a := range st.Accounts {
		for _, u := range a.Users {
			byUser[u] = acct
		}
	}
	workers := scanWorkers(e.Cfg.ProcDir, func(uid uint32) string { return c.uidName(e, uid) })
	want := map[string]bool{}
	for _, d := range domains {
		want[d] = true
	}
	next := map[string]procState{}
	php := map[string]*PHP{}
	users := map[string]string{}
	for _, w := range workers {
		if !want[w.pool] {
			continue
		}
		p := php[w.pool]
		if p == nil {
			p = &PHP{MemoryKind: w.kind}
			php[w.pool] = p
		}
		p.Workers++
		p.PSSBytes += w.pss
		if w.kind == "rss" {
			p.MemoryKind = "rss"
		}
		users[w.pool] = w.user
		next[w.key] = w.state
		prev, seen := c.procs[w.key]
		if interval > 0 {
			if seen {
				if w.state.cpuTicks >= prev.cpuTicks {
					p.CPUSeconds += float64(w.state.cpuTicks-prev.cpuTicks) / clockTicks
				}
				if w.state.rb >= prev.rb {
					p.ReadBytes += int64(w.state.rb - prev.rb)
				}
				if w.state.wb >= prev.wb {
					p.WriteBytes += int64(w.state.wb - prev.wb)
				}
			} else { // born since the previous sample: everything it used so far is in this interval
				p.WorkersStarted++
				p.CPUSeconds += float64(w.state.cpuTicks) / clockTicks
				p.ReadBytes += int64(w.state.rb)
				p.WriteBytes += int64(w.state.wb)
			}
		}
	}
	if interval > 0 {
		for k := range c.procs {
			if _, still := next[k]; !still {
				if pool, _, ok := strings.Cut(k, "|"); ok && php[pool] != nil {
					php[pool].WorkersExited++
				}
			}
		}
	}
	c.procs = next
	sort.Strings(domains)
	if sites.FPMStatusEnabled(e) {
		c.queryFPM(e, domains, php, interval > 0)
	}
	accounts := map[string]bool{}
	for _, d := range domains {
		s := Site{Domain: d}
		if p := php[d]; p != nil {
			s.PHP = p
			s.User = users[d]
			s.Account = byUser[s.User]
		}
		if real, err := e.Root.Path(filepath.Join(e.Cfg.SitesRoot, d, "logs", "access.log")); err == nil {
			if w, ok := c.readLog(d, real); ok && w != nil && interval > 0 {
				s.Web = w
			}
		}
		if s.Account != "" {
			accounts[s.Account] = true
		}
		res.Sites = append(res.Sites, s)
	}
	names := make([]string, 0, len(accounts))
	for a := range accounts {
		names = append(names, a)
	}
	sort.Strings(names)
	for _, a := range names {
		res.Accounts = append(res.Accounts, Account{Account: a, Users: st.Accounts[a].Users, Limits: st.Accounts[a].Limits, Usage: limits.ReadUsage(e, a)})
	}
	c.prevAt = now
	return res
}

// Register adds sites.metrics.
func Register(r *actions.Registry, e *host.Env) {
	c := New()
	r.Register(actions.Action{Name: "sites.metrics", Timeout: 60 * time.Second, Description: "Resource use of the given websites since the previous call: PHP-FPM workers' CPU time, memory and disk bytes (attributed by pool), requests and status classes from the website's own access log, and the cgroup counters of the websites' limits accounts. Read-only; the first call after an agent start reports no rates.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Domains []string `json:"domains"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Domains) > maxDomains {
			return nil, actions.InvalidParams("domains: at most 2000")
		}
		for _, d := range in.Domains {
			if err := validate.Domain("domains", d); err != nil {
				return nil, host.Invalid(err)
			}
		}
		return c.Collect(e, in.Domains, time.Now()), nil
	}})
}

// queryFPM asks each website's pool for its status page (a few at a time) and
// attaches the answer to the website's PHP block. A pool that does not answer
// (not reloaded yet, no PHP) simply has no FPM block.
func (c *Collector) queryFPM(e *host.Env, domains []string, php map[string]*PHP, rates bool) {
	type answer struct {
		domain string
		st     statusJSON
	}
	jobs := make(chan string)
	out := make(chan answer)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				sock, err := e.Root.Path(filepath.Join(e.M.PHPSocketDir, "nimvor-"+d+".sock"))
				if err != nil {
					continue
				}
				body, err := QueryStatus(sock, sites.FPMStatusPath, 2*time.Second)
				if err != nil {
					continue
				}
				if st, err := ParseStatus(body); err == nil {
					out <- answer{d, st}
				}
			}
		}()
	}
	go func() {
		for _, d := range domains {
			jobs <- d
		}
		close(jobs)
		wg.Wait()
		close(out)
	}()
	for a := range out {
		var prev *fpmCounters
		if p, ok := c.fpm[a.domain]; ok && rates {
			prev = &p
		}
		f, cur := fpmOf(a.st, prev)
		c.fpm[a.domain] = cur
		p := php[a.domain]
		if p == nil { // an on-demand pool with no worker alive right now still answers: zero workers is a measurement
			p = &PHP{MemoryKind: "pss"}
			php[a.domain] = p
		}
		p.FPM = f
		p.Workers = a.st.Total
	}
}
