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
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/capability"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/ffmpegmgr"
	"github.com/NimVor/nimvor/agent/internal/hardening"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/shacrypt"
)

// Components are the packages NimVor offers to install (PHP versions are
// handled by phpmgr). Anything else is installed by the administrator.
var Components = map[string]string{
	"nginx":               "Web server",
	"mariadb-server":      "MariaDB database server",
	"redis-server":        "Redis cache server",
	"memcached":           "Memcached cache server",
	"mongodb-org":         "MongoDB NoSQL database server (needs MongoDB's repository)",
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
	"strace":              "System call tracer (Process Manager → Trace)",
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
	// State of the functionality itself, for components NimVor can check:
	// missing, installed (present but not loaded or usable) or functional.
	State  string `json:"state,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Source: "package", or "other" when it works without the package.
	Source string `json:"source,omitempty"`
	// Unavailable: not installed and not offered by the enabled
	// repositories (e.g. Brotli on Ubuntu 22.04); Install would fail.
	Unavailable bool `json:"unavailable,omitempty"`
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

// primaryIPv4 is the server's first global unicast IPv4 address, used when
// the panel does not tell us which address to map the host name to.
func primaryIPv4() string {
	ifs, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
				return ip.String()
			}
		}
	}
	return ""
}

// hostsManagedBegin/End delimit the one line NimVor owns in /etc/hosts.
const (
	hostsManagedBegin = "# BEGIN NimVor host name"
	hostsManagedEnd   = "# END NimVor host name"
)

// applyEtcHosts keeps /etc/hosts in step with the server's host name: the
// fully-qualified name (and its short form) resolve to ip when known, so
// the machine does not answer with Debian's 127.0.1.1 placeholder — which
// otherwise fails the mail host-name check and breaks HELO/greeting. All
// other entries (localhost, operator-added names) are left untouched; the
// host-name line lives in a marked block that is rewritten in place.
func applyEtcHosts(env *host.Env, fqdn, ip string) (bool, error) {
	var existing string
	if b, err := env.Root.ReadFile("/etc/hosts"); err == nil {
		existing = string(b)
	}
	short := fqdn
	if i := strings.IndexByte(fqdn, '.'); i > 0 {
		short = fqdn[:i]
	}

	// The mapping NimVor owns. With a real address, map it there; otherwise
	// fall back to Debian's 127.0.1.1 so the name at least resolves locally.
	target := ip
	if target == "" {
		target = "127.0.1.1"
	}
	names := fqdn
	if short != fqdn {
		names = fqdn + " " + short
	}
	managed := hostsManagedBegin + "\n" + target + "\t" + names + "\n" + hostsManagedEnd

	var kept []string
	var haveLocalhost bool
	inBlock := false
	for _, line := range strings.Split(existing, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == hostsManagedBegin {
			inBlock = true
			continue
		}
		if trimmed == hostsManagedEnd {
			inBlock = false
			continue
		}
		if inBlock {
			continue
		}
		fields := strings.Fields(trimmed)
		// Drop Debian's 127.0.1.1 placeholder and any stray line that already
		// maps our own names, so resolution cannot return a second address.
		if len(fields) > 0 && fields[0] == "127.0.1.1" {
			continue
		}
		if len(fields) > 1 && (contains(fields[1:], fqdn) || contains(fields[1:], short)) && fields[0] != "127.0.0.1" {
			continue
		}
		if len(fields) > 0 && fields[0] == "127.0.0.1" {
			haveLocalhost = true
		}
		kept = append(kept, line)
	}

	// Trim trailing blank lines left by removals.
	for len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
		kept = kept[:len(kept)-1]
	}

	var out []string
	if !haveLocalhost {
		out = append(out, "127.0.0.1\tlocalhost")
	}
	out = append(out, kept...)
	out = append(out, managed)
	content := strings.Join(out, "\n") + "\n"

	return env.Writer.WriteFile("/etc/hosts", []byte(content), 0o644, agentfs.RootOwner)
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// Register adds package and server-management actions.
func Register(r *actions.Registry, env *host.Env) {
	registerGraphical(r, env)
	registerHostnameRefs(r, env)
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
		// Which missing components the repositories cannot provide.
		var query []string
		for _, n := range names {
			if _, ok := inst[n]; !ok && caps[n].State != capability.Functional {
				if native, has := env.Platform.PackageName(n); has && native != "" {
					query = append(query, native)
				}
			}
		}
		notOffered := map[string]bool{}
		if len(query) > 0 {
			if missing, ok := pkgs.Unavailable(ctx, env, query); ok {
				for _, m := range missing {
					notOffered[m] = true
				}
			}
		}
		out := make([]Package, 0, len(names))
		for _, n := range names {
			v, ok := inst[n]
			native, _ := env.Platform.PackageName(n)
			p := Package{Name: n, Package: native, Description: Components[n], Installed: ok, Version: v, Unavailable: !ok && native != "" && notOffered[native]}
			if c, has := caps[n]; has {
				p.State, p.Reason = c.State, c.Reason
				switch {
				case ok:
					p.Source = "package"
				case c.State == capability.Functional:
					p.Installed, p.Source, p.Unavailable = true, "other", false
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
	r.Register(actions.Action{Name: "packages.advisories", Description: "Pending updates with the vulnerability information the OS provides: advisory id, severity and CVEs on RHEL (dnf updateinfo), security vs normal from the security pocket on Debian/Ubuntu. No automatic upgrades.", Timeout: 2 * time.Minute, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !pkgs.Available(env) {
			return nil, actions.Failed("The package manager is not available on this system.", "")
		}
		advs, err := pkgs.Advisories(ctx, env)
		if err != nil {
			return nil, actions.Failed("The advisory check failed.", err.Error())
		}
		n := map[string]int{"security": 0, "normal": 0, "unknown": 0}
		for _, a := range advs {
			n[a.Kind]++
		}
		return map[string]any{"advisories": advs, "count": len(advs), "security": n["security"], "normal": n["normal"], "unknown": n["unknown"]}, nil
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
			if err != nil {
				return nil, err
			}
			// Installed means working.
			return nil, verifyComponents(ctx, env, log, want)
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

	r.Register(actions.Action{Name: "packages.health", Description: "Broken package state: install scripts that failed, missing dependencies, duplicates.", Timeout: 2 * time.Minute, Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !pkgs.Available(env) {
			return nil, actions.Failed("The package manager is not available on this system.", "")
		}
		h := pkgs.CheckHealth(ctx, env)
		return map[string]any{"checked": h.Checked, "problems": h.Problems, "repairable": h.Repairable(), "duplicates": h.Duplicates()}, nil
	}})
	r.Register(actions.Action{Name: "packages.repair", Mutating: true, Description: "Repair broken package state: reinstall packages whose install script failed, install missing dependencies; remove_duplicates (confirmed) also removes older duplicate versions (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			RemoveDuplicates bool `json:"remove_duplicates"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		j := env.Jobs.Start("packages.repair", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			h, err := pkgs.Repair(ctx, env, log, pkgs.RepairOptions{RemoveDuplicates: in.RemoveDuplicates})
			restrictAgain(ctx, env, log)
			return map[string]any{"problems": h.Problems}, err
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
	r.Register(actions.Action{Name: "server.set_hostname", Mutating: true, Description: "Change the host name and keep /etc/hosts in step so the machine resolves its own name to its real address.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Hostname string `json:"hostname"`
			IP       string `json:"ip"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		in.Hostname = strings.ToLower(strings.TrimSpace(in.Hostname))
		if len(in.Hostname) > 253 || !hostnameRe.MatchString(in.Hostname) {
			return nil, actions.InvalidParams("hostname: use letters, digits and hyphens, e.g. web1 or web1.example.com")
		}
		ip := strings.TrimSpace(in.IP)
		if ip != "" {
			if parsed := net.ParseIP(ip); parsed == nil || parsed.IsLoopback() {
				return nil, actions.InvalidParams("ip: give the server's own public address, or leave it empty")
			}
		} else {
			ip = primaryIPv4()
		}
		if err := run(ctx, env, "Changing the host name", "hostnamectl", "set-hostname", "--", in.Hostname); err != nil {
			return nil, err
		}
		hostsChanged, err := applyEtcHosts(env, in.Hostname, ip)
		if err != nil {
			return nil, actions.Failed("The host name was changed, but /etc/hosts could not be updated.", err.Error())
		}
		return map[string]any{"hostname": in.Hostname, "ip": ip, "hosts_updated": hostsChanged}, nil
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
		if err := run(ctx, env, "Scheduling the restart", "shutdown", "-r", "+"+strconv.Itoa(in.Minutes), "Restart requested from NimVor"); err != nil {
			return nil, err
		}
		return map[string]any{"in_minutes": in.Minutes}, nil
	}})
	r.Register(actions.Action{Name: "server.reboot_cancel", Mutating: true, Description: "Cancel a scheduled restart.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"cancelled": true}, run(ctx, env, "Cancelling the restart", "shutdown", "-c")
	}})
}

// restrictAgain applies NimVor's limits on installed programs again after a
// package job (package updates reset file modes on RHEL).
// RepairAtStart checks the installed packages when the agent starts (after
// an installation or update of NimVor) and, when an earlier package
// transaction left something to repair without asking, starts the repair
// as a job the Packages page lists. The same problems start it once.
func RepairAtStart(ctx context.Context, env *host.Env) {
	if !pkgs.Available(env) {
		return
	}
	h := pkgs.CheckHealth(ctx, env)
	if !pkgs.AutoRepairDue(env, h) {
		return
	}
	pkgs.MarkAutoRepair(env, h)
	env.Log.Info("package problems found; repairing them", "problems", len(h.Problems))
	env.Jobs.Start("packages.repair", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
		fmt.Fprintln(log, "Started automatically by the agent: an earlier package installation left problems behind.")
		after, err := pkgs.Repair(ctx, env, log, pkgs.RepairOptions{})
		restrictAgain(ctx, env, log)
		return map[string]any{"problems": after.Problems}, err
	})
}

func restrictAgain(ctx context.Context, env *host.Env, log io.Writer) {
	if err := ffmpegmgr.Reapply(ctx, env); err != nil {
		fmt.Fprintf(log, "FFmpeg could not be limited to its group again: %v\n", err)
	}
	if err := hardening.Reapply(ctx, env); err != nil {
		fmt.Fprintf(log, "Compiler or traceroute restrictions could not be applied again: %v\n", err)
	}
}
