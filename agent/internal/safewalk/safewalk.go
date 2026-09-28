// Package safewalk walks a directory tree that its users can write to, for
// root to change ownership and modes safely: every entry is opened relative
// to its parent's descriptor with O_NOFOLLOW, and changes are made on the
// open descriptor, so symbolic links (placed beforehand or swapped in
// during the walk) are never followed. Other filesystems mounted inside
// the tree are not entered.
package safewalk

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// Entry is an open file or directory.
type Entry struct {
	FD    int
	Rel   string // path relative to the walk's start
	Dir   bool
	Stat  syscall.Stat_t
	Depth int
}

// Visit is called for every directory (before its entries) and regular
// file. It may Fchown or Fchmod e.FD.
type Visit func(e *Entry) error

// Result lists what was not visited.
type Result struct {
	Skipped []string `json:"skipped,omitempty"` // links, special files, other filesystems (at most 50)
	Count   int      `json:"skipped_count"`
}

func (r *Result) skip(rel string) {
	r.Count++
	if len(r.Skipped) < 50 {
		r.Skipped = append(r.Skipped, rel)
	}
}

// Walk visits the tree name (relative to the directory descriptor parent).
// A missing start returns an error satisfying errors.Is(err, os.ErrNotExist).
func Walk(parent int, name string, visit Visit) (Result, error) {
	var res Result
	fd, err := syscall.Openat(parent, name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return res, fmt.Errorf("%s: %w", name, os.ErrNotExist)
		}
		return res, fmt.Errorf("%s: %w", name, err)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return res, err
	}
	err = walkDir(fd, name, st, st.Dev, 0, visit, &res)
	return res, err
}

// walkDir takes ownership of fd.
func walkDir(fd int, rel string, st syscall.Stat_t, dev uint64, depth int, visit Visit, res *Result) error {
	dir := os.NewFile(uintptr(fd), rel)
	defer dir.Close()
	if depth > 128 {
		return fmt.Errorf("%s: nested too deep", rel)
	}
	if err := visit(&Entry{FD: fd, Rel: rel, Dir: true, Stat: st, Depth: depth}); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	for {
		entries, rerr := dir.ReadDir(512)
		for _, e := range entries {
			child := rel + "/" + e.Name()
			if !e.IsDir() && !e.Type().IsRegular() {
				res.skip(child)
				continue
			}
			flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
			if e.IsDir() {
				flags |= syscall.O_DIRECTORY
			} else {
				flags |= syscall.O_NONBLOCK // never wait on a FIFO swapped in
			}
			cfd, err := syscall.Openat(fd, e.Name(), flags, 0)
			if err != nil {
				res.skip(child) // gone, swapped for a link, or unreadable
				continue
			}
			var cst syscall.Stat_t
			if err := syscall.Fstat(cfd, &cst); err != nil {
				syscall.Close(cfd)
				res.skip(child)
				continue
			}
			switch {
			case uint64(cst.Dev) != dev:
				syscall.Close(cfd)
				res.skip(child)
			case cst.Mode&syscall.S_IFMT == syscall.S_IFDIR:
				if err := walkDir(cfd, child, cst, dev, depth+1, visit, res); err != nil {
					return err
				}
			case cst.Mode&syscall.S_IFMT == syscall.S_IFREG:
				err := visit(&Entry{FD: cfd, Rel: child, Stat: cst, Depth: depth + 1})
				syscall.Close(cfd)
				if err != nil {
					return fmt.Errorf("%s: %w", child, err)
				}
			default:
				syscall.Close(cfd)
				res.skip(child)
			}
		}
		if rerr != nil {
			return nil // io.EOF (or a read error: what was read is done)
		}
	}
}
