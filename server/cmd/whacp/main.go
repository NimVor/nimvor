// Command whacp is the Go WhaCP control-panel server. It opens the existing
// SQLite database, applies any pending migrations, and serves the panel over
// HTTP (behind the nginx reverse proxy in production).
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mchkhawarshahzad/whacp/server/internal/config"
	"github.com/mchkhawarshahzad/whacp/server/internal/database"
	whttp "github.com/mchkhawarshahzad/whacp/server/internal/http"
)

// set by the build (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	noMigrate := flag.Bool("no-migrate", false, "do not apply pending migrations on start")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if *showVersion {
		log.Info("whacp", "version", version)
		return
	}
	cfg := config.Load()

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Error("open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if !*noMigrate {
		ran, err := database.Migrate(db)
		if err != nil {
			log.Error("migrate", "error", err)
			os.Exit(1)
		}
		if len(ran) > 0 {
			log.Info("migrations applied", "count", len(ran), "last", ran[len(ran)-1])
		}
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           whttp.New(cfg, db, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
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
		os.Exit(1)
	}
	<-idle
}
