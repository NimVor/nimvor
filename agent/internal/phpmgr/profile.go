package phpmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/pkgs"
	"github.com/NimVor/nimvor/agent/internal/validate"
)

// A PHP profile is the set of PHP versions the server should have and the
// extensions each of them gets (in the manner of cPanel's EasyApache): the
// plan compares it with what is installed, the apply job carries the plan
// out. The plan is always computed again by the agent; the panel only
// sends the wanted profile.

// Profile is the wanted state.
type Profile struct {
	Versions   []string `json:"versions"`
	Extensions []string `json:"extensions"`
}

// Plan item actions.
const (
	ActInstall   = "install"
	ActUpdate    = "update"
	ActUnchanged = "unchanged"
	ActEnable    = "enable"
	ActDisable   = "disable"
	ActRemove    = "remove"
	ActBlocked   = "blocked"
)

// Item is one change (or non-change) of a plan.
type Item struct {
	Action    string   `json:"action"`
	Version   string   `json:"version"`
	Extension string   `json:"extension,omitempty"` // "" for the version itself
	Packages  []string `json:"packages,omitempty"`
	Detail    string   `json:"detail,omitempty"`
}

// PlanResult is the plan and what it needs.
type PlanResult struct {
	Items []Item `json:"items"`
	// Changes: anything to do; Removals: something is removed (the apply
	// needs confirm_removals).
	Changes  bool `json:"changes"`
	Removals bool `json:"removals"`
}

func (p *Profile) normalize() error {
	seenV := map[string]bool{}
	var vs []string
	for _, v := range p.Versions {
		if err := validate.PHPVersion("versions", v); err != nil {
			return err
		}
		if !seenV[v] {
			seenV[v] = true
			vs = append(vs, v)
		}
	}
	if len(vs) > 10 {
		return fmt.Errorf("versions: at most 10")
	}
	sort.Strings(vs)
	seenE := map[string]bool{}
	var es []string
	for _, e := range p.Extensions {
		if _, ok := findExtension(e); !ok {
			return fmt.Errorf("extensions: %s is not an extension WhaCP manages", e)
		}
		if !seenE[e] {
			seenE[e] = true
			es = append(es, e)
		}
	}
	sort.Strings(es)
	p.Versions, p.Extensions = vs, es
	return nil
}

