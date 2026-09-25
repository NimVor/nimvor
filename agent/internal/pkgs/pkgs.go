// Package pkgs installs, lists and updates distribution packages with the
// system's package manager: apt on Debian and Ubuntu, dnf on AlmaLinux,
// Rocky Linux and RHEL. Callers use WhaCP's (Debian) package names; the
// platform maps them.
package pkgs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Upgrade is one pending update.
type Upgrade struct {
	Name     string `json:"name"`
	Current  string `json:"current"`
	New      string `json:"new"`
	Security bool   `json:"security"`
}

// Resolve maps names to this system's packages (duplicates removed) and
// reports names that have no equivalent here.
func Resolve(env *host.Env, names []string) (pkgs, missing []string) {
	seen := map[string]bool{}
	for _, n := range names {
		p, ok := env.Platform.PackageName(n)
		if !ok {
			missing = append(missing, n)
			continue
		}
		if !seen[p] {
			seen[p] = true
			pkgs = append(pkgs, p)
		}
	}
	return pkgs, missing
}

func dnf(env *host.Env) bool { return env.Platform.PackageManager() == "dnf" }

// Available reports whether the package manager can be used.
func Available(env *host.Env) bool {
	if dnf(env) {
		return env.Runner.Available("dnf")
	}
	return env.Runner.Available("apt-get")
}

// Install refreshes the package lists and installs packages (WhaCP names),
// streaming the output to log. Configuration files already present are
// kept.
func Install(ctx context.Context, env *host.Env, log io.Writer, names []string) error {
	pkgs, missing := Resolve(env, names)
	if len(missing) > 0 {
		return fmt.Errorf("not available on this system: %s", strings.Join(missing, ", "))
	}
	return InstallNative(ctx, env, log, pkgs)
}

// InstallNative installs packages given by this system's own names.
func InstallNative(ctx context.Context, env *host.Env, log io.Writer, pkgs []string) error {
	for _, p := range pkgs {
		if err := validate.Package("package", p); err != nil {
			return err
		}
	}
	if len(pkgs) == 0 {
		return nil
	}
	opts := execx.Opts{Output: log, Timeout: -1}
	if dnf(env) {
		args := append([]string{"install", "-y", "-q", "--setopt=install_weak_deps=False", "--"}, pkgs...)
		if _, err := env.Runner.RunWith(ctx, "dnf", opts, args...); err != nil {
			return fmt.Errorf("dnf install failed; see the log above")
		}
		return nil
	}
	if _, err := env.Runner.RunWith(ctx, "apt-get", opts, "update", "-q"); err != nil {
		return fmt.Errorf("apt-get update failed")
	}
	args := append([]string{"install", "-y", "-q", "--no-install-recommends", "-o", "Dpkg::Options::=--force-confold", "--"}, pkgs...)
	if _, err := env.Runner.RunWith(ctx, "apt-get", opts, args...); err != nil {
		return fmt.Errorf("apt-get install failed; see the log above")
	}
	return nil
}

// Installed returns WhaCP name → version for installed packages among names.
func Installed(ctx context.Context, env *host.Env, names []string) map[string]string {
	out := map[string]string{}
	native := map[string][]string{}
	var query []string
	for _, n := range names {
		if p, ok := env.Platform.PackageName(n); ok {
			if native[p] == nil {
				query = append(query, p)
			}
			native[p] = append(native[p], n)
		}
	}
	if len(query) == 0 {
		return out
	}
	if dnf(env) {
		if !env.Runner.Available("rpm") {
			return out
		}
		// rpm exits 1 when some are missing but prints the others.
		b, _ := env.Runner.Run(ctx, "rpm", append([]string{"-q", "--qf", "%{NAME}\t%{VERSION}-%{RELEASE}\n", "--"}, query...)...)
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Split(l, "\t"); len(f) == 2 {
				for _, n := range native[f[0]] {
					out[n] = f[1]
				}
			}
		}
		return out
	}
	if !env.Runner.Available("dpkg-query") {
		return out
	}
	// dpkg-query exits 1 when some names are unknown but still prints the rest.
	b, _ := env.Runner.Run(ctx, "dpkg-query", append([]string{"-W", "-f=${Package}\t${db:Status-Abbrev}\t${Version}\n", "--"}, query...)...)
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Split(l, "\t"); len(f) == 3 && strings.HasPrefix(f[1], "ii") {
			for _, n := range native[f[0]] {
				out[n] = f[2]
			}
		}
	}
	return out
}

