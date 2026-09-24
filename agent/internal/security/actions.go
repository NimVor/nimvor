package security

import (
	"context"
	"encoding/json"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Service implements the security actions.
type Service struct {
	env   *host.Env
	guard *Guard
}

// NewService builds the service and recovers any unconfirmed change.
func NewService(env *host.Env) *Service { return &Service{env: env, guard: NewGuard(env)} }

func guarded(p *Pending, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	if p == nil {
		return map[string]any{"changed": false}, nil
	}
	return map[string]any{"changed": true, "pending": p}, nil
}

// Register adds security actions.
func Register(r *actions.Registry, env *host.Env) {
	s := NewService(env)
	r.Register(actions.Action{Name: "security.audit", Description: "Security checks with findings and remedies (read-only).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return Audit(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "security.pending", Description: "The security change awaiting confirmation, if any.", Handler: func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"pending": s.guard.Current()}, nil
	}})
	r.Register(actions.Action{Name: "security.confirm", Mutating: true, Description: "Keep a firewall/SSH change (otherwise it is rolled back automatically).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return map[string]any{"confirmed": in.ID}, s.guard.Confirm(in.ID)
	}})
	r.Register(actions.Action{Name: "security.undo", Mutating: true, Description: "Roll back the pending firewall/SSH change now.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			ID string `json:"id"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := s.guard.Undo(in.ID); err != nil {
			if _, ok := err.(*actions.Error); ok {
				return nil, err
			}
			return nil, actions.Failed("The rollback did not complete cleanly.", err.Error())
		}
		return map[string]any{"rolled_back": in.ID}, nil
	}})

	r.Register(actions.Action{Name: "firewall.status", Description: "UFW state, defaults and rules.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		st, err := Firewall(ctx, env)
		if err != nil {
			return nil, actions.Failed("The firewall state could not be read.", err.Error())
		}
		return st, nil
	}})
	r.Register(actions.Action{Name: "firewall.rule_add", Mutating: true, Description: "Add a UFW rule (auto-reverted unless confirmed).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in RuleInput
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return guarded(s.AddRule(ctx, in))
	}})
	r.Register(actions.Action{Name: "firewall.rule_delete", Mutating: true, Description: "Delete a UFW rule (auto-reverted unless confirmed).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Spec string `json:"spec"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return guarded(s.DeleteRule(ctx, in.Spec))
	}})
	r.Register(actions.Action{Name: "firewall.set_enabled", Mutating: true, Description: "Enable or disable UFW (auto-reverted unless confirmed).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return guarded(s.SetEnabled(ctx, in.Enabled))
	}})

	r.Register(actions.Action{Name: "ssh.status", Description: "Effective SSH server settings and key-based access.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		cfg, err := SSHEffective(ctx, env)
		if err != nil {
			return nil, actions.Failed("The SSH configuration could not be read.", err.Error())
		}
		rootKeys, admins := KeyAccess(env)
		keys := []string{"port", "permitrootlogin", "passwordauthentication", "pubkeyauthentication", "maxauthtries", "x11forwarding", "kbdinteractiveauthentication"}
		eff := map[string]string{}
		for _, k := range keys {
			eff[k] = cfg[k]
		}
		managed := ""
		if b, err := env.Root.ReadFile(env.M.SSHDropIn); err == nil {
			managed = string(b)
		}
		if admins == nil {
			admins = []string{}
		}
		return map[string]any{"effective": eff, "root_has_keys": rootKeys, "admins_with_keys": admins, "managed_file": env.M.SSHDropIn, "managed_config": managed}, nil
	}})
	r.Register(actions.Action{Name: "ssh.configure", Mutating: true, Description: "Harden the SSH server (validated with sshd -t, auto-reverted unless confirmed).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in SSHSettings
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return guarded(s.ConfigureSSH(ctx, in))
	}})

	r.Register(actions.Action{Name: "fail2ban.status", Description: "fail2ban jails and banned addresses.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return Fail2ban(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "fail2ban.configure", Mutating: true, Description: "Write the WhaCP jail file, test it and reload fail2ban.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in Fail2banSettings
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, s.ConfigureFail2ban(ctx, in)
	}})
	r.Register(actions.Action{Name: "fail2ban.unban", Mutating: true, Description: "Unban an IP address from a jail.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Jail string `json:"jail"`
			IP   string `json:"ip"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return map[string]any{"unbanned": in.IP}, s.Unban(ctx, in.Jail, in.IP)
	}})
}
