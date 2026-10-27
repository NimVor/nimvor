// Package selinux keeps SELinux (enforcing by default on AlmaLinux, Rocky
// Linux and RHEL) consistent with what WhaCP sets up: file labels for
// folders services must write (recorded with semanage so a relabel keeps
// them), booleans for what services may do, and port labels for ports
// moved from their defaults. On systems without SELinux, or with it
// disabled, every function does nothing.
package selinux

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// Enabled reports whether SELinux is enforcing or permissive (labels
// matter in both: permissive may become enforcing later).
func Enabled(ctx context.Context, env *host.Env) bool {
	if !env.Runner.Available("getenforce") {
		return false
	}
	out, err := env.Runner.Run(ctx, "getenforce")
	if err != nil {
		return false
	}
	mode := strings.TrimSpace(string(out))
	return mode == "Enforcing" || mode == "Permissive"
}

var (
	typeRe = regexp.MustCompile(`^[a-z0-9_]+_t$`)
	boolRe = regexp.MustCompile(`^[a-z0-9_]+$`)
	// Paths WhaCP labels are plain absolute paths.
	pathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)
)

// Label gives path and everything below it the type t, persistently, and
// applies it now.
func Label(ctx context.Context, env *host.Env, path, t string) error {
	if err := Rule(ctx, env, path, t); err != nil {
		return err
	}
	return Restore(ctx, env, path)
}

// rules caches recorded rules (semanage is slow).
var rules sync.Map

// Rule records that path and everything below it have type t (applied by
// restorecon, including after a full relabel).
func Rule(ctx context.Context, env *host.Env, path, t string) error {
	if !typeRe.MatchString(t) || !pathRe.MatchString(path) || strings.Contains(path, "..") {
		return fmt.Errorf("invalid SELinux label request %s %s", path, t)
	}
	if !Enabled(ctx, env) {
		return nil
	}
	spec := regexp.QuoteMeta(strings.TrimSuffix(path, "/")) + "(/.*)?"
	if v, ok := rules.Load(spec); ok && v == t {
		return nil
	}
	// -a fails when the rule exists; -m then updates it.
	if out, err := env.Runner.Run(ctx, "semanage", "fcontext", "-a", "-t", t, spec); err != nil {
		if out2, err2 := env.Runner.Run(ctx, "semanage", "fcontext", "-m", "-t", t, spec); err2 != nil {
			return fmt.Errorf("semanage fcontext %s: %s %s", path, host.Output(out), host.Output(out2))
		}
	}
	rules.Store(spec, t)
	return nil
}

// Restore reapplies the recorded labels below path (after creating files).
func Restore(ctx context.Context, env *host.Env, path string) error {
	if !pathRe.MatchString(path) || !Enabled(ctx, env) || !env.Root.Exists(path) {
		return nil
	}
	if out, err := env.Runner.Run(ctx, "restorecon", "-R", "--", path); err != nil {
		return fmt.Errorf("restorecon %s: %s", path, host.Output(out))
	}
	return nil
}

// Boolean turns an SELinux boolean on (persistently) when it is off.
func Boolean(ctx context.Context, env *host.Env, name string) error {
	if !boolRe.MatchString(name) || !Enabled(ctx, env) {
		return nil
	}
	if out, err := env.Runner.Run(ctx, "getsebool", name); err == nil && strings.HasSuffix(strings.TrimSpace(string(out)), "--> on") {
		return nil
	}
	if out, err := env.Runner.Run(ctx, "setsebool", "-P", name, "on"); err != nil {
		return fmt.Errorf("setsebool %s: %s", name, host.Output(out))
	}
	return nil
}

// Port lets services of type t use a TCP port (e.g. ssh_port_t 2222).
func Port(ctx context.Context, env *host.Env, t string, port int) error {
	if !typeRe.MatchString(t) || port < 1 || port > 65535 || !Enabled(ctx, env) {
		return nil
	}
	p := fmt.Sprint(port)
	if out, err := env.Runner.Run(ctx, "semanage", "port", "-a", "-t", t, "-p", "tcp", p); err != nil {
		if out2, err2 := env.Runner.Run(ctx, "semanage", "port", "-m", "-t", t, "-p", "tcp", p); err2 != nil {
			return fmt.Errorf("semanage port %s: %s %s", p, host.Output(out), host.Output(out2))
		}
	}
	return nil
}
