// Package sysmgr manages system packages and server settings: installed
// and upgradable packages, installing an allowlisted component, applying
// updates, host name, time zone and scheduled restarts.
package sysmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/capability"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ffmpegmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/hardening"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/pkgs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/shacrypt"
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
	"awstats":             "Website statistics (AWStats)",
	"bubblewrap":          "Sandbox for jailed shells (bubblewrap)",
	"clamav":              "Malware scanner (ClamAV)",
	"clamav-freshclam":    "ClamAV signature updates",
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
	"ffmpeg":              "Audio and video processing (FFmpeg)",
	"libnginx-mod-brotli": "Brotli compression for Nginx",
	// ModSecurity: the web application firewall and its generic rules.
	"libnginx-mod-http-modsecurity": "Web application firewall for Nginx (ModSecurity)",
	"modsecurity-crs":               "OWASP Core Rule Set for ModSecurity",
}

// Package is the state of one component.
type Package struct {
	Name string `json:"name"`
	// Package is this system's package name (e.g. redis on RHEL; "" when
	// there is none and the functionality came from elsewhere).
	Package     string `json:"package"`
	Description string `json:"description"`
	// Installed: the package is installed, or the functionality is there
	// without it (built from source, copied by hand).
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
	// State of the functionality itself, for components WhaCP can check:
	// missing, installed (present but not loaded or usable) or functional.
	State  string `json:"state,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Source: "package", or "other" when it works without the package.
	Source string `json:"source,omitempty"`
}

// commandOf maps components to the program that provides them.
var commandOf = map[string]string{"ffmpeg": "ffmpeg", "composer": "composer", "git": "git", "certbot": "certbot", "clamav": "clamscan"}

// capabilities checks the functionality of the components that have one.
func capabilities(ctx context.Context, env *host.Env) map[string]capability.Status {
	out := map[string]capability.Status{}
	mods := capability.Modules(ctx, env, true)
	for _, c := range capability.NginxModules {
		out[c.Package] = mods[c.ID]
	}
	for comp, cmd := range commandOf {
		out[comp] = capability.Command(env, cmd, Components[comp])
	}
	return out
}

// Upgrade is one pending update.
type Upgrade = pkgs.Upgrade

// ParseUpgrades parses `apt-get -s upgrade` output.
func ParseUpgrades(out string) []Upgrade { return pkgs.ParseUpgrades(out) }

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
	// RHEL: needs-restarting -r exits 1 when a restart is required.
	if !s.RebootNeeded && env.Platform.PackageManager() == "dnf" && env.Runner.Available("needs-restarting") {
		if _, err := env.Runner.Run(ctx, "needs-restarting", "-r"); err != nil {
			var exit *exec.ExitError
			s.RebootNeeded = errors.As(err, &exit) && exit.ExitCode() == 1
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
		caps := capabilities(ctx, env)
		names := make([]string, 0, len(Components))
		for n := range Components {
			_, hasPkg := env.Platform.PackageName(n)
			// Without a package here, listed only when it works anyway.
			if hasPkg || caps[n].State == capability.Functional {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		inst := pkgs.Installed(ctx, env, names)
		out := make([]Package, 0, len(names))
		for _, n := range names {
			v, ok := inst[n]
			native, _ := env.Platform.PackageName(n)
			p := Package{Name: n, Package: native, Description: Components[n], Installed: ok, Version: v}
			if c, has := caps[n]; has {
				p.State, p.Reason = c.State, c.Reason
				switch {
				case ok:
					p.Source = "package"
				case c.State == capability.Functional:
					p.Installed, p.Source = true, "other"
				}
			}
			out = append(out, p)
		}
		return out, nil
	}})
	r.Register(actions.Action{Name: "packages.updates", Description: "Pending package updates (from the last package index refresh).", Timeout: 2 * time.Minute, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !pkgs.Available(env) {
			return nil, actions.Failed("The package manager is not available on this system.", "")
		}
		ups, err := pkgs.Updates(ctx, env)
		if err != nil {
			return nil, actions.Failed("The update check failed.", err.Error())
		}
		sec := 0
		for _, u := range ups {
			if u.Security {
				sec++
			}
		}
		res := map[string]any{"updates": ups, "count": len(ups), "security": sec, "index_refreshed": nil, "index_stale": true}
		if t := pkgs.IndexRefreshed(env); !t.IsZero() {
			res["index_refreshed"] = t.UTC().Format(time.RFC3339)
			res["index_stale"] = time.Since(t) > pkgs.IndexStaleAfter
		}
		return res, nil
	}})
	r.Register(actions.Action{Name: "packages.refresh", Mutating: true, Description: "Refresh the package index (apt-get update or dnf makecache) (job).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		j := env.Jobs.Start("packages.refresh", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			return nil, pkgs.Refresh(ctx, env, log)
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
			if _, ok := env.Platform.PackageName(n); !ok {
				return nil, actions.Conflict(n + " is not available on this operating system.")
			}
		}
		j := env.Jobs.Start("packages.install", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			// A functionality that already works (a module built from
			// source, a program installed by hand) needs no package.
			caps := capabilities(ctx, env)
			var want []string
			for _, n := range names {
				if c, has := caps[n]; has && c.State == capability.Functional {
					if inst := pkgs.Installed(ctx, env, []string{n}); len(inst) == 0 {
						fmt.Fprintf(log, "%s: already available without the package; skipped.\n", n)
						continue
					}
				}
				want = append(want, n)
			}
			if len(want) == 0 {
				return nil, nil
			}
			err := pkgs.Install(ctx, env, log, want)
			restrictAgain(ctx, env, log)
			return nil, err
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "packages.upgrade", Mutating: true, Description: "Install all pending updates, keeping existing configuration files (job).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		j := env.Jobs.Start("packages.upgrade", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			err := pkgs.UpgradeAll(ctx, env, log)
			restrictAgain(ctx, env, log)
			if err != nil {
				return nil, err
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
	r.Register(actions.Action{Name: "server.root_password", Mutating: true, Description: "Set the root password. It is hashed (SHA-512 crypt) in the agent and handed to chpasswd -e as a hash on standard input; it is never logged or returned.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Password string `json:"password"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if n := utf8.RuneCountInString(in.Password); n < 12 || len(in.Password) > 512 || strings.ContainsAny(in.Password, "\x00\r\n:") || !utf8.ValidString(in.Password) {
			return nil, actions.InvalidParams("password: 12 to 512 characters, without line breaks or colons")
		}
		hash := shacrypt.HashPassword(in.Password)
		in.Password = ""
		if out, err := env.Runner.RunWith(ctx, "chpasswd", execx.Opts{Stdin: strings.NewReader("root:" + hash + "\n")}, "-e"); err != nil {
			return nil, actions.Failed("The root password was not changed.", host.Output(out))
		}
		return map[string]any{"changed": true}, nil
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

// restrictAgain applies WhaCP's limits on installed programs again after a
// package job (package updates reset file modes on RHEL).
func restrictAgain(ctx context.Context, env *host.Env, log io.Writer) {
	if err := ffmpegmgr.Reapply(ctx, env); err != nil {
		fmt.Fprintf(log, "FFmpeg could not be limited to its group again: %v\n", err)
	}
	if err := hardening.Reapply(ctx, env); err != nil {
		fmt.Fprintf(log, "Compiler or traceroute restrictions could not be applied again: %v\n", err)
	}
}
