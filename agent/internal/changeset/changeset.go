// Package changeset applies a group of file changes as one unit:
//
//	Detect (snapshot current state) → Backup (persist originals)
//	→ Apply (atomic writes) → Validate (e.g. nginx -t) → Rollback on failure
//
// If nothing changed, validation and reloads are skipped, which makes every
// action built on changesets idempotent.
package changeset

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
)

type kind int

const (
	opWrite kind = iota
	opRemove
	opLink
	opMkdir
)

type op struct {
	kind   kind
	path   string
	data   []byte
	mode   fs.FileMode
	owner  agentfs.Owner
	target string
}

// Set is an ordered list of pending changes.
type Set struct {
	w         agentfs.Writer
	backupDir string
	label     string
	ops       []op
}

// New creates a Set. backupDir is a host path (permitted by the writer's
// policy) where originals are kept; label names the backup folder.
func New(w agentfs.Writer, backupDir, label string) *Set {
	return &Set{w: w, backupDir: backupDir, label: label}
}

// Write schedules replacing a file.
func (s *Set) Write(path string, data []byte, mode fs.FileMode, owner agentfs.Owner) {
	s.ops = append(s.ops, op{kind: opWrite, path: path, data: data, mode: mode, owner: owner})
}

// Remove schedules removing a file or symlink.
func (s *Set) Remove(path string) { s.ops = append(s.ops, op{kind: opRemove, path: path}) }

// Link schedules pointing a symlink at target.
func (s *Set) Link(target, link string) {
	s.ops = append(s.ops, op{kind: opLink, path: link, target: target})
}

// Mkdir schedules creating a directory (and applying mode/owner).
func (s *Set) Mkdir(path string, mode fs.FileMode, owner agentfs.Owner) {
	s.ops = append(s.ops, op{kind: opMkdir, path: path, mode: mode, owner: owner})
}

// Applied is the result of a successful Apply.
type Applied struct {
	w       agentfs.Writer
	done    []applied
	Changed []string `json:"changed"`
	Backup  string   `json:"backup,omitempty"`
}

type applied struct {
	path  string
	prior agentfs.State
}

// Rollback restores every changed path to its prior state (reverse order).
func (a *Applied) Rollback() error {
	var errs []string
	for i := len(a.done) - 1; i >= 0; i-- {
		d := a.done[i]
		if err := a.w.Restore(d.path, d.prior); err != nil {
			errs = append(errs, d.path+": "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New("rollback incomplete: " + strings.Join(errs, "; "))
	}
	return nil
}

// Apply snapshots, backs up and applies all changes. On any error the
// already-applied changes are rolled back before returning.
func (s *Set) Apply() (*Applied, error) {
	a := &Applied{w: s.w, Changed: []string{}}
	priors := make([]agentfs.State, len(s.ops))
	for i, o := range s.ops {
		st, err := s.w.Snapshot(o.path)
		if err != nil {
			return nil, err
		}
		priors[i] = st
	}
	for i, o := range s.ops {
		var changed bool
		var err error
		switch o.kind {
		case opWrite:
			changed, err = s.w.WriteFile(o.path, o.data, o.mode, o.owner)
		case opRemove:
			changed, err = s.w.Remove(o.path)
		case opLink:
			changed, err = s.w.Symlink(o.target, o.path)
		case opMkdir:
			var created bool
			created, err = s.w.MkdirAll(o.path, o.mode, o.owner)
			changed = created || priors[i].Mode != o.mode
		}
		if err != nil {
			if rbErr := a.Rollback(); rbErr != nil {
				return nil, fmt.Errorf("%s: %w (%v)", o.path, err, rbErr)
			}
			return nil, fmt.Errorf("%s: %w", o.path, err)
		}
		if changed {
			a.done = append(a.done, applied{path: o.path, prior: priors[i]})
			a.Changed = append(a.Changed, o.path)
		}
	}
	if len(a.done) > 0 && s.backupDir != "" {
		a.Backup = s.persistBackup(a.done)
	}
	return a, nil
}

// ValidationError carries the output of a failed validation step.
type ValidationError struct {
	Step   string
	Output string
	Err    error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s failed: %v", e.Step, e.Err)
}

// Commit applies the set, runs validate when something changed, and rolls
// back if validation fails. It returns the applied changes on success.
func (s *Set) Commit(validate func() error) (*Applied, error) {
	a, err := s.Apply()
	if err != nil {
		return nil, err
	}
	if len(a.Changed) == 0 || validate == nil {
		return a, nil
	}
	if verr := validate(); verr != nil {
		if rbErr := a.Rollback(); rbErr != nil {
			return nil, fmt.Errorf("%w; additionally %v", verr, rbErr)
		}
		return nil, verr
	}
	return a, nil
}

type manifestEntry struct {
	Path    string `json:"path"`
	Existed bool   `json:"existed"`
	Link    string `json:"link,omitempty"`
	File    string `json:"file,omitempty"`
}

// persistBackup stores originals on disk so an operator can always recover
// the previous working configuration, even after the agent restarts.
func (s *Set) persistBackup(done []applied) string {
	dir := filepath.Join(s.backupDir, time.Now().UTC().Format("20060102T150405.000Z")+"-"+safe(s.label))
	if _, err := s.w.MkdirAll(dir, 0o700, agentfs.RootOwner); err != nil {
		return ""
	}
	var manifest []manifestEntry
	for i, d := range done {
		e := manifestEntry{Path: d.path, Existed: d.prior.Exists, Link: d.prior.Target}
		if d.prior.Exists && !d.prior.IsLink && !d.prior.IsDir {
			e.File = fmt.Sprintf("%03d.orig", i)
			_, _ = s.w.WriteFile(filepath.Join(dir, e.File), d.prior.Data, 0o600, agentfs.RootOwner)
		}
		manifest = append(manifest, e)
	}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	_, _ = s.w.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o600, agentfs.RootOwner)
	s.prune(200)
	return dir
}

// prune keeps the newest n backup folders.
func (s *Set) prune(n int) {
	real, err := s.w.Root.Path(s.backupDir)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(real)
	if err != nil || len(entries) <= n {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names[:max(0, len(names)-n)] {
		_ = os.RemoveAll(filepath.Join(real, name))
	}
}

func safe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	if b.Len() > 60 {
		return b.String()[:60]
	}
	return b.String()
}
