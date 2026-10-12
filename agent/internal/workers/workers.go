// Package workers runs long-lived application processes (queue workers,
// Node.js or Python apps) as systemd services.
//
// A worker is whacp-worker-<slug>.service running as the website account.
// The command is stored in a root-owned launcher script, readable by the
// website account only, so the unit file itself contains no user text, and
// the service is sandboxed (NoNewPrivileges, PrivateTmp, read-only system
// directories).
package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/runtimemgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/systemd"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Spec describes a worker.
type Spec struct {
	Slug      string            `json:"slug"`
	Name      string            `json:"name"`
	Domain    string            `json:"domain"`
	User      string            `json:"user"`
	Root      string            `json:"root"`
	Directory string            `json:"directory"`
	Command   string            `json:"command"`
	Env       map[string]string `json:"env"`
	Restart   string            `json:"restart"`
	MemoryMB  int               `json:"memory_mb"`
	// Procs is how many identical copies of the command to run (e.g. several
	// queue workers). 1 (or 0) keeps a single service; more add sibling
	// services. The memory limit applies to each copy.
	Procs int `json:"procs"`
	// Runtime selects a WhaCP-managed language runtime for the command, e.g.
	// "node:20.17.0" or "python:3.12"; "" or "system" uses the system's own.
	Runtime string `json:"runtime"`
	Enabled bool   `json:"enabled"`
	// slice is the account's slice when it has resource limits (set by
	// the agent from its own record, never from the request).
	slice string
	// runtimeBin is the runtime's bin directory (resolved from Runtime),
	// prepended to PATH in the launcher; empty for the system runtime.
	runtimeBin string
}

var envKey = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// MaxProcs caps how many copies of one worker may run.
const MaxProcs = 20

// Unit returns the systemd unit name for a slug (the first/only copy).
func Unit(slug string) string { return "whacp-worker-" + slug + ".service" }

// unitN returns the unit name for the n-th copy (n starts at 1). The first
// copy keeps the plain name, so a single-process worker is byte-for-byte
// what earlier versions wrote; extra copies are siblings "-2", "-3"…
func unitN(slug string, n int) string {
	if n <= 1 {
		return Unit(slug)
	}
	return fmt.Sprintf("whacp-worker-%s-%d.service", slug, n)
}

// procUnits returns the unit names for a worker running procs copies.
func procUnits(slug string, procs int) []string {
	if procs < 1 {
		procs = 1
	}
	units := make([]string, 0, procs)
	for n := 1; n <= procs; n++ {
		units = append(units, unitN(slug, n))
	}
	return units
}

