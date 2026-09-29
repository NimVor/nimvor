package services

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/systemd"
)

// WhaCP's own services: their state, and restarts. The agent and the
// panel are restarted a moment after the answer is sent (restarting them
// at once would cut off the request that asked for it).

// WhacpUnit is one of WhaCP's units.
type WhacpUnit struct {
	Key         string `json:"key"`
	Unit        string `json:"unit"`
	Label       string `json:"label"`
	Restartable bool   `json:"restartable"`
	Delayed     bool   `json:"delayed"`
}

// WhacpUnits lists them.
var WhacpUnits = []WhacpUnit{
	{"agent", "whacp-agent.service", "Agent (privileged actions)", true, true},
	{"panel", "whacp-panel.service", "Panel (web interface)", true, true},
	{"scheduler", "whacp-scheduler.timer", "Scheduler (every 5 minutes)", true, false},
	{"greylist", "whacp-greylist.service", "Greylisting policy service", true, false},
	{"mailrate", "whacp-mailrate.service", "Outgoing mail limit service", true, false},
	{"dkim", "whacp-opendkim.service", "DKIM signing (OpenDKIM)", true, false},
	{"ftpd", "whacp-ftpd.service", "FTP server", true, false},
}

// restartDelay lets the answer reach the panel first.
var restartDelay = 2 * time.Second

func registerWhacp(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "whacp.services", Description: "State of WhaCP's own services (agent, panel, scheduler, greylisting, DKIM, FTP).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		units := make([]string, len(WhacpUnits))
		for i, u := range WhacpUnits {
			units[i] = u.Unit
		}
		st, err := systemd.Client{Runner: env.Runner}.Status(ctx, units)
		if err != nil {
			return nil, actions.Failed("The services could not be read.", err.Error())
		}
		byUnit := map[string]systemd.Service{}
		for _, s := range st {
			byUnit[s.Unit] = s
		}
		out := []map[string]any{}
		for _, u := range WhacpUnits {
			s, ok := byUnit[u.Unit]
			if !ok || s.LoadState == "not-found" {
				continue // not installed or not set up
			}
			out = append(out, map[string]any{"key": u.Key, "unit": u.Unit, "label": u.Label, "restartable": u.Restartable, "status": s})
		}
		return map[string]any{"services": out}, nil
	}})

	r.Register(actions.Action{Name: "whacp.restart", Mutating: true, Description: "Restart one of WhaCP's services (key: agent, panel, scheduler, greylist, dkim, ftpd). The agent and the panel restart two seconds after answering.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in struct {
			Key string `json:"key"`
		}
		if err := host.Decode(raw, &in); err != nil {
			return nil, err
		}
		var unit *WhacpUnit
		for i := range WhacpUnits {
			if WhacpUnits[i].Key == in.Key {
				unit = &WhacpUnits[i]
			}
		}
		if unit == nil || !unit.Restartable {
			return nil, actions.InvalidParams("key: agent, panel, scheduler, greylist, dkim or ftpd")
		}
		if unit.Delayed {
			u := unit.Unit
			go func() {
				time.Sleep(restartDelay)
				// --no-block: systemd restarts the unit (this agent too) on its own.
				if out, err := env.Runner.Run(context.Background(), "systemctl", "restart", "--no-block", "--", u); err != nil {
					env.Log.Error("restart", "unit", u, "err", err, "out", host.Output(out))
				}
			}()
			return map[string]any{"unit": unit.Unit, "scheduled": true}, nil
		}
		if out, err := env.Runner.Run(ctx, "systemctl", "restart", "--", unit.Unit); err != nil {
			return nil, actions.Failed(unit.Label+" did not restart.", host.Output(out))
		}
		return map[string]any{"unit": unit.Unit, "scheduled": false, "active": env.UnitActive(ctx, unit.Unit)}, nil
	}})
}
