package hosting

import (
	"context"
	"encoding/json"
)

// This file ports the read side of app/Services/RuntimeService.php: the
// Node.js and Python runtimes installed on the server and the selection
// strings a worker (or a Node.js/Python website) may use. The install/remove
// actions are agent jobs (Runtimes admin batch).

// RuntimeInfo is one runtime entry of the runtime.status payload.
type RuntimeInfo struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Bin     string `json:"bin"`
	Binary  string `json:"binary"`
}

// RuntimeStatus is the runtime.status agent payload.
type RuntimeStatus struct {
	Arch   string        `json:"arch"`
	Node   []RuntimeInfo `json:"node"`
	Python []RuntimeInfo `json:"python"`
	System []RuntimeInfo `json:"system"`
}

// RuntimeService reads the server's runtimes through the agent.
type RuntimeService struct{ agent AgentCaller }

// NewRuntimeService wraps an agent caller.
func NewRuntimeService(agent AgentCaller) *RuntimeService { return &RuntimeService{agent: agent} }

// Status is the installed and system runtimes (RuntimeService::status).
func (s *RuntimeService) Status(ctx context.Context) (RuntimeStatus, error) {
	var st RuntimeStatus
	raw, err := s.agent.Call(ctx, "runtime.status")
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, err
	}
	return st, nil
}

// Selectable lists the selection strings ("node:20.17.0", "python:3.12") a
// worker or app website may choose: the WhaCP-managed runtimes plus the
// system's Python interpreters (there is no managed Python install path);
// the system Node is not offered (RuntimeService::selectable). Agent failures
// yield an empty list.
func (s *RuntimeService) Selectable(ctx context.Context) []string {
	st, err := s.Status(ctx)
	if err != nil {
		return []string{}
	}
	out := []string{}
	seen := map[string]bool{}
	add := func(sel string) {
		if sel != "" && !seen[sel] {
			seen[sel] = true
			out = append(out, sel)
		}
	}
	for _, rt := range st.Node {
		add("node:" + rt.Version)
	}
	for _, rt := range st.Python {
		add("python:" + rt.Version)
	}
	for _, rt := range st.System {
		if rt.Kind == "python" && rt.Version != "" && rt.Version != "system" {
			add("python:" + rt.Version)
		}
	}
	return out
}
