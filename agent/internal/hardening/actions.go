package hardening

import (
	"context"
	"encoding/json"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/egress"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// Register adds the hardening.* actions.
func Register(r *actions.Registry, env *host.Env) {
	AllowPaths(env)
	egress.AllowPaths(env)
	r.Register(actions.Action{Name: "hardening.status", Description: "Server hardening: compiler access (programs, allowed users), traceroute, shell fork bomb protection, SMTP restrictions and website accounts' outgoing connections.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{
			"compilers":  CompilerStatus(env),
			"traceroute": TracerouteStatus(env),
			"forkbomb":   map[string]any{"enabled": ForkBombEnabled(env), "file": ForkBombFile, "soft": ForkSoft, "hard": ForkHard},
			"smtp":       SMTPStatus(ctx, env),
			"egress":     egress.Status(ctx, env),
		}, nil
	}})
	r.Register(actions.Action{Name: "hardening.egress", Mutating: true, Description: "Website accounts' outgoing connections (nftables table loaded at boot): metadata_blocked (cloud metadata services, on by default), block_private (private and link-local networks), tcp_ports/udp_ports (when set, the only remote ports allowed).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			MetadataBlocked bool  `json:"metadata_blocked"`
			BlockPrivate    bool  `json:"block_private"`
			TCPPorts        []int `json:"tcp_ports"`
			UDPPorts        []int `json:"udp_ports"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		s := egress.Settings{MetadataAllowed: !in.MetadataBlocked, BlockPrivate: in.BlockPrivate, TCPPorts: in.TCPPorts, UDPPorts: in.UDPPorts}
		if err := s.Normalize(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		if err := egress.Set(ctx, env, s); err != nil {
			return nil, actions.Failed("Outgoing connection limits were not changed.", err.Error())
		}
		return egress.Status(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "hardening.compilers", Mutating: true, Description: "Restrict the C/C++ compilers and the assembler to root and the whacp-compiler group (mode 0750), with these members; or open them again (earlier owner and mode restored).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Restricted bool     `json:"restricted"`
			Users      []string `json:"users"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if len(in.Users) > 1000 {
			return nil, actions.InvalidParams("users: at most 1000")
		}
		for _, u := range in.Users {
			if err := validate.Username("users", u); err != nil {
				return nil, host.Invalid(err)
			}
			acct, err := env.LookupUser(u)
			if err != nil {
				return nil, actions.InvalidParams("users: there is no account " + u)
			}
			if acct.UID < 1000 {
				return nil, actions.InvalidParams("users: " + u + " is a system account")
			}
		}
		if err := SetCompilers(ctx, env, in.Restricted, in.Users); err != nil {
			return nil, actions.Failed("Compiler access was not changed completely.", err.Error())
		}
		return CompilerStatus(env), nil
	}})
	r.Register(actions.Action{Name: "hardening.traceroute", Mutating: true, Description: "Enable traceroute for everyone (earlier modes restored) or limit it to root (mode 0700).", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := SetTraceroute(ctx, env, in.Enabled); err != nil {
			return nil, actions.Failed("Traceroute was not changed completely.", err.Error())
		}
		return TracerouteStatus(env), nil
	}})
	r.Register(actions.Action{Name: "hardening.forkbomb", Mutating: true, Description: "Shell fork bomb protection: cap the processes of accounts (UID 1000 and up) in their sessions with pam_limits, or remove the cap.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := SetForkBomb(env, in.Enabled); err != nil {
			return nil, actions.Failed("Fork bomb protection was not changed.", err.Error())
		}
		return map[string]any{"enabled": ForkBombEnabled(env)}, nil
	}})
	r.Register(actions.Action{Name: "hardening.smtp", Mutating: true, Description: "SMTP restrictions: only root, the mail server, Mailman and the panel may connect to remote SMTP servers (nftables table loaded at boot); or remove the restriction.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := SetSMTP(ctx, env, in.Enabled); err != nil {
			return nil, actions.Failed("SMTP restrictions were not changed.", err.Error())
		}
		return SMTPStatus(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "hardening.reapply", Mutating: true, Description: "Apply compiler and traceroute restrictions again (after package updates reset modes).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if err := Reapply(ctx, env); err != nil {
			return nil, actions.Failed("Restrictions could not be applied again.", err.Error())
		}
		return map[string]any{"ok": true}, nil
	}})
}
