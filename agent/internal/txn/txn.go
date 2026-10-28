// Package txn records system changes that must be undone completely if
// anything fails, even across an agent restart: a persistent, write-ahead
// transaction over the agent's file writer (agentfs), the same atomic
// writes and path policy the changesets use, for operations that go beyond
// one set of configuration files (new binaries and libraries, packages, a
// build account).
//
// Every change is recorded in the transaction's journal (tx.json) before
// it is made, with a copy of what it replaces, so a rollback — automatic
// on failure, after an interrupted run, or asked for later — puts back the
// previous state: replaced files are restored byte for byte with their
// owner and mode, files and folders it created are removed, and packages
// and accounts it added are removed again. Nothing it replaced is deleted
// before the transaction is committed, and a committed transaction keeps
// what it replaced, so it can still be rolled back.
//
// A transaction can also keep a snapshot of whole folders taken before
// any change (e.g. /etc/nginx), a last-resort copy for manual recovery.
package txn

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Statuses.
const (
	Running        = "running"
	Committed      = "committed"
	RolledBack     = "rolled_back"
	RollbackFailed = "rollback_failed"
)

// Change is one recorded change.
type Change struct {
	// Op: write (replaced or created a file), mkdir (created a folder),
	// package (installed a package), account (created a system account).
	Op   string `json:"op"`
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
	// What was there before a write.
	Existed bool   `json:"existed,omitempty"`
	Link    string `json:"link,omitempty"`
	Backup  string `json:"backup,omitempty"` // file in the transaction folder
	Mode    uint32 `json:"mode,omitempty"`
	UID     int    `json:"uid,omitempty"`
	GID     int    `json:"gid,omitempty"`
	At      string `json:"at"`
}

// Tx is a transaction.
type Tx struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Status     string          `json:"status"`
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt *time.Time      `json:"finished_at,omitempty"`
	Error      string          `json:"error,omitempty"`
	Changes    []Change        `json:"changes"`
	Snapshots  []string        `json:"snapshots,omitempty"`
	Meta       json.RawMessage `json:"meta,omitempty"`
	// RollbackOf is set on a transaction's record when it was rolled back
	// later (manually): why and when.
	RolledBackAt *time.Time `json:"rolled_back_at,omitempty"`
	Log          []string   `json:"log,omitempty"`

	e   *host.Env
	dir string
	log io.Writer
}

// Hooks undo what files cannot: packages and accounts.
type Hooks struct {
	RemovePackages func(ctx context.Context, names []string) error
	RemoveAccount  func(ctx context.Context, name string) error
	// AfterRestore runs after the files are back (e.g. nginx -t and a
	// reload); its error is reported, the files stay restored.
	AfterRestore func(ctx context.Context) error
}

// Root is where transactions of a kind are kept.
func Root(e *host.Env, kind string) string {
	return filepath.Join(e.Cfg.StateDir, "transactions", kind)
}

var seq atomic.Int64

// newID sorts in creation order: time, then a counter (several in one
// second), then random characters (unique across restarts).
func newID(now time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s-%06d-%s", now.UTC().Format("20060102T150405Z"), seq.Add(1)%1000000, hex.EncodeToString(b[:]))
}

// Begin starts a transaction and writes its journal.
func Begin(e *host.Env, kind string, log io.Writer) (*Tx, error) {
	if log == nil {
		log = io.Discard
	}
	now := e.Now().UTC()
	t := &Tx{ID: newID(now), Kind: kind, Status: Running, StartedAt: now, Changes: []Change{}, e: e, log: log}
	t.dir = filepath.Join(Root(e, kind), t.ID)
	if _, err := e.Writer.MkdirAll(t.dir, 0o700, agentfs.RootOwner); err != nil {
		return nil, err
	}
	t.Logf("transaction %s started", t.ID)
	return t, t.save()
}

// Dir is the transaction's folder (host path).
func (t *Tx) Dir() string { return t.dir }

