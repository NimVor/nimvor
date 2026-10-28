package terminal

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// A website account's terminal is opened with that Linux account's own
// password: the one in /etc/shadow, which is also its SSH/SFTP password
// (set from the FTP / SFTP page). The browser sends it inside the
// WebSocket, straight to the agent; the panel never receives it. The agent
// checks it with PAM's unix_chkpwd (password on standard input, never in
// arguments), so every hash PAM understands works (yescrypt, SHA-512…),
// and a locked, expired or suspended account is refused. The password is
// never logged, stored or sent back.

const (
	maxAttempts   = 3                // wrong passwords per ticket
	failWindow    = 15 * time.Minute // throttling window
	maxFailures   = 5                // per account and per address in the window
	maxPasswordSz = 512
)

// failurePause slows down guessing after a wrong password (tests shorten it).
var failurePause = time.Second

// errWrongPassword is the only failure a browser sees (no hint whether the
// account is locked or expired: sshd tells as little).
var errWrongPassword = errors.New("wrong password")

// privilegedGroups must not be held by a website account: a terminal for
// such an account is refused (a misconfiguration the administrator fixes).
// PrivilegedGroups returns the privileged groups user belongs to.
func PrivilegedGroups(e *host.Env, user string, primaryGID int) []string {
	return e.PrivilegedGroups(user, primaryGID)
}

// checkPassword verifies a Linux account's password with PAM's helper.
func checkPassword(ctx context.Context, e *host.Env, user, password string) error {
	if password == "" || len(password) > maxPasswordSz || strings.ContainsRune(password, 0) {
		return errWrongPassword
	}
	if !e.Runner.Available("unix_chkpwd") {
		return fmt.Errorf("PAM's unix_chkpwd is not installed")
	}
	// Exit 0: correct; anything else (7: wrong or locked): refused.
	if _, err := e.Runner.RunWith(ctx, "unix_chkpwd", execx.Opts{Stdin: strings.NewReader(password + "\x00")}, user, "nonull"); err != nil {
		return errWrongPassword
	}
	// An expired account (suspended accounts expire) is refused too.
	if _, err := e.Runner.RunWith(ctx, "unix_chkpwd", execx.Opts{}, user, "chkexpiry"); err != nil {
		return errWrongPassword
	}
	return nil
}

// throttle counts wrong passwords per account and per client address.
type throttle struct {
	mu   sync.Mutex
	fail map[string][]time.Time
}

func (t *throttle) prune(k string, now time.Time) []time.Time {
	var keep []time.Time
	for _, at := range t.fail[k] {
		if now.Sub(at) < failWindow {
			keep = append(keep, at)
		}
	}
	if keep == nil {
		delete(t.fail, k)
	} else {
		t.fail[k] = keep
	}
	return keep
}

// blocked says how long keys are still blocked (0: not blocked).
func (t *throttle) blocked(now time.Time, keys ...string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	var wait time.Duration
	for _, k := range keys {
		if f := t.prune(k, now); len(f) >= maxFailures {
			if w := failWindow - now.Sub(f[len(f)-maxFailures]); w > wait {
				wait = w
			}
		}
	}
	return wait
}

func (t *throttle) record(now time.Time, keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fail == nil {
		t.fail = map[string][]time.Time{}
	}
	for _, k := range keys {
		t.fail[k] = append(t.prune(k, now), now)
	}
}

func (t *throttle) clear(keys ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, k := range keys {
		delete(t.fail, k)
	}
}

// KillUser ends every session of a Linux account (its password or shell
// access changed, or it was suspended).
func (m *Manager) KillUser(user, reason string) int {
	m.mu.Lock()
	var hit []*Session
	for _, s := range m.sessions {
		if s.User == user {
			hit = append(hit, s)
		}
	}
	for k, t := range m.tickets {
		if t.spec.User == user {
			delete(m.tickets, k)
		}
	}
	m.mu.Unlock()
	for _, s := range hit {
		s.end(reason)
	}
	return len(hit)
}
