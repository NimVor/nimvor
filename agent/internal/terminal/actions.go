package terminal

import (
	"context"
	"encoding/json"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/ftpmgr"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Register adds the terminal actions and sets Shared.
func Register(r *actions.Registry, e *host.Env) {
	m := NewManager(e)
	Shared = m
	// A changed password, taken-away shell or suspension ends the
	// account's sessions (like sshd sessions, they would otherwise live on).
	ftpmgr.CredentialsChanged = func(user string) {
		m.KillUser(user, "The account's password or shell access changed. Sign in again.")
	}
	RegisterWith(r, m)
}

// RegisterWith adds the actions of manager m.
func RegisterWith(r *actions.Registry, m *Manager) {
	r.Register(actions.Action{Name: "terminal.status", Description: "Web terminal: whether it is enabled, whether root shells are allowed (terminal_root), and the open sessions.", Handler: func(context.Context, json.RawMessage) (any, error) {
		return map[string]any{"enabled": m.Enabled, "root_allowed": m.Env.Cfg.TerminalRoot && m.Enabled, "simulation": m.Env.Cfg.SimulateCommands,
			"sessions": m.List(), "max_sessions": maxSessions, "max_per_account": maxPerAccount}, nil
	}})
	r.Register(actions.Action{Name: "terminal.open", Mutating: true, Description: "Prepare a web terminal session for a website account (its shell access must be jailed or normal) or, when terminal_root is on, a root shell. Returns a single-use ticket valid for a minute, which the browser sends over the WebSocket together with the account's Linux password (checked by the agent with PAM's unix_chkpwd; the panel never sees it). Accounts in privileged groups are refused.", Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
		var p OpenParams
		if err := host.Decode(raw, &p); err != nil {
			return nil, err
		}
		return m.Open(p)
	}})
	r.Register(actions.Action{Name: "terminal.kill", Mutating: true, Description: "End a web terminal session (id), or every session of a panel sign-in (owner).", Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
		var p struct {
			ID    string `json:"id"`
			Owner string `json:"owner"`
		}
		if err := host.Decode(raw, &p); err != nil {
			return nil, err
		}
		if (p.ID == "") == (p.Owner == "") || (p.ID != "" && !idRe.MatchString(p.ID)) || (p.Owner != "" && !ownerRe.MatchString(p.Owner)) {
			return nil, actions.InvalidParams("give a session id or an owner")
		}
		reason := "Closed from the panel."
		if p.Owner != "" {
			reason = "Closed: you signed out of the panel."
		}
		return map[string]any{"closed": m.Kill(p.ID, p.Owner, reason)}, nil
	}})
}
