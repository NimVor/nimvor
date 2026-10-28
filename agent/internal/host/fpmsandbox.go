package host

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// The PHP-FPM services run every website's PHP code. Their master runs as
// root and switches each pool to the website's account; NimVor confines the
// whole service with a systemd drop-in (and the same settings in the
// services of resource-limited accounts' own masters):
//
//   - NoNewPrivileges: PHP code (and what it starts) cannot gain privileges
//     through setuid or file-capability binaries (su, sudo, pkexec, …).
//     PHP's mail goes through NimVor's sendmail, which needs none.
//   - The capabilities the master needs and no others: switching to the
//     pool accounts, socket ownership, rlimits and priorities of pools,
//     ptrace for the slow log. No CAP_SYS_ADMIN, CAP_NET_ADMIN,
//     CAP_SYS_MODULE, CAP_MKNOD, raw I/O.
//   - The system read-only (/usr, /boot, /etc; ProtectSystem=full), no
//     devices but the pseudo devices, kernel tunables, modules, logs, the
//     clock and control groups protected, IP and local sockets only.
//
// /tmp stays as the distribution set it (private on RHEL, the host's on
// Debian): the commands NimVor runs as an account and cron jobs share it.
// RestrictSUIDSGID is not set, as for those commands (see
// execx.transientHardening); NoNewPrivileges makes setuid files grant
// nothing. MemoryDenyWriteExecute is not set: PCRE's and OPcache's JIT
// need writable executable memory.

// FPMSandboxFile is the drop-in's file name.
const FPMSandboxFile = "nimvor-sandbox.conf"

// fpmCapabilities are those the FPM master keeps.
var fpmCapabilities = []string{
	"CAP_CHOWN", "CAP_DAC_OVERRIDE", "CAP_DAC_READ_SEARCH", "CAP_FOWNER", "CAP_FSETID",
	"CAP_KILL", "CAP_SETGID", "CAP_SETUID", "CAP_NET_BIND_SERVICE",
	"CAP_SYS_RESOURCE", "CAP_SYS_NICE", "CAP_SYS_PTRACE", "CAP_SYS_CHROOT",
}

// FPMSandbox is the [Service] settings of PHP-FPM services.
func FPMSandbox() string {
	return "NoNewPrivileges=yes\n" +
		"CapabilityBoundingSet=" + strings.Join(fpmCapabilities, " ") + "\n" +
		"ProtectSystem=full\n" +
		// Debian's php-fpm-socket-helper registers the version's socket
		// with update-alternatives (/etc/alternatives).
		"ReadWritePaths=-/etc/alternatives\n" +
		"PrivateDevices=yes\n" +
		"ProtectKernelTunables=yes\n" +
		"ProtectKernelModules=yes\n" +
		"ProtectKernelLogs=yes\n" +
		"ProtectControlGroups=yes\n" +
		"ProtectClock=yes\n" +
		"ProtectHostname=yes\n" +
		"RestrictRealtime=yes\n" +
		"LockPersonality=yes\n" +
		"RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK\n"
}

// FPMSandboxPath is the drop-in of PHP version v's FPM service.
func (e *Env) FPMSandboxPath(v string) string {
	return filepath.Join(e.M.UnitDir, e.M.FPMUnit(v)+".d", FPMSandboxFile)
}

// EnsureFPMSandbox writes the drop-in of version v's FPM service when it is
// missing or differs, and reloads systemd's units; true: the service must
// be restarted for it to take effect (a reload keeps the old process).
func (e *Env) EnsureFPMSandbox(ctx context.Context, v string) (bool, error) {
	path := e.FPMSandboxPath(v)
	want := "# Managed by NimVor: the sandbox of the PHP-FPM service (websites' PHP code).\n[Service]\n" + FPMSandbox()
	if b, err := e.Root.ReadFile(path); err == nil && string(b) == want {
		return false, nil
	}
	set := e.Changes("php-fpm-sandbox")
	set.Mkdir(filepath.Dir(path), 0o755, agentfs.RootOwner)
	set.Write(path, []byte(want), 0o644, agentfs.RootOwner)
	if _, err := set.Apply(); err != nil {
		return false, fmt.Errorf("writing %s: %w", path, err)
	}
	if err := e.DaemonReload(ctx); err != nil {
		return false, err
	}
	return true, nil
}

// SandboxFPMServices applies the sandbox to every installed PHP-FPM
// version's service (when the agent starts after an update), restarting
// those that run so it takes effect.
func (e *Env) SandboxFPMServices(ctx context.Context) {
	for _, v := range e.Discovery.PHP().Versions {
		if v.FPMBinary == "" || validate.PHPVersion("version", v.Version) != nil {
			continue
		}
		// What the version needs (e.g. SELinux labels) before it starts.
		relabelled := PrepareFPM != nil && PrepareFPM(ctx, e, v.Version)
		changed, err := e.EnsureFPMSandbox(ctx, v.Version)
		if err != nil {
			e.Log.Warn("php-fpm sandbox not applied", "version", v.Version, "error", err)
			continue
		}
		if unit := e.M.FPMUnit(v.Version); (changed || relabelled) && e.UnitActive(ctx, unit) {
			if err := e.FPMTest(ctx, v.Version); err != nil {
				e.Log.Warn("php-fpm configuration test failed; not restarted", "version", v.Version, "error", err)
				continue
			}
			if err := e.Systemctl(ctx, "restart", unit); err != nil {
				e.Log.Warn("php-fpm not restarted in its sandbox", "unit", unit, "error", err)
			}
		}
	}
}
