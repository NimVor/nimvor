package sites

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// AccessLogReadable lets the website's account read its access log (for
// its statistics, which run as the account): group gid, mode 0640. Nginx
// creates the file as root when the agent tests the configuration, and the
// agent's umask (0027) leaves it unreadable to the account.
//
// Only logs/access.log is touched, opened inside the website folder (no
// symlink leads out of it), and only a single-link regular file owned by
// root or the web server's account: never a file the account planted.
// Owner and write access are unchanged. A missing file is left to Nginx.
func AccessLogReadable(e *host.Env, siteRoot string, gid int) error {
	real, err := e.Root.Path(siteRoot)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(real); err != nil || !info.IsDir() {
		return nil
	}
	root, err := os.OpenRoot(real)
	if err != nil {
		return err
	}
	defer root.Close()
	name := filepath.Join("logs", "access.log")
	if info, err := root.Lstat(name); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return nil
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || st.Nlink != 1 {
		return nil
	}
	web, err := e.WebUser()
	if st.Uid != 0 && (err != nil || int(st.Uid) != web.UID) {
		return nil
	}
	if int(st.Gid) != gid {
		if err := f.Chown(-1, gid); err != nil {
			return err
		}
	}
	if mode := info.Mode().Perm(); mode&0o040 == 0 || mode&0o007 != 0 {
		// Group read added; nothing for others.
		if err := f.Chmod(mode&0o770 | 0o040); err != nil {
			return err
		}
	}
	return nil
}
