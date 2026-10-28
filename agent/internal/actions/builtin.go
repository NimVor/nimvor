package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"time"

	"github.com/NimVor/nimvor/agent/internal/discovery"
	"github.com/NimVor/nimvor/agent/internal/monitoring"
	"github.com/NimVor/nimvor/agent/internal/platform"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// Deps are the services the built-in actions use.
type Deps struct {
	Version   string
	ProcDir   string
	RootDir   string
	Platform  platform.Platform
	Collector *monitoring.Collector
	Discovery discovery.Service
}

// RegisterBuiltin registers all read-only actions shipped with the agent.
func RegisterBuiltin(r *Registry, d Deps) {
	noParams := func(fn func(ctx context.Context) (any, error)) Handler {
		return func(ctx context.Context, params json.RawMessage) (any, error) {
			var none struct{}
			if err := DecodeParams(params, &none); err != nil {
				return nil, err
			}
			return fn(ctx)
		}
	}

	r.Register(Action{
		Name:        "agent.actions",
		Description: "List the actions this agent supports.",
		Handler: noParams(func(context.Context) (any, error) {
			return r.List(), nil
		}),
	})
	r.Register(Action{
		Name:        "system.info",
		Description: "Operating system, kernel, CPU, network interfaces and uptime.",
		Handler: noParams(func(context.Context) (any, error) {
			return map[string]any{
				"agent_version": d.Version,
				"os":            d.Platform.Info(),
				"system":        monitoring.ReadSystemInfo(d.ProcDir, d.RootDir),
			}, nil
		}),
	})
	r.Register(Action{
		Name:        "system.metrics",
		Description: "Latest CPU, memory, disk, load and network sample.",
		Handler: noParams(func(context.Context) (any, error) {
			return d.Collector.Latest(), nil
		}),
	})
	r.Register(Action{
		Name:        "system.live",
		Description: "Live statistics over half a second: CPU per core and details, memory, load, processes (busiest by CPU and memory, program and user only), network interfaces with rates, disks.",
		Handler: noParams(func(ctx context.Context) (any, error) {
			return monitoring.SampleLive(ctx, d.ProcDir, filepath.Join(d.RootDir, "sys"), d.RootDir, 500*time.Millisecond, func() []monitoring.Disk {
				return d.Collector.Latest().Disks
			}), nil
		}),
	})
	r.Register(Action{
		Name:        "system.metrics_history",
		Description: "Recent metrics history for charts.",
		Handler: noParams(func(context.Context) (any, error) {
			return d.Collector.History(), nil
		}),
	})
	r.Register(Action{
		Name:        "services.status",
		Description: "Status of core services and PHP-FPM units.",
		Handler: noParams(func(ctx context.Context) (any, error) {
			return d.Discovery.Services(ctx)
		}),
	})
	r.Register(Action{
		Name:        "discovery.websites",
		Description: "Existing websites correlated from Nginx, PHP-FPM and certificates.",
		Handler: noParams(func(ctx context.Context) (any, error) {
			return d.Discovery.Websites(ctx), nil
		}),
	})
	r.Register(Action{
		Name:        "discovery.nginx",
		Description: "Raw Nginx virtual host discovery.",
		Handler: noParams(func(ctx context.Context) (any, error) {
			return d.Discovery.Nginx(ctx), nil
		}),
	})
	r.Register(Action{
		Name:        "discovery.php",
		Description: "Installed PHP versions and PHP-FPM pools.",
		Handler: noParams(func(context.Context) (any, error) {
			return d.Discovery.PHP(), nil
		}),
	})
	r.Register(Action{
		Name:        "discovery.databases",
		Description: "MySQL/MariaDB installation and schemas.",
		Handler: noParams(func(ctx context.Context) (any, error) {
			return d.Discovery.Databases(ctx), nil
		}),
	})
	r.Register(Action{
		Name:        "discovery.certificates",
		Description: "Let's Encrypt certificates managed by certbot.",
		Handler: noParams(func(context.Context) (any, error) {
			return d.Discovery.Certificates(), nil
		}),
	})
	r.Register(Action{
		Name:        "discovery.redis",
		Description: "Redis installation and configuration summary.",
		Handler: noParams(func(ctx context.Context) (any, error) {
			return d.Discovery.Redis(ctx), nil
		}),
	})
	r.Register(Action{
		Name:        "discovery.cron",
		Description: "System and user cron jobs (secrets redacted).",
		Handler: noParams(func(context.Context) (any, error) {
			return d.Discovery.Cron(), nil
		}),
	})
	r.Register(Action{
		Name:        "discovery.users",
		Description: "Local system accounts.",
		Handler: noParams(func(context.Context) (any, error) {
			return d.Discovery.Users()
		}),
	})
	r.Register(Action{
		Name:        "discovery.firewall",
		Description: "Detected firewall and whether it is enabled.",
		Handler: noParams(func(context.Context) (any, error) {
			return d.Discovery.Firewall(), nil
		}),
	})
	r.Register(Action{
		Name:        "discovery.inventory",
		Description: "Complete read-only inventory of the server.",
		Handler: noParams(func(ctx context.Context) (any, error) {
			return d.Discovery.Full(ctx), nil
		}),
	})
}
