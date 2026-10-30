// Package nginxmgr manages the Nginx service and a small set of safe,
// server-wide tuning directives.
//
// The tuning is written as a single managed drop-in in conf.d (the http
// context), so it never touches the main nginx.conf or any website's vhost.
// Every change is validated with "nginx -t" and rolled back if the
// configuration would become invalid, so a bad value can never take the web
// server down.
package nginxmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// TuningFile is the managed drop-in's name under conf.d.
const TuningFile = "zz-nimvor-tuning.conf"

// Units are the Nginx service names.
var Units = []string{"nginx.service", "nginx"}

// Tuning holds the http-context directives NimVor manages. All are safe in a
// conf.d drop-in and bounded to sensible ranges.
type Tuning struct {
	ClientMaxBodyMB   int  `json:"client_max_body_mb"`  // client_max_body_size (MB)
	KeepaliveTimeout  int  `json:"keepalive_timeout"`   // seconds
	ClientBodyTimeout int  `json:"client_body_timeout"` // seconds
	SendTimeout       int  `json:"send_timeout"`        // seconds
	Gzip              bool `json:"gzip"`
	HideVersion       bool `json:"hide_version"` // server_tokens off
}

func (t Tuning) validate() error {
	if t.ClientMaxBodyMB < 1 || t.ClientMaxBodyMB > 4096 {
		return actions.InvalidParams("client_max_body_mb: 1 to 4096")
	}
	for _, v := range []struct {
		name string
		val  int
	}{{"keepalive_timeout", t.KeepaliveTimeout}, {"client_body_timeout", t.ClientBodyTimeout}, {"send_timeout", t.SendTimeout}} {
		if v.val < 1 || v.val > 3600 {
			return actions.InvalidParams(v.name + ": 1 to 3600 seconds")
		}
	}
	return nil
}

func (t Tuning) conf() string {
	var b strings.Builder
	b.WriteString("# Managed by NimVor (Server → Nginx). Do not edit.\n")
	fmt.Fprintf(&b, "client_max_body_size %dm;\n", t.ClientMaxBodyMB)
	fmt.Fprintf(&b, "keepalive_timeout %ds;\n", t.KeepaliveTimeout)
	fmt.Fprintf(&b, "client_body_timeout %ds;\n", t.ClientBodyTimeout)
	fmt.Fprintf(&b, "send_timeout %ds;\n", t.SendTimeout)
	if t.HideVersion {
		b.WriteString("server_tokens off;\n")
	} else {
		b.WriteString("server_tokens on;\n")
	}
	if t.Gzip {
		b.WriteString("gzip on;\ngzip_vary on;\ngzip_proxied any;\ngzip_comp_level 5;\n")
		b.WriteString("gzip_types text/plain text/css application/json application/javascript application/x-javascript text/xml application/xml application/rss+xml image/svg+xml;\n")
	} else {
		b.WriteString("gzip off;\n")
	}
	return b.String()
}

func installed(env *host.Env) bool { return env.Runner.Available("nginx") }

