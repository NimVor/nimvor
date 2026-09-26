package backup

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"syscall"
)

// Reserve is the free space backups and restores always leave on a
// filesystem: the same disks hold the databases, the websites and the logs,
// and a full disk breaks all of them.
const Reserve int64 = 512 << 20

// spaceCheckEvery is how much a guarded writer writes between checks.
const spaceCheckEvery = 16 << 20

// statfs is replaced in tests.
var statfs = syscall.Statfs

// freeSpace returns the bytes available to root on the filesystem of path.
func freeSpace(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// ErrNoSpace reports that an operation stopped (or did not start) to keep
// Reserve free.
var ErrNoSpace = errors.New("not enough free disk space")

func mib(n int64) string { return fmt.Sprintf("%d MiB", (n+(1<<20)-1)>>20) }

// needSpace checks that the filesystem of path has need bytes free on top
// of Reserve. A filesystem that cannot be queried is not checked.
func needSpace(path string, need int64, what string) error {
	free, err := freeSpace(path)
	if err != nil {
		return nil
	}
	if need < 0 {
		need = 0
	}
	if free < need+Reserve {
		return fmt.Errorf("%w for %s on the disk of %s: about %s needed and %s kept free, %s available; nothing was changed",
			ErrNoSpace, what, path, mib(need), mib(Reserve), mib(free))
	}
	return nil
}

// spaceGuard writes to w and stops with ErrNoSpace before the filesystem of
// dir falls below Reserve, so a large backup or download cannot fill the
// disk. It checks before the first write and then every spaceCheckEvery.
type spaceGuard struct {
	w     io.Writer
	dir   string
	since int64
}

func newSpaceGuard(w io.Writer, dir string) *spaceGuard {
	return &spaceGuard{w: w, dir: dir, since: spaceCheckEvery}
}

func (g *spaceGuard) Write(p []byte) (int, error) {
	if g.since >= spaceCheckEvery {
		if free, err := freeSpace(g.dir); err == nil && free < Reserve+int64(len(p)) {
			return 0, fmt.Errorf("%w on the disk of %s: stopped with %s left to keep %s free; the partial file was removed", ErrNoSpace, g.dir, mib(free), mib(Reserve))
		}
		g.since = 0
	}
	n, err := g.w.Write(p)
	g.since += int64(n)
	return n, err
}

// treeSize is the apparent size of the regular files under root (symbolic
// links are not followed).
func treeSize(root string) int64 {
	var total int64
	filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// countingWriter counts what passes through it.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
