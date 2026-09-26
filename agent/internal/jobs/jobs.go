// Package jobs runs long operations (certificate issuance, backups,
// deployments, package installs) in the background with a streamed log.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/logging"
)

// Status values.
const (
	Queued    = "queued"
	Running   = "running"
	Succeeded = "succeeded"
	Failed    = "failed"
)

// Job describes one background operation.
type Job struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Target     string     `json:"target"`
	Status     string     `json:"status"`
	Error      string     `json:"error,omitempty"`
	Result     any        `json:"result,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// Func performs the work; everything written to log is kept in the job log
// (after secret redaction).
type Func func(ctx context.Context, log io.Writer) (any, error)

// Manager schedules jobs with bounded concurrency.
type Manager struct {
	dir     string
	log     *slog.Logger
	sem     chan struct{}
	timeout time.Duration

	mu   sync.Mutex
	jobs map[string]*Job
	// locks serialises jobs on the same target (e.g. one site).
	locks map[string]*sync.Mutex
	// running counts jobs whose goroutine has not returned yet.
	running sync.WaitGroup
}

// NewManager stores job logs under dir.
func NewManager(dir string, concurrency int, timeout time.Duration, log *slog.Logger) (*Manager, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	m := &Manager{dir: dir, log: log, sem: make(chan struct{}, concurrency), timeout: timeout, jobs: map[string]*Job{}, locks: map[string]*sync.Mutex{}}
	m.loadHistory()
	return m, nil
}

// ErrUnknownJob is returned for missing IDs.
var ErrUnknownJob = errors.New("unknown job")

var idRe = regexp.MustCompile(`^[a-f0-9]{16}$`)

// Start queues fn and returns immediately.
func (m *Manager) Start(kind, target string, fn Func) Job {
	var b [8]byte
	_, _ = rand.Read(b[:])
	j := &Job{ID: hex.EncodeToString(b[:]), Kind: kind, Target: target, Status: Queued, CreatedAt: time.Now().UTC()}
	m.mu.Lock()
	m.jobs[j.ID] = j
	lock := m.locks[target]
	if lock == nil {
		lock = &sync.Mutex{}
		m.locks[target] = lock
	}
	snapshot := *j
	m.mu.Unlock()

	m.running.Add(1)
	go func() {
		defer m.running.Done()
		lock.Lock()
		defer lock.Unlock()
		m.sem <- struct{}{}
		defer func() { <-m.sem }()
		m.run(j, fn)
	}()
	return snapshot
}

func (m *Manager) run(j *Job, fn Func) {
	f, err := os.OpenFile(filepath.Join(m.dir, j.ID+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		m.finish(j, nil, err)
		return
	}
	defer f.Close()
	now := time.Now().UTC()
	m.mu.Lock()
	j.Status, j.StartedAt = Running, &now
	m.mu.Unlock()
	w := &redactingWriter{w: f}
	fmt.Fprintf(w, "[%s] %s started\n", now.Format(time.RFC3339), j.Kind)

	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	var result any
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("internal error")
				m.log.Error("job panicked", "job", j.ID, "panic", r)
			}
		}()
		result, err = fn(ctx, w)
	}()
	if err != nil {
		fmt.Fprintf(w, "[%s] FAILED: %s\n", time.Now().UTC().Format(time.RFC3339), logging.Redact(err.Error()))
	} else {
		fmt.Fprintf(w, "[%s] completed successfully\n", time.Now().UTC().Format(time.RFC3339))
	}
	m.finish(j, result, err)
}

func (m *Manager) finish(j *Job, result any, err error) {
	now := time.Now().UTC()
	m.mu.Lock()
	j.FinishedAt = &now
	j.Result = result
	if err != nil {
		j.Status, j.Error = Failed, logging.Redact(err.Error())
	} else {
		j.Status = Succeeded
	}
	snapshot := *j
	m.mu.Unlock()
	b, _ := json.Marshal(snapshot)
	_ = os.WriteFile(filepath.Join(m.dir, j.ID+".json"), b, 0o600)
	m.log.Info("job finished", "job", j.ID, "kind", j.Kind, "target", j.Target, "status", snapshot.Status)
	m.prune(500)
}

// Drain waits until every started job has finished, including writing its
// record (used on shutdown and by tests before their folders are removed).
func (m *Manager) Drain() { m.running.Wait() }

// DrainWithin waits like Drain but at most until ctx ends. It reports
// whether every job finished.
func (m *Manager) DrainWithin(ctx context.Context) bool {
	done := make(chan struct{})
	go func() { m.running.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// Active returns the jobs that are queued or running.
func (m *Manager) Active() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Job
	for _, j := range m.jobs {
		if Active(j.Status) {
			out = append(out, *j)
		}
	}
	return out
}

// Active reports whether a job with this status has not finished yet.
func Active(status string) bool { return status == Queued || status == Running }

// Get returns a job by ID.
func (m *Manager) Get(id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrUnknownJob
	}
	return *j, nil
}

// Log returns log content from offset (max 256 KiB) and the next offset.
func (m *Manager) Log(id string, offset int64) (string, int64, error) {
	if !idRe.MatchString(id) {
		return "", 0, ErrUnknownJob
	}
	if _, err := m.Get(id); err != nil {
		return "", 0, err
	}
	f, err := os.Open(filepath.Join(m.dir, id+".log"))
	if err != nil {
		return "", offset, nil // not started yet
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", offset, err
	}
	b, _ := io.ReadAll(io.LimitReader(f, 256<<10))
	return string(b), offset + int64(len(b)), nil
}

// Wait blocks until the job finishes or ctx ends (used by tests and by
// synchronous callers).
func (m *Manager) Wait(ctx context.Context, id string) (Job, error) {
	for {
		j, err := m.Get(id)
		if err != nil || j.Status == Succeeded || j.Status == Failed {
			return j, err
		}
		select {
		case <-ctx.Done():
			return j, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// List returns recent jobs, newest first, optionally filtered by target.
func (m *Manager) List(target string, limit int) []Job {
	m.mu.Lock()
	out := make([]Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		if target == "" || j.Target == target {
			out = append(out, *j)
		}
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, k int) bool { return out[i].CreatedAt.After(out[k].CreatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *Manager) loadHistory() {
	files, _ := filepath.Glob(filepath.Join(m.dir, "*.json"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(b, &j) == nil && idRe.MatchString(j.ID) {
			m.jobs[j.ID] = &j
		}
	}
	// Jobs that were running when the agent stopped are marked failed.
	logs, _ := filepath.Glob(filepath.Join(m.dir, "*.log"))
	for _, l := range logs {
		id := filepath.Base(l[:len(l)-4])
		if _, ok := m.jobs[id]; !ok && idRe.MatchString(id) {
			info, _ := os.Stat(l)
			t := info.ModTime().UTC()
			m.jobs[id] = &Job{ID: id, Kind: "unknown", Status: Failed, Error: "interrupted by an agent restart", CreatedAt: t, FinishedAt: &t}
		}
	}
}

func (m *Manager) prune(keep int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.jobs) <= keep {
		return
	}
	all := make([]*Job, 0, len(m.jobs))
	for _, j := range m.jobs {
		if j.FinishedAt != nil {
			all = append(all, j)
		}
	}
	sort.Slice(all, func(i, k int) bool { return all[i].CreatedAt.Before(all[k].CreatedAt) })
	for _, j := range all[:max(0, len(m.jobs)-keep)] {
		delete(m.jobs, j.ID)
		os.Remove(filepath.Join(m.dir, j.ID+".log"))
		os.Remove(filepath.Join(m.dir, j.ID+".json"))
	}
}

// redactingWriter strips secrets line by line before they reach disk.
type redactingWriter struct {
	w       io.Writer
	pending []byte
	mu      sync.Mutex
}

func (r *redactingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, p...)
	for {
		i := indexByte(r.pending, '\n')
		if i < 0 {
			break
		}
		line := logging.Redact(string(r.pending[:i]))
		if _, err := io.WriteString(r.w, line+"\n"); err != nil {
			return 0, err
		}
		r.pending = r.pending[i+1:]
	}
	if len(r.pending) > 64<<10 { // very long line without newline
		io.WriteString(r.w, logging.Redact(string(r.pending)))
		r.pending = r.pending[:0]
	}
	return len(p), nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}
