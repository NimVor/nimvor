// Command whacp is the Go WhaCP control-panel server. It opens the existing
// SQLite database, applies any pending migrations, and serves the panel over
// HTTP (behind the nginx reverse proxy in production).
//
// Subcommands:
//
//	(no args)        serve the panel (default)
//	serve            same as no args
//	bootstrap-admin  create the first administrator (reads username/email/role
//	                 from flags; password from stdin). Exits 0 if an admin
//	                 already exists (nothing to do).
//	version          print version and exit
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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
	case "serve", "":
		os.Exit(runServe(log))
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (use: serve | bootstrap-admin | set-admin-password | has-admin | version)\n", cmd)
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
	// (idempotent), exactly as the PHP panel does at boot.
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
