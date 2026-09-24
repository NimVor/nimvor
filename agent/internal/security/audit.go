package security

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Check is one audit finding.
type Check struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`   // pass, warn, fail, info
	Severity string `json:"severity"` // low, medium, high
	Detail   string `json:"detail"`
	Remedy   string `json:"remedy,omitempty"`
}

var securityUpgrade = regexp.MustCompile(`(?m)^Inst \S+ .*-security`)

// Audit inspects the server and returns findings (read-only).
func Audit(ctx context.Context, env *host.Env) []Check {
	var out []Check
	add := func(c Check) { out = append(out, c) }

	fw, err := Firewall(ctx, env)
	switch {
	case err != nil || !fw.Installed:
		add(Check{ID: "firewall", Title: "Firewall", Status: "warn", Severity: "high", Detail: "UFW is not installed or not readable.", Remedy: "Install ufw and enable it under Security → Firewall."})
	case !fw.Active:
		add(Check{ID: "firewall", Title: "Firewall", Status: "fail", Severity: "high", Detail: "UFW is installed but inactive: every listening service is reachable from the internet.", Remedy: "Allow SSH, HTTP and HTTPS, then enable the firewall."})
	default:
		add(Check{ID: "firewall", Title: "Firewall", Status: "pass", Severity: "high", Detail: fmt.Sprintf("UFW is active with %d rules (default incoming: %s).", len(fw.Rules), fw.Incoming)})
	}

	if ssh, err := SSHEffective(ctx, env); err != nil {
		add(Check{ID: "ssh", Title: "SSH server", Status: "info", Severity: "low", Detail: "The SSH configuration could not be read."})
	} else {
		switch ssh["permitrootlogin"] {
		case "yes":
			add(Check{ID: "ssh_root", Title: "SSH root login", Status: "fail", Severity: "high", Detail: "root can log in with a password.", Remedy: "Set root login to “keys only” or “no” under Security → SSH."})
		default:
			add(Check{ID: "ssh_root", Title: "SSH root login", Status: "pass", Severity: "high", Detail: "root login: " + ssh["permitrootlogin"] + "."})
		}
		if ssh["passwordauthentication"] == "yes" {
			add(Check{ID: "ssh_password", Title: "SSH password logins", Status: "warn", Severity: "medium", Detail: "Password logins are allowed, which invites brute-force attacks.", Remedy: "Add an SSH key for an administrator, then turn off password logins."})
		} else {
			add(Check{ID: "ssh_password", Title: "SSH password logins", Status: "pass", Severity: "medium", Detail: "Only key-based logins are accepted."})
		}
	}

	f2b := Fail2ban(ctx, env)
	switch {
	case !f2b.Installed:
		add(Check{ID: "fail2ban", Title: "Brute-force protection", Status: "warn", Severity: "medium", Detail: "fail2ban is not installed.", Remedy: "Install fail2ban and enable the sshd jail."})
	case !f2b.Running:
		add(Check{ID: "fail2ban", Title: "Brute-force protection", Status: "warn", Severity: "medium", Detail: "fail2ban is installed but not running.", Remedy: "Start fail2ban under Services."})
	default:
		names := []string{}
		for _, j := range f2b.Jails {
			names = append(names, j.Name)
		}
		add(Check{ID: "fail2ban", Title: "Brute-force protection", Status: "pass", Severity: "medium", Detail: "fail2ban jails: " + strings.Join(names, ", ") + "."})
	}

	if env.Runner.Available("apt-get") {
		if o, err := env.Runner.Run(ctx, "apt-get", "-s", "-o", "Debug::NoLocking=1", "upgrade"); err == nil {
			n := len(securityUpgrade.FindAll(o, -1))
			if n > 0 {
				add(Check{ID: "updates", Title: "Security updates", Status: "fail", Severity: "high", Detail: fmt.Sprintf("%d security update(s) are waiting.", n), Remedy: "Install updates under Server → Packages."})
			} else {
				add(Check{ID: "updates", Title: "Security updates", Status: "pass", Severity: "high", Detail: "No pending security updates (according to the last package index refresh)."})
			}
		}
	}
	auto := false
	if b, err := env.Root.ReadFile("/etc/apt/apt.conf.d/20auto-upgrades"); err == nil {
		auto = regexp.MustCompile(`Unattended-Upgrade\s+"1"`).Match(b)
	}
	if auto {
		add(Check{ID: "auto_updates", Title: "Automatic security updates", Status: "pass", Severity: "medium", Detail: "unattended-upgrades is enabled."})
	} else {
		add(Check{ID: "auto_updates", Title: "Automatic security updates", Status: "warn", Severity: "medium", Detail: "Security updates are not installed automatically.", Remedy: "Run dpkg-reconfigure -plow unattended-upgrades."})
	}
	if env.Root.Exists("/var/run/reboot-required") || env.Root.Exists("/run/reboot-required") {
		add(Check{ID: "reboot", Title: "Restart required", Status: "warn", Severity: "medium", Detail: "Updated system components are only used after a restart.", Remedy: "Restart the server at a quiet time under Server."})
	}
	if b, err := env.Root.ReadFile(env.M.RedisConfig); err == nil {
		conf := string(b)
		bindPublic := regexp.MustCompile(`(?m)^\s*bind\s+.*(0\.0\.0\.0|\*)`).MatchString(conf)
		noPass := !regexp.MustCompile(`(?m)^\s*requirepass\s+\S`).MatchString(conf)
		protectedOff := regexp.MustCompile(`(?m)^\s*protected-mode\s+no`).MatchString(conf)
		if bindPublic && noPass && protectedOff {
			add(Check{ID: "redis", Title: "Redis exposure", Status: "fail", Severity: "high", Detail: "Redis listens on all addresses without a password.", Remedy: "Bind Redis to 127.0.0.1 or set a password under Redis."})
		} else {
			add(Check{ID: "redis", Title: "Redis exposure", Status: "pass", Severity: "high", Detail: "Redis is not exposed without a password."})
		}
	}
	return out
}
