package security

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// Service implements the security actions.
type Service struct {
	env   *host.Env
	guard *Guard
}

// NewService builds the service and recovers any unconfirmed change.
func NewService(env *host.Env) *Service {
	AllowProtectionPaths(env)
	return &Service{env: env, guard: NewGuard(env)}
}

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
	r.Register(actions.Action{Name: "firewall.protection", Description: "Firewall protections in the manner of CSF (SYN flood, connection limits, port flood, invalid packets, ping rate): the applied configuration, whether its nftables table is loaded, and what each protection dropped.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return ProtectionStatus(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "firewall.protection_set", Mutating: true, Description: "Apply the firewall protections (nftables table inet nimvor_flood, next to UFW or firewalld; it only drops or rejects). Undone automatically unless confirmed.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in Protection
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		pending, err := s.SetProtection(ctx, in)
		var ae *actions.Error
		if err != nil && !errors.As(err, &ae) {
			return nil, actions.Failed("The firewall protections were not applied.", err.Error())
		}
		return guarded(pending, err)
	}})
	r.Register(actions.Action{Name: "security.integrity", Description: "File integrity check: package-owned files verified with dpkg/rpm and NimVor-owned files compared against a baseline, classified and graded (read-only).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return CheckIntegrity(ctx, env, time.Now()), nil
	}})
	r.Register(actions.Action{Name: "security.integrity_baseline", Mutating: true, Description: "Record the current state of the NimVor-owned files (agent binary, agent configuration) as the integrity baseline. Run after an install or upgrade.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		n, err := WriteIntegrityBaseline(env)
		if err != nil {
			return nil, actions.Failed("The integrity baseline could not be written.", err.Error())
		}
		return map[string]any{"baselined": n}, nil
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
		st.Listening = Listening(env)
		st.Required = RequiredPorts(ctx, env, st.Rules)
		return st, nil
	}})
	r.Register(actions.Action{Name: "firewall.allow_ports", Mutating: true, Description: "Allow several ports from anywhere in one change (a component's ports), auto-reverted unless confirmed; ports already allowed are skipped.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Ports   []PortSpec `json:"ports"`
			Comment string     `json:"comment"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return guarded(s.AllowPorts(ctx, in.Ports, in.Comment))
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
	r.Register(actions.Action{Name: "firewall.set_enabled", Mutating: true, Description: "Enable or disable the firewall (auto-reverted unless confirmed). Enabling first opens SSH, the panel and the agent's remote listener, optionally the ports of listening services (allow_services) and every port for one address (allow_from); undoing removes them again.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
			EnableOptions
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		return guarded(s.SetEnabled(ctx, in.Enabled, in.EnableOptions))
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
	env.Writer.Policy.Patterns = append(env.Writer.Policy.Patterns, fixPaths()...)
	r.Register(actions.Action{Name: "security.host_known", Mutating: true, Description: "Mark the scheduled tasks and services the latest host scan lists as known (pinned by content: a changed file is reported again), or forget the marks.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Forget bool `json:"forget"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		marked, err := MarkPersistKnown(env, in.Forget)
		if err != nil {
			return nil, actions.Conflict(err.Error())
		}
		return map[string]any{"marked": marked}, nil
	}})
	r.Register(actions.Action{Name: "security.fix_kernel", Mutating: true, Description: "Apply the recommended kernel security settings (kept in " + SysctlFile + "; send_redirects is left alone on a router), or undo them (the earlier values are put back).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Undo bool `json:"undo"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if in.Undo {
			return map[string]any{"undone": true}, UndoKernel(ctx, env)
		}
		changed, err := FixKernel(ctx, env)
		if err != nil {
			return nil, err
		}
		if changed == nil {
			changed = []string{}
		}
		return map[string]any{"changed": changed}, nil
	}})
	r.Register(actions.Action{Name: "security.hide_processes", Mutating: true, Description: "Mount /proc with hidepid=2 (each account sees only its own processes; members of " + ProcGroup + " see all), kept in /etc/fstab; or turn it off.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := HideProcesses(ctx, env, in.Enabled); err != nil {
			return nil, err
		}
		return map[string]any{"enabled": in.Enabled}, nil
	}})
	r.Register(actions.Action{Name: "security.auto_updates", Mutating: true, Description: "Turn automatic security updates on (unattended-upgrades, or dnf-automatic with security updates only and no reboot; installed when missing) or off (job).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		j := env.Jobs.Start("security.auto_updates", "server:security", func(ctx context.Context, log io.Writer) (any, error) {
			return map[string]any{"enabled": in.Enabled}, SetAutoUpdates(ctx, env, log, in.Enabled)
		})
		return map[string]any{"job_id": j.ID}, nil
	}})
	r.Register(actions.Action{Name: "ssh.keys", Description: "Administrators (root and members of sudo/admin/wheel) and the public keys in their authorized_keys.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"admins": Admins(env)}, nil
	}})
	r.Register(actions.Action{Name: "ssh.key_add", Mutating: true, Description: "Add a public key (one line, no options; Ed25519, ECDSA, RSA ≥ 2048 bits) to an administrator's ~/.ssh/authorized_keys.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			User string `json:"user"`
			Key  string `json:"key"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		k, err := AddKey(ctx, env, in.User, in.Key)
		if err != nil {
			return nil, err
		}
		return map[string]any{"user": in.User, "key": k}, nil
	}})
	r.Register(actions.Action{Name: "ssh.key_remove", Mutating: true, Description: "Remove a key (by SHA256 fingerprint) from an administrator's authorized_keys; the last one is kept while password logins are off.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			User        string `json:"user"`
			Fingerprint string `json:"fingerprint"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := RemoveKey(ctx, env, in.User, in.Fingerprint); err != nil {
			return nil, err
		}
		return map[string]any{"removed": in.Fingerprint}, nil
	}})
	r.Register(actions.Action{Name: "ssh.logins", Description: "SSH logins of the last seven days: accepted and failed counts, the last of each (read from the journal).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return Logins(ctx, env, time.Now()), nil
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
	r.Register(actions.Action{Name: "fail2ban.configure", Mutating: true, Description: "Write the NimVor jail file, test it and reload fail2ban.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
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
