package sites

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// ConfigMode is the mode of WhaCP's Nginx sites and PHP-FPM pools: read by
// the daemons' root masters, not by website accounts (a pool or site file
// shows another website's paths and settings).
const ConfigMode = 0o640

// TightenConfigModes removes the "other" permissions from WhaCP's existing
// site and pool files (written 0644 before 1.0.1). Only regular root-owned
// files are changed, through a descriptor that does not follow links.
func TightenConfigModes(e *host.Env) int {
	patterns := []string{filepath.Join(e.M.NginxAvailable, "whacp-*.conf"), filepath.Join(FPMRoot, "*", "php-fpm.conf"), filepath.Join(FPMRoot, "*", "pool.d", "whacp-*.conf")}
	patterns = append(patterns, filepath.Join(e.M.PoolDir("*"), "whacp-*.conf"))
	n := 0
	for _, p := range patterns {
		files, _ := e.Root.Glob(p)
		for _, f := range files {
			real, err := e.Root.Path(f)
			if err != nil {
				continue
			}
			fd, err := os.OpenFile(real, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				continue
			}
			var st syscall.Stat_t
			if syscall.Fstat(int(fd.Fd()), &st) == nil && st.Mode&syscall.S_IFMT == syscall.S_IFREG && st.Uid == 0 && st.Mode&0o007 != 0 {
				if fd.Chmod(os.FileMode(st.Mode&0o770)&ConfigMode|0o600) == nil {
					n++
				}
			}
			fd.Close()
		}
	}
	return n
}
