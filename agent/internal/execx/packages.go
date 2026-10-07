package execx

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Package transactions (installing, removing and upgrading packages, and
// refreshing the package index) do not run inside the agent's sandbox.
//
// Package managers depend on privilege transitions that the agent's own
// systemd unit refuses by design:
//
//   - APT downloads as its "_apt" user (seteuid/setresuid). On systemd 255
//     (Ubuntu 24.04) the agent starts without CAP_SETUID in its permitted
//     set, and with NoNewPrivileges=yes it can never regain it, so every
//     download failed: "seteuid 42 failed", "Method http has died".
//   - RPM runs each %pre/%post scriptlet in the rpm_script_t SELinux domain
//     through an explicit exec context. Under NoNewPrivileges=yes the
//     kernel refuses such a transition, so on SELinux-enforcing RHEL
//     systems every package with a shell scriptlet failed ("Error in
//     PREIN scriptlet in rpm package dovecot") while "sudo dnf install"
//     worked.
//   - Maintainer scripts create users, start services, run setgid helpers
//     and load kernel settings; the agent's limits (capabilities, memory,
//     tasks, private /tmp) are not theirs.
//
// So a package transaction runs as root in its own transient unit started
// by PID 1, with the environment of "sudo apt-get" or of RHEL's own
// dnf-automatic.service: no NoNewPrivileges, the default capabilities, its
// own cgroup. This is a deliberate, narrow exception: only the allowlisted
// package managers, only for transaction verbs (PackageTransaction), with
// arguments the callers have already validated (package names). The agent
// itself keeps NoNewPrivileges=yes and its capability set. A package
// manager runs whatever the signed packages' scripts contain, as root, in
// any case: confining it does not protect anything, it only breaks it.
//
// One transaction runs at a time (packageSlot), also across agent restarts
// (units of an earlier agent are waited for), and a transaction is never
// stopped half-way: if the agent stops waiting (timeout, shutdown) the unit
// finishes on its own.

// PackageUnitPrefix names the transient units of package transactions.
const PackageUnitPrefix = "whacp-pkg-"

// packageHardening: only what cannot affect a package transaction.
var packageHardening = []string{
	"KillMode=control-group",
	"TimeoutStopSec=30min",
}

// packageSlot serialises package transactions within the agent.
var packageSlot = make(chan struct{}, 1)

// PackageTransaction reports whether a command changes installed packages
// or the package index (and so runs as a package transaction).
func PackageTransaction(id string, args []string) bool {
	has := func(flags ...string) bool {
		for _, a := range args {
			for _, f := range flags {
				if a == f {
					return true
				}
			}
		}
		return false
	}
	switch id {
	case "apt-get":
		return !has("-s", "--simulate", "--just-print", "--dry-run", "--recon", "--no-act")
	case "dpkg":
		return has("-i", "--install", "--unpack", "--configure", "-r", "--remove", "-P", "--purge", "--triggers-only")
	case "dnf":
		if has("--assumeno", "--cacheonly", "-C") {
			return false
		}
		for _, a := range args {
			if strings.HasPrefix(a, "-") {
				continue
			}
			switch a {
			case "install", "remove", "erase", "upgrade", "update", "reinstall", "downgrade", "distro-sync",
				"autoremove", "swap", "group", "groupinstall", "module", "makecache", "download":
				return true
			}
			return false // the first word is the verb
		}
		return false
	case "rpm":
		if has("--test") {
			return false
		}
		for _, a := range args {
			if a == "--rebuilddb" || a == "--import" || a == "--install" || a == "--upgrade" || a == "--freshen" || a == "--erase" {
				return true
			}
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a[1:], "iUFe") && !strings.ContainsAny(a[1:], "qV") {
				return true
			}
		}
	}
	return false
}

// acquirePackageSlot waits for the agent's other package transaction,
// telling log why it waits.
func acquirePackageSlot(ctx context.Context, log io.Writer) (release func(), err error) {
	select {
	case packageSlot <- struct{}{}:
	default:
		if log != nil {
			fmt.Fprintln(log, "Waiting for another package operation of WhaCP to finish…")
		}
		select {
		case packageSlot <- struct{}{}:
		case <-ctx.Done():
			return nil, fmt.Errorf("stopped while waiting for another package operation: %w", ctx.Err())
		}
	}
	return func() { <-packageSlot }, nil
}

// activePackageUnits lists running package transaction units (e.g. one an
// earlier agent started before it was restarted).
func (r *SystemRunner) activePackageUnits(ctx context.Context) []string {
	sc, ok := r.path("systemctl")
	if !ok {
		return nil
	}
	cmd := exec.CommandContext(ctx, sc, "list-units", "--plain", "--no-legend", "--no-pager", "--state=activating,active,deactivating,reloading", PackageUnitPrefix+"*")
	cmd.Env = baseEnv
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var units []string
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) > 0 && strings.HasPrefix(f[0], PackageUnitPrefix) {
			units = append(units, f[0])
		}
	}
	return units
}

// waitPackageUnits waits, with backoff, until no package transaction unit
// is running.
func (r *SystemRunner) waitPackageUnits(ctx context.Context, log io.Writer) error {
	delay := 2 * time.Second
	told := ""
	for {
		units := r.activePackageUnits(ctx)
		if len(units) == 0 {
			return nil
		}
		if w := strings.Join(units, ", "); w != told && log != nil {
			fmt.Fprintf(log, "Waiting for a package transaction still running (%s) to finish…\n", w)
			told = w
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("stopped while waiting for package transaction %s: %w", strings.Join(units, ", "), ctx.Err())
		case <-time.After(delay):
		}
		if delay < 15*time.Second {
			delay *= 2
		}
	}
}
