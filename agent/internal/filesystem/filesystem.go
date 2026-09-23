// Package filesystem provides path-safe, size-limited, read-only file access.
//
// All discovery code reads the host through a Root. A Root confines every
// path to a base directory (normally "/"), rejects traversal and bounds the
// amount of data read, so a malicious or huge file cannot exhaust memory.
package filesystem

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultMaxRead bounds a single file read (2 MiB).
const DefaultMaxRead = 2 << 20

// ErrOutsideRoot is returned when a path escapes the permitted root.
var ErrOutsideRoot = errors.New("path is outside the permitted root")

// ErrTooLarge is returned when a file exceeds the read limit.
var ErrTooLarge = errors.New("file exceeds the maximum readable size")

// Root is a read-only view of the filesystem anchored at Base.
type Root struct {
	Base    string
	MaxRead int64
}

// NewRoot returns a Root for base (which must be absolute).
func NewRoot(base string) (Root, error) {
	if !filepath.IsAbs(base) {
		return Root{}, fmt.Errorf("root %q must be absolute", base)
	}
	return Root{Base: filepath.Clean(base), MaxRead: DefaultMaxRead}, nil
}

// Host is the Root for the real filesystem.
func Host() Root { return Root{Base: "/", MaxRead: DefaultMaxRead} }

// Path maps a host-absolute path (e.g. "/etc/nginx/nginx.conf") to the real
// path under Base. It rejects relative paths and any ".." component.
func (r Root) Path(hostPath string) (string, error) {
	if !filepath.IsAbs(hostPath) {
		return "", fmt.Errorf("%w: %q is not absolute", ErrOutsideRoot, hostPath)
	}
	for _, part := range strings.Split(hostPath, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: %q contains '..'", ErrOutsideRoot, hostPath)
		}
	}
	if strings.ContainsRune(hostPath, 0) {
		return "", fmt.Errorf("%w: NUL byte in path", ErrOutsideRoot)
	}
	return filepath.Join(r.Base, filepath.Clean(hostPath)), nil
}

// HostPath converts a real path under Base back to its host-absolute form.
func (r Root) HostPath(real string) string {
	if r.Base == "/" {
		return real
	}
	rel, err := filepath.Rel(r.Base, real)
	if err != nil || strings.HasPrefix(rel, "..") {
		return real
	}
	return "/" + filepath.ToSlash(rel)
}

// Resolve returns the host path after following symlinks. Absolute link
// targets are interpreted relative to Base, so a Root can never be escaped.
func (r Root) Resolve(hostPath string) (string, error) {
	if _, err := r.Path(hostPath); err != nil {
		return "", err
	}
	if r.Base == "/" {
		return filepath.EvalSymlinks(hostPath)
	}
	return r.resolveInBase(filepath.Clean(hostPath))
}

// resolveInBase walks hostPath component by component, following links
// with Base acting as "/" (like a chroot).
func (r Root) resolveInBase(hostPath string) (string, error) {
	const maxLinks = 40
	links := 0
	resolved := "/"
	pending := strings.Split(strings.TrimPrefix(hostPath, "/"), "/")
	for len(pending) > 0 {
		part := pending[0]
		pending = pending[1:]
		switch part {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, part)
		real := filepath.Join(r.Base, next)
		info, err := os.Lstat(real)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			resolved = next
			continue
		}
		links++
		if links > maxLinks {
			return "", fmt.Errorf("too many symlinks resolving %s", hostPath)
		}
		target, err := os.Readlink(real)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(resolved, target)
		}
		pending = append(strings.Split(strings.TrimPrefix(filepath.Clean(target), "/"), "/"), pending...)
		resolved = "/"
	}
	return resolved, nil
}

// followed maps hostPath to a real path with symlinks resolved inside Base.
func (r Root) followed(hostPath string) (string, error) {
	if r.Base == "/" {
		return r.Path(hostPath)
	}
	resolved, err := r.Resolve(hostPath)
	if err != nil {
		return "", err
	}
	return r.Path(resolved)
}

// ReadFile reads a regular file, bounded by MaxRead.
func (r Root) ReadFile(hostPath string) ([]byte, error) {
	p, err := r.followed(hostPath)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", hostPath)
	}
	limit := r.MaxRead
	if limit <= 0 {
		limit = DefaultMaxRead
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, hostPath)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %s", ErrTooLarge, hostPath)
	}
	return data, nil
}

// Stat returns file information (following symlinks).
func (r Root) Stat(hostPath string) (fs.FileInfo, error) {
	p, err := r.followed(hostPath)
	if err != nil {
		return nil, err
	}
	return os.Stat(p)
}

// Lstat returns file information without following symlinks.
func (r Root) Lstat(hostPath string) (fs.FileInfo, error) {
	p, err := r.Path(hostPath)
	if err != nil {
		return nil, err
	}
	return os.Lstat(p)
}

// Exists reports whether hostPath exists.
func (r Root) Exists(hostPath string) bool {
	_, err := r.Stat(hostPath)
	return err == nil
}

// IsDir reports whether hostPath is a directory.
func (r Root) IsDir(hostPath string) bool {
	info, err := r.Stat(hostPath)
	return err == nil && info.IsDir()
}

// ReadDir lists a directory, sorted by name.
func (r Root) ReadDir(hostPath string) ([]fs.DirEntry, error) {
	p, err := r.followed(hostPath)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(p)
}

// Readlink returns the target of a symlink.
func (r Root) Readlink(hostPath string) (string, error) {
	p, err := r.Path(hostPath)
	if err != nil {
		return "", err
	}
	return os.Readlink(p)
}

// Glob expands a host-absolute pattern and returns host paths, sorted.
func (r Root) Glob(pattern string) ([]string, error) {
	p, err := r.Path(pattern)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(p)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, r.HostPath(m))
	}
	sort.Strings(out)
	return out, nil
}

// DirSize sums regular file sizes below hostPath, visiting at most
// maxEntries entries. The bool result reports whether the walk was complete.
func (r Root) DirSize(hostPath string, maxEntries int) (int64, bool, error) {
	p, err := r.Path(hostPath)
	if err != nil {
		return 0, false, err
	}
	var total int64
	visited := 0
	complete := true
	errStop := errors.New("stop")
	walkErr := filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		visited++
		if visited > maxEntries {
			complete = false
			return errStop
		}
		if d.Type().IsRegular() {
			if info, e := d.Info(); e == nil {
				total += info.Size()
			}
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errStop) {
		return total, false, walkErr
	}
	return total, complete, nil
}

// WithinPrefixes reports whether hostPath (cleaned) is equal to or below one
// of the allowed host prefixes.
func WithinPrefixes(hostPath string, prefixes []string) bool {
	clean := filepath.Clean(hostPath)
	for _, pre := range prefixes {
		pre = filepath.Clean(pre)
		if clean == pre || strings.HasPrefix(clean, pre+"/") {
			return true
		}
	}
	return false
}
