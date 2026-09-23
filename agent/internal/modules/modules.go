// Package modules registers every modifying (and job-related) action.
package modules

import (
	"context"
	"encoding/json"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
)

// Module is implemented by each feature package.
type Module func(r *actions.Registry, env *host.Env)

// Register adds the core actions and every module.
func Register(r *actions.Registry, env *host.Env, version string, writeEnabled bool, mods ...Module) {
	r.Register(actions.Action{
		Name:        "agent.status",
		Description: "Agent version, write mode and managed layout.",
		Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
			return map[string]any{
				"version":       version,
				"write_enabled": writeEnabled,
				"os":            env.Platform.Info(),
				"sites_root":    env.Cfg.SitesRoot,
				"backup_dir":    env.Cfg.BackupDir,
				"web_user":      env.M.WebUser,
			}, nil
		},
	})
	r.Register(actions.Action{
		Name:        "jobs.get",
		Description: "Status of a background job.",
		Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
			var in struct {
				ID string `json:"id"`
			}
			if err := host.Decode(p, &in); err != nil {
				return nil, err
			}
			j, err := env.Jobs.Get(in.ID)
			if err != nil {
				return nil, actions.NotFound("The job does not exist (it may have expired).")
			}
			return j, nil
		},
	})
	r.Register(actions.Action{
		Name:        "jobs.log",
		Description: "Log output of a background job from an offset.",
		Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
			var in struct {
				ID     string `json:"id"`
				Offset int64  `json:"offset"`
			}
			if err := host.Decode(p, &in); err != nil {
				return nil, err
			}
			j, err := env.Jobs.Get(in.ID)
			if err != nil {
				return nil, actions.NotFound("The job does not exist (it may have expired).")
			}
			text, next, err := env.Jobs.Log(in.ID, in.Offset)
			if err != nil {
				return nil, actions.NotFound("The job log is unavailable.")
			}
			return map[string]any{"job": j, "log": text, "offset": next, "done": j.Status == jobs.Succeeded || j.Status == jobs.Failed}, nil
		},
	})
	r.Register(actions.Action{
		Name:        "jobs.list",
		Description: "Recent background jobs, optionally for one target.",
		Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
			var in struct {
				Target string `json:"target"`
				Limit  int    `json:"limit"`
			}
			if err := host.Decode(p, &in); err != nil {
				return nil, err
			}
			if in.Limit <= 0 || in.Limit > 200 {
				in.Limit = 50
			}
			return env.Jobs.List(in.Target, in.Limit), nil
		},
	})
	for _, m := range mods {
		m(r, env)
	}
}

// StartJob wraps jobs.Start and returns the job reference actions return.
func StartJob(env *host.Env, kind, target string, fn jobs.Func) map[string]any {
	j := env.Jobs.Start(kind, target, fn)
	return map[string]any{"job_id": j.ID, "status": j.Status}
}