// Logf writes to the job log and the journal.
func (t *Tx) Logf(format string, a ...any) {
	line := fmt.Sprintf(format, a...)
	fmt.Fprintln(t.log, line)
	t.Log = append(t.Log, t.e.Now().UTC().Format(time.RFC3339)+" "+line)
	if t.e.Log != nil {
		t.e.Log.Info(t.Kind+": "+line, "transaction", t.ID)
	}
}

// SetMeta stores caller data (versions, sources…) in the journal.
func (t *Tx) SetMeta(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	t.Meta = b
	return t.save()
}

func (t *Tx) save() error {
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	_, err = t.e.Writer.WriteFile(filepath.Join(t.dir, "tx.json"), b, 0o600, agentfs.RootOwner)
	return err
}

func (t *Tx) record(c Change) error {
	c.At = t.e.Now().UTC().Format(time.RFC3339)
	t.Changes = append(t.Changes, c)
	return t.save()
}

// Snapshot copies whole folders or files as they are now into the
// transaction (a last-resort copy; rollbacks use the journal). Missing
// paths are skipped. The total is limited to limit bytes.
func (t *Tx) Snapshot(limit int64, paths ...string) error {
	var total int64
	for _, p := range paths {
		p = filepath.Clean(p)
		covered := false
		for _, done := range t.Snapshots {
			covered = covered || p == done || strings.HasPrefix(p, strings.TrimSuffix(done, "/")+"/")
		}
		if covered {
			continue // inside a folder already copied
		}
		src, err := t.e.Root.Path(p)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		dstRoot, err := t.e.Root.Path(filepath.Join(t.dir, "snapshot"))
		if err != nil {
			return err
		}
		err = filepath.WalkDir(src, func(sp string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(filepath.Dir(src), sp)
			dp := filepath.Join(dstRoot, strings.TrimPrefix(filepath.Dir(p), "/"), rel)
			info, err := os.Lstat(sp)
			if err != nil {
				return err
			}
			st, _ := info.Sys().(*syscall.Stat_t)
			switch {
			case info.Mode()&fs.ModeSymlink != 0:
				target, err := os.Readlink(sp)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(dp), 0o700); err != nil {
					return err
				}
				if err := os.Symlink(target, dp); err != nil {
					return err
				}
			case info.IsDir():
				if err := os.MkdirAll(dp, 0o700); err != nil {
					return err
				}
			case info.Mode().IsRegular():
				total += info.Size()
				if total > limit {
					return fmt.Errorf("the snapshot of %s is larger than %d MiB", p, limit>>20)
				}
				if err := copyFile(sp, dp, info.Mode().Perm()); err != nil {
					return err
				}
			default:
				return nil // sockets, devices: not configuration
			}
			if st != nil {
				_ = os.Lchown(dp, int(st.Uid), int(st.Gid))
			}
			if info.Mode()&fs.ModeSymlink == 0 {
				_ = os.Chmod(dp, info.Mode()&(fs.ModePerm|fs.ModeSetgid|fs.ModeSetuid|fs.ModeSticky))
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("snapshot of %s: %v", p, err)
		}
		t.Snapshots = append(t.Snapshots, p)
		t.Logf("snapshot of %s saved", p)
	}
	return t.save()
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// WriteFile replaces (or creates) a file atomically, after recording what
// was there.
func (t *Tx) WriteFile(path string, data []byte, mode fs.FileMode, owner agentfs.Owner) error {
	st, err := t.e.Writer.Snapshot(path)
	if err != nil {
		return err
	}
	c := Change{Op: "write", Path: path, Existed: st.Exists, Link: st.Target, Mode: uint32(st.Mode), UID: st.UID, GID: st.GID}
	if st.IsDir {
		return fmt.Errorf("%s is a folder", path)
	}
	if st.Exists && !st.IsLink {
		c.Backup = fmt.Sprintf("backup-%03d", len(t.Changes))
		if _, err := t.e.Writer.WriteFile(filepath.Join(t.dir, c.Backup), st.Data, 0o600, agentfs.RootOwner); err != nil {
			return err
		}
	}
	if err := t.record(c); err != nil {
		return err
	}
	if _, err := t.e.Writer.WriteFile(path, data, mode, owner); err != nil {
		return err
	}
	t.Logf("wrote %s", path)
	return nil
}

// InstallFile copies a (possibly large) file into a new place. It never
// replaces anything: an existing path is an error.
func (t *Tx) InstallFile(path, srcReal string, mode fs.FileMode, owner agentfs.Owner) error {
	if st, err := t.e.Writer.Snapshot(path); err == nil && st.Exists {
		return fmt.Errorf("%s already exists; it is not replaced", path)
	} else if err != nil && !strings.Contains(err.Error(), "too large") {
		return err
	}
	// The source may lie in a folder an unprivileged account controls:
	// never follow a link, only a regular file.
	f, err := os.OpenFile(srcReal, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("%s: %v", filepath.Base(srcReal), err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return fmt.Errorf("%s is not a regular file", filepath.Base(srcReal))
	}
	b, err := io.ReadAll(io.LimitReader(f, 512<<20))
	f.Close()
	if err != nil {
		return err
	}
	if err := t.record(Change{Op: "write", Path: path}); err != nil {
		return err
	}
	if _, err := t.e.Writer.WriteFile(path, b, mode, owner); err != nil {
		return err
	}
	t.Logf("installed %s (%d bytes)", path, len(b))
	return nil
}

// Remove deletes a file (or link), keeping a copy to restore.
func (t *Tx) Remove(path string) error {
	st, err := t.e.Writer.Snapshot(path)
	if err != nil {
		return err
	}
	if !st.Exists {
		return nil
	}
	if st.IsDir {
		return fmt.Errorf("%s is a folder", path)
	}
	c := Change{Op: "write", Path: path, Existed: true, Link: st.Target, Mode: uint32(st.Mode), UID: st.UID, GID: st.GID}
	if !st.IsLink {
		c.Backup = fmt.Sprintf("backup-%03d", len(t.Changes))
		if _, err := t.e.Writer.WriteFile(filepath.Join(t.dir, c.Backup), st.Data, 0o600, agentfs.RootOwner); err != nil {
			return err
		}
	}
	if err := t.record(c); err != nil {
		return err
	}
	if _, err := t.e.Writer.Remove(path); err != nil {
		return err
	}
	t.Logf("removed %s", path)
	return nil
}

// RemoveDir deletes an empty folder (recorded: a rollback recreates it).
func (t *Tx) RemoveDir(path string, mode fs.FileMode) error {
	real, err := t.e.Root.Path(path)
	if err != nil {
		return err
	}
	if entries, err := os.ReadDir(real); err != nil || len(entries) > 0 {
		return nil // gone already, or still used by something else: kept
	}
	if err := t.record(Change{Op: "rmdir", Path: path, Mode: uint32(mode)}); err != nil {
		return err
	}
	if err := os.Remove(real); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Revert undoes a finished transaction's file changes as changes of this
// one (so they can themselves be undone): files it replaced get their
// earlier content back, files and folders it created are removed.
// Packages and accounts are the caller's.
func (t *Tx) Revert(old *Tx) error {
	for i := len(old.Changes) - 1; i >= 0; i-- {
		c := old.Changes[i]
		switch c.Op {
		case "write":
			switch {
			case !c.Existed:
				if err := t.Remove(c.Path); err != nil {
					return err
				}
			case c.Link != "":
				return fmt.Errorf("%s was a link; restore it by hand", c.Path)
			default:
				real, err := t.e.Root.Path(filepath.Join(old.dir, c.Backup))
				if err != nil {
					return err
				}
				b, err := os.ReadFile(real)
				if err != nil {
					return fmt.Errorf("the saved copy of %s is missing: %v", c.Path, err)
				}
				if err := t.WriteFile(c.Path, b, fs.FileMode(c.Mode), agentfs.Owner{UID: c.UID, GID: c.GID}); err != nil {
					return err
				}
			}
		case "mkdir":
			if err := t.RemoveDir(c.Path, 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

// MkdirAll creates missing folders, recording each one it creates.
func (t *Tx) MkdirAll(path string, mode fs.FileMode, owner agentfs.Owner) error {
	var missing []string
	for p := filepath.Clean(path); p != "/" && p != "."; p = filepath.Dir(p) {
		real, err := t.e.Root.Path(p)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(real); err == nil {
			break
		}
		missing = append([]string{p}, missing...)
	}
	for _, p := range missing {
		if err := t.record(Change{Op: "mkdir", Path: p}); err != nil {
			return err
		}
		if _, err := t.e.Writer.MkdirAll(p, mode, owner); err != nil {
			return err
		}
	}
	return nil
}

// Package records a package this transaction installed.
func (t *Tx) Package(name string) error {
	t.Logf("package %s installed", name)
	return t.record(Change{Op: "package", Name: name})
}

// Account records a system account this transaction created.
func (t *Tx) Account(name string) error {
	t.Logf("account %s created", name)
	return t.record(Change{Op: "account", Name: name})
}

func (t *Tx) finish(status string, err error) error {
	now := t.e.Now().UTC()
	t.FinishedAt = &now
	t.Status = status
	if err != nil {
		t.Error = err.Error()
	}
	return t.save()
}

// Commit marks the transaction done; what it replaced is kept (a later
// rollback can still restore it).
func (t *Tx) Commit() error {
	t.Logf("transaction %s committed", t.ID)
	return t.finish(Committed, nil)
}

// MarkRolledBack records that a committed transaction was undone by
// another one (see Revert).
func (t *Tx) MarkRolledBack(by string) error {
	now := t.e.Now().UTC()
	t.RolledBackAt = &now
	t.Status = RolledBack
	t.Error = "rolled back by " + by
	return t.save()
}

// Rollback undoes every recorded change, newest first, and records why.
// It continues past individual failures and reports them all.
func (t *Tx) Rollback(ctx context.Context, h Hooks, why error) error {
	t.Logf("rolling back transaction %s: %v", t.ID, why)
	var errs []string
	var pkgs []string
	for i := len(t.Changes) - 1; i >= 0; i-- {
		c := t.Changes[i]
		switch c.Op {
		case "write":
			if err := t.restoreFile(c); err != nil {
				errs = append(errs, c.Path+": "+err.Error())
			} else {
				t.Logf("restored %s", c.Path)
			}
		case "rmdir":
			if _, err := t.e.Writer.MkdirAll(c.Path, fs.FileMode(c.Mode), agentfs.RootOwner); err != nil {
				errs = append(errs, c.Path+": "+err.Error())
			}
		case "mkdir":
			real, err := t.e.Root.Path(c.Path)
			if err == nil {
				if err := os.Remove(real); err != nil && !errors.Is(err, fs.ErrNotExist) {
					errs = append(errs, c.Path+": "+err.Error())
				} else {
					t.Logf("removed folder %s", c.Path)
				}
			}
		case "package":
			pkgs = append(pkgs, c.Name)
		case "account":
			if h.RemoveAccount != nil {
				if err := h.RemoveAccount(ctx, c.Name); err != nil {
					errs = append(errs, "account "+c.Name+": "+err.Error())
				} else {
					t.Logf("removed account %s", c.Name)
				}
			}
		}
	}
	if len(pkgs) > 0 && h.RemovePackages != nil {
		sort.Strings(pkgs)
		if err := h.RemovePackages(ctx, pkgs); err != nil {
			errs = append(errs, "packages "+strings.Join(pkgs, ", ")+": "+err.Error())
		} else {
			t.Logf("removed packages %s", strings.Join(pkgs, ", "))
		}
	}
	if h.AfterRestore != nil {
		if err := h.AfterRestore(ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		rerr := fmt.Errorf("rollback incomplete: %s", strings.Join(errs, "; "))
		t.Logf("%v", rerr)
		t.finish(RollbackFailed, fmt.Errorf("%v; %v", why, rerr))
		return rerr
	}
	if t.Status == Committed {
		// A committed transaction undone later keeps its record.
		now := t.e.Now().UTC()
		t.RolledBackAt = &now
		t.Status = RolledBack
		t.Error = why.Error()
		return t.save()
	}
	return t.finish(RolledBack, why)
}

func (t *Tx) restoreFile(c Change) error {
	switch {
	case !c.Existed:
		_, err := t.e.Writer.Remove(c.Path)
		return err
	case c.Link != "":
		return t.e.Writer.Restore(c.Path, agentfs.State{Exists: true, IsLink: true, Target: c.Link})
	default:
		real, err := t.e.Root.Path(filepath.Join(t.dir, c.Backup))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(real)
		if err != nil {
			return err
		}
		return t.e.Writer.Restore(c.Path, agentfs.State{Exists: true, Data: b, Mode: fs.FileMode(c.Mode), UID: c.UID, GID: c.GID})
	}
}

// Load reads a transaction's journal.
func Load(e *host.Env, kind, id string) (*Tx, error) {
	if len(id) > 40 || strings.ContainsAny(id, "/.\\") || id == "" {
		return nil, fs.ErrNotExist
	}
	dir := filepath.Join(Root(e, kind), id)
	b, err := e.Root.ReadFile(filepath.Join(dir, "tx.json"))
	if err != nil {
		return nil, err
	}
	t := &Tx{}
	if err := json.Unmarshal(b, t); err != nil {
		return nil, err
	}
	t.e, t.dir, t.log = e, dir, io.Discard
	return t, nil
}

// SetLog sets where a loaded transaction logs.
func (t *Tx) SetLog(w io.Writer) { t.log = w }

// List returns the transactions of a kind, newest first.
func List(e *host.Env, kind string) []*Tx {
	real, err := e.Root.Path(Root(e, kind))
	if err != nil {
		return nil
	}
	entries, _ := os.ReadDir(real)
	var out []*Tx
	for _, en := range entries {
		if t, err := Load(e, kind, en.Name()); err == nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// Recover rolls back transactions left running (the agent stopped in the
// middle): what they changed was never validated.
func Recover(ctx context.Context, e *host.Env, kind string, h Hooks, log io.Writer) []string {
	var done []string
	for _, t := range List(e, kind) {
		if t.Status != Running {
			continue
		}
		t.SetLog(log)
		err := t.Rollback(ctx, h, errors.New("interrupted before it finished"))
		line := "interrupted transaction " + t.ID + " rolled back"
		if err != nil {
			line += " incompletely: " + err.Error()
		}
		done = append(done, line)
	}
	return done
}

// Prune deletes finished transactions older than keep, but never the
// newest committed one that was not rolled back (the last known-good
// state), a running one, or one protect keeps.
func Prune(e *host.Env, kind string, keep time.Duration, protect ...func(*Tx) bool) []string {
	var deleted []string
	all := List(e, kind)
	lastGood := ""
	for _, t := range all {
		if t.Status == Committed {
			lastGood = t.ID
			break
		}
	}
	cutoff := e.Now().Add(-keep)
	for _, t := range all {
		kept := false
		for _, p := range protect {
			kept = kept || p(t)
		}
		if kept || t.ID == lastGood || t.Status == Running || t.StartedAt.After(cutoff) {
			continue
		}
		if real, err := e.Root.Path(t.dir); err == nil && os.RemoveAll(real) == nil {
			deleted = append(deleted, t.ID)
		}
	}
	return deleted
}
