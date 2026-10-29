package phpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// ownsPackage says whether package name belongs to PHP version v: Debian and
// Ubuntu name them php8.3 and php8.3-<x>; Remi's are php83-<x>, with the
// extensions as php83-php-<x>. Shared libraries (libssl, libicu…) are never
// PHP's: they are updated with the server's other packages.
func ownsPackage(env *host.Env, v, name string) bool {
	t := "php" + tag(env, v)
	return name == t || strings.HasPrefix(name, t+"-")
}

// versionUpdates are the pending updates of PHP version v's own packages.
func versionUpdates(env *host.Env, v string, all []pkgs.Upgrade) []pkgs.Upgrade {
	out := []pkgs.Upgrade{}
	for _, u := range all {
		if ownsPackage(env, v, u.Name) {
			out = append(out, u)
		}
	}
	return out
}

func registerUpdates(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "php.updates", Timeout: 2 * time.Minute, Description: "Pending updates of each installed PHP version's own packages (from the last package index refresh).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		if !pkgs.Available(env) {
			return nil, actions.Failed("The package manager is not available on this system.", "")
		}
		all, err := pkgs.Updates(ctx, env)
		if err != nil {
			return nil, actions.Failed("The update check failed.", err.Error())
		}
		versions := map[string]any{}
		for _, v := range env.Discovery.PHP().Versions {
			ups := versionUpdates(env, v.Version, all)
			if len(ups) == 0 {
				continue
			}
			sec := 0
			for _, u := range ups {
				if u.Security {
					sec++
				}
			}
			versions[v.Version] = map[string]any{"updates": ups, "count": len(ups), "security": sec}
		}
		res := map[string]any{"versions": versions, "index_refreshed": nil, "index_stale": true}
		if t := pkgs.IndexRefreshed(env); !t.IsZero() {
			res["index_refreshed"] = t.UTC().Format(time.RFC3339)
			res["index_stale"] = time.Since(t) > pkgs.IndexStaleAfter
		}
		return res, nil
	}})

	r.Register(actions.Action{Name: "php.update", Mutating: true, Timeout: 2 * time.Minute, Description: "Update an installed PHP version's own packages (PHP, PHP-FPM and its extensions) to the repositories' newest release (job); PHP-FPM is tested and reloaded.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Version string `json:"version"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.PHPVersion("version", in.Version); err != nil {
			return nil, host.Invalid(err)
		}
		installed, hasFPM := false, false
		for _, v := range env.Discovery.PHP().Versions {
			if v.Version == in.Version {
				installed, hasFPM = true, v.FPMBinary != ""
			}
		}
		if !installed {
			return nil, actions.Conflict("PHP " + in.Version + " is not installed.")
		}
		all, err := pkgs.Updates(ctx, env)
		if err != nil {
			return nil, actions.Failed("The update check failed.", err.Error())
		}
		var names []string
		for _, u := range versionUpdates(env, in.Version, all) {
			names = append(names, u.Name)
		}
		if len(names) == 0 {
			return nil, actions.Conflict("PHP " + in.Version + " is up to date (as far as the last package index refresh shows; refresh it to look again).")
		}
		j := env.Jobs.Start("php.update", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			fmt.Fprintf(log, "Updating %d package(s) of PHP %s: %s\n", len(names), in.Version, strings.Join(names, ", "))
			if err := pkgs.UpgradeNative(ctx, env, log, names); err != nil {
				return nil, err
			}
			if !hasFPM {
				return nil, nil
			}
			defer env.LockWebConfig()()
			if err := env.ReloadFPM(ctx, in.Version); err != nil {
				return nil, fmt.Errorf("updated, but PHP-FPM %s did not reload: %v", in.Version, err)
			}
			fmt.Fprintf(log, "PHP-FPM %s reloaded.\n", in.Version)
			return nil, nil
		})
		return map[string]any{"job_id": j.ID, "packages": names}, nil
	}})
}
