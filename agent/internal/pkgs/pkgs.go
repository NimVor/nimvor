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
	"time"

	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/validate"
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
	opts := execx.Opts{Output: log, Timeout: -1, Env: packageEnv}
	if dnf(env) {
		if err := ensureAvailable(ctx, env, log, pkgs); err != nil {
			return err
		}
		args := append([]string{"install", "-y", "-q", "--setopt=install_weak_deps=False", "--"}, pkgs...)
		return transact(ctx, env, log, "install", pkgs, func(ctx context.Context) ([]byte, error) {
			return env.Runner.RunWith(ctx, "dnf", opts, args...)
		})
	}
	if err := transact(ctx, env, log, "refresh the package index", nil, func(ctx context.Context) ([]byte, error) {
		return env.Runner.RunWith(ctx, "apt-get", opts, append([]string{"update", "-q"}, aptLockWait...)...)
	}); err != nil {
		return err
	}
	if err := ensureAvailable(ctx, env, log, pkgs); err != nil {
		return err
	}
	args := append([]string{"install", "-y", "-q", "--no-install-recommends", "-o", "Dpkg::Options::=--force-confold"}, aptLockWait...)
	args = append(append(args, "--"), pkgs...)
	return transact(ctx, env, log, "install", pkgs, func(ctx context.Context) ([]byte, error) {
		return env.Runner.RunWith(ctx, "apt-get", opts, args...)
	})
}

// RemoveNative removes packages given by this system's own names, but only
// them: when the package manager would also remove other packages (that
// need these), nothing is removed and the error names them.
func RemoveNative(ctx context.Context, env *host.Env, log io.Writer, pkgs []string) error {
	for _, p := range pkgs {
		if err := validate.Package("package", p); err != nil {
			return err
		}
	}
	if len(pkgs) == 0 {
		return nil
	}
	opts := execx.Opts{Output: log, Timeout: -1, Env: packageEnv}
	if dnf(env) {
		// rpm -e --test fails, naming them, when other packages need these.
		if out, err := env.Runner.Run(ctx, "rpm", append([]string{"-e", "--test", "--"}, pkgs...)...); err != nil {
			return fmt.Errorf("other packages need %s: %s", strings.Join(pkgs, ", "), host.Why(out, err))
		}
		args := append([]string{"remove", "-y", "-q", "--noautoremove", "--"}, pkgs...)
		return transact(ctx, env, log, "remove", pkgs, func(ctx context.Context) ([]byte, error) {
			return env.Runner.RunWith(ctx, "dnf", opts, args...)
		})
	}
	out, err := env.Runner.Run(ctx, "apt-get", append([]string{"-s", "remove", "--"}, pkgs...)...)
	if err != nil {
		return fmt.Errorf("apt-get could not plan the removal: %s", host.Why(out, err))
	}
	if extra := ExtraRemovals(string(out), pkgs); len(extra) > 0 {
		return fmt.Errorf("removing %s would also remove %s; nothing was removed", strings.Join(pkgs, ", "), strings.Join(extra, ", "))
	}
	args := append(append([]string{"remove", "-y", "-q"}, aptLockWait...), "--")
	args = append(args, pkgs...)
	return transact(ctx, env, log, "remove", pkgs, func(ctx context.Context) ([]byte, error) {
		return env.Runner.RunWith(ctx, "apt-get", opts, args...)
	})
}

var aptRemv = regexp.MustCompile(`(?m)^Remv (\S+)`)

// phpVersioned is a versioned PHP package ("php8.3-redis").
var phpVersioned = regexp.MustCompile(`^php[0-9]+\.[0-9]+-(.+)$`)

// ExtraRemovals lists the packages an apt-get -s remove would remove
// beyond those asked for. Debian's unversioned PHP metapackage of an asked
// package ("php-redis" for "php8.3-redis") only points at it and may go.
func ExtraRemovals(simulation string, asked []string) []string {
	want := map[string]bool{}
	for _, p := range asked {
		want[p] = true
		if m := phpVersioned.FindStringSubmatch(p); m != nil {
			want["php-"+m[1]] = true
		}
	}
	var extra []string
	for _, m := range aptRemv.FindAllStringSubmatch(simulation, -1) {
		if name, _, _ := strings.Cut(m[1], ":"); !want[name] {
			extra = append(extra, name)
		}
	}
	return extra
}

// Installed returns WhaCP name → version for installed packages among names.
func Installed(ctx context.Context, env *host.Env, names []string) map[string]string {
	return installed(ctx, env, names, false)
}

