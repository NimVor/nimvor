// Package agentfs performs the agent's writes to the host.
//
// Every write goes through a Policy: an allowlist of the exact files and
// subtrees WhaCP owns (e.g. /etc/nginx/sites-available/whacp-*.conf). A bug
// or a malicious request can therefore never overwrite a file WhaCP does not
// manage, such as a site configured by another panel or /etc/passwd.
// Files are replaced atomically (write temp, fsync, rename).
package agentfs

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
)

// ErrNotPermitted is returned for paths outside the write policy.
var ErrNotPermitted = errors.New("path is not managed by WhaCP")

// Policy lists writable host paths. A pattern ending in "/**" permits the
// whole subtree (symlinks inside it are refused); other patterns are
// path.Match globs matched against the full cleaned path.
type Policy struct {
	Patterns []string
}

// Allows reports whether hostPath may be written.
func (p Policy) Allows(hostPath string) bool {
	_, ok := p.match(hostPath)
	return ok
}

// match returns the subtree base when the path matched a "/**" pattern.
func (p Policy) match(hostPath string) (string, bool) {
	if !filepath.IsAbs(hostPath) {
		return "", false
	}
	for _, part := range strings.Split(hostPath, "/") {
		if part == ".." {
			return "", false
		}
	}
	clean := filepath.Clean(hostPath)
	for _, pat := range p.Patterns {
		if base, ok := strings.CutSuffix(pat, "/**"); ok {
			if clean == base || strings.HasPrefix(clean, base+"/") {
				return base, true
			}
			continue
		}
		if ok, _ := path.Match(pat, clean); ok {
			return "", true
		}
	}
	return "", false
}

// Writer writes inside a filesystem.Root subject to a Policy.
type Writer struct {
	Root   filesystem.Root
	Policy Policy
	// Owners maps requested owners before they are applied; nil applies
	// them as given. Tests running without root use SelfOwners.
	Owners func(Owner) Owner
}

// SelfOwners returns nil for root, otherwise a mapping of every owner to
// the current user (an unprivileged process cannot chown to others).
func SelfOwners() func(Owner) Owner {
	if os.Geteuid() == 0 {
		return nil
	}
	uid, gid := os.Getuid(), os.Getgid()
	return func(Owner) Owner { return Owner{uid, gid} }
}

func (w Writer) owner(o Owner) Owner {
	if o == Keep || w.Owners == nil {
		return o
	}
	return w.Owners(o)
}

// Owner is a uid/gid pair; -1 leaves the value unchanged.
type Owner struct{ UID, GID int }

// Keep leaves ownership unchanged.
var Keep = Owner{-1, -1}

// Root owner.
var RootOwner = Owner{0, 0}

func (w Writer) real(hostPath string) (string, error) {
	base, ok := w.Policy.match(hostPath)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNotPermitted, hostPath)
	}
	real, err := w.Root.Path(filepath.Clean(hostPath))
	if err != nil {
		return "", err
	}
	if base != "" {
		// Inside user-writable subtrees no component below the base may be
		// a symlink, otherwise a site user could redirect our writes.
		rb, _ := w.Root.Path(base)
		rel, _ := filepath.Rel(rb, filepath.Dir(real))
		cur := rb
		if rel != "." {
			for _, part := range strings.Split(rel, string(os.PathSeparator)) {
				cur = filepath.Join(cur, part)
				if info, err := os.Lstat(cur); err == nil && info.Mode()&os.ModeSymlink != 0 {
					return "", fmt.Errorf("%w: %s contains a symbolic link", ErrNotPermitted, hostPath)
				}
			}
		}
		if info, err := os.Lstat(real); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %s is a symbolic link", ErrNotPermitted, hostPath)
		}
	}
	return real, nil
}

// State is the previous state of a path, used for rollback.
type State struct {
	Exists bool
	IsLink bool
	IsDir  bool
	Target string
	Data   []byte
	Mode   fs.FileMode
	UID    int
	GID    int
}

// Snapshot captures the current state of hostPath (files up to 8 MiB).
func (w Writer) Snapshot(hostPath string) (State, error) {
	real, err := w.real(hostPath)
	if err != nil {
		return State{}, err
	}
	info, err := os.Lstat(real)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	st := State{Exists: true, Mode: info.Mode() & (fs.ModePerm | fs.ModeSetgid | fs.ModeSetuid | fs.ModeSticky), UID: -1, GID: -1}
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		st.UID, st.GID = int(sys.Uid), int(sys.Gid)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		st.IsLink = true
		st.Target, err = os.Readlink(real)
	case info.IsDir():
		st.IsDir = true
	default:
		if info.Size() > 8<<20 {
			return st, fmt.Errorf("%s is too large to back up", hostPath)
		}
		st.Data, err = os.ReadFile(real)
	}
	return st, err
}

