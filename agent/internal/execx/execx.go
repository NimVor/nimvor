// Package execx runs a fixed allowlist of system binaries.
//
// There is deliberately no way to run a shell or an arbitrary program.
// Callers name a command ID; the Runner maps it to an absolute binary path
// discovered at start-up and passes arguments directly (no shell parsing).
package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// ErrNotAllowed is returned for commands outside the allowlist.
var ErrNotAllowed = errors.New("command is not allowed")

// ErrUnavailable is returned when an allowed binary is not installed.
var ErrUnavailable = errors.New("command is not installed")

// Runner executes allowlisted commands.
type Runner interface {
	Run(ctx context.Context, id string, args ...string) ([]byte, error)
	Available(id string) bool
}

// maxOutput bounds captured output (1 MiB).
const maxOutput = 1 << 20

// defaultCandidates maps command IDs to candidate absolute paths.
var defaultCandidates = map[string][]string{
	"systemctl":    {"/usr/bin/systemctl", "/bin/systemctl"},
	"nginx":        {"/usr/sbin/nginx", "/usr/bin/nginx"},
	"mysqld":       {"/usr/sbin/mysqld", "/usr/bin/mysqld", "/usr/libexec/mysqld"},
	"mariadbd":     {"/usr/sbin/mariadbd", "/usr/bin/mariadbd", "/usr/libexec/mariadbd"},
	"redis-server": {"/usr/bin/redis-server", "/usr/sbin/redis-server"},
}

// SystemRunner is the production Runner.
type SystemRunner struct {
	paths   map[string]string
	timeout time.Duration
}

// NewSystemRunner resolves the allowlist against the host filesystem.
func NewSystemRunner(timeout time.Duration) *SystemRunner {
	r := &SystemRunner{paths: map[string]string{}, timeout: timeout}
	for id, cands := range defaultCandidates {
		for _, c := range cands {
			if info, err := os.Stat(c); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
				r.paths[id] = c
				break
			}
		}
	}
	return r
}

// Available reports whether the command is allowlisted and installed.
func (r *SystemRunner) Available(id string) bool {
	_, ok := r.paths[id]
	return ok
}

// Run executes the command with a minimal environment and returns combined
// output. Arguments are validated to contain no control characters.
func (r *SystemRunner) Run(ctx context.Context, id string, args ...string) ([]byte, error) {
	if _, known := defaultCandidates[id]; !known {
		return nil, fmt.Errorf("%w: %s", ErrNotAllowed, id)
	}
	bin, ok := r.paths[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, id)
	}
	for _, a := range args {
		if strings.ContainsAny(a, "\x00\n\r") {
			return nil, fmt.Errorf("invalid argument for %s", id)
		}
	}
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C", "SYSTEMD_PAGER=", "SYSTEMD_COLORS=0"}
	cmd.Dir = "/"
	var buf limitedBuffer
	buf.limit = maxOutput
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.Len()
	if room > 0 {
		if len(p) > room {
			b.Buffer.Write(p[:room])
		} else {
			b.Buffer.Write(p)
		}
	}
	return len(p), nil
}

// FakeRunner is a test double returning canned output keyed by
// "id arg1 arg2 ...".
type FakeRunner struct {
	Outputs map[string]string
	Errors  map[string]error
	Calls   []string
}

// Available reports true if any output is registered for id.
func (f *FakeRunner) Available(id string) bool {
	for k := range f.Outputs {
		if k == id || strings.HasPrefix(k, id+" ") {
			return true
		}
	}
	return false
}

// Run returns the canned output for the call.
func (f *FakeRunner) Run(_ context.Context, id string, args ...string) ([]byte, error) {
	key := strings.TrimSpace(id + " " + strings.Join(args, " "))
	f.Calls = append(f.Calls, key)
	if err, ok := f.Errors[key]; ok {
		return []byte(f.Outputs[key]), err
	}
	out, ok := f.Outputs[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, id)
	}
	return []byte(out), nil
}