// Updates lists pending updates (from the last index refresh).
func Updates(ctx context.Context, env *host.Env) ([]Upgrade, error) {
	if dnf(env) {
		// check-update exits 100 when updates are available.
		out, err := env.Runner.RunWith(ctx, "dnf", execx.Opts{Timeout: -1}, "-q", "--cacheonly", "check-update")
		var exit *exec.ExitError
		if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 100) {
			return nil, fmt.Errorf("dnf check-update: %s", host.Output(out))
		}
		ups := ParseDNFUpdates(string(out))
		if len(ups) > 0 {
			sec, _ := env.Runner.RunWith(ctx, "dnf", execx.Opts{Timeout: -1}, "-q", "--cacheonly", "updateinfo", "list", "--security", "--updates")
			MarkSecurity(ups, string(sec))
		}
		return sortUpgrades(ups), nil
	}
	out, err := env.Runner.RunWith(ctx, "apt-get", execx.Opts{Timeout: -1}, "-s", "-o", "Debug::NoLocking=1", "upgrade")
	if err != nil {
		return nil, fmt.Errorf("apt-get upgrade -s: %s", host.Output(out))
	}
	return ParseUpgrades(string(out)), nil
}

// Refresh downloads the package index.
func Refresh(ctx context.Context, env *host.Env, log io.Writer) error {
	opts := execx.Opts{Output: log, Timeout: -1}
	if dnf(env) {
		if _, err := env.Runner.RunWith(ctx, "dnf", opts, "-q", "makecache"); err != nil {
			return fmt.Errorf("dnf makecache failed")
		}
		return nil
	}
	if _, err := env.Runner.RunWith(ctx, "apt-get", opts, "update", "-q"); err != nil {
		return fmt.Errorf("apt-get update failed")
	}
	return nil
}

// UpgradeAll installs every pending update, keeping local configuration.
func UpgradeAll(ctx context.Context, env *host.Env, log io.Writer) error {
	if err := Refresh(ctx, env, log); err != nil {
		return err
	}
	opts := execx.Opts{Output: log, Timeout: -1}
	if dnf(env) {
		// rpm keeps changed configuration files (new ones become .rpmnew).
		if _, err := env.Runner.RunWith(ctx, "dnf", opts, "upgrade", "-y", "-q"); err != nil {
			return fmt.Errorf("dnf upgrade failed; see the log above")
		}
		return nil
	}
	// "upgrade" never removes packages; confold keeps local config edits.
	if _, err := env.Runner.RunWith(ctx, "apt-get", opts, "upgrade", "-y", "-q", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"); err != nil {
		return fmt.Errorf("apt-get upgrade failed; see the log above")
	}
	return nil
}

var instRe = regexp.MustCompile(`(?m)^Inst (\S+) (?:\[([^\]]+)\] )?\((\S+) ([^)]*)\)`)

// ParseUpgrades parses `apt-get -s upgrade` output.
func ParseUpgrades(out string) []Upgrade {
	ups := []Upgrade{}
	for _, m := range instRe.FindAllStringSubmatch(out, -1) {
		ups = append(ups, Upgrade{Name: m[1], Current: m[2], New: m[3], Security: strings.Contains(m[4], "-security")})
	}
	return sortUpgrades(ups)
}

var dnfLineRe = regexp.MustCompile(`^(\S+)\.(\S+)\s+(\S+)\s+(\S+)$`)

// ParseDNFUpdates parses `dnf check-update` output ("name.arch version repo").
func ParseDNFUpdates(out string) []Upgrade {
	ups := []Upgrade{}
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "Obsoleting") {
			break
		}
		if m := dnfLineRe.FindStringSubmatch(l); m != nil {
			ups = append(ups, Upgrade{Name: m[1], New: m[3]})
		}
	}
	return ups
}

// MarkSecurity flags updates listed by `dnf updateinfo list --security`
// ("ALSA-2026:1234 Important/Sec. name-version.arch").
func MarkSecurity(ups []Upgrade, out string) {
	for i := range ups {
		for _, l := range strings.Split(out, "\n") {
			f := strings.Fields(l)
			if len(f) == 3 && strings.HasPrefix(f[2], ups[i].Name+"-") && strings.Contains(f[1], "Sec") {
				ups[i].Security = true
				break
			}
		}
	}
}

func sortUpgrades(ups []Upgrade) []Upgrade {
	sort.Slice(ups, func(i, j int) bool {
		if ups[i].Security != ups[j].Security {
			return ups[i].Security
		}
		return ups[i].Name < ups[j].Name
	})
	return ups
}