// Register adds the Nginx actions.
func Register(r *actions.Registry, env *host.Env) {
	confPath := filepath.Join(filepath.Dir(env.M.NginxAvailable), "conf.d", TuningFile)

	r.Register(actions.Action{Name: "nginx.status", Description: "Nginx: installed, running and whether the current configuration is valid.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		out := map[string]any{"installed": installed(env), "running": false, "config_ok": false, "config_error": "", "tuning": env.Root.Exists(confPath)}
		if !installed(env) {
			return out, nil
		}
		for _, u := range Units {
			if env.UnitActive(ctx, u) {
				out["running"] = true
			}
		}
		if err := env.NginxTest(ctx); err != nil {
			out["config_error"] = host.Output([]byte(err.Error()))
		} else {
			out["config_ok"] = true
		}
		return out, nil
	}})

	r.Register(actions.Action{Name: "nginx.test", Description: "Validate the Nginx configuration (nginx -t).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !installed(env) {
			return nil, actions.Conflict("Nginx is not installed.")
		}
		if err := env.NginxTest(ctx); err != nil {
			return map[string]any{"ok": false, "output": err.Error()}, nil
		}
		return map[string]any{"ok": true}, nil
	}})

	r.Register(actions.Action{Name: "nginx.reload", Mutating: true, Description: "Reload Nginx (after validating the configuration).", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		if !installed(env) {
			return nil, actions.Conflict("Nginx is not installed.")
		}
		if err := env.NginxTest(ctx); err != nil {
			return nil, actions.Failed("Nginx was not reloaded: the configuration is invalid.", err.Error())
		}
		if err := env.ReloadNginx(ctx); err != nil {
			return nil, actions.Failed("Nginx could not be reloaded.", err.Error())
		}
		return map[string]any{"reloaded": true}, nil
	}})

	r.Register(actions.Action{Name: "nginx.tuning_apply", Mutating: true, Description: "Apply NimVor's server-wide Nginx tuning (a managed conf.d drop-in), validated with nginx -t and rolled back if invalid.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var t Tuning
		if err := host.Decode(p, &t); err != nil {
			return nil, err
		}
		if err := t.validate(); err != nil {
			return nil, host.Invalid(err)
		}
		if !installed(env) {
			return nil, actions.Conflict("Nginx is not installed.")
		}
		set := env.Changes("nginx-tuning")
		set.Write(confPath, []byte(t.conf()), 0o644, agentfs.RootOwner)
		// Nginx refuses a directive set twice in the http block ("is duplicate"), and the distribution's
		// nginx.conf already sets some (Debian and Ubuntu: gzip on). Those lines are commented out, in the
		// same change, so the managed file is the one place that decides them.
		mainConf := filepath.Join(filepath.Dir(env.M.NginxAvailable), "nginx.conf")
		if b, err := env.Root.ReadFile(mainConf); err == nil {
			if moved, changed := releaseTunedDirectives(string(b)); changed {
				set.Write(mainConf, []byte(moved), 0o644, agentfs.RootOwner)
			}
		}
		a, err := set.Apply()
		if err != nil {
			return nil, host.FailWithValidation("The Nginx tuning was not saved", err)
		}
		if err := env.NginxTest(ctx); err != nil {
			_ = a.Rollback()
			_ = env.ReloadNginx(ctx)
			return nil, host.FailWithValidation("The tuning made the Nginx configuration invalid and was rolled back.", err)
		}
		if err := env.ReloadNginx(ctx); err != nil {
			return nil, actions.Failed("Nginx could not be reloaded.", err.Error())
		}
		return map[string]any{"applied": true}, nil
	}})
}

// tunedDirectives are the directives Tuning.conf writes into the http block.
var tunedDirectives = map[string]bool{
	"client_max_body_size": true, "keepalive_timeout": true, "client_body_timeout": true, "send_timeout": true,
	"server_tokens": true, "gzip": true, "gzip_vary": true, "gzip_proxied": true, "gzip_comp_level": true, "gzip_types": true,
}

// releaseTunedDirectives comments out, in nginx.conf's own http block (not in server, location or other
// blocks inside it), the directives the managed tuning file now sets, and reports whether it changed
// anything. A directive that continues over several lines is commented to its end. Already commented
// lines are left as they are, so running it again changes nothing.
func releaseTunedDirectives(conf string) (string, bool) {
	lines := strings.Split(conf, "\n")
	depth, httpDepth := 0, -1
	inDirective, changed := false, false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		code := t
		if h := strings.Index(code, "#"); h >= 0 {
			code = strings.TrimSpace(code[:h])
		}
		if inDirective {
			lines[i] = "# NimVor: moved to conf.d/" + TuningFile + ": " + l
			if strings.HasSuffix(code, ";") {
				inDirective = false
			}
			continue
		}
		if httpDepth < 0 && depth == 0 && strings.HasPrefix(code, "http") && strings.HasSuffix(code, "{") && strings.TrimSpace(strings.TrimSuffix(code, "{")) == "http" {
			httpDepth = 1
		} else if httpDepth > 0 && depth == httpDepth {
			if f := strings.Fields(code); len(f) > 1 && tunedDirectives[f[0]] {
				lines[i] = "# NimVor: moved to conf.d/" + TuningFile + ": " + l
				changed = true
				inDirective = !strings.HasSuffix(code, ";")
				continue
			}
		}
		depth += strings.Count(code, "{") - strings.Count(code, "}")
		if httpDepth > 0 && depth < httpDepth {
			httpDepth = -1
		}
	}
	return strings.Join(lines, "\n"), changed
}
