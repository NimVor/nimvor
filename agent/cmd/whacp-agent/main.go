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
	"github.com/NimVor/nimvor/agent/internal/greylist"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/NimVor/nimvor/agent/internal/actions"
	"github.com/NimVor/nimvor/agent/internal/agentfs"
	"github.com/NimVor/nimvor/agent/internal/config"
	"github.com/NimVor/nimvor/agent/internal/discovery"
	"github.com/NimVor/nimvor/agent/internal/egress"
	"github.com/NimVor/nimvor/agent/internal/execx"
	"github.com/NimVor/nimvor/agent/internal/filesystem"
	"github.com/NimVor/nimvor/agent/internal/host"
	"github.com/NimVor/nimvor/agent/internal/jobs"
	"github.com/NimVor/nimvor/agent/internal/logging"
	"github.com/NimVor/nimvor/agent/internal/mailrate"
	"github.com/NimVor/nimvor/agent/internal/modsec"
	"github.com/NimVor/nimvor/agent/internal/modules"
	"github.com/NimVor/nimvor/agent/internal/monitoring"
	"github.com/NimVor/nimvor/agent/internal/platform"
	"github.com/NimVor/nimvor/agent/internal/procmgr"
	"github.com/NimVor/nimvor/agent/internal/sendmail"
	"github.com/NimVor/nimvor/agent/internal/server"
	"github.com/NimVor/nimvor/agent/internal/sslmgr"
	"github.com/NimVor/nimvor/agent/internal/sysmgr"
	"github.com/NimVor/nimvor/agent/internal/terminal"
)

// shutdownGrace bounds how long a stopping agent waits for running jobs
// (whacp-agent.service allows it with TimeoutStopSec).
const shutdownGrace = 5 * time.Minute

// version is set at build time with -ldflags "-X main.version=...".
var version = "1.0.0-rc1"

func main() {
	// Commands run as a website account see this binary as their sendmail
	// (bind-mounted in their units): it then only submits the message over
	// SMTP to this machine and never reads the agent's configuration.
	if strings.HasPrefix(filepath.Base(os.Args[0]), "sendmail") {
		os.Exit(sendmail.Main(os.Args[1:], os.Stdin, os.Stderr))
	}
	// The same, run by path (PHP's sendmail_path): whacp-agent sendmail ...
	if len(os.Args) > 1 && os.Args[1] == "sendmail" {
		os.Exit(sendmail.Main(os.Args[2:], os.Stdin, os.Stderr))
	}
	// Outgoing mail limit policy service: its own systemd unit
	// (DynamicUser, no capabilities); it never reads the agent's config.
	if len(os.Args) > 1 && os.Args[1] == "mailrate-policy" {
		os.Exit(mailrate.Main(os.Args[2:]))
	}
	// Greylisting policy service: its own unprivileged systemd unit
	// (DynamicUser, no capabilities); it never reads the agent's config.
	if len(os.Args) > 1 && os.Args[1] == "greylist-policy" {
		os.Exit(greylist.Main(os.Args[2:]))
	}
	os.Exit(run())
}

