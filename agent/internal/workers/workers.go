// Package workers runs long-lived application processes (queue workers,
// Node.js or Python apps) as systemd services.
//
// A worker is whacp-worker-<slug>.service running as the website account.
// The command is stored in a root-owned launcher script so the unit file
// itself contains no user text, and the service is sandboxed
// (NoNewPrivileges, PrivateTmp, read-only system directories).
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
	Enabled   bool              `json:"enabled"`
}

var envKey = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// Unit returns the systemd unit name for a slug.
func Unit(slug string) string { return "whacp-worker-" + slug + ".service" }

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

func (s Spec) unit(script string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Managed by WhaCP. Do not edit.\n[Unit]\nDescription=WhaCP worker %s (%s)\nAfter=network.target\nStartLimitIntervalSec=60\nStartLimitBurst=10\n\n", s.Slug, s.Domain)
	fmt.Fprintf(&b, "[Service]\nType=simple\nUser=%s\nGroup=%s\nExecStart=/bin/sh %s\nRestart=%s\nRestartSec=5s\n", s.User, s.User, script, s.Restart)
	b.WriteString("NoNewPrivileges=yes\nPrivateTmp=yes\nProtectSystem=full\nProtectKernelTunables=yes\nProtectKernelModules=yes\nProtectControlGroups=yes\nRestrictSUIDSGID=yes\nLockPersonality=yes\nUMask=0027\n")
	if s.MemoryMB > 0 {
		fmt.Fprintf(&b, "MemoryMax=%dM\n", s.MemoryMB)
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return b.String()
}

// Register adds worker actions.
func Register(r *actions.Registry, env *host.Env) {
	scriptPath := func(slug string) string { return filepath.Join(env.Cfg.StateDir, "workers", slug+".sh") }
	unitPath := func(slug string) string { return filepath.Join(env.M.UnitDir, Unit(slug)) }

	r.Register(actions.Action{Name: "workers.apply", Mutating: true, Description: "Create or update a worker service and start/stop it.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var s Spec
		if err := host.Decode(p, &s); err != nil {
			return nil, err
		}
		if err := s.validate(env); err != nil {
			return nil, host.Invalid(err)
		}
		if _, _, err := env.Credential(s.User); err != nil {
			return nil, actions.InvalidParams("user: " + err.Error())
		}
		set := env.Changes("worker-" + s.Slug)
		set.Mkdir(filepath.Join(env.Cfg.StateDir, "workers"), 0o755, agentfs.RootOwner)
		set.Write(scriptPath(s.Slug), []byte(s.script()), 0o755, agentfs.RootOwner)
		set.Write(unitPath(s.Slug), []byte(s.unit(scriptPath(s.Slug))), 0o644, agentfs.RootOwner)
		a, err := set.Apply()
		if err != nil {
			return nil, host.FailWithValidation("The worker was not saved", err)
		}
		unit := Unit(s.Slug)
		if len(a.Changed) > 0 {
			if err := env.DaemonReload(ctx); err != nil {
				_ = a.Rollback()
				_ = env.DaemonReload(ctx)
				return nil, actions.Failed("systemd rejected the worker unit.", err.Error())
			}
		}
		if s.Enabled {
			verb := "restart"
			if len(a.Changed) == 0 && env.UnitActive(ctx, unit) {
				verb = ""
			}
			if err := env.Systemctl(ctx, "enable", unit); err != nil {
				return nil, actions.Failed("The worker could not be enabled.", err.Error())
			}
			if verb != "" {
				if err := env.Systemctl(ctx, verb, unit); err != nil {
					return nil, actions.Failed("The worker did not start. Check its log.", err.Error())
				}
			}
		} else {
			_ = env.Systemctl(ctx, "stop", unit)
			_ = env.Systemctl(ctx, "disable", unit)
		}
		return map[string]any{"unit": unit, "changed": a.Changed, "active": env.UnitActive(ctx, unit)}, nil
	}})

	r.Register(actions.Action{Name: "workers.remove", Mutating: true, Description: "Stop and remove a worker service.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Slug string `json:"slug"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Slug("slug", in.Slug); err != nil {
			return nil, host.Invalid(err)
		}
		unit := Unit(in.Slug)
		_ = env.Systemctl(ctx, "stop", unit)
		_ = env.Systemctl(ctx, "disable", unit)
		set := env.Changes("worker-remove-" + in.Slug)
		set.Remove(unitPath(in.Slug))
		set.Remove(scriptPath(in.Slug))
		if _, err := set.Apply(); err != nil {
			return nil, host.FailWithValidation("The worker was not removed", err)
		}
		_ = env.DaemonReload(ctx)
		return map[string]any{"removed": unit}, nil
	}})

	r.Register(actions.Action{Name: "workers.status", Description: "State of worker services.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Slugs []string `json:"slugs"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		units := []string{}
		for _, s := range in.Slugs {
			if validate.Slug("slug", s) == nil {
				units = append(units, Unit(s))
			}
		}
		if len(units) == 0 {
			return []systemd.Service{}, nil
		}
		return systemd.Client{Runner: env.Runner}.Status(ctx, units)
	}})
}