func (s *Spec) validate(env *host.Env) error {
	if err := validate.Slug("slug", s.Slug); err != nil {
		return err
	}
	if len(s.Name) == 0 || len(s.Name) > 100 || strings.ContainsAny(s.Name, "\n\r") {
		return &validate.Error{Field: "name", Message: "1–100 characters on one line"}
	}
	if err := validate.Domain("domain", s.Domain); err != nil {
		return err
	}
	if err := validate.Username("user", s.User); err != nil {
		return err
	}
	if s.Root == "" {
		s.Root = filepath.Join(env.Cfg.SitesRoot, s.Domain)
	}
	if err := validate.Within("root", env.Cfg.SitesRoot, s.Root); err != nil {
		return err
	}
	if err := validate.RelPath("directory", s.Directory); err != nil {
		return err
	}
	if err := validate.Within("directory", s.Root, filepath.Join(s.Root, s.Directory)); err != nil {
		return err
	}
	if err := validate.Command("command", s.Command); err != nil {
		return err
	}
	switch s.Restart {
	case "":
		s.Restart = "always"
	case "always", "on-failure", "no":
	default:
		return &validate.Error{Field: "restart", Message: "must be always, on-failure or no"}
	}
	if s.MemoryMB < 0 || s.MemoryMB > 65536 {
		return &validate.Error{Field: "memory_mb", Message: "must be 0 (unlimited) to 65536"}
	}
	if s.Procs == 0 {
		s.Procs = 1
	}
	if s.Procs < 1 || s.Procs > MaxProcs {
		return &validate.Error{Field: "procs", Message: fmt.Sprintf("must be 1 to %d", MaxProcs)}
	}
	bin, err := runtimemgr.BinDir(env, s.Runtime)
	if err != nil {
		return &validate.Error{Field: "runtime", Message: err.Error()}
	}
	s.runtimeBin = bin
	if len(s.Env) > 50 {
		return &validate.Error{Field: "env", Message: "at most 50 variables"}
	}
	for k, v := range s.Env {
		if !envKey.MatchString(k) {
			return &validate.Error{Field: "env", Message: k + " is not a valid variable name"}
		}
		if len(v) > 4096 || strings.ContainsAny(v, "\x00\n\r") {
			return &validate.Error{Field: "env", Message: k + " has an invalid value"}
		}
	}
	return nil
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func (s Spec) script() string {
	var b strings.Builder
	fmt.Fprintf(&b, "#!/bin/sh\n# Managed by WhaCP: worker %q of %s. Do not edit.\n", s.Name, s.Domain)
	if s.runtimeBin != "" {
		fmt.Fprintf(&b, "export PATH=%s:$PATH\n", shQuote(s.runtimeBin))
	}
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "export %s=%s\n", k, shQuote(s.Env[k]))
	}
	fmt.Fprintf(&b, "cd %s || exit 1\nexec %s\n", shQuote(filepath.Join(s.Root, s.Directory)), s.Command)
	return b.String()
}

