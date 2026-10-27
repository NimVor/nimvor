package sysmgr

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// A component is installed when it works, not when the package manager
// is done: after installing, each component WhaCP can check is verified,
// and the services WhaCP runs as such (the web server, the database, the
// caches) are enabled and started (Debian starts them on installation,
// RHEL does not). Mail, FTP and DNS servers are only checked for a valid
// configuration: WhaCP starts them when they are set up.

type componentCheck struct {
	// unit returns the service to enable and start ("" none).
	unit func(e *host.Env) string
	// cmd checks the component; ok decides from its output (nil: exit 0).
	cmd []string
	ok  func(out string) bool
	// what the check shows, for the log.
	what string
}

var componentChecks = map[string]componentCheck{
	"nginx": {unit: func(e *host.Env) string { return e.M.NginxUnit }, cmd: []string{"nginx", "-t"}, what: "configuration valid, service running"},
	"redis-server": {unit: func(e *host.Env) string { return e.M.RedisUnit }, cmd: []string{"redis-cli", "ping"},
		// NOAUTH: it answers, with a password set.
		ok: func(out string) bool { return strings.Contains(out, "PONG") || strings.Contains(out, "NOAUTH") }, what: "answers PING"},
	"mariadb-server": {unit: func(e *host.Env) string { return e.M.MySQLUnit[0] }, cmd: []string{"mysqladmin", "ping"},
		ok: func(out string) bool { return strings.Contains(out, "alive") || strings.Contains(out, "Access denied") }, what: "answers ping"},
	"memcached":     {unit: func(e *host.Env) string { return e.M.MemcachedUnit }, what: "service running"},
	"postfix":       {cmd: []string{"postfix", "check"}, what: "configuration valid"},
	"dovecot-imapd": {cmd: []string{"doveconf", "-n"}, what: "configuration valid"},
	"dovecot-lmtpd": {cmd: []string{"doveconf", "-n"}, what: "configuration valid"},
	"bind9":         {cmd: []string{"named-checkconf"}, what: "configuration valid"},
}

// Service start failures, by what the unit's log says.
var startFailures = []struct{ match, category, hint string }{
	{"Address already in use", "PORT_CONFLICT", "another program already listens on its port"},
	{"avc:  denied", "SELINUX_DENIAL", "SELinux denied it (ausearch -m AVC -ts recent)"},
	{"SELinux is preventing", "SELINUX_DENIAL", "SELinux denied it (ausearch -m AVC -ts recent)"},
	{"Permission denied", "PERMISSION_ERROR", "a file or folder has the wrong owner or permissions"},
	{"getpwnam", "USER_GROUP_ERROR", "its system user or group is missing"},
	{"Unknown user", "USER_GROUP_ERROR", "its system user or group is missing"},
	{"No such file or directory", "MISSING_FILE", "a file or folder it needs is missing"},
	{"syntax error", "CONFIGURATION_ERROR", "its configuration is invalid"},
	{"Bad configuration", "CONFIGURATION_ERROR", "its configuration is invalid"},
	{"invalid", "CONFIGURATION_ERROR", "its configuration is invalid"},
}

// classifyStart names why a service did not start.
func classifyStart(journal string) (category, hint string) {
	for _, f := range startFailures {
		if strings.Contains(journal, f.match) {
			return f.category, f.hint
		}
	}
	return "SERVICE_START_FAILED", "see its log above"
}

// verifyComponents checks installed components; a failure names the
// component and why.
func verifyComponents(ctx context.Context, env *host.Env, log io.Writer, names []string) error {
	if env.Cfg.SimulateCommands {
		fmt.Fprintln(log, "Health checks skipped: commands are simulated (development mode).")
		return nil
	}
	var bad []string
	for _, n := range names {
		c, ok := componentChecks[n]
		if !ok {
			continue
		}
		fmt.Fprintf(log, "Checking %s…\n", n)
		if c.unit != nil {
			if unit := c.unit(env); unit != "" {
				if err := startService(ctx, env, log, unit); err != nil {
					bad = append(bad, fmt.Sprintf("%s: %v", n, err))
					continue
				}
			}
		}
		if len(c.cmd) > 0 && env.Runner.Available(c.cmd[0]) {
			var out []byte
			var err error
			// A service just started may need a moment before it answers.
			for i := 0; i < 5; i++ {
				out, err = env.Runner.Run(ctx, c.cmd[0], c.cmd[1:]...)
				if err == nil && (c.ok == nil || c.ok(string(out))) {
					break
				}
				if c.ok != nil && c.ok(string(out)) {
					err = nil
					break
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(time.Second):
				}
			}
			if err != nil || (c.ok != nil && !c.ok(string(out))) {
				bad = append(bad, fmt.Sprintf("%s: %s failed: %s", n, strings.Join(c.cmd, " "), host.Why(out, err)))
				fmt.Fprintf(log, "  ✗ %s: %s\n", strings.Join(c.cmd, " "), strings.TrimSpace(string(out)))
				continue
			}
		}
		fmt.Fprintf(log, "  ✓ %s: %s\n", n, c.what)
	}
	if len(bad) > 0 {
		return fmt.Errorf("installed, but not working: %s", strings.Join(bad, "; "))
	}
	return nil
}

// startService enables and starts a unit unless it already runs; when it
// does not start, its log is collected and the cause classified. A broken
// service is not restarted again and again.
func startService(ctx context.Context, env *host.Env, log io.Writer, unit string) error {
	if env.UnitActive(ctx, unit) {
		return nil
	}
	env.Systemctl(ctx, "enable", unit)
	err := env.Systemctl(ctx, "start", unit)
	if err == nil {
		for i := 0; i < 10 && !env.UnitActive(ctx, unit); i++ {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
		if env.UnitActive(ctx, unit) {
			fmt.Fprintf(log, "  started %s\n", unit)
			return nil
		}
	}
	journal := env.JournalTail(ctx, unit, 100)
	cat, hint := classifyStart(journal)
	fmt.Fprintf(log, "  ✗ %s did not start [%s]: %s\n", unit, cat, hint)
	if journal != "" {
		fmt.Fprintf(log, "  Its log:\n%s\n", indent(journal))
	}
	return fmt.Errorf("%s did not start [%s]: %s", unit, cat, hint)
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > 40 {
		lines = lines[len(lines)-40:]
	}
	return "    " + strings.Join(lines, "\n    ")
}
