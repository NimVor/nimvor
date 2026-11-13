package intrusion

import (
	"context"
	"encoding/json"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Register adds the intrusion.* actions.
func Register(r *actions.Registry, env *host.Env) {
	AllowPaths(env)
	r.Register(actions.Action{Name: "intrusion.status", Description: "Anti-intrusion: the master switch, the accounts that can be logged or protected (with their switches and counts of programs started and refused), the program blacklist, suggested programs, the operation log and whether the audit system and ACL tools are installed.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return Status(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "intrusion.executions", Description: "Programs started by logged accounts (from the audit log), newest first; optionally for one account.", Handler: func(_ context.Context, p json.RawMessage) (any, error) {
		var in struct {
			User  string `json:"user"`
			Limit int    `json:"limit"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.User != "" && !userRe.MatchString(in.User) {
			return nil, actions.InvalidParams("user: not a valid account name")
		}
		res, err := Executions(env, in.User, in.Limit)
		if err != nil {
			return nil, actions.Failed("The log could not be read.", err.Error())
		}
		return res, nil
	}})
	r.Register(actions.Action{Name: "intrusion.configure", Mutating: true, Description: "Turn anti-intrusion on or off and set, per account, whether its program starts are logged (audit rules, key nimvor-exec) and whether it is protected (execution of the blacklisted programs denied with ACL entries). Accounts not listed have both off.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled  bool               `json:"enabled"`
			Accounts map[string]Setting `json:"accounts"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := Configure(ctx, env, in.Enabled, in.Accounts); err != nil {
			return nil, actions.Failed("Anti-intrusion was not changed completely.", err.Error())
		}
		return Status(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "intrusion.blacklist", Mutating: true, Description: "Replace the program blacklist (names or full paths of programs in the system's program directories). Protected accounts may not run the listed programs; nothing else is restricted.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Programs []string `json:"programs"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := SetBlacklist(ctx, env, in.Programs); err != nil {
			return nil, actions.Failed("The blacklist was not changed completely.", err.Error())
		}
		return Status(ctx, env), nil
	}})
}
