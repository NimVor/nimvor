package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/workers"
)

// A Plan is what installing an application would do, worked out without
// changing anything: the requirements and whether each is met, the changes
// that would be made, and anything that conflicts. It is the dry run shown to
// the customer before they confirm.

// Check is one requirement.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// PlanStep is one change the installation would make.
type PlanStep struct {
	Action string `json:"action"`
	Detail string `json:"detail"`
}

// Plan is the dry-run result.
type Plan struct {
	App          string     `json:"app"`
	Name         string     `json:"name"`
	Kind         Kind       `json:"kind"`
	Status       Status     `json:"status"`
	Strategy     string     `json:"strategy"`
	License      string     `json:"license"`
	Integrity    string     `json:"integrity"`
	OK           bool       `json:"ok"`
	Checks       []Check    `json:"checks"`
	Steps        []PlanStep `json:"steps"`
	Conflicts    []string   `json:"conflicts"`
	DiskNeededMB int        `json:"disk_needed_mb"`
	DiskFreeMB   int        `json:"disk_free_mb"`
}

// defaultDiskMB is the free space an installation needs when its manifest
// does not say.
const defaultDiskMB = 300

// diskFree reports the free bytes available to unprivileged accounts under a
// path. A variable so tests can set it.
var diskFree = func(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// BuildPlan works out the plan for r. It only reads: the folder, the running
// jobs, the PHP extensions (a read-only check as the site account) and free
// disk space.
func BuildPlan(ctx context.Context, env *host.Env, r Request, cred *execx.Credential) Plan {
	p := Plan{App: r.App, Checks: []Check{}, Steps: []PlanStep{}, Conflicts: []string{}}
	m, ok := ManifestFor(r.App)
	if !ok {
		p.Conflicts = append(p.Conflicts, "unknown application")
		return p
	}
	p.Name, p.Kind, p.Status, p.Strategy = m.Name, m.Kind, m.Status, m.Strategy
	p.License, p.Integrity = m.License.SPDX, m.Source.Integrity
	check := func(name string, ok bool, detail string) {
		p.Checks = append(p.Checks, Check{Name: name, OK: ok, Detail: detail})
		if !ok {
			p.Conflicts = append(p.Conflicts, name+": "+detail)
		}
	}

	if errs := m.Validate(); len(errs) > 0 {
		check("Catalog entry is valid", false, strings.Join(errs, "; "))
	} else {
		check("Catalog entry is valid", true, "")
	}
	check("Application can be installed", m.Status == StatusEnabled || m.Status == StatusTesting, fmt.Sprintf("its status is %s", m.Status))

	target := filepath.Join(r.Root, r.Path)
	if empty, err := emptyTarget(env, r, target); err != nil {
		check("Folder is empty", false, "the folder could not be read")
	} else {
		check("Folder is empty", empty, fmt.Sprintf("%s already has files", r.Path))
	}
	busy := false
	for _, j := range env.Jobs.Active() {
		if j.Kind == "apps.install" && j.Target == "site:"+r.Domain {
			busy = true
		}
	}
	check("No other installation is running for this website", !busy, "wait for it to finish")

	if m.Runtime.Type == "php" {
		check("PHP version is supported", m.Runtime.Supports(r.PHPVersion), fmt.Sprintf("%s needs PHP %s", m.Name, versionRange(m.Runtime)))
		if len(m.Runtime.Extensions) > 0 && cred != nil {
			missing, err := missingPHPExtensions(ctx, env, "php"+r.PHPVersion, cred, r.Root, m.Runtime.Extensions)
			check("PHP extensions are installed", err == nil, "missing: "+missing)
		}
	}
	for _, pkg := range m.Runtime.OSPackages {
		if pkg == "git" {
			check("git is installed", env.Runner.Available("git"), m.Name+" needs git on the server")
		}
	}
	if m.Service != nil {
		check("The application's port is free", r.Port > 0 && portFree(r.Port), fmt.Sprintf("something already listens on 127.0.0.1:%d", r.Port))
		unit := filepath.Join(env.M.UnitDir, workers.Unit(r.ServiceSlug))
		check("The service name is unused", r.ServiceSlug != "" && !env.Root.Exists(unit), fmt.Sprintf("a service called %s already exists", r.ServiceSlug))
	}
	if m.Compat.MinSQLite != "" && m.Runtime.Type == "python" && cred != nil && r.Runtime != "" {
		if interp, _, err := pythonOf(env, r.Runtime); err == nil {
			if root, err := env.Root.Path(r.Root); err == nil {
				msg := sqliteProblem(m.Name, sqliteOf(ctx, env, cred, root, interp), m.Compat.MinSQLite)
				check("SQLite is new enough", msg == "", msg)
			}
		}
	}
	if len(m.Compat.Arch) > 0 {
		check("CPU architecture is supported", contains(m.Compat.Arch, runtime.GOARCH), fmt.Sprintf("%s runs on %s, this server is %s", m.Name, strings.Join(m.Compat.Arch, ", "), runtime.GOARCH))
	}

	p.DiskNeededMB = m.Compat.MinDiskMB
	if p.DiskNeededMB == 0 {
		p.DiskNeededMB = defaultDiskMB
	}
	if root, err := env.Root.Path(r.Root); err == nil {
		if free, err := diskFree(root); err == nil {
			p.DiskFreeMB = int(free >> 20)
			check("Enough free disk space", p.DiskFreeMB >= p.DiskNeededMB, fmt.Sprintf("%d MB needed, %d MB free", p.DiskNeededMB, p.DiskFreeMB))
		}
	}

	// What would change.
	add := func(action, detail string) { p.Steps = append(p.Steps, PlanStep{action, detail}) }
	if m.Database.Type == "mysql" {
		add("Create database", fmt.Sprintf("a new MySQL/MariaDB database and user (%s) for this website; the password is shown once and stored encrypted", r.DB.Name))
	}
	switch m.Source.Kind {
	case "url":
		add("Download", hostOf(m.Source.URL)+" ("+integrityText(m.Source)+")")
	case "github-release":
		add("Download", "the latest release file matching "+m.Source.AssetRe+" from github.com/"+m.Source.GitHub+" ("+integrityText(m.Source)+")")
	case "composer":
		add("Install with Composer", m.Source.Package+" as the website's account (packages verified by Composer's lock hashes)")
	case "pypi":
		add("Install with pip", m.Source.Package+" into a virtual environment owned by the website's account")
	}
	add("Write files", fmt.Sprintf("into %s, owned by %s (no other website can read them)", r.Path, r.User))
	if m.Service != nil {
		add("Create service", "a supervised service for the application running as "+r.User+", behind the website's reverse proxy")
	}
	switch m.Strategy {
	case "archive", "composer-project":
		add("Finish", "the application's own web installer completes the setup")
	case "wordpress":
		add("Configure", "wp-config.php with the new database, then WordPress is installed with the administrator you chose")
	}
	add("Record", "the installed version and what was created, so it can be backed up, upgraded or removed")

	p.OK = len(p.Conflicts) == 0
	return p
}

func versionRange(r RuntimeSpec) string {
	switch {
	case r.Min != "" && r.Max != "":
		return r.Min + " to " + r.Max
	case r.Min != "":
		return r.Min + " or newer"
	case r.Max != "":
		return "up to " + r.Max
	}
	return "any supported version"
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Hostname()
	}
	return raw
}

func integrityText(s SourceSpec) string {
	switch s.Integrity {
	case "https-only":
		return "over HTTPS; the vendor publishes no checksum to verify"
	case "sha256-sidecar":
		return "verified against the published SHA-256"
	case "sha1-sidecar":
		return "verified against the published SHA-1"
	case "checksums-file":
		return "verified against the release's checksums file"
	case "signature":
		return "verified against the vendor's signature"
	case "composer-lock":
		return "verified by Composer"
	}
	return s.Integrity
}

// registerPlan adds the read-only plan action.
func registerPlan(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "apps.plan", Description: "Dry run: what installing an application into a website would require and change (read-only).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in Request
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := in.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		cred, _, err := env.Credential(in.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		return BuildPlan(ctx, env, in, cred), nil
	}})
	r.Register(actions.Action{Name: "apps.manifests", Description: "The application catalog as validated manifests (read-only).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		type entry struct {
			Manifest Manifest `json:"manifest"`
			Errors   []string `json:"errors"`
		}
		out := []entry{}
		for _, m := range AllManifests() {
			out = append(out, entry{m, m.Validate()})
		}
		return map[string]any{"manifests": out}, nil
	}})
}
