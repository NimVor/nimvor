// Command whacp is the Go WhaCP control-panel server. It opens the existing
// SQLite database, applies any pending migrations, and serves the panel over
// HTTP (behind the nginx reverse proxy in production).
//
// Subcommands:
//
//	(no args)           serve the panel (default)
//	serve               same as no args
//	bootstrap-admin     create the first administrator (reads username/email/role
//	                    from flags; password from stdin). Exits 0 if an admin
//	                    already exists (nothing to do).
//	set-admin-password  reset an existing administrator's password (username
//	                    flag; password from stdin or --password-file).
//	doctor              run local self-checks (config, key file, token file,
//	                    database integrity, pending migrations, admin count,
//	                    agent socket, listen address); exits 0/1.
//	has-admin           probe: exit 0/1
//	version             print version and exit
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/authz"
	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	"github.com/mchkhawarshahzad/whacp/server/internal/identity"
	whttp "github.com/mchkhawarshahzad/whacp/server/internal/http"
)

// set by the build (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	// Dispatch subcommands. The convention is:
	//   whacp [flags]                      serve (default)
	//   whacp serve [flags]                same as default
	//   whacp bootstrap-admin [flags]      create first admin
	//   whacp set-admin-password [flags]   reset an existing admin's password
	//   whacp has-admin                    probe: exit 0/1
	//   whacp version                      print version
	cmd := "serve"
	args := os.Args[1:]
	// Detect "has-admin" first because it is a flag-looking subcommand (used
	// by the installer as `whacp -has-admin`); strip it before flag.Parse.
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		if a == "-has-admin" || a == "has-admin" {
			cmd = "has-admin"
			continue
		}
		filtered = append(filtered, a)
	}
	os.Args = append([]string{os.Args[0]}, filtered...)
	if len(filtered) > 0 && !strings.HasPrefix(filtered[0], "-") {
		cmd = filtered[0]
		os.Args = append([]string{os.Args[0]}, filtered[1:]...)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	switch cmd {
	case "version":
		fmt.Println("whacp", version)
		return
	case "has-admin":
		os.Exit(runHasAdmin(log))
	case "bootstrap-admin":
		os.Exit(runBootstrapAdmin(log))
	case "set-admin-password":
		os.Exit(runSetAdminPassword(log))
	case "doctor":
		os.Exit(runDoctor(log))
	case "serve", "":
		os.Exit(runServe(log))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (use: serve | bootstrap-admin | set-admin-password | doctor | has-admin | version)\n", cmd)
		os.Exit(2)
	}
}

func runServe(log *slog.Logger) int {
	noMigrate := flag.Bool("no-migrate", false, "do not apply pending migrations on start")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		log.Info("whacp", "version", version)
		return 0
	}
	cfg := config.Load()

	// Tee structured logs to the configured file so the Logs viewer can tail
	// it; stderr always receives logs too (for journalctl / docker logs).
	if cfg.LogFile != "" {
		if lf, err := os.OpenFile(cfg.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640); err == nil {
			log = slog.New(slog.NewJSONHandler(io.MultiWriter(os.Stderr, lf), &slog.HandlerOptions{Level: slog.LevelInfo}))
		}
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Error("open database", "error", err)
		return 1
	}
	defer db.Close()

	if !*noMigrate {
		ran, err := database.Migrate(db)
		if err != nil {
			log.Error("migrate", "error", err)
			return 1
		}
		if len(ran) > 0 {
			log.Info("migrations applied", "count", len(ran), "last", ran[len(ran)-1])
		}
	}

	// Mirror the code-defined roles/permissions into the DB on every start
	// (idempotent).
	if err := authz.SyncDefaults(context.Background(), db); err != nil {
		log.Error("sync roles/permissions", "error", err)
		return 1
	}

	panel, err := whttp.New(cfg, db, log)
	if err != nil {
		log.Error("build server", "error", err)
		return 1
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           panel.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Info("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Error("shutdown", "error", err)
		}
		close(idle)
	}()

	log.Info("listening", "addr", cfg.Listen, "version", version)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("serve", "error", err)
		return 1
	}
	<-idle
	return 0
}

// runHasAdmin exits 0 if at least one active administrator exists, 1 otherwise.
// Used by the installer to decide whether to prompt for first-admin creation.
func runHasAdmin(log *slog.Logger) int {
	flag.Parse()
	cfg := config.Load()
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Error("open database", "error", err)
		return 2
	}
	defer db.Close()
	// Migrate+sync first so the roles table exists on a fresh DB.
	if _, err := database.Migrate(db); err != nil {
		log.Error("migrate", "error", err)
		return 2
	}
	if err := authz.SyncDefaults(context.Background(), db); err != nil {
		log.Error("sync roles/permissions", "error", err)
		return 2
	}
	users := identity.NewRepository(db)
	n, err := users.CountActiveWithRole(context.Background(), "admin")
	if err != nil {
		log.Error("count admins", "error", err)
		return 2
	}
	if n > 0 {
		fmt.Println("yes")
		return 0
	}
	fmt.Println("no")
	return 1
}