func (s Spec) unit(script string, instance int) string {
	var b strings.Builder
	label := s.Slug
	if instance > 1 {
		label = fmt.Sprintf("%s (copy %d)", s.Slug, instance)
	}
	fmt.Fprintf(&b, "# Managed by WhaCP. Do not edit.\n[Unit]\nDescription=WhaCP worker %s (%s)\nAfter=network.target\nStartLimitIntervalSec=60\nStartLimitBurst=10\n\n", label, s.Domain)
	fmt.Fprintf(&b, "[Service]\nType=simple\nUser=%s\nGroup=%s\nEnvironment=WHACP_WORKER_INSTANCE=%d\nExecStart=/bin/sh %s\nRestart=%s\nRestartSec=5s\n", s.User, s.User, instance, script, s.Restart)
	// RestrictSUIDSGID is not set, as for the other commands run as the
	// account (see execx.transientHardening): its seccomp filter answers
	// openat2() with ENOSYS, which tar 1.35 on Ubuntu 26.04 does not fall
	// back from, and it only stops the account creating setuid/setgid files,
	// which it can do anyway through cron, PHP-FPM or SFTP and which grant
	// nothing inside the unit (NoNewPrivileges).
	b.WriteString("NoNewPrivileges=yes\nPrivateTmp=yes\nProtectSystem=full\nProtectKernelTunables=yes\nProtectKernelModules=yes\nProtectControlGroups=yes\nLockPersonality=yes\nUMask=0027\n")
	if s.MemoryMB > 0 {
		fmt.Fprintf(&b, "MemoryMax=%dM\n", s.MemoryMB)
	}
	if s.slice != "" {
		fmt.Fprintf(&b, "Slice=%s\n", s.slice)
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// Register adds worker actions.
func Register(r *actions.Registry, env *host.Env) {
	scriptPath := func(slug string) string { return filepath.Join(host.WorkersDir, slug+".sh") }
	// Before 1.0.0 launchers were written to the state directory, where the
	// account could not read them (the worker never started).
	legacyPath := func(slug string) string { return filepath.Join(env.Cfg.StateDir, "workers", slug+".sh") }
	unitFile := func(unit string) string { return filepath.Join(env.M.UnitDir, unit) }
	// presentUnits lists the worker's unit files that exist on disk (the
	// primary plus any extra copies), used to act on all of them without
	// knowing how many copies were configured before.
	presentUnits := func(slug string) []string {
		units := []string{}
		for n := 1; n <= MaxProcs; n++ {
			if u := unitN(slug, n); env.Root.Exists(unitFile(u)) {
				units = append(units, u)
			}
		}
		return units
	}

	r.Register(actions.Action{Name: "workers.apply", Mutating: true, Description: "Create or update a worker service (optionally several copies) and start/stop it.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var s Spec
		if err := host.Decode(p, &s); err != nil {
			return nil, err
		}
		if err := s.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		cred, _, err := env.Credential(s.User)
		if err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		s.slice = env.AccountSlice(s.User)
		want := procUnits(s.Slug, s.Procs)
		// Copies that used to run but are no longer wanted: stop and disable
		// them before their unit files are removed.
		for n := s.Procs + 1; n <= MaxProcs; n++ {
			if u := unitN(s.Slug, n); env.Root.Exists(unitFile(u)) {
				_ = env.Systemctl(ctx, "stop", u)
				_ = env.Systemctl(ctx, "disable", u)
			}
		}
		set := env.Changes("worker-" + s.Slug)
		set.Mkdir(host.WorkersDir, 0o711, agentfs.RootOwner)
		set.Write(scriptPath(s.Slug), []byte(s.script()), 0o640, agentfs.Owner{UID: 0, GID: int(cred.GID)})
		set.Remove(legacyPath(s.Slug))
		for n, u := range want {
			set.Write(unitFile(u), []byte(s.unit(scriptPath(s.Slug), n+1)), 0o644, agentfs.RootOwner)
		}
		for n := s.Procs + 1; n <= MaxProcs; n++ {
			set.Remove(unitFile(unitN(s.Slug, n)))
		}
		a, err := set.Apply()
		if err != nil {
			return nil, host.FailWithValidation("The worker was not saved", err)
		}
		if len(a.Changed) > 0 {
			if err := env.DaemonReload(ctx); err != nil {
				_ = a.Rollback()
				_ = env.DaemonReload(ctx)
				return nil, actions.Failed("systemd rejected the worker unit.", err.Error())
			}
		}
		changed := map[string]bool{}
		for _, c := range a.Changed {
			changed[c] = true
		}
		for _, u := range want {
			if s.Enabled {
				if err := env.Systemctl(ctx, "enable", u); err != nil {
					return nil, actions.Failed("The worker could not be enabled.", err.Error())
				}
				// Restart a copy whose unit changed or is not running; leave an
				// unchanged, already-running copy alone.
				if changed[unitFile(u)] || !env.UnitActive(ctx, u) {
					if err := env.Systemctl(ctx, "restart", u); err != nil {
						return nil, actions.Failed("The worker did not start. Check its log.", err.Error())
					}
				}
			} else {
				_ = env.Systemctl(ctx, "stop", u)
				_ = env.Systemctl(ctx, "disable", u)
			}
		}
		return map[string]any{"unit": Unit(s.Slug), "units": want, "changed": a.Changed, "active": env.UnitActive(ctx, Unit(s.Slug))}, nil
	}})

	r.Register(actions.Action{Name: "workers.remove", Mutating: true, Description: "Stop and remove a worker service and all its copies.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Slug string `json:"slug"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Slug("slug", in.Slug); err != nil {
			return nil, host.Invalid(err)
		}
		set := env.Changes("worker-remove-" + in.Slug)
		for n := 1; n <= MaxProcs; n++ {
			u := unitN(in.Slug, n)
			if !env.Root.Exists(unitFile(u)) {
				continue
			}
			_ = env.Systemctl(ctx, "stop", u)
			_ = env.Systemctl(ctx, "disable", u)
			set.Remove(unitFile(u))
		}
		set.Remove(scriptPath(in.Slug))
		set.Remove(legacyPath(in.Slug))
		if _, err := set.Apply(); err != nil {
			return nil, host.FailWithValidation("The worker was not removed", err)
		}
		_ = env.DaemonReload(ctx)
		return map[string]any{"removed": Unit(in.Slug)}, nil
	}})

	r.Register(actions.Action{Name: "workers.restart", Mutating: true, Description: "Restart a running worker service (all its copies).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Slug string `json:"slug"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Slug("slug", in.Slug); err != nil {
			return nil, host.Invalid(err)
		}
		units := presentUnits(in.Slug)
		if len(units) == 0 {
			return nil, actions.NotFound("The worker service does not exist.")
		}
		for _, u := range units {
			if err := env.Systemctl(ctx, "restart", u); err != nil {
				return nil, actions.Failed("The worker did not restart. Check its log.", err.Error())
			}
		}
		return map[string]any{"active": env.UnitActive(ctx, Unit(in.Slug))}, nil
	}})
	r.Register(actions.Action{Name: "workers.status", Description: "State of worker services (one aggregated entry per worker, over all its copies).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Slugs []string `json:"slugs"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		// Collect every present copy of each requested worker, remembering
		// which slug each unit belongs to.
		all := []string{}
		slugOf := map[string]string{}
		order := []string{}
		for _, slug := range in.Slugs {
			if validate.Slug("slug", slug) != nil {
				continue
			}
			order = append(order, slug)
			for _, u := range presentUnits(slug) {
				all = append(all, u)
				slugOf[u] = slug
			}
		}
		if len(all) == 0 {
			return []map[string]any{}, nil
		}
		svcs, err := systemd.Client{Runner: env.Runner}.Status(ctx, all)
		if err != nil {
			return nil, err
		}
		bySlug := map[string]*aggStatus{}
		for _, svc := range svcs {
			slug := slugOf[svc.Unit]
			if slug == "" {
				continue
			}
			agg := bySlug[slug]
			if agg == nil {
				agg = &aggStatus{slug: slug, unit: Unit(slug)}
				bySlug[slug] = agg
			}
			agg.add(svc)
		}
		out := []map[string]any{}
		for _, slug := range order {
			if agg := bySlug[slug]; agg != nil {
				out = append(out, agg.result())
			}
		}
		return out, nil
	}})
}

