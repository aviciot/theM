// Command them is the Phase 1 Foundation entrypoint for the THEM v2 Go platform.
// It wires configuration, database, Redis, telemetry, health checks, and the
// HTTP server together, then blocks until a shutdown signal is received.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/health"
	"github.com/aviciot/them/internal/server"
	"github.com/aviciot/them/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
}

// run contains all startup and shutdown logic. It returns an error on any
// unrecoverable condition so that main can log and exit cleanly.
func run() error {
	// ── 1. Load and validate configuration ───────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		// Config errors occur before the logger is available; write directly to
		// stderr so the failure is always visible.
		fmt.Fprintf(os.Stderr, "FATAL config error: %v\n", err)
		os.Exit(1)
	}

	// ── 2. Set up telemetry (structured logger) ───────────────────────────────
	tel := telemetry.New(cfg.LogLevel, cfg.LogFormat, cfg.InstanceID)
	log := tel.Logger

	log.Info("configuration loaded", "config", cfg.SafeString())

	// ── 3. Connect to PostgreSQL ──────────────────────────────────────────────
	ctx := context.Background()

	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		log.Error("failed to connect to postgres", slog.String("error", err.Error()))
		return fmt.Errorf("startup: postgres: %w", err)
	}
	log.Info("postgres connected", "host", cfg.DBHost, "dbname", cfg.DBName)

	// ── 4. Connect to Redis ───────────────────────────────────────────────────
	redisCache, err := cache.New(ctx, cfg.RedisAddr(), cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		database.Close()
		log.Error("failed to connect to redis", slog.String("error", err.Error()))
		return fmt.Errorf("startup: redis: %w", err)
	}
	log.Info("redis connected", "addr", cfg.RedisAddr(), "db", cfg.RedisDB)

	// ── 5. Build health handler and HTTP server ───────────────────────────────
	healthHandler := health.New(cfg.InstanceID, database, redisCache)
	addr := fmt.Sprintf("%s:%d", cfg.AppHost, cfg.AppPort)

	// Register dependencies as Closers so ListenAndServe releases them on
	// shutdown in the correct order (HTTP drains first, then DB, then Redis).
	srv := server.New(addr, healthHandler, log, database, redisCache)

	log.Info("starting server", "addr", addr, "env", cfg.AppEnv)

	// ListenAndServe blocks until SIGTERM/SIGINT, drains connections, and
	// calls Close on each registered Closer before returning.
	return srv.ListenAndServe()
}