// WriteFile atomically replaces hostPath. It returns false when the file
// already had exactly this content, mode and owner (idempotency).
func (w Writer) WriteFile(hostPath string, data []byte, mode fs.FileMode, owner Owner) (bool, error) {
	real, err := w.real(hostPath)
	if err != nil {
		return false, err
	}
	if info, err := os.Lstat(real); err == nil && info.Mode().IsRegular() && info.Mode().Perm() == mode {
		if cur, err := os.ReadFile(real); err == nil && bytes.Equal(cur, data) && ownerMatches(info, w.owner(owner)) {
			return false, nil
		}
	}
	dir := filepath.Dir(real)
	tmp, err := os.CreateTemp(dir, ".whacp-tmp-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return false, err
	}
	if owner = w.owner(owner); owner != Keep {
		if err := tmp.Chown(owner.UID, owner.GID); err != nil {
			tmp.Close()
			return false, err
		}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmpName, real); err != nil {
		return false, err
	}
	syncDir(dir)
	return true, nil
}

// Symlink points link at target (replacing any previous link atomically).
func (w Writer) Symlink(target, link string) (bool, error) {
	real, err := w.real(link)
	if err != nil {
		return false, err
	}
	if cur, err := os.Readlink(real); err == nil && cur == target {
		return false, nil
	}
	if info, err := os.Lstat(real); err == nil && info.Mode()&os.ModeSymlink == 0 {
		return false, fmt.Errorf("%s exists and is not a symbolic link", link)
	}
	tmp := filepath.Join(filepath.Dir(real), ".whacp-link-"+filepath.Base(real))
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, real); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, nil
}

// Remove deletes a file or symlink (never a directory). Missing is fine.
func (w Writer) Remove(hostPath string) (bool, error) {
	real, err := w.real(hostPath)
	if err != nil {
		return false, err
	}
	info, err := os.Lstat(real)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, fmt.Errorf("%s is a directory", hostPath)
	}
	return true, os.Remove(real)
}

// MkdirAll creates hostPath and missing parents with mode/owner. Existing
// directories get mode and owner applied too.
func (w Writer) MkdirAll(hostPath string, mode fs.FileMode, owner Owner) (bool, error) {
	real, err := w.real(hostPath)
	if err != nil {
		return false, err
	}
	created := false
	if info, err := os.Lstat(real); err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("%s exists and is not a directory", hostPath)
		}
	} else {
		if err := os.MkdirAll(real, mode); err != nil {
			return false, err
		}
		created = true
	}
	if err := os.Chmod(real, mode); err != nil {
		return created, err
	}
	if owner = w.owner(owner); owner != Keep {
		if err := os.Lchown(real, owner.UID, owner.GID); err != nil {
			return created, err
		}
	}
	return created, nil
}

// Rename moves oldPath to newPath (both must be permitted).
func (w Writer) Rename(oldPath, newPath string) error {
	o, err := w.real(oldPath)
	if err != nil {
		return err
	}
	n, err := w.real(newPath)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(n); err == nil {
		return fmt.Errorf("%s already exists", newPath)
	}
	return os.Rename(o, n)
}

// Restore puts a path back into a previously captured state.
func (w Writer) Restore(hostPath string, st State) error {
	switch {
	case !st.Exists:
		real, err := w.real(hostPath)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(real); err == nil && info.IsDir() {
			return os.Remove(real) // only removes if empty
		}
		_, err = w.Remove(hostPath)
		return err
	case st.IsLink:
		real, err := w.real(hostPath)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(real); err == nil && info.Mode()&os.ModeSymlink == 0 && !info.IsDir() {
			os.Remove(real)
		}
		_, err = w.Symlink(st.Target, hostPath)
		return err
	case st.IsDir:
		_, err := w.MkdirAll(hostPath, st.Mode, Owner{st.UID, st.GID})
		return err
	default:
		real, err := w.real(hostPath)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(real); err == nil && info.Mode()&os.ModeSymlink != 0 {
			os.Remove(real)
		}
		_, err = w.WriteFile(hostPath, st.Data, st.Mode, Owner{st.UID, st.GID})
		return err
	}
}

func ownerMatches(info fs.FileInfo, o Owner) bool {
	if o == Keep {
		return true
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return (o.UID < 0 || int(sys.Uid) == o.UID) && (o.GID < 0 || int(sys.Gid) == o.GID)
}

func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}
