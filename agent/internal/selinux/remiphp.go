package selinux

import (
	"context"
	"regexp"
	"strings"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

// Remi's parallel PHP versions live in /opt/remi/phpNN/root, /etc/opt/remi/
// phpNN and /var/opt/remi/phpNN. Their packages' install scripts make those
// trees take the labels of /, /etc and /var (semanage fcontext -e), so the
// php-fpm binary is httpd_exec_t and PHP-FPM runs as the web server
// (httpd_t), which Nginx may connect to. When those scripts did not run (an
// install whose scriptlets failed, e.g. under an older NimVor agent), PHP-FPM
// runs as unconfined_service_t and SELinux refuses Nginx its socket
// ("connect() ... failed (13: Permission denied)", avc denied connectto
// tcontext=unconfined_service_t): every PHP site of that version answers
// 502. This repairs exactly what the scripts would have done.

func init() { host.PrepareFPM = remiPHP }

var remiTagRe = regexp.MustCompile(`^[0-9]{2}$`)

// fileLabel reads a file's SELinux label (tests replace it).
var fileLabel = func(path string) string {
	buf := make([]byte, 256)
	n, err := syscall.Getxattr(path, "security.selinux", buf)
	if err != nil || n <= 0 {
		return ""
	}
	return strings.TrimRight(string(buf[:n]), "\x00")
}

// remiPHP relabels a Remi PHP version whose php-fpm is not httpd_exec_t;
// true when it did (PHP-FPM must then be restarted to change domain).
func remiPHP(ctx context.Context, env *host.Env, v string) bool {
	if !env.M.PHPCompact {
		return false
	}
	tag := platform.PHPTag(true, v)
	if !remiTagRe.MatchString(tag) {
		return false
	}
	root := "/opt/remi/php" + tag + "/root"
	bin := root + "/usr/sbin/php-fpm"
	real, err := env.Root.Path(bin)
	if err != nil || !env.Root.Exists(bin) || !Enabled(ctx, env) {
		return false
	}
	if l := fileLabel(real); l == "" || strings.Contains(l, ":httpd_exec_t:") {
		return false
	}
	trees := [][2]string{{"/", root}, {"/etc", "/etc/opt/remi/php" + tag}, {"/var", "/var/opt/remi/php" + tag}}
	for _, t := range trees {
		if out, err := env.Runner.Run(ctx, "semanage", "fcontext", "-a", "-e", t[0], t[1]); err != nil && !strings.Contains(string(out), "already") {
			env.Log.Warn("SELinux: could not record the labels of Remi's PHP", "version", v, "path", t[1], "error", host.Output(out))
			return false
		}
	}
	for _, t := range trees {
		if env.Root.Exists(t[1]) {
			if err := Restore(ctx, env, t[1]); err != nil {
				env.Log.Warn("SELinux: relabelling Remi's PHP failed", "version", v, "error", err)
				return false
			}
		}
	}
	env.Log.Info("SELinux: relabelled Remi's PHP (its install scripts had not run); PHP-FPM restarts as the web server", "version", v)
	return true
}
