package hulk

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Register adds the hulk.* actions.
func Register(r *actions.Registry, env *host.Env) {
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, execx.NftHulkFile)
	r.Register(actions.Action{Name: "hulk.failures", Description: "Brute force protection: login attempts since a Unix time (at most a day back) from the SSH server, Dovecot, Postfix SMTP AUTH, pure-ftpd (journal) and webmail (Roundcube log). Failures, plus successful SSH logins (for root login notifications).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Since int64 `json:"since"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		evs, available := Failures(ctx, env, in.Since, time.Now())
		if evs == nil {
			evs = []Event{}
		}
		return map[string]any{"events": evs, "journal": available}, nil
	}})
	r.Register(actions.Action{Name: "hulk.sync", Mutating: true, Description: "Brute force protection: enforce the blocked addresses (login services only, or every port), blacklisted countries' address ranges (login services) and the whitelist with the nftables table inet whacp_hulk; or remove the table.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in State
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		changed, err := Sync(ctx, env, in, time.Now())
		if err != nil {
			return nil, actions.Failed("The brute force protection rules were not applied.", err.Error())
		}
		return map[string]any{"changed": changed, "active": loaded(ctx, env)}, nil
	}})
	r.Register(actions.Action{Name: "hulk.status", Description: "Brute force protection: whether nftables is available and the table is loaded.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"available": env.Runner.Available("nft"), "active": env.Runner.Available("nft") && loaded(ctx, env)}, nil
	}})
}
