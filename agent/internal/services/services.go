// Package services starts, stops and reloads an allowlisted set of units.
package services

import (
	"context"
	"encoding/json"
	"path"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/validate"
)

// controllable lists unit patterns WhaCP may operate on.
var controllable = []string{
	"nginx.service", "php*-fpm.service", "php*-php-fpm.service", "mariadb.service", "mysql.service", "mysqld.service",
	"redis-server.service", "redis.service", "memcached.service", "whacp-ftpd.service", "named.service", "postfix.service", "dovecot.service", "whacp-opendkim.service", "cron.service", "crond.service", "fail2ban.service", "ssh.service", "sshd.service",
	"whacp-worker-*.service", "whacp-panel.service",
}

// neverStop protects remote access and the agent itself.
var neverStop = map[string]bool{"ssh.service": true, "sshd.service": true}

// Allowed reports whether unit may be controlled.
func Allowed(unit string) bool {
	for _, p := range controllable {
		if ok, _ := path.Match(p, unit); ok {
			return true
		}
	}
	return false
}

// Register adds services.control.
func Register(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "services.control", Mutating: true, Description: "Start, stop, restart, reload, enable or disable an allowlisted service.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Unit string `json:"unit"`
			Op   string `json:"op"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		if err := validate.Unit("unit", in.Unit); err != nil {
			return nil, host.Invalid(err)
		}
		if !Allowed(in.Unit) {
			return nil, actions.InvalidParams("unit: " + in.Unit + " cannot be controlled from WhaCP")
		}
		switch in.Op {
		case "start", "restart", "reload", "enable":
		case "stop", "disable":
			if neverStop[in.Unit] {
				return nil, actions.InvalidParams("SSH cannot be stopped from WhaCP; you could lock yourself out")
			}
		default:
			return nil, actions.InvalidParams("op must be start, stop, restart, reload, enable or disable")
		}
		// Never (re)load a configuration that does not pass its test.
		if in.Unit == env.M.NginxUnit && (in.Op == "reload" || in.Op == "restart" || in.Op == "start") {
			if err := env.NginxTest(ctx); err != nil {
				return nil, host.FailWithValidation("Nginx was not "+in.Op+"ed", err)
			}
		}
		if in.Unit == "ssh.service" || in.Unit == "sshd.service" {
			if out, err := env.Runner.Run(ctx, "sshd", "-t"); err != nil {
				return nil, actions.Failed("The SSH configuration test failed; SSH was not restarted.", host.Output(out))
			}
		}
		if err := env.Systemctl(ctx, in.Op, in.Unit); err != nil {
			return nil, actions.Failed("The service operation failed.", err.Error())
		}
		return map[string]any{"unit": in.Unit, "op": in.Op, "active": env.UnitActive(ctx, in.Unit)}, nil
	}})
}
