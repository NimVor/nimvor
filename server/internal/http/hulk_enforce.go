package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"os/exec"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/audit"
	"github.com/mchkhawarshahzad/whacp/server/internal/notify"
	"github.com/mchkhawarshahzad/whacp/server/internal/security"
)

// hulkCommandTimeout bounds an administrator's block command.
const hulkCommandTimeout = 15 * time.Second

// hulkHooks connects brute force protection to the panel's notifications,
// audit log and the administrator's "command to run when an address is
// blocked" (run as the panel's own account, never root, 15 seconds at most,
// every variable shell-quoted).
func (s *Server) hulkHooks() security.HulkHooks {
	notes := notify.NewRepository(s.db)
	return security.HulkHooks{
		Notify: func(ctx context.Context, severity, title, body, link, key string) {
			_, _ = notes.Store(ctx, sql.NullInt64{}, "security", severity, title, body, link, key)
		},
		Audit: func(ctx context.Context, action, result string, details map[string]any) {
			s.audit.Record(ctx, audit.Entry{Username: "system", Action: action, TargetType: "server", TargetID: "hulk", Result: result, Details: details})
		},
		RunCommand: func(ctx context.Context, command string, vars map[string]string) (int, error) {
			ctx, cancel := context.WithTimeout(ctx, hulkCommandTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", security.ExpandCommand(command, vars))
			cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
			cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
			err := cmd.Run()
			if ctx.Err() != nil {
				return -1, ctx.Err()
			}
			if ee, ok := err.(*exec.ExitError); ok {
				return ee.ExitCode(), nil
			}
			return 0, err
		},
	}
}

// hulkAuth is brute force protection as WhaCP sign-ins see it.
type hulkAuth struct{ s *Server }

func (a hulkAuth) Refusal(ctx context.Context, username, ip string) string {
	return a.s.hulk.LoginRefusal(ctx, username, ip, a.s.hulkHooks())
}

func (a hulkAuth) Failure(ctx context.Context, username, ip string) {
	if a.s.hulk.RecordPanelFailure(ctx, username, ip, a.s.hulkHooks()) > 0 {
		a.s.hulkSyncQuietly(ctx, a.s.hulk, "brute force protection: blocks not applied yet")
	}
}

// hulkRun is the scheduler pass (HulkService::run): ask the agent for the
// failed logins of the login services, store and judge them, drop old data
// and push the blocks to the server.
func (s *Server) hulkRun(ctx context.Context) []string {
	h := s.hulk
	if !h.Enabled(ctx) {
		if v, _ := s.settings.String(ctx, "hulk_synced", ""); v != "off" {
			if _, err := s.agent.Call(ctx, "hulk.sync", map[string]any{"enabled": false}, 60*time.Second); err != nil {
				return []string{"brute force protection not checked: " + agentMessage(err)}
			}
			_ = s.settings.Set(ctx, "hulk_synced", "off", 0)
		}
		return nil
	}
	hk := s.hulkHooks()
	var log []string
	// The country database, once a month, while countries are marked or one is loaded.
	geo := security.NewGeo(s.db)
	if marks, _ := h.Countries(ctx); len(marks) > 0 || geoLoadedOK(ctx, geo) {
		if n, err := geo.Refresh(ctx, s.settings, time.Now()); err != nil {
			log = append(log, "brute force protection: the country database was not updated: "+err.Error())
		} else if n > 0 {
			log = append(log, "brute force protection: country database updated ("+itoa(n)+" ranges)")
		}
	}
	raw, err := s.agent.Call(ctx, "hulk.failures", map[string]any{"since": h.FailureSince(ctx, 1, hk)}, 120*time.Second)
	if err != nil {
		return append(log, "brute force protection not checked: "+agentMessage(err))
	}
	var res struct {
		Events []security.HulkEvent `json:"events"`
	}
	_ = json.Unmarshal(raw, &res)
	if n := h.Evaluate(ctx, h.Store(ctx, 1, res.Events), hk); n > 0 {
		log = append(log, "brute force protection: "+itoa(n)+" new block(s)")
	}
	h.Prune(ctx, hk)
	if err := s.hulkSync(ctx, h); err != nil {
		return append(log, "brute force protection: blocks not applied: "+agentMessage(err))
	}
	_ = s.settings.Set(ctx, "hulk_synced", "on", 0)
	return log
}

func geoLoadedOK(ctx context.Context, g *security.Geo) bool {
	ok, _ := g.Loaded(ctx)
	return ok
}
