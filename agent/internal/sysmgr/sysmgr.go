// Package sysmgr manages system packages and server settings: installed
// and upgradable packages, installing an allowlisted component, applying
// updates, host name, time zone and scheduled restarts.
package sysmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/phpmgr"
)

// Components are the packages WhaCP offers to install (PHP versions are
// handled by phpmgr). Anything else is installed by the administrator.
var Components = map[string]string{
	"nginx":               "Web server",
	"mariadb-server":      "MariaDB database server",
	"redis-server":        "Redis cache server",
	"memcached":           "Memcached cache server",
	"pure-ftpd":           "FTP server (FTPS accounts for websites)",
	"bind9":               "DNS server (BIND) for hosted zones",
	"postfix":             "Mail server (SMTP)",
	"dovecot-imapd":       "Mailbox server (IMAP)",
	"dovecot-lmtpd":       "Mail delivery to mailboxes (LMTP)",
	"opendkim":            "DKIM signing of outgoing mail",
	"dovecot-sieve":       "Mail filtering (Sieve)",
	"rspamd":              "Spam filter for email",
	"certbot":             "Let's Encrypt client",
	"ufw":                 "Firewall",
	"fail2ban":            "Brute-force protection",
	"git":                 "Git (deployments)",
	"composer":            "PHP dependency manager",
	"nodejs":              "Node.js runtime",
	"npm":                 "Node.js package manager",
	"python3-venv":        "Python virtual environments",
	"unattended-upgrades": "Automatic security updates",
	"zip":                 "Zip archives",
	"unzip":               "Unzip archives",
	"rsync":               "File synchronisation",
	"htop":                "Process viewer",
	"openssh-server":      "SSH server",
	"mariadb-client":      "MariaDB command-line client",
	"imagemagick":         "Image processing",
	"libnginx-mod-brotli": "Brotli compression for Nginx",
}

// Package is the state of one component.
type Package struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Installed   bool   `json:"installed"`
	Version     string `json:"version,omitempty"`
}

// Upgrade is one pending update.
type Upgrade struct {
	Name     string `json:"name"`
	Current  string `json:"current"`
	New      string `json:"new"`
	Security bool   `json:"security"`
}

var instRe = regexp.MustCompile(`(?m)^Inst (\S+) (?:\[([^\]]+)\] )?\((\S+) ([^)]*)\)`)

// ParseUpgrades parses `apt-get -s upgrade` output.
func ParseUpgrades(out string) []Upgrade {
	ups := []Upgrade{}
	for _, m := range instRe.FindAllStringSubmatch(out, -1) {
		ups = append(ups, Upgrade{Name: m[1], Current: m[2], New: m[3], Security: strings.Contains(m[4], "-security")})
	}
	sort.Slice(ups, func(i, j int) bool {
		if ups[i].Security != ups[j].Security {
			return ups[i].Security
		}
		return ups[i].Name < ups[j].Name
	})
	return ups
}

// Installed returns name → version for installed packages among names.
func Installed(ctx context.Context, env *host.Env, names []string) map[string]string {
	out := map[string]string{}
	if len(names) == 0 || !env.Runner.Available("dpkg-query") {
		return out
	}
	// dpkg-query exits 1 when some names are unknown but still prints the rest.
	b, _ := env.Runner.Run(ctx, "dpkg-query", append([]string{"-W", "-f=${Package}\t${db:Status-Abbrev}\t${Version}\n", "--"}, names...)...)
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, "\t")
		if len(f) == 3 && strings.HasPrefix(f[1], "ii") {
			out[f[0]] = f[2]
		}
	}
	return out
}