// runBootstrapAdmin creates the first administrator. It reads the password
// from stdin (one line, trailing newline stripped). If an active admin
// already exists, it prints a message and exits 0 without changing anything.
func runBootstrapAdmin(log *slog.Logger) int {
	username := flag.String("username", "admin", "admin username")
	email := flag.String("email", "", "admin email (required)")
	role := flag.String("role", "admin", "role name (admin, reseller, user)")
	passwordFile := flag.String("password-file", "-", "file to read password from ('-' for stdin)")
	flag.Parse()

	if *email == "" {
		fmt.Fprintln(os.Stderr, "--email is required")
		return 2
	}

	cfg := config.Load()
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Error("open database", "error", err)
		return 1
	}
	defer db.Close()

	if _, err := database.Migrate(db); err != nil {
		log.Error("migrate", "error", err)
		return 1
	}
	if err := authz.SyncDefaults(context.Background(), db); err != nil {
		log.Error("sync roles/permissions", "error", err)
		return 1
	}

	users := identity.NewRepository(db)
	existing, err := users.CountActiveWithRole(context.Background(), "admin")
	if err != nil {
		log.Error("count admins", "error", err)
		return 1
	}
	if existing > 0 {
		fmt.Println("administrator already exists")
		return 0
	}

	// Find the role id.
	var roleID int64
	if err := db.QueryRowContext(context.Background(), "SELECT id FROM roles WHERE name = ?", *role).Scan(&roleID); err != nil {
		log.Error("lookup role", "role", *role, "error", err)
		return 1
	}

	// Read the password.
	var pwBytes []byte
	if *passwordFile == "-" {
		pwBytes, err = io.ReadAll(os.Stdin)
	} else {
		pwBytes, err = os.ReadFile(*passwordFile)
	}
	if err != nil {
		log.Error("read password", "error", err)
		return 1
	}
	pw := strings.TrimRight(string(pwBytes), "\r\n")
	if len(pw) < 8 {
		fmt.Fprintln(os.Stderr, "password must be at least 8 characters")
		return 1
	}

	hash, err := identity.HashPassword(pw)
	if err != nil {
		log.Error("hash password", "error", err)
		return 1
	}

	if _, err := users.Create(context.Background(), &identity.NewUser{
		Username:     *username,
		Email:        *email,
		DisplayName:  *username,
		PasswordHash: hash,
		RoleID:       roleID,
		IsActive:     true,
	}); err != nil {
		log.Error("create admin", "error", err)
		return 1
	}
	fmt.Printf("created administrator %q\n", *username)
	return 0
}

// runSetAdminPassword resets an existing administrator's password. It reads
// the new password from stdin (or --password-file) and updates the user row
// directly. Used by integration tests and by operators who need a scriptable
// password reset when the mail/reset-email flow is not available.
func runSetAdminPassword(log *slog.Logger) int {
	username := flag.String("username", "admin", "admin username to reset")
	passwordFile := flag.String("password-file", "-", "file to read password from ('-' for stdin)")
	flag.Parse()

	cfg := config.Load()
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Error("open database", "error", err)
		return 1
	}
	defer db.Close()

	// Ensure migrations/roles exist so queries succeed on a freshly-installed DB.
	if _, err := database.Migrate(db); err != nil {
		log.Error("migrate", "error", err)
		return 1
	}
	if err := authz.SyncDefaults(context.Background(), db); err != nil {
		log.Error("sync roles/permissions", "error", err)
		return 1
	}

	var pwBytes []byte
	if *passwordFile == "-" {
		pwBytes, err = io.ReadAll(os.Stdin)
	} else {
		pwBytes, err = os.ReadFile(*passwordFile)
	}
	if err != nil {
		log.Error("read password", "error", err)
		return 1
	}
	pw := strings.TrimRight(string(pwBytes), "\r\n")
	if len(pw) < 8 {
		fmt.Fprintln(os.Stderr, "password must be at least 8 characters")
		return 1
	}

	hash, err := identity.HashPassword(pw)
	if err != nil {
		log.Error("hash password", "error", err)
		return 1
	}

	res, err := db.ExecContext(context.Background(),
		`UPDATE users SET password_hash = ?, password_changed_at = ?, updated_at = ?
		 WHERE username = ? AND is_active = 1 AND role_id IN (SELECT id FROM roles WHERE name = 'admin')`,
		hash, time.Now().UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339), *username)
	if err != nil {
		log.Error("update password", "error", err)
		return 1
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		fmt.Fprintf(os.Stderr, "no active administrator named %q\n", *username)
		return 1
	}
	fmt.Printf("password reset for administrator %q\n", *username)
	return 0
}