// versionPackages are the packages of version v WhaCP would remove with it
// (only those installed are removed).
func versionPackages(env *host.Env, v string) []string {
	var base []string
	if rhel(env) {
		t := "php" + tag(env, v) + "-php-"
		for _, s := range []string{"fpm", "cli", "common", "opcache", "pdo", "process", "xml"} {
			base = append(base, t+s)
		}
	} else {
		for _, s := range []string{"fpm", "cli", "common", "opcache", "readline"} {
			base = append(base, "php"+v+"-"+s)
		}
	}
	for _, e := range Extensions {
		if p := e.pkg(env, v); p != "" {
			base = append(base, p)
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range base {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// ProfilePlan compares the profile with the server.
func ProfilePlan(ctx context.Context, env *host.Env, p Profile) (PlanResult, error) {
	if err := p.normalize(); err != nil {
		return PlanResult{}, actions.InvalidParams(err.Error())
	}
	installed := map[string]bool{}
	for _, v := range env.Discovery.PHP().Versions {
		if v.FPMBinary != "" {
			installed[v.Version] = true
		}
	}
	available := map[string]bool{}
	for _, v := range Available(ctx, env) {
		available[v] = true
	}
	updates := map[string]pkgs.Upgrade{}
	if ups, err := pkgs.Updates(ctx, env); err == nil {
		for _, u := range ups {
			updates[u.Name] = u
		}
	}
	wantExt := map[string]bool{}
	for _, e := range p.Extensions {
		wantExt[e] = true
	}
	res := PlanResult{Items: []Item{}}
	add := func(it Item) {
		if it.Action != ActUnchanged && it.Action != ActBlocked {
			res.Changes = true
		}
		if it.Action == ActRemove || it.Action == ActDisable {
			res.Removals = true
		}
		res.Items = append(res.Items, it)
	}
	wanted := map[string]bool{}
	for _, v := range p.Versions {
		wanted[v] = true
		if !installed[v] {
			if !available[v] {
				add(Item{Action: ActBlocked, Version: v, Detail: "PHP " + v + " is not offered by this server's repositories (add the PHP repository on the PHP page)."})
				continue
			}
			pk := env.Platform.PHPPackages(v, nil)
			for _, e := range p.Extensions {
				x, _ := findExtension(e)
				if pp := x.pkg(env, v); pp != "" {
					pk = append(pk, pp)
				}
			}
			add(Item{Action: ActInstall, Version: v, Packages: pk, Detail: "PHP " + v + " with PHP-FPM and " + fmt.Sprint(len(p.Extensions)) + " extension(s)"})
			continue
		}
		// The version itself: its core packages' updates.
		var coreUp []string
		for _, pk := range env.Platform.PHPPackages(v, nil) {
			if _, ok := updates[pk]; ok {
				coreUp = append(coreUp, pk)
			}
		}
		if len(coreUp) > 0 {
			add(Item{Action: ActUpdate, Version: v, Packages: coreUp, Detail: "Update available"})
		} else {
			add(Item{Action: ActUnchanged, Version: v, Detail: "Installed"})
		}
		for _, st := range ExtensionStates(ctx, env, v) {
			it := Item{Version: v, Extension: st.Name}
			if st.Package != "" {
				it.Packages = []string{st.Package}
			}
			switch {
			case wantExt[st.Name] && !st.Installed && st.Package == "":
				it.Action, it.Detail = ActBlocked, "Part of PHP "+v+"'s base packages on this system, but not present."
			case wantExt[st.Name] && !st.Installed:
				it.Action = ActInstall
			case wantExt[st.Name] && !st.Enabled:
				it.Action, it.Detail = ActEnable, "Installed but switched off"
			case wantExt[st.Name]:
				it.Action = ActUnchanged
				if u, ok := updates[st.Package]; ok && st.Package != "" {
					it.Action, it.Detail = ActUpdate, u.Current+" → "+u.New
				}
			case !st.Installed:
				continue // not wanted, not there: not listed
			case st.Protected != "":
				it.Action, it.Detail = ActUnchanged, "Kept: "+st.Protected
			case st.Package != "":
				it.Action = ActRemove
			case st.Enabled:
				it.Action, it.Detail, it.Packages = ActDisable, "Part of the base packages: switched off", nil
			default:
				continue
			}
			add(it)
		}
	}
	// Installed versions not in the profile.
	var others []string
	for v := range installed {
		if !wanted[v] {
			others = append(others, v)
		}
	}
	sort.Strings(others)
	for _, v := range others {
		switch users := SitesUsing(ctx, env, v); {
		case panelVersion(env, v):
			add(Item{Action: ActBlocked, Version: v, Detail: "Kept: WhaCP's panel runs on PHP " + v + "."})
		case len(users) > 0:
			add(Item{Action: ActBlocked, Version: v, Detail: "Kept: used by " + strings.Join(users, ", ") + ". Move those websites to another version first."})
		default:
			var pk []string
			for n := range pkgs.InstalledNative(ctx, env, versionPackages(env, v)) {
				pk = append(pk, n)
			}
			sort.Strings(pk)
			add(Item{Action: ActRemove, Version: v, Packages: pk, Detail: "No website uses it"})
		}
	}
	return res, nil
}

// applyProfile carries out a plan (the job).
func applyProfile(ctx context.Context, env *host.Env, log io.Writer, plan PlanResult) error {
	byVersion := map[string][]Item{}
	var order []string
	for _, it := range plan.Items {
		if _, ok := byVersion[it.Version]; !ok {
			order = append(order, it.Version)
		}
		byVersion[it.Version] = append(byVersion[it.Version], it)
	}
	failed := 0
	fail := func(format string, a ...any) {
		failed++
		fmt.Fprintf(log, "  ✗ "+format+"\n", a...)
	}
	for _, v := range order {
		items := byVersion[v]
		var install, update, remove []string
		var enable, disable []Item
		removeVersion := false
		for _, it := range items {
			switch {
			case it.Action == ActInstall:
				install = append(install, it.Packages...)
			case it.Action == ActUpdate:
				update = append(update, it.Packages...)
			case it.Action == ActRemove && it.Extension == "":
				removeVersion, remove = true, it.Packages
			case it.Action == ActRemove:
				remove = append(remove, it.Packages...)
			case it.Action == ActEnable:
				enable = append(enable, it)
			case it.Action == ActDisable:
				disable = append(disable, it)
			}
		}
		if len(install)+len(update)+len(remove)+len(enable)+len(disable) == 0 {
			continue
		}
		fmt.Fprintf(log, "== PHP %s\n", v)
		unit := env.M.FPMUnit(v)
		if removeVersion {
			if users := SitesUsing(ctx, env, v); len(users) > 0 {
				fail("PHP %s is used by %s now; it was kept.", v, strings.Join(users, ", "))
				continue
			}
			env.Systemctl(ctx, "disable", unit)
			env.Systemctl(ctx, "stop", unit)
			if err := pkgs.RemoveNative(ctx, env, log, remove); err != nil {
				fail("PHP %s was not removed: %v", v, err)
				continue
			}
			fmt.Fprintf(log, "  ✓ PHP %s removed\n", v)
			continue
		}
		if len(install) > 0 {
			if err := pkgs.InstallNative(ctx, env, log, install); err != nil {
				fail("PHP %s: installing %s failed: %v", v, strings.Join(install, ", "), err)
				continue
			}
			fmt.Fprintf(log, "  ✓ installed %s\n", strings.Join(install, ", "))
		}
		if len(update) > 0 {
			if err := pkgs.UpgradeNative(ctx, env, log, update); err != nil {
				fail("PHP %s: updating %s failed: %v", v, strings.Join(update, ", "), err)
			} else {
				fmt.Fprintf(log, "  ✓ updated %s\n", strings.Join(update, ", "))
			}
		}
		for _, it := range enable {
			if e, ok := findExtension(it.Extension); ok {
				if err := setModules(ctx, env, v, e.Modules, true); err != nil {
					fail("PHP %s: %s was not switched on: %v", v, e.Name, err)
				}
			}
		}
		for _, it := range disable {
			if e, ok := findExtension(it.Extension); ok {
				if err := setModules(ctx, env, v, e.Modules, false); err != nil {
					fail("PHP %s: %s was not switched off: %v", v, e.Name, err)
				}
			}
		}
		if len(remove) > 0 {
			if err := pkgs.RemoveNative(ctx, env, log, remove); err != nil {
				fail("PHP %s: removing %s failed: %v", v, strings.Join(remove, ", "), err)
			} else {
				fmt.Fprintf(log, "  ✓ removed %s\n", strings.Join(remove, ", "))
			}
		}
		// The version must work afterwards, and start at boot.
		if err := env.FPMTest(ctx, v); err != nil {
			fail("PHP-FPM %s refuses its configuration: %v", v, err)
			continue
		}
		env.Systemctl(ctx, "enable", unit)
		var err error
		if env.UnitActive(ctx, unit) {
			unlock := env.LockWebConfig()
			err = env.ReloadFPM(ctx, v)
			unlock()
		} else {
			err = env.Systemctl(ctx, "start", unit)
		}
		if err != nil {
			fail("PHP-FPM %s did not start or reload: %v", v, err)
			continue
		}
		fmt.Fprintf(log, "  ✓ PHP-FPM %s works and starts at boot\n", v)
	}
	if failed > 0 {
		return fmt.Errorf("%d step(s) failed (see above); the rest was applied", failed)
	}
	return nil
}

func registerProfile(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "php.catalog", Description: "The PHP extensions WhaCP manages (name, description) and those a new version gets by default.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		list := make([]map[string]string, 0, len(Extensions))
		for _, e := range Extensions {
			list = append(list, map[string]string{"name": e.Name, "description": e.Description})
		}
		return map[string]any{"extensions": list, "defaults": DefaultExtensions}, nil
	}})
	r.Register(actions.Action{Name: "php.profile_plan", Description: "Compare a PHP profile (versions and extensions) with the server: what would be installed, updated, switched on or off, removed, left unchanged or kept (read-only).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in Profile
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return ProfilePlan(ctx, env, in)
	}})
	r.Register(actions.Action{Name: "php.profile_apply", Mutating: true, Description: "Apply a PHP profile (job): the plan is computed again; removals need confirm_removals. Versions used by websites or by the panel are never removed.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Profile
			ConfirmRemovals bool `json:"confirm_removals"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		plan, err := ProfilePlan(ctx, env, in.Profile)
		if err != nil {
			return nil, err
		}
		if !plan.Changes {
			return nil, actions.Conflict("Nothing to change: the server already matches this profile.")
		}
		if plan.Removals && !in.ConfirmRemovals {
			return nil, actions.Conflict("This profile removes or switches off PHP versions or extensions; confirm the removals.")
		}
		j := env.Jobs.Start("php.profile_apply", "server:packages", func(ctx context.Context, log io.Writer) (any, error) {
			return nil, applyProfile(ctx, env, log, plan)
		})
		return map[string]any{"job_id": j.ID, "plan": plan}, nil
	}})
}