// aggStatus collects the systemd state of a worker's copies into one entry.
type aggStatus struct {
	slug, unit  string
	total       int
	running     int
	memory      uint64
	hasMemory   bool
	primary     *systemd.Service
	activeState string
	subState    string
}

func (a *aggStatus) add(s systemd.Service) {
	if s.LoadState == "not-found" {
		return
	}
	a.total++
	if s.Running {
		a.running++
	}
	if s.MemoryBytes != nil {
		a.memory += *s.MemoryBytes
		a.hasMemory = true
	}
	if s.Unit == a.unit {
		cp := s
		a.primary = &cp
	}
}

func (a *aggStatus) result() map[string]any {
	// Prefer the primary copy's state; fall back to "active" when any copy
	// runs. The worker is shown active when at least one copy is up.
	active, sub := "inactive", "dead"
	if a.primary != nil {
		active, sub = a.primary.ActiveState, a.primary.SubState
	}
	if a.running > 0 && active != "active" {
		active, sub = "active", "running"
	} else if a.running == 0 && a.total > 0 && a.primary == nil {
		active = "inactive"
	}
	res := map[string]any{
		"unit":          a.unit,
		"active_state":  active,
		"sub_state":     sub,
		"running":       a.running > 0,
		"procs_total":   a.total,
		"procs_running": a.running,
	}
	if a.hasMemory {
		res["memory_bytes"] = a.memory
	}
	if a.primary != nil && a.primary.ActiveSince != nil {
		res["active_since"] = a.primary.ActiveSince
	}
	return res
}