// runDoctor performs a set of local self-checks and prints a human-readable
// summary. Exit code 0 means everything checked passed; 1 means at least one
// check failed; 2 means a check could not be evaluated (misconfiguration).
//
// The checks mirror what an operator needs after install/upgrade: config
// resolves, files exist with safe permissions, database opens and passes an
// integrity check, migrations are applied, an administrator exists, the
// agent socket is present with the expected mode, and the panel binary
// reports a version. It intentionally does NOT try to reach the agent over
// the socket or probe running HTTP services (those belong to the live
// /healthz endpoint and `systemctl status`); this command is safe to run
// while the services are stopped.
func runDoctor(log *slog.Logger) int {
	flag.Parse()
	cfg := config.Load()

	status := func(ok bool, name, msg string) {
		if ok {
			fmt.Printf("  [ok]  %-28s %s\n", name, msg)
		} else {
			fmt.Printf("  [!!]  %-28s %s\n", name, msg)
		}
	}

	failures := 0
	fail := func(name, msg string) { status(false, name, msg); failures++ }
	ok := func(name, msg string) { status(true, name, msg) }

	fmt.Printf("whacp %s — self-check\n", version)
	fmt.Printf("config: listen=%s db=%s key=%s agent=%s token=%s log=%s production=%t\n\n",
		cfg.Listen, cfg.DBPath, cfg.KeyFile, cfg.AgentSocket, cfg.AgentTokenFile, cfg.LogFile, cfg.Production)

	// Binary reports a version.
	if version != "" && version != "dev" {
		ok("version", version)
	} else {
		ok("version", "dev (no -ldflags version set)")
	}

	// Panel key file exists, is a regular file, mode 0640 or tighter, is
	// 32 bytes after base64-decode.
	if info, err := os.Stat(cfg.KeyFile); err != nil {
		fail("panel.key", err.Error())
	} else {
		mode := info.Mode().Perm()
		// Expected 0600 or 0640 (root:whacp): no world/group-write, no
		// world-read. Group-read is acceptable for the whacp group since it
		// matches the installer's 0640 root:whacp permissions.
		if mode&0o022 != 0 || mode&0o004 != 0 {
			fail("panel.key", fmt.Sprintf("%s is mode %04o; expected 0600 or 0640 (no world/group-write, no world-read)", cfg.KeyFile, mode))
		} else {
			if raw, err := os.ReadFile(cfg.KeyFile); err != nil {
				fail("panel.key", "cannot read: "+err.Error())
			} else {
				decoded, err := decodePanelKey(strings.TrimSpace(string(raw)))
				if err != nil || len(decoded) != 32 {
					fail("panel.key", "key does not decode to 32 bytes (expected base64 of a 32-byte random key)")
				} else {
					ok("panel.key", fmt.Sprintf("%s (%04o, %d bytes decoded)", cfg.KeyFile, mode, len(decoded)))
				}
			}
		}
	}

	// Agent token file exists and is not empty.
	if info, err := os.Stat(cfg.AgentTokenFile); err != nil {
		fail("agent.token", err.Error())
	} else {
		mode := info.Mode().Perm()
		if mode&0o044 != 0 {
			fail("agent.token", fmt.Sprintf("%s is world-readable (mode %04o); expected 0640 root:whacp", cfg.AgentTokenFile, mode))
		} else if info.Size() == 0 {
			fail("agent.token", cfg.AgentTokenFile+" is empty")
		} else {
			ok("agent.token", fmt.Sprintf("%s (%04o, %d bytes)", cfg.AgentTokenFile, mode, info.Size()))
		}
	}

	// Database: parent directory exists, DB opens, PRAGMA integrity_check = ok,
	// migrations applied, at least one admin exists.
	db, err := database.Open(cfg.DBPath)
	if err != nil {
		fail("database", "cannot open "+cfg.DBPath+": "+err.Error())
	} else {
		ok("database", "opened "+cfg.DBPath)
		// integrity_check
		var ic string
		if err := db.QueryRowContext(context.Background(), "PRAGMA integrity_check;").Scan(&ic); err != nil {
			fail("database integrity", err.Error())
		} else if ic != "ok" {
			fail("database integrity", ic)
		} else {
			ok("database integrity", "ok")
		}
		if _, err := database.Migrate(db); err != nil {
			fail("migrations", err.Error())
		} else {
			pending, perr := database.Pending(db)
			if perr != nil {
				fail("migrations", perr.Error())
			} else if len(pending) > 0 {
				fail("migrations", fmt.Sprintf("%d pending: %s", len(pending), strings.Join(pending, ", ")))
			} else {
				ok("migrations", "all applied")
			}
		}
		if err := authz.SyncDefaults(context.Background(), db); err != nil {
			fail("roles", err.Error())
		} else {
			ok("roles", "synced")
		}
		var one int
		if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM users WHERE is_active = 1 AND role_id IN (SELECT id FROM roles WHERE name = 'admin')").Scan(&one); err != nil {
			fail("administrator", err.Error())
		} else if one < 1 {
			fail("administrator", "no active administrator — run 'whacp bootstrap-admin'")
		} else {
			ok("administrator", fmt.Sprintf("%d active admin(s)", one))
		}
		db.Close()
	}

	// Agent socket path: parent dir exists. If the socket itself exists we
	// report its mode; if not, we note it (the agent may just not be running).
	if fi, err := os.Stat(filepath.Dir(cfg.AgentSocket)); err != nil {
		fail("agent socket dir", err.Error())
	} else if !fi.IsDir() {
		fail("agent socket dir", filepath.Dir(cfg.AgentSocket)+" is not a directory")
	} else {
		if si, serr := os.Stat(cfg.AgentSocket); serr != nil {
			ok("agent socket", cfg.AgentSocket+" (not present — agent may not be running)")
		} else {
			if si.Mode()&os.ModeSocket == 0 {
				fail("agent socket", cfg.AgentSocket+" is not a Unix socket")
			} else {
				mode := si.Mode().Perm()
				if mode&0o006 != 0 {
					fail("agent socket", fmt.Sprintf("%s is world/group read-writable (mode %04o)", cfg.AgentSocket, mode))
				} else {
					ok("agent socket", fmt.Sprintf("%s (%04o)", cfg.AgentSocket, mode))
				}
			}
		}
	}

	// Listen address parses and the port looks free? Only warn (the panel
	// binds at serve time; something else may legitimately occupy it).
	if _, port, err := net.SplitHostPort(cfg.Listen); err != nil {
		fail("listen", err.Error())
	} else if _, e := strconv.Atoi(port); e != nil {
		fail("listen", "port is not an integer: "+port)
	} else {
		ln, lerr := net.Listen("tcp", cfg.Listen)
		if lerr != nil {
			ok("listen", cfg.Listen+" (in use or not bindable here — that is fine if the panel is running)")
		} else {
			ln.Close()
			ok("listen", cfg.Listen+" (available)")
		}
	}

	// Log file directory exists / can be created?
	if cfg.LogFile != "" {
		logDir := filepath.Dir(cfg.LogFile)
		if fi, err := os.Stat(logDir); err != nil {
			fail("log dir", err.Error())
		} else if !fi.IsDir() {
			fail("log dir", logDir+" is not a directory")
		} else {
			ok("log dir", logDir)
		}
	}

	// StaticDir (if set) must exist; empty means use embedded assets.
	if cfg.StaticDir != "" {
		if fi, err := os.Stat(cfg.StaticDir); err != nil {
			fail("static dir", err.Error())
		} else if !fi.IsDir() {
			fail("static dir", cfg.StaticDir+" is not a directory")
		} else {
			ok("static dir", cfg.StaticDir)
		}
	} else {
		ok("static assets", "embedded (WHACP_STATIC unset)")
	}

	fmt.Println()
	if failures > 0 {
		fmt.Printf("doctor: %d problem(s) found.\n", failures)
		return 1
	}
	fmt.Println("doctor: all checks passed.")
	return 0
}

// decodePanelKey decodes the panel-key file's contents tolerating either
// standard base64 (the documented format) or hex (a format an older dev
// script briefly produced). Returns the raw 32-byte key.
func decodePanelKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	// Hex (64 hex chars = 32 bytes) — accept for backwards compat.
	if len(s) == 64 {
		if b, err := hexDecode(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func hexDecode(s string) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, errors.New("odd length")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(s); i += 2 {
		hi, ok1 := hexNibble(s[i])
		lo, ok2 := hexNibble(s[i+1])
		if !ok1 || !ok2 {
			return nil, errors.New("invalid hex")
		}
		out[i/2] = hi<<4 | lo
	}
	return out, nil
}

func hexNibble(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
