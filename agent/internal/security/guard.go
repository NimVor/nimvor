// Package security manages the firewall (UFW or firewalld), fail2ban and the SSH
// server, and audits the server's security posture.
//
// Changes that could lock an administrator out (firewall rules, enabling
// the firewall, SSH settings) are applied under a Guard: the change is
// reverted automatically unless it is confirmed from the panel within the
// confirmation window. The pending rollback is persisted in state_dir, so
// it also happens if the agent restarts in the meantime.
package security

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// ConfirmWindow is how long a guarded change waits for confirmation.
var ConfirmWindow = 120 * time.Second

// FileRestore puts a file back as it was before a change.
type FileRestore struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	Content string `json:"content"` // base64
	Mode    uint32 `json:"mode"`
}

// Pending is a change awaiting confirmation.
type Pending struct {
	ID          string        `json:"id"`
	Kind        string        `json:"kind"` // firewall, ssh
	Summary     string        `json:"summary"`
	Deadline    time.Time     `json:"deadline"`
	UFW         [][]string    `json:"ufw,omitempty"`      // inverse ufw commands, run in order
	Commands    [][]string    `json:"commands,omitempty"` // inverse firewalld commands (tool first)
	Units       [][2]string   `json:"units,omitempty"`    // systemctl verb and unit, run in order
	Files       []FileRestore `json:"files,omitempty"`    // restored before Reload
	ReloadUnits []string      `json:"reload_units,omitempty"`
	Restart     []string      `json:"restart,omitempty"` // units restarted (after a daemon-reload) once Files are restored
}

// Guard tracks at most one pending change.
type Guard struct {
	env     *host.Env
	mu      sync.Mutex
	pending *Pending
	timer   *time.Timer
}

func (g *Guard) path() string { return filepath.Join(g.env.Cfg.StateDir, "security-pending.json") }

// NewGuard loads a pending change left by a previous run: an expired one
// is rolled back immediately, otherwise its timer is re-armed.
func NewGuard(env *host.Env) *Guard {
	g := &Guard{env: env}
	b, err := env.Root.ReadFile(g.path())
	if err != nil {
		return g
	}
	var p Pending
	if json.Unmarshal(b, &p) != nil || p.ID == "" {
		return g
	}
	wait := time.Until(p.Deadline)
	if wait < 0 {
		wait = 0
	}
	// Arm under the lock: an expired change fires at once, and expire must
	// not run before pending and timer are set.
	g.mu.Lock()
	g.pending = &p
	g.timer = time.AfterFunc(wait, func() { g.expire(p.ID) })
	g.mu.Unlock()
	env.Log.Warn("unconfirmed security change found at startup", "id", p.ID, "kind", p.Kind, "rollback_in", wait.String())
	return g
}

// Busy returns an error when another change still awaits confirmation.
func (g *Guard) Busy() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending != nil {
		return actions.Conflict(fmt.Sprintf("Confirm or undo the previous change (%s) first.", g.pending.Summary))
	}
	return nil
}

// Start registers a change that was just applied.
func (g *Guard) Start(p Pending) (*Pending, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending != nil {
		return nil, errors.New("another change is pending")
	}
	var b [6]byte
	_, _ = rand.Read(b[:])
	p.ID = hex.EncodeToString(b[:])
	p.Deadline = g.env.Now().Add(ConfirmWindow)
	data, _ := json.Marshal(p)
	if _, err := g.env.Writer.WriteFile(g.path(), data, 0o600, agentfs.RootOwner); err != nil {
		return nil, err
	}
	g.pending = &p
	id := p.ID
	g.timer = time.AfterFunc(ConfirmWindow, func() { g.expire(id) })
	g.env.Log.Info("security change applied, awaiting confirmation", "id", p.ID, "kind", p.Kind, "summary", p.Summary)
	return &p, nil
}

// Current returns the pending change, if any.
func (g *Guard) Current() *Pending {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending == nil {
		return nil
	}
	p := *g.pending
	return &p
}

// Confirm keeps the pending change.
func (g *Guard) Confirm(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending == nil || g.pending.ID != id {
		return actions.NotFound("There is no pending change with this id (it may already have been rolled back).")
	}
	g.clear()
	g.env.Log.Info("security change confirmed", "id", id)
	return nil
}

// Undo rolls the pending change back now.
func (g *Guard) Undo(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending == nil || g.pending.ID != id {
		return actions.NotFound("There is no pending change with this id.")
	}
	return g.rollback()
}

func (g *Guard) expire(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.pending == nil || g.pending.ID != id {
		return
	}
	g.env.Log.Warn("security change not confirmed in time, rolling back", "id", id, "summary", g.pending.Summary)
	if err := g.rollback(); err != nil {
		g.env.Log.Error("security rollback failed", "id", id, "error", err)
	}
}

// clear forgets the pending change (caller holds mu).
func (g *Guard) clear() {
	if g.timer != nil {
		g.timer.Stop()
	}
	g.pending, g.timer = nil, nil
	_, _ = g.env.Writer.Remove(g.path())
}

// rollback undoes the pending change (caller holds mu). Every step is
// attempted; the first error is returned.
func (g *Guard) rollback() error {
	p := g.pending
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}
	for _, args := range p.UFW {
		out, err := g.env.Runner.Run(ctx, "ufw", args...)
		if err != nil {
			keep(fmt.Errorf("ufw %v: %s", args, host.Output(out)))
		}
	}
	if len(p.Commands) > 0 {
		keep(runFirewalld(ctx, g.env, p.Commands))
	}
	for _, u := range p.Units {
		keep(g.env.Systemctl(ctx, u[0], u[1]))
	}
	for _, f := range p.Files {
		keep(restoreFile(g.env, f))
	}
	if len(p.Restart) > 0 {
		keep(g.env.DaemonReload(ctx))
		for _, u := range p.Restart {
			if out, err := g.env.Runner.Run(ctx, "systemctl", "restart", "--", u); err != nil {
				keep(fmt.Errorf("systemctl restart %s: %s", u, host.Output(out)))
			}
		}
	}
	for _, u := range p.ReloadUnits {
		keep(g.env.Systemctl(ctx, "reload", u))
	}
	g.clear()
	g.env.Log.Info("security change rolled back", "id", p.ID, "kind", p.Kind)
	return first
}

// restoreFile puts a snapshotted file back (or removes it when it did not exist).
func restoreFile(env *host.Env, f FileRestore) error {
	if !f.Existed {
		_, err := env.Writer.Remove(f.Path)
		return err
	}
	data, err := base64.StdEncoding.DecodeString(f.Content)
	if err != nil {
		return err
	}
	_, err = env.Writer.WriteFile(f.Path, data, fs.FileMode(f.Mode), agentfs.RootOwner)
	return err
}

// snapshot records the current state of a file for FileRestore.
func snapshot(env *host.Env, path string) FileRestore {
	b, err := env.Root.ReadFile(path)
	if err != nil {
		return FileRestore{Path: path}
	}
	mode := uint32(0o644)
	if info, err := env.Root.Stat(path); err == nil {
		mode = uint32(info.Mode().Perm())
	}
	return FileRestore{Path: path, Existed: true, Content: base64.StdEncoding.EncodeToString(b), Mode: mode}
}
