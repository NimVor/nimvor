package sysmgr

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/host"
)

// The graphical login screen (GDM, LightDM, SDDM): a server installed
// with a desktop runs it, with GNOME Shell and its helpers, although
// nobody signs in on a screen. Turning it off sets the default target to
// multi-user (text console) and stops the display manager; turning it on
// again restores graphical.target. Nothing is removed.

// Graphical is the login screen's state.
type Graphical struct {
	DisplayManager string `json:"display_manager"` // unit, "" when none is installed
	Active         bool   `json:"active"`
	DefaultTarget  string `json:"default_target"`
}

var displayManagerRe = regexp.MustCompile(`^(gdm|gdm3|lightdm|sddm|lxdm|xdm)\.service$`)

// ReadGraphical reports the display manager and the default target.
func ReadGraphical(ctx context.Context, env *host.Env) Graphical {
	g := Graphical{}
	if t, err := env.Root.Readlink("/etc/systemd/system/display-manager.service"); err == nil {
		if u := filepath.Base(t); displayManagerRe.MatchString(u) {
			g.DisplayManager = u
		}
	}
	if g.DisplayManager == "" {
		for _, u := range []string{"gdm.service", "gdm3.service", "lightdm.service", "sddm.service"} {
			for _, d := range []string{"/usr/lib/systemd/system", "/lib/systemd/system"} {
				if g.DisplayManager == "" && env.Root.Exists(filepath.Join(d, u)) {
					g.DisplayManager = u
				}
			}
		}
	}
	if g.DisplayManager != "" {
		g.Active = env.UnitActive(ctx, g.DisplayManager)
	}
	if out, err := env.Runner.Run(ctx, "systemctl", "get-default"); err == nil {
		g.DefaultTarget = strings.TrimSpace(string(out))
	}
	return g
}

func registerGraphical(r *actions.Registry, env *host.Env) {
	r.Register(actions.Action{Name: "server.graphical", Description: "Whether the graphical login screen (GDM, LightDM, SDDM) runs, and the default boot target.", Handler: func(ctx context.Context, _ json.RawMessage) (any, error) {
		return ReadGraphical(ctx, env), nil
	}})
	r.Register(actions.Action{Name: "server.graphical_set", Timeout: 3 * time.Minute, Mutating: true, Description: "Turn the graphical login screen off (boot to the text console, stop the display manager) or on again.", Handler: func(ctx context.Context, p json.RawMessage) (any, error) {
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := host.Decode(p, &in); err != nil {
			return nil, err
		}
		g := ReadGraphical(ctx, env)
		if g.DisplayManager == "" {
			return nil, actions.Conflict("No graphical login screen is installed on this server.")
		}
		target, verb := "multi-user.target", "stop"
		if in.Enabled {
			target, verb = "graphical.target", "start"
		}
		if err := run(ctx, env, "Changing the default boot target", "systemctl", "set-default", "--", target); err != nil {
			return nil, err
		}
		if err := env.Systemctl(ctx, verb, g.DisplayManager); err != nil {
			return nil, actions.Failed("The login screen could not be "+map[bool]string{true: "started", false: "stopped"}[in.Enabled]+".", err.Error())
		}
		return ReadGraphical(ctx, env), nil
	}})
}
