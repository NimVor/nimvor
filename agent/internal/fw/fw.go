// Package fw opens and closes ports for services WhaCP sets up (mail, DNS,
// FTP, SSH) in whichever firewall is active: UFW on Debian and Ubuntu,
// firewalld on AlmaLinux, Rocky Linux and RHEL. When neither is active it
// changes nothing and says which ports to allow. Rules for other purposes
// are managed in package security.
package fw

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/host"
)

// Backend names the active firewall ("ufw", "firewalld" or "").
func Backend(ctx context.Context, env *host.Env) string {
	if env.Runner.Available("ufw") {
		if out, err := env.Runner.Run(ctx, "ufw", "status"); err == nil && strings.Contains(string(out), "Status: active") {
			return "ufw"
		}
	}
	if env.Runner.Available("firewall-cmd") {
		if out, err := env.Runner.Run(ctx, "firewall-cmd", "--state"); err == nil && strings.TrimSpace(string(out)) == "running" {
			return "firewalld"
		}
	}
	return ""
}

// Port is a port or range with a protocol: "25/tcp", "30000:30100/tcp",
// "53/udp".
type Port string

var portRe = regexp.MustCompile(`^([0-9]{1,5})(:[0-9]{1,5})?/(tcp|udp)$`)

func (p Port) valid() bool { return portRe.MatchString(string(p)) }

// firewalld writes ranges with a dash.
func (p Port) firewalld() string { return strings.Replace(string(p), ":", "-", 1) }

// Open allows the ports from anywhere; purpose labels UFW rules. It
// returns a note for the administrator.
func Open(ctx context.Context, env *host.Env, purpose string, ports ...Port) string {
	list := describe(ports)
	for _, p := range ports {
		if !p.valid() {
			return "Invalid port " + string(p) + "."
		}
	}
	var failed []string
	switch Backend(ctx, env) {
	case "ufw":
		for _, p := range ports {
			if out, err := env.Runner.Run(ctx, "ufw", "allow", string(p), "comment", "whacp: "+purpose); err != nil {
				failed = append(failed, string(p)+": "+host.Output(out))
			}
		}
	case "firewalld":
		for _, p := range ports {
			// Runtime (now) and permanent (after a restart).
			for _, perm := range []bool{false, true} {
				if out, err := env.Runner.Run(ctx, "firewall-cmd", firewalldArgs(perm, "--add-port="+p.firewalld())...); err != nil {
					failed = append(failed, string(p)+": "+host.Output(out))
				}
			}
		}
	default:
		return "If a firewall protects this server, allow " + list + "."
	}
	if len(failed) > 0 {
		return "Firewall: some ports could not be opened (" + strings.Join(failed, "; ") + "); allow " + list + " yourself."
	}
	return "Firewall: opened " + list + "."
}

// Close removes rules added by Open (errors are ignored: the rule may be
// gone already).
func Close(ctx context.Context, env *host.Env, ports ...Port) {
	switch Backend(ctx, env) {
	case "ufw":
		for _, p := range ports {
			if p.valid() {
				env.Runner.Run(ctx, "ufw", "delete", "allow", string(p))
			}
		}
	case "firewalld":
		for _, p := range ports {
			if p.valid() {
				for _, perm := range []bool{false, true} {
					env.Runner.Run(ctx, "firewall-cmd", firewalldArgs(perm, "--remove-port="+p.firewalld())...)
				}
			}
		}
	}
}

func firewalldArgs(permanent bool, arg string) []string {
	if permanent {
		return []string{"--permanent", arg}
	}
	return []string{arg}
}

func describe(ports []Port) string {
	s := make([]string, len(ports))
	for i, p := range ports {
		s[i] = string(p)
	}
	if len(s) > 1 {
		return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
	}
	return fmt.Sprint(strings.Join(s, ""))
}
