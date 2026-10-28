// Package phpmgr installs PHP versions and controls their FPM services.
package phpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// DefaultExtensions are installed with every new PHP version.
var DefaultExtensions = []string{"mysql", "curl", "gd", "mbstring", "xml", "zip", "intl", "bcmath", "opcache"}

var extRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// Register adds PHP actions.
func Register(r *actions.Registry, env *host.Env) {
	registerExtensions(r, env)
	registerIonCube(r, env)
	registerIni(r, env)
	registerFPMGlobal(r, env)
	registerRepository(r, env)
	registerProfile(r, env)
	r.Register(actions.Action{Name: "php.available", Description: "PHP versions offered by the configured package repositories.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		return Available(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "php.modules", Description: "Loaded modules of a PHP version.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Version string `json:"version"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.PHPVersion("version", in.Version); err != nil {
			return nil, host.Invalid(err)
		}
		out, err := env.Runner.Run(ctx, "php"+in.Version, "-m")
		if err != nil {
			return nil, actions.Failed("PHP "+in.Version+" is not installed.", host.Output(out))
		}
		return ParseModules(string(out)), nil
	}})
	r.Register(actions.Action{Name: "php.install", Mutating: true, Description: "Install a PHP version (FPM, CLI and common extensions) with the package manager.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Version    string   `json:"version"`
			Extensions []string `json:"extensions"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.PHPVersion("version", in.Version); err != nil {
			return nil, host.Invalid(err)
		}
		if len(in.Extensions) == 0 {
			in.Extensions = DefaultExtensions
		}
		for _, e := range in.Extensions {
			if !extRe.MatchString(e) {
				return nil, actions.InvalidParams("extensions: invalid name " + e)
			}
		}
		packages := env.Platform.PHPPackages(in.Version, in.Extensions)
		j := env.Jobs.Start("php.install", "php:"+in.Version, func(ctx context.Context, log io.Writer) (any, error) {
			if err := pkgs.InstallNative(ctx, env, log, packages); err != nil {
				return nil, err
			}
			// Installed means working: the CLI runs and FPM accepts its
			// configuration.
			if out, err := env.Runner.Run(ctx, "php"+in.Version, "-v"); err != nil {
				return nil, fmt.Errorf("PHP %s was installed, but its command line does not run: %s", in.Version, host.Why(out, err))
			}
			if err := env.FPMTest(ctx, in.Version); err != nil {
				return nil, fmt.Errorf("PHP %s was installed, but PHP-FPM refuses its configuration: %v", in.Version, err)
			}
			// RHEL packages do not enable their services (Debian's do): PHP-FPM
			// must also start at boot, or websites on this version stop
			// working after a restart.
			unit := env.M.FPMUnit(in.Version)
			if err := env.Systemctl(ctx, "enable", unit); err != nil {
				return nil, fmt.Errorf("PHP %s was installed, but PHP-FPM could not be enabled at boot: %v", in.Version, err)
			}
			if err := env.Systemctl(ctx, "start", unit); err != nil {
				return nil, fmt.Errorf("PHP %s was installed, but PHP-FPM did not start: %v", in.Version, err)
			}
			fmt.Fprintf(log, "  ✓ PHP %s: command line and PHP-FPM configuration work; %s is running and starts at boot\n", in.Version, unit)
			return nil, nil
		})
		return map[string]any{"job_id": j.ID, "packages": packages}, nil
	}})
	r.Register(actions.Action{Name: "php.set_enabled", Mutating: true, Description: "Start and enable, or stop and disable, the FPM service of a PHP version.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Version string `json:"version"`
			Enabled bool   `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.PHPVersion("version", in.Version); err != nil {
			return nil, host.Invalid(err)
		}
		unit := env.M.FPMUnit(in.Version)
		if !in.Enabled {
			if users := SitesUsing(ctx, env, in.Version); len(users) > 0 {
				return nil, actions.Conflict(fmt.Sprintf("PHP %s is still used by: %s. Move those websites to another version first.", in.Version, strings.Join(users, ", ")))
			}
			if err := env.Systemctl(ctx, "disable", unit); err != nil {
				return nil, actions.Failed("Could not disable PHP "+in.Version+".", err.Error())
			}
			if err := env.Systemctl(ctx, "stop", unit); err != nil {
				return nil, actions.Failed("Could not stop PHP "+in.Version+".", err.Error())
			}
			return map[string]any{"version": in.Version, "enabled": false}, nil
		}
		defer env.LockWebConfig()()
		if err := env.FPMTest(ctx, in.Version); err != nil {
			return nil, host.FailWithValidation("PHP "+in.Version+" was not started", err)
		}
		if err := env.Systemctl(ctx, "enable", unit); err != nil {
			return nil, actions.Failed("Could not enable PHP "+in.Version+".", err.Error())
		}
		if err := env.Systemctl(ctx, "start", unit); err != nil {
			return nil, actions.Failed("Could not start PHP "+in.Version+".", err.Error())
		}
		return map[string]any{"version": in.Version, "enabled": true}, nil
	}})
}

// Available lists the PHP versions the configured repositories offer.
func Available(ctx context.Context, env *host.Env) []string {
	if env.Platform.PackageManager() == "dnf" {
		// Remi's parallel versions: php83-php-fpm.x86_64 …
		out, err := env.Runner.Run(ctx, "dnf", "-q", "--cacheonly", "list", "php[0-9][0-9]-php-fpm")
		if err != nil {
			// No metadata cached yet.
			if out, err = env.Runner.Run(ctx, "dnf", "-q", "list", "php[0-9][0-9]-php-fpm"); err != nil {
				return []string{}
			}
		}
		return ParseAvailableRemi(string(out))
	}
	out, err := env.Runner.Run(ctx, "apt-cache", "search", "--names-only", `^php[0-9]+\.[0-9]+-fpm$`)
	if err != nil {
		return []string{}
	}
	return ParseAvailable(string(out))
}

// SitesUsing lists websites (managed or not) whose FastCGI backend is a
// pool of PHP version v.
func SitesUsing(ctx context.Context, env *host.Env, v string) []string {
	res := env.Discovery.Websites(ctx)
	var out []string
	for _, w := range res.Websites {
		if w.Enabled && w.PHP != nil && w.PHP.Version == v {
			out = append(out, w.Domain)
		}
	}
	sort.Strings(out)
	return out
}

var availRe = regexp.MustCompile(`(?m)^php([0-9]+\.[0-9]+)-fpm\b`)

// ParseAvailable extracts versions from apt-cache search output.
func ParseAvailable(out string) []string {
	seen := map[string]bool{}
	var vs []string
	for _, m := range availRe.FindAllStringSubmatch(out, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			vs = append(vs, m[1])
		}
	}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
	return vs
}

var remiAvailRe = regexp.MustCompile(`(?m)^php([0-9])([0-9]+)-php-fpm\.`)

// ParseAvailableRemi extracts versions from `dnf list` output of Remi's
// php83-php-fpm style packages.
func ParseAvailableRemi(out string) []string {
	seen := map[string]bool{}
	var vs []string
	for _, m := range remiAvailRe.FindAllStringSubmatch(out, -1) {
		v := m[1] + "." + m[2]
		if !seen[v] {
			seen[v] = true
			vs = append(vs, v)
		}
	}
	sort.Slice(vs, func(i, j int) bool { return versionLess(vs[i], vs[j]) })
	return vs
}

// ParseModules parses "php -m" output.
func ParseModules(out string) []string {
	var mods []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "[") {
			continue
		}
		mods = append(mods, l)
	}
	return mods
}

// VersionLess compares PHP versions such as "8.3" and "8.10".
func VersionLess(a, b string) bool { return versionLess(a, b) }

func versionLess(a, b string) bool {
	var am, an, bm, bn int
	fmt.Sscanf(a, "%d.%d", &am, &an)
	fmt.Sscanf(b, "%d.%d", &bm, &bn)
	if am != bm {
		return am < bm
	}
	return an < bn
}
