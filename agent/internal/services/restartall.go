package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
)

// Restarting every service at once (the top bar's restart button): the
// running services WhaCP may control, in an order that keeps websites
// answering as long as possible (databases and caches first, then PHP,
// mail and the rest, Nginx last), then WhaCP's own agent and panel. SSH is
// left alone (nothing gains from it, a failed start would lock the
// administrator out). Nginx and SSH-like checks apply as for one service.

// restartRank orders units; lower first.
func restartRank(unit string) int {
	switch {
	case strings.HasPrefix(unit, "mariadb") || strings.HasPrefix(unit, "mysql") || strings.HasPrefix(unit, "redis") || unit == "valkey.service" || unit == "memcached.service":
		return 0
	case strings.HasPrefix(unit, "php"):
		return 1
	case unit == "nginx.service":
		return 3
	}
	return 2
}

// RestartPlan is the units restartAll restarts, in order, from the active
// service units.
func RestartPlan(active []string) []string {
	var plan []string
	for _, u := range active {
		if Allowed(u) && !neverStop[u] {
			plan = append(plan, u)
		}
	}
	sort.SliceStable(plan, func(i, j int) bool {
		if a, b := restartRank(plan[i]), restartRank(plan[j]); a != b {
			return a < b
		}
		return plan[i] < plan[j]
	})
	return plan
}

func activeServices(ctx context.Context, env *host.Env) ([]string, error) {
	out, err := env.Runner.Run(ctx, "systemctl", "list-units", "--type=service", "--state=active", "--plain", "--no-legend", "--no-pager")
	if err != nil {
		return nil, fmt.Errorf("systemctl list-units: %s", host.Output(out))
	}
	var units []string
	for _, line := range strings.Split(string(out), "\n") {
		if f := strings.Fields(line); len(f) > 0 && strings.HasSuffix(f[0], ".service") {
			units = append(units, f[0])
		}
	}
	return units, nil
}

func registerRestartAll(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "services.restart_all", Mutating: true, Description: "Restart every running service WhaCP manages (databases and caches, PHP-FPM, mail, DNS, FTP, Nginx last; never SSH), then WhaCP's agent and panel. Runs as a job.", Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
		if err := host.Decode(raw, &struct{}{}); err != nil {
			return nil, err
		}
		active, err := activeServices(ctx, env)
		if err != nil {
			return nil, actions.Failed("The running services could not be listed.", err.Error())
		}
		plan := RestartPlan(active)
		j := env.Jobs.Start("services.restart_all", "server:services", func(ctx context.Context, log io.Writer) (any, error) {
			var failed []string
			for _, u := range plan {
				if err := restartOne(ctx, env, u); err != nil {
					fmt.Fprintf(log, "✗ %s: %v\n", u, err)
					failed = append(failed, u)
					continue
				}
				fmt.Fprintf(log, "✓ %s restarted\n", u)
			}
			// WhaCP's own units that are not in the list above.
			for _, w := range WhacpUnits {
				if w.Delayed || !w.Restartable || Allowed(w.Unit) || !env.UnitActive(ctx, w.Unit) {
					continue
				}
				if out, err := env.Runner.Run(ctx, "systemctl", "restart", "--", w.Unit); err != nil {
					fmt.Fprintf(log, "✗ %s: %s\n", w.Unit, host.Output(out))
					failed = append(failed, w.Unit)
					continue
				}
				fmt.Fprintf(log, "✓ %s restarted\n", w.Unit)
			}
			fmt.Fprintln(log, "The panel and the agent restart in a few seconds.")
			go func() {
				time.Sleep(restartDelay)
				for _, u := range []string{"whacp-panel.service", "whacp-agent.service"} {
					if out, err := env.Runner.Run(context.Background(), "systemctl", "restart", "--no-block", "--", u); err != nil {
						env.Log.Error("restart", "unit", u, "err", err, "out", host.Output(out))
					}
				}
			}()
			res := map[string]any{"restarted": len(plan) - len(failed), "failed": failed}
			if len(failed) > 0 {
				return res, fmt.Errorf("%d service(s) did not restart: %s", len(failed), strings.Join(failed, ", "))
			}
			return res, nil
		})
		return map[string]any{"job_id": j.ID, "services": plan}, nil
	}})
}

// restartOne restarts a unit after the same checks as services.control.
func restartOne(ctx context.Context, env *host.Env, u string) error {
	if u == env.M.NginxUnit || strings.HasPrefix(u, "php") {
		defer env.LockWebConfig()()
	}
	if u == env.M.NginxUnit {
		if err := env.NginxTest(ctx); err != nil {
			return fmt.Errorf("the configuration test failed, not restarted: %w", err)
		}
	}
	return env.Systemctl(ctx, "restart", u)
}
