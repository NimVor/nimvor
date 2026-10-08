package security

import (
	"context"
	"fmt"
	"strings"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// PortSpec is a port (or range "30000:30049") and its protocol.
type PortSpec struct {
	Port  string `json:"port"`
	Proto string `json:"proto"`
}

// PortCovered reports whether an allow rule opens the port from anywhere.
func PortCovered(p PortSpec, rules []Rule) bool {
	return covered(RequiredPort{Port: p.Port, Proto: p.Proto}, rules)
}

// AllowPorts opens several ports from anywhere in one guarded change (a
// component's ports: they are undone together unless confirmed). Ports a
// rule already allows are skipped; nothing to open is not an error.
func (s *Service) AllowPorts(ctx context.Context, ports []PortSpec, comment string) (*Pending, error) {
	if len(ports) == 0 || len(ports) > 20 {
		return nil, actions.InvalidParams("ports: 1 to 20")
	}
	var inputs []RuleInput
	for _, p := range ports {
		in := RuleInput{Action: "allow", Port: p.Port, Proto: p.Proto, Comment: comment}
		if _, err := in.Args(); err != nil {
			return nil, actions.InvalidParams(err.Error())
		}
		inputs = append(inputs, in)
	}
	if err := s.guard.Busy(); err != nil {
		return nil, err
	}
	st, err := Firewall(ctx, s.env)
	if err != nil {
		return nil, actions.Failed("The firewall state could not be read.", err.Error())
	}
	if !st.Installed {
		return nil, actions.Conflict("The firewall is not installed.")
	}
	var todo []RuleInput
	var names []string
	for i, in := range inputs {
		if !PortCovered(ports[i], st.Rules) {
			todo = append(todo, in)
			names = append(names, in.Port+"/"+in.Proto)
		}
	}
	if len(todo) == 0 {
		return nil, nil
	}
	summary := "firewall: allow " + strings.Join(names, ", ")
	if useFirewalld(s.env) {
		var do, undo [][]string
		for _, in := range todo {
			items, err := in.FirewalldArgs()
			if err != nil {
				return nil, actions.InvalidParams(err.Error())
			}
			for _, it := range items {
				do = append(do, firewalldCmds(st.Active, addOrRemove(it, true))...)
				undo = append(undo, firewalldCmds(st.Active, addOrRemove(it, false))...)
			}
		}
		if err := runFirewalld(ctx, s.env, do); err != nil {
			runFirewalld(ctx, s.env, undo)
			return nil, err
		}
		return s.guard.Start(Pending{Kind: "firewall", Summary: summary, Commands: undo})
	}
	var undo [][]string
	for _, in := range todo {
		args, _ := in.Args()
		out, err := s.env.Runner.Run(ctx, "ufw", args...)
		if err != nil {
			for i := len(undo) - 1; i >= 0; i-- {
				s.env.Runner.Run(ctx, "ufw", undo[i]...)
			}
			return nil, actions.Failed(fmt.Sprintf("UFW rejected %s/%s.", in.Port, in.Proto), host.Output(out))
		}
		if !strings.Contains(string(out), "Skipping") {
			undo = append(undo, append([]string{"--force", "delete"}, withoutComment(args)...))
		}
	}
	if len(undo) == 0 {
		return nil, nil
	}
	return s.guard.Start(Pending{Kind: "firewall", Summary: summary, UFW: undo})
}
