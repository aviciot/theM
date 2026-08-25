// Command mcp-service is the-M's dedicated MCP Store service.
// It manages MCP server health checks, tool discovery, and tool execution.
// It is intentionally isolated from them-go-bridge — no shared code at the
// binary level; internal packages are shared via the Go module.
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/health"
	"github.com/aviciot/them/internal/mcp"
	"github.com/aviciot/them/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ── 1. Config ─────────────────────────────────────────────────────────────
	cfg, err := mcp.LoadConfig()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// ── 2. Telemetry ──────────────────────────────────────────────────────────
	tel := telemetry.New(cfg.LogLevel, cfg.LogFormat, cfg.InstanceID)
	log := tel.Logger
	log.Info("mcp-service configuration loaded", "config", cfg.SafeString())

	// ── 3. Context ────────────────────────────────────────────────────────────
	ctx := context.Background()
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	// ── 4. Postgres ───────────────────────────────────────────────────────────
	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		return fmt.Errorf("startup: postgres: %w", err)
	}
	defer database.Close()
	log.Info("postgres connected", "host", cfg.DBHost, "dbname", cfg.DBName)

	// ── 5. Redis ──────────────────────────────────────────────────────────────
	redisCache, err := cache.New(ctx, cfg.RedisAddr(), cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		return fmt.Errorf("startup: redis: %w", err)
	}
	defer redisCache.Close()
	log.Info("redis connected", "addr", cfg.RedisAddr())

	// ── 6. MCP internals ──────────────────────────────────────────────────────
	dal := mcp.NewDAL(database.Pool())
	registry := mcp.NewRegistry(redisCache.Client())
	leader := mcp.NewLeaderLock(redisCache.Client(), cfg.InstanceID)
	supervisor := mcp.NewSupervisor(dal, registry, leader, cfg.HealthIntervalSeconds, cfg.SecretKey, log)
	executor := mcp.NewExecutor(dal, registry, cfg.SecretKey)

	// ── 7. HTTP server ────────────────────────────────────────────────────────
	healthHandler := health.New(cfg.InstanceID, database, redisCache)
	router := mcp.NewRouter(healthHandler, supervisor, executor, "1.0.0")

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// ── 8. Supervisor (per-server worker goroutines, leader-elected) ──────────
	go supervisor.Run(runCtx)
	log.Info("supervisor started")

	// ── 9. Serve with graceful shutdown ───────────────────────────────────────
	serverErr := make(chan error, 1)
	go func() {
		log.Info("mcp-service listening", "addr", cfg.Addr())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		return fmt.Errorf("serve: %w", err)
	case sig := <-stop:
		log.Info("shutdown signal received", "signal", sig.String())
	}

	// Cancel background goroutines before draining HTTP.
	runCancel()
	leader.Release(ctx)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("mcp-service stopped cleanly")
	return nil
}