// InstalledNative returns native package name → version for installed
// packages among natives (this system's own names).
func InstalledNative(ctx context.Context, env *host.Env, natives []string) map[string]string {
	return installed(ctx, env, natives, true)
}

func installed(ctx context.Context, env *host.Env, names []string, isNative bool) map[string]string {
	out := map[string]string{}
	native := map[string][]string{}
	var query []string
	for _, n := range names {
		p, ok := n, true
		if !isNative {
			p, ok = env.Platform.PackageName(n)
		}
		if ok {
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
		// The format's \t and \n are escapes rpm expands: the agent refuses
		// arguments with real line breaks.
		b, _ := env.Runner.Run(ctx, "rpm", append([]string{"-q", "--qf", `%{NAME}\t%{VERSION}-%{RELEASE}\n`, "--"}, query...)...)
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
	// The format's \t and \n are escapes dpkg-query expands: the agent
	// refuses arguments with real line breaks.
	b, _ := env.Runner.Run(ctx, "dpkg-query", append([]string{"-W", `-f=${Package}\t${db:Status-Abbrev}\t${Version}\n`, "--"}, query...)...)
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
	return transact(ctx, env, log, "refresh the package index", nil, func(ctx context.Context) ([]byte, error) {
		opts := execx.Opts{Output: log, Timeout: -1, Env: packageEnv}
		if dnf(env) {
			return env.Runner.RunWith(ctx, "dnf", opts, "-q", "makecache")
		}
		return env.Runner.RunWith(ctx, "apt-get", opts, append([]string{"update", "-q"}, aptLockWait...)...)
	})
}

// UpgradeAll installs every pending update, keeping local configuration.
func UpgradeAll(ctx context.Context, env *host.Env, log io.Writer) error {
	if err := Refresh(ctx, env, log); err != nil {
		return err
	}
	opts := execx.Opts{Output: log, Timeout: -1, Env: packageEnv}
	return transact(ctx, env, log, "upgrade", nil, func(ctx context.Context) ([]byte, error) {
		if dnf(env) {
			// rpm keeps changed configuration files (new ones become .rpmnew).
			return env.Runner.RunWith(ctx, "dnf", opts, "upgrade", "-y", "-q")
		}
		// "upgrade" never removes packages; confold keeps local config edits.
		return env.Runner.RunWith(ctx, "apt-get", opts, append([]string{"upgrade", "-y", "-q", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}, aptLockWait...)...)
	})
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

// IndexRefreshed is when the package index was last refreshed (the newest
// file of apt's lists or dnf's cache); zero when unknown.
func IndexRefreshed(env *host.Env) time.Time {
	patterns := []string{"/var/lib/apt/lists/*_InRelease", "/var/lib/apt/lists/*_Release", "/var/lib/apt/periodic/update-success-stamp"}
	if dnf(env) {
		patterns = []string{"/var/cache/dnf/*.solv", "/var/cache/dnf/*/repodata/repomd.xml", "/var/cache/dnf/expired_repos.json"}
	}
	var newest time.Time
	for _, p := range patterns {
		files, _ := env.Root.Glob(p)
		for _, f := range files {
			if st, err := env.Root.Stat(f); err == nil && st.ModTime().After(newest) {
				newest = st.ModTime()
			}
		}
	}
	return newest
}

// IndexStaleAfter is when update information stops being trustworthy.
const IndexStaleAfter = 7 * 24 * time.Hour

// UpgradeNative updates installed packages (this system's names) to the
// repositories' versions, keeping local configuration; packages that are
// not installed are not installed by it.
func UpgradeNative(ctx context.Context, env *host.Env, log io.Writer, pkgs []string) error {
	for _, p := range pkgs {
		if err := validate.Package("package", p); err != nil {
			return err
		}
	}
	if len(pkgs) == 0 {
		return nil
	}
	opts := execx.Opts{Output: log, Timeout: -1, Env: packageEnv}
	if dnf(env) {
		args := append([]string{"upgrade", "-y", "-q", "--"}, pkgs...)
		return transact(ctx, env, log, "update", pkgs, func(ctx context.Context) ([]byte, error) {
			return env.Runner.RunWith(ctx, "dnf", opts, args...)
		})
	}
	args := append([]string{"install", "--only-upgrade", "-y", "-q", "-o", "Dpkg::Options::=--force-confold"}, aptLockWait...)
	args = append(append(args, "--"), pkgs...)
	return transact(ctx, env, log, "update", pkgs, func(ctx context.Context) ([]byte, error) {
		return env.Runner.RunWith(ctx, "apt-get", opts, args...)
	})
}
