// Command whacp-agent is the privileged WhaCP server agent.
//
// It listens on a local Unix socket and executes a fixed set of named
// actions on behalf of the WhaCP panel.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/agent/internal/actions"
	"github.com/mchkhawarshahzad/whacp/agent/internal/agentfs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/config"
	"github.com/mchkhawarshahzad/whacp/agent/internal/discovery"
	"github.com/mchkhawarshahzad/whacp/agent/internal/execx"
	"github.com/mchkhawarshahzad/whacp/agent/internal/filesystem"
	"github.com/mchkhawarshahzad/whacp/agent/internal/host"
	"github.com/mchkhawarshahzad/whacp/agent/internal/jobs"
	"github.com/mchkhawarshahzad/whacp/agent/internal/logging"
	"github.com/mchkhawarshahzad/whacp/agent/internal/modules"
	"github.com/mchkhawarshahzad/whacp/agent/internal/monitoring"
	"github.com/mchkhawarshahzad/whacp/agent/internal/platform"
	"github.com/mchkhawarshahzad/whacp/agent/internal/server"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "0.5.0-dev"

func main() {
	os.Exit(run())
}

func run() int {
	cfgPath := flag.String("config", config.DefaultPath, "path to the agent configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	checkConfig := flag.Bool("check-config", false, "validate the configuration and token, then exit")
	showPin := flag.Bool("pin", false, "print the TLS public-key pin for remote panels (creates the certificate if needed), then exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("whacp-agent", version)
		return 0
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		return 2
	}
	log := logging.New(os.Stdout, cfg.LogLevel)

	token, err := cfg.ReadToken()
	if err != nil {
		log.Error("cannot load agent token", "error", err)
		return 2
	}
	if *checkConfig {
		fmt.Println("configuration OK")
		return 0
	}
	if *showPin {
		cert, err := server.LoadOrCreateCert(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, "certificate error:", err)
			return 1
		}
		pin, _ := server.Pin(cert)
		fmt.Println(pin)
		return 0
	}

	root, err := filesystem.NewRoot(cfg.RootDir)
	if err != nil {
		log.Error("invalid root_dir", "error", err)
		return 2
	}
	plat := platform.Detect(root)
	info := plat.Info()
	log.Info("starting whacp-agent", "version", version, "os", info.PrettyName, "family", info.Family, "supported", info.Supported, "write_actions", cfg.AllowWriteActions)
	if !info.Supported {
		log.Warn("this operating system is not officially supported; discovery runs in best-effort mode")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	collector := monitoring.NewCollector(cfg.ProcDir, cfg.MetricsInterval.Duration, cfg.MetricsHistory, log)
	go collector.Run(ctx)

	var runner execx.Runner = execx.NewSystemRunner(15 * time.Second)
	if cfg.SimulateCommands {
		log.Warn("simulate_commands is on: no system tool will be executed (development mode)")
		runner = &execx.SimRunner{Base: cfg.RootDir, Log: os.Stderr}
	}
	stateReal, _ := root.Path(cfg.StateDir)
	jobDir := filepath.Join(stateReal, "jobs")
	if cfg.AllowWriteActions {
		if err := os.MkdirAll(stateReal, 0o700); err != nil {
			log.Error("cannot create state directory", "dir", cfg.StateDir, "error", err)
			return 1
		}
	} else {
		// Read-only mode: the filesystem is read-only except /run/whacp.
		jobDir = filepath.Join(filepath.Dir(cfg.SocketPath), "jobs")
	}
	jobManager, err := jobs.NewManager(jobDir, cfg.MaxJobs, 2*time.Hour, log)
	if err != nil {
		log.Error("cannot create job directory", "dir", jobDir, "error", err)
		return 1
	}
	env := host.New(cfg, root, plat, runner, jobManager, log)
	if cfg.SimulateCommands {
		// Development against a copy of the fixture, usually without root:
		// files are owned by the current user instead of root/site accounts.
		env.Writer.Owners = agentfs.SelfOwners()
	}
	registry := actions.NewRegistry(cfg.AllowWriteActions)
	modules.Register(registry, env, version, cfg.AllowWriteActions, modules.All()...)
	actions.RegisterBuiltin(registry, actions.Deps{
		Version:   version,
		ProcDir:   cfg.ProcDir,
		RootDir:   cfg.RootDir,
		Platform:  plat,
		Collector: collector,
		Discovery: discovery.Service{Root: root, Platform: plat, Runner: runner},
	})

	srv := &server.Server{
		Registry:      registry,
		Token:         token,
		Log:           log,
		ActionTimeout: cfg.ActionTimeout.Duration,
		Version:       version,
		PeerAllowed:   peerPolicy(cfg),
	}

	mode, _ := cfg.SocketFileMode()
	ln, err := server.ListenUnix(cfg.SocketPath, mode, cfg.SocketGroup)
	if err != nil {
		log.Error("cannot listen", "socket", cfg.SocketPath, "error", err)
		return 1
	}

	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ConnContext:       srv.ConnContext,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// Some actions (certificate issuance) may run up to 10 minutes.
		WriteTimeout:   cfg.ActionTimeout.Duration + 10*time.Minute,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 64 << 10,
		ErrorLog:       slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	errCh := make(chan error, 2)
	go func() { errCh <- httpSrv.Serve(ln) }()
	log.Info("listening", "socket", cfg.SocketPath)

	// Optional remote access for a WhaCP panel on another server: TLS 1.3
	// with a pinned self-signed certificate, an address allowlist and a
	// lockout after repeated bad tokens, in addition to the bearer token.
	var tcpSrv *http.Server
	if cfg.ListenTCP != "" {
		cert, err := server.LoadOrCreateCert(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			log.Error("cannot load TLS certificate", "error", err)
			return 1
		}
		nets, _ := server.ParseCIDRs(cfg.AllowedClients)
		policy := &server.ClientPolicy{Allowed: nets, MaxFailures: 10, Window: 10 * time.Minute}
		tln, err := server.ListenTLS(cfg.ListenTCP, cert)
		if err != nil {
			log.Error("cannot listen", "address", cfg.ListenTCP, "error", err)
			return 1
		}
		tcpSrv = &http.Server{
			Handler:           policy.Guard(srv.Handler()),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      cfg.ActionTimeout.Duration + 10*time.Minute,
			IdleTimeout:       60 * time.Second,
			MaxHeaderBytes:    64 << 10,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		}
		go func() { errCh <- tcpSrv.Serve(tln) }()
		pin, _ := server.Pin(cert)
		log.Info("listening for remote panels", "address", cfg.ListenTCP, "pin", pin, "allowed_clients", cfg.AllowedClients)
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server stopped", "error", err)
			return 1
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	if tcpSrv != nil {
		_ = tcpSrv.Shutdown(shutdownCtx)
	}
	_ = os.Remove(cfg.SocketPath)
	log.Info("stopped")
	return 0
}

func peerPolicy(cfg config.Config) func(int) bool {
	allowed := map[int]bool{}
	if len(cfg.AllowedUIDs) > 0 {
		for _, uid := range cfg.AllowedUIDs {
			allowed[uid] = true
		}
	} else {
		allowed = server.GroupMembersUIDs(cfg.SocketGroup)
	}
	allowed[os.Getuid()] = true
	return func(uid int) bool { return allowed[uid] }
}