var (
	hostnameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
	tzRe       = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+){0,2}$`)
)

// Settings are the server's identity and clock.
type Settings struct {
	Hostname     string     `json:"hostname"`
	Timezone     string     `json:"timezone"`
	NTP          bool       `json:"ntp_enabled"`
	Synchronized bool       `json:"ntp_synchronized"`
	LocalTime    string     `json:"local_time"`
	Reboot       *time.Time `json:"scheduled_reboot,omitempty"`
	RebootNeeded bool       `json:"reboot_required"`
	RebootPkgs   []string   `json:"reboot_required_by,omitempty"`
}

func keyValues(b []byte) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok {
			m[k] = v
		}
	}
	return m
}

// Read returns the current settings.
func Read(ctx context.Context, env *host.Env) Settings {
	s := Settings{}
	for _, p := range []string{"/etc/hostname", "/proc/sys/kernel/hostname"} {
		if b, err := env.Root.ReadFile(p); err == nil && strings.TrimSpace(string(b)) != "" {
			s.Hostname = strings.TrimSpace(string(b))
			break
		}
	}
	if b, err := env.Runner.Run(ctx, "timedatectl", "show"); err == nil {
		kv := keyValues(b)
		s.Timezone, s.NTP, s.Synchronized, s.LocalTime = kv["Timezone"], kv["NTP"] == "yes", kv["NTPSynchronized"] == "yes", kv["TimeUSec"]
	}
	if s.Timezone == "" {
		if b, err := env.Root.ReadFile("/etc/timezone"); err == nil {
			s.Timezone = strings.TrimSpace(string(b))
		}
	}
	if b, err := env.Root.ReadFile("/run/systemd/shutdown/scheduled"); err == nil {
		kv := keyValues(b)
		if usec, err := strconv.ParseInt(kv["USEC"], 10, 64); err == nil && kv["MODE"] == "reboot" {
			t := time.UnixMicro(usec).UTC()
			s.Reboot = &t
		}
	}
	for _, p := range []string{"/run/reboot-required", "/var/run/reboot-required"} {
		if env.Root.Exists(p) {
			s.RebootNeeded = true
			if b, err := env.Root.ReadFile(p + ".pkgs"); err == nil {
				s.RebootPkgs = strings.Fields(string(b))
			}
			break
		}
	}
	return s
}

func run(ctx context.Context, env *host.Env, what string, id string, args ...string) error {
	if out, err := env.Runner.Run(ctx, id, args...); err != nil {
		return actions.Failed(what+" failed.", host.Output(out))
	}
	return nil
}

// Register adds package and server-management actions.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "packages.list", Description: "Installable components and their versions.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		names := make([]string, 0, len(Components))
		for n := range Components {
			names = append(names, n)
		}
		sort.Strings(names)
		inst := Installed(ctx, env, names)
		out := make([]Package, 0, len(names))
		for _, n := range names {
			v, ok := inst[n]
			out = append(out, Package{Name: n, Description: Components[n], Installed: ok, Version: v})
		}
		return out, nil
	}})
	r.Register(actions.Action{Name: "packages.updates", Description: "Pending package updates (from the last package index refresh).", Timeout: 2 * time.Minute, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !env.Runner.Available("apt-get") {
			return nil, actions.Failed("apt-get is not available on this system.", "")
		}
		out, err := env.Runner.RunWith(ctx, "apt-get", execx.Opts{Timeout: 2 * time.Minute}, "-s", "-o", "Debug::NoLocking=1", "upgrade")
		if err != nil {
			return nil, actions.Failed("The update check failed.", host.Output(out))
		}
		ups := ParseUpgrades(string(out))
		sec := 0
		for _, u := range ups {
			if u.Security {
				sec++
			}
		}
		return map[string]any{"updates": ups, "count": len(ups), "security": sec}, nil
	}})
	r.Register(actions.Action{Name: "packages.refresh", Mutating: true, Description: "Refresh the package index (apt-get update) (job).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		j := env.Jobs.Start("packages.refresh", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			if _, err := env.Runner.RunWith(ctx, "apt-get", execx.Opts{Output: log, Timeout: -1}, "update", "-q"); err != nil {
				return nil, fmt.Errorf("apt-get update failed")
			}
			return nil, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "packages.install", Mutating: true, Description: "Install allowlisted components: name, or names for several at once (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Name  string   `json:"name"`
			Names []string `json:"names"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		names := in.Names
		if in.Name != "" {
			names = append([]string{in.Name}, names...)
		}
		if len(names) == 0 || len(names) > 10 {
			return nil, actions.InvalidParams("give 1–10 component names")
		}
		for _, n := range names {
			if _, ok := Components[n]; !ok {
				return nil, actions.InvalidParams("name: " + n + " is not an installable component")
			}
		}
		j := env.Jobs.Start("packages.install", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			return nil, phpmgr.AptInstall(ctx, env, log, names)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "packages.upgrade", Mutating: true, Description: "Install all pending updates, keeping existing configuration files (job).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		j := env.Jobs.Start("packages.upgrade", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			opts := execx.Opts{Output: log, Timeout: -1}
			if _, err := env.Runner.RunWith(ctx, "apt-get", opts, "update", "-q"); err != nil {
				return nil, fmt.Errorf("apt-get update failed")
			}
			// "upgrade" never removes packages; confold keeps local config edits.
			if _, err := env.Runner.RunWith(ctx, "apt-get", opts, "upgrade", "-y", "-q", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"); err != nil {
				return nil, fmt.Errorf("apt-get upgrade failed; see the log above")
			}
			s := Read(ctx, env)
			if s.RebootNeeded {
				fmt.Fprintln(log, "A restart is required to finish the update.")
			}
			return map[string]any{"reboot_required": s.RebootNeeded}, nil
		})
		return map[string]any{"job_id": j.ID}, nil
	}})

	r.Register(actions.Action{Name: "server.settings", Description: "Host name, time zone, clock synchronisation and restart state.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return Read(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "server.set_hostname", Mutating: true, Description: "Change the host name.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Hostname string `json:"hostname"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		in.Hostname = strings.ToLower(strings.TrimSpace(in.Hostname))
		if len(in.Hostname) > 253 || !hostnameRe.MatchString(in.Hostname) {
			return nil, actions.InvalidParams("hostname: use letters, digits and hyphens, e.g. web1 or web1.example.com")
		}
		return map[string]any{"hostname": in.Hostname}, run(ctx, env, "Changing the host name", "hostnamectl", "set-hostname", "--", in.Hostname)
	}})
	r.Register(actions.Action{Name: "server.set_timezone", Mutating: true, Description: "Change the time zone.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Timezone string `json:"timezone"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if !tzRe.MatchString(in.Timezone) || strings.Contains(in.Timezone, "..") || !env.Root.Exists("/usr/share/zoneinfo/"+in.Timezone) {
			return nil, actions.InvalidParams("timezone: unknown time zone")
		}
		return map[string]any{"timezone": in.Timezone}, run(ctx, env, "Changing the time zone", "timedatectl", "set-timezone", in.Timezone)
	}})
	r.Register(actions.Action{Name: "server.timezones", Description: "Available time zones.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		out, err := env.Runner.Run(ctx, "timedatectl", "list-timezones")
		if err != nil {
			return []string{"UTC"}, nil
		}
		return strings.Fields(string(out)), nil
	}})
	r.Register(actions.Action{Name: "server.reboot", Mutating: true, Description: "Schedule a restart (requires the host name as confirmation).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Confirm string `json:"confirm"`
			Minutes int    `json:"minutes"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		s := Read(ctx, env)
		if in.Confirm == "" || in.Confirm != s.Hostname {
			return nil, actions.InvalidParams("confirm: type the host name to confirm the restart")
		}
		if in.Minutes < 1 || in.Minutes > 1440 {
			return nil, actions.InvalidParams("minutes: 1–1440")
		}
		if err := run(ctx, env, "Scheduling the restart", "shutdown", "-r", "+"+strconv.Itoa(in.Minutes), "Restart requested from WhaCP"); err != nil {
			return nil, err
		}
		return map[string]any{"in_minutes": in.Minutes}, nil
	}})
	r.Register(actions.Action{Name: "server.reboot_cancel", Mutating: true, Description: "Cancel a scheduled restart.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"cancelled": true}, run(ctx, env, "Cancelling the restart", "shutdown", "-c")
	}})
}