func run() int {
	cfgPath := flag.String("config", config.DefaultPath, "path to the agent configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	checkConfig := flag.Bool("check-config", false, "validate the configuration and token, then exit")
	showPin := flag.Bool("pin", false, "print the TLS public-key pin for remote panels (creates the certificate if needed), then exit")
	waitFor := flag.Duration("wait-jobs", 0, "wait up to this long until the running agent has no queued or running job, then exit (0: none; 3: still running; 1: error)")
	procScan := flag.Bool("proc-scan", false, "print every process's executable, folder and sockets as JSON, then exit (run by the agent with CAP_SYS_PTRACE)")
	procDetail := flag.Int("proc-detail", 0, "print one process's details as JSON, then exit (run by the agent with CAP_SYS_PTRACE)")
	flag.Parse()

	if *procScan || *procDetail > 0 {
		if err := procmgr.ScanMain("/proc", *procDetail, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		return 0
	}
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
	if *waitFor > 0 {
		return waitJobs(context.Background(), cfg.SocketPath, token, *waitFor, 5*time.Second, os.Stdout)
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
	log.Info("starting whacp-agent", "version", version, "os", info.PrettyName, "family", info.Family, "supported", info.Supported, "preview", info.Preview, "write_actions", cfg.AllowWriteActions)
	if !info.Supported {
		log.Warn("this operating system is not officially supported; discovery runs in best-effort mode")
	} else if info.Preview {
		log.Warn("this operating system is a preview for WhaCP: tested, but not yet recommended for production servers")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	collector := monitoring.NewCollector(cfg.ProcDir, cfg.MetricsInterval.Duration, cfg.MetricsHistory, log)
	go collector.Run(ctx)

	if exe, err := os.Executable(); err == nil {
		execx.SendmailShim = exe
	}
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

	if cfg.AllowWriteActions {
		// An installation the agent stopped in the middle of was never
		// validated: roll it back before anything else.
		go func() {
			for _, line := range modsec.RecoverInstalls(context.Background(), env, io.Discard) {
				log.Warn("modsec: " + line)
			}
			// Packages an earlier transaction left broken (failed install
			// scripts, missing dependencies) are repaired after an update.
			sysmgr.RepairAtStart(ctx, env)
			// PHP-FPM services in their sandbox (after an update).
			env.SandboxFPMServices(ctx)
			// Website accounts' outgoing connection limits (cloud
			// metadata blocked by default) for the current accounts.
			if err := egress.Refresh(ctx, env); err != nil {
				log.Warn("outgoing connection limits not applied", "error", err)
			}
			// Certificates exist but nothing renews them (EPEL's certbot
			// leaves its timer off).
			if sslmgr.HasCertificates(env) {
				if err := sslmgr.EnsureRenewal(ctx, env); err != nil {
					log.Warn("certificate renewal is not scheduled", "error", err)
				}
			}
		}()
	}

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

	// Web terminal: WebSockets from the panel's Nginx site, on loopback.
	var termSrv *http.Server
	if cfg.TerminalListen != "" && terminal.Shared != nil {
		if !cfg.AllowWriteActions {
			log.Warn("terminal_listen is set but write actions are off; the web terminal stays disabled")
		} else if tln, err := net.Listen("tcp", cfg.TerminalListen); err != nil {
			log.Error("cannot listen for the web terminal; it stays disabled", "address", cfg.TerminalListen, "error", err)
		} else {
			terminal.Shared.Enabled = true
			termSrv = &http.Server{
				Handler:           terminal.Shared.Handler(),
				ReadHeaderTimeout: 5 * time.Second,
				MaxHeaderBytes:    16 << 10,
				ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
			}
			go func() { errCh <- termSrv.Serve(tln) }()
			log.Info("web terminal listening", "address", cfg.TerminalListen, "root_shells", cfg.TerminalRoot)
		}
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server stopped", "error", err)
			return 1
		}
	}
	// No new requests (so no new jobs); let running actions finish.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ActionTimeout.Duration+5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	if tcpSrv != nil {
		_ = tcpSrv.Shutdown(shutdownCtx)
	}
	if termSrv != nil {
		_ = termSrv.Shutdown(shutdownCtx)
		terminal.Shared.CloseAll("The server agent is restarting.")
	}
	_ = os.Remove(cfg.SocketPath)
	// Background jobs (restores, deployments) get time to finish instead of
	// stopping half-way; the unit's TimeoutStopSec allows for this.
	if active := jobManager.Active(); len(active) > 0 {
		log.Info("waiting for running jobs before stopping", "jobs", len(active), "limit", shutdownGrace)
		drainCtx, cancelDrain := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancelDrain()
		if !jobManager.DrainWithin(drainCtx) {
			for _, j := range jobManager.Active() {
				log.Warn("job interrupted by the agent stopping", "job", j.ID, "kind", j.Kind, "target", j.Target)
			}
		}
	}
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
