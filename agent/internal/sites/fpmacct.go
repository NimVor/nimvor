package sites

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/changeset"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// PHP-FPM of resource-limited accounts: each system user of such an
// account gets its own FPM master per PHP version, a systemd service in the
// account's slice, so every PHP process of its websites (and whatever they
// start) counts against the account's limits. The master runs as root like
// the distribution's (it switches to the pool user); its pools, sockets and
// php.ini are the same as with the shared master.

// FPMRoot holds the per-account masters' configuration.
const FPMRoot = "/etc/whacp-fpm"

// AccountFPM identifies one per-account master.
type AccountFPM struct {
	User    string
	Version string // e.g. 8.3
}

// Name is "<user>-<version>".
func (a AccountFPM) Name() string { return a.User + "-" + a.Version }

// Dir is the master's configuration folder.
func (a AccountFPM) Dir() string { return filepath.Join(FPMRoot, a.Name()) }

// PoolDir holds its pools.
func (a AccountFPM) PoolDir() string { return filepath.Join(a.Dir(), "pool.d") }

// Conf is its php-fpm.conf.
func (a AccountFPM) Conf() string { return filepath.Join(a.Dir(), "php-fpm.conf") }

// Unit is its service.
func (a AccountFPM) Unit() string {
	return "whacp-fpm-" + a.User + "-" + strings.ReplaceAll(a.Version, ".", "") + ".service"
}

var accountPoolRe = regexp.MustCompile(`^` + regexp.QuoteMeta(FPMRoot) + `/([a-z][a-z0-9_-]{1,30})-([5-9]\.[0-9]{1,2})/pool\.d/`)

// accountOfPool returns the master a pool file belongs to (ok=false for the
// distribution's pool folders).
func accountOfPool(p string) (AccountFPM, bool) {
	m := accountPoolRe.FindStringSubmatch(p)
	if m == nil {
		return AccountFPM{}, false
	}
	return AccountFPM{User: m[1], Version: m[2]}, true
}

// fpmBinary is the FPM program of version v (as execx resolves it).
func fpmBinary(e *host.Env, v string) string {
	for _, p := range []string{"/usr/sbin/php-fpm" + v, "/opt/remi/php" + strings.ReplaceAll(v, ".", "") + "/root/usr/sbin/php-fpm"} {
		if e.Root.Exists(p) {
			return p
		}
	}
	return "/usr/sbin/php-fpm" + v
}

// RenderAccountFPMConf is the master's configuration.
func RenderAccountFPMConf(a AccountFPM) string {
	return fmt.Sprintf("; %s\n; PHP %s FastCGI master of %s (an account with resource limits).\n\n[global]\nerror_log = syslog\nsyslog.ident = whacp-fpm-%s\ndaemonize = no\ninclude = %s/*.conf\n", header, a.Version, a.User, a.Name(), a.PoolDir())
}

// RenderAccountFPMUnit is the master's service, in the account's slice.
func RenderAccountFPMUnit(a AccountFPM, bin, slice, socketDir string) string {
	return fmt.Sprintf(`# %s
[Unit]
Description=WhaCP PHP %s FastCGI for %s (resource-limited account)
After=network.target

[Service]
Type=simple
Slice=%s
ExecStartPre=+/bin/mkdir -p %s
ExecStart=%s --nodaemonize --fpm-config %s
ExecReload=/bin/kill -USR2 $MAINPID
Restart=on-failure
RestartSec=5s
PrivateTmp=true
%s
[Install]
WantedBy=multi-user.target
`, header, a.Version, a.User, slice, socketDir, bin, a.Conf(), host.FPMSandbox())
}

// writeAccountFPM adds a master's configuration and unit to set.
func (m *Manager) writeAccountFPM(set *changeset.Set, a AccountFPM, slice string) {
	e := m.Env
	set.Mkdir(FPMRoot, 0o755, agentfs.RootOwner)
	set.Mkdir(a.Dir(), 0o755, agentfs.RootOwner)
	set.Mkdir(a.PoolDir(), 0o755, agentfs.RootOwner)
	set.Write(a.Conf(), []byte(RenderAccountFPMConf(a)), ConfigMode, agentfs.RootOwner)
	set.Write(filepath.Join(e.M.UnitDir, a.Unit()), []byte(RenderAccountFPMUnit(a, fpmBinary(e, a.Version), slice, e.M.PHPSocketDir)), 0o644, agentfs.RootOwner)
}

// accountPools lists the pool files of a master.
func (m *Manager) accountPools(a AccountFPM) []string {
	files, _ := m.Env.Root.Glob(filepath.Join(a.PoolDir(), "*.conf"))
	return files
}

// testAccountFPM checks a master's configuration with the FPM program.
func (m *Manager) testAccountFPM(ctx context.Context, a AccountFPM) error {
	e := m.Env
	id := fmt.Sprintf(e.M.PHPFPMBinary, a.Version)
	if !e.Runner.Available(id) {
		return &changeset.ValidationError{Step: "php-fpm" + a.Version + " -t", Err: fmt.Errorf("PHP %s FPM is not installed", a.Version)}
	}
	if len(m.accountPools(a)) == 0 {
		return nil // stopped below
	}
	out, err := e.Runner.Run(ctx, id, "-t", "-y", a.Conf())
	if err != nil {
		return &changeset.ValidationError{Step: "php-fpm" + a.Version + " -t -y " + a.Conf(), Output: host.Output(out), Err: err}
	}
	return nil
}

// reloadAccountFPM starts or reloads a master, or stops and removes it when
// it has no pools left.
func (m *Manager) reloadAccountFPM(ctx context.Context, a AccountFPM) error {
	e := m.Env
	if len(m.accountPools(a)) == 0 {
		e.Systemctl(ctx, "disable", a.Unit())
		e.Systemctl(ctx, "stop", a.Unit())
		for _, f := range []string{filepath.Join(e.M.UnitDir, a.Unit()), a.Conf()} {
			e.Writer.Remove(f)
		}
		if real, err := e.Root.Path(a.PoolDir()); err == nil {
			removeEmptyDir(real)
		}
		if real, err := e.Root.Path(a.Dir()); err == nil {
			removeEmptyDir(real)
		}
		return e.DaemonReload(ctx)
	}
	if err := e.DaemonReload(ctx); err != nil {
		return err
	}
	if err := e.Systemctl(ctx, "enable", a.Unit()); err != nil {
		return err
	}
	if e.UnitActive(ctx, a.Unit()) {
		return e.Systemctl(ctx, "reload", a.Unit())
	}
	return e.Systemctl(ctx, "start", a.Unit())
}

// AccountFPMs lists the existing per-account masters.
func (m *Manager) AccountFPMs() []AccountFPM {
	dirs, _ := m.Env.Root.Glob(filepath.Join(FPMRoot, "*", "pool.d"))
	var out []AccountFPM
	for _, d := range dirs {
		if a, ok := accountOfPool(d + "/"); ok {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// removeEmptyDir removes a folder that is empty (errors are ignored: a
// folder with files stays).
func removeEmptyDir(p string) { _ = os.Remove(p) }
