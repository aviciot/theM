// Command mcp-service is the-M's dedicated MCP Store service.
// It manages MCP server health checks, tool discovery, and tool execution.
// It is intentionally isolated from them-go-bridge — no shared code at the
// binary level; internal packages are shared via the Go module.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/events"
	"github.com/aviciot/them/internal/health"
	"github.com/aviciot/them/internal/mcp"
	"github.com/aviciot/them/internal/metrics"
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

	// ── 5b. Redis Events (dedicated queue for observability events) ──────────
	var redisEvents *cache.Cache
	if cfg.RedisEventsAddr() != "" {
		redisEvents, err = cache.New(ctx, cfg.RedisEventsAddr(), cfg.RedisEventsPassword, 0)
		if err != nil {
			return fmt.Errorf("startup: redis events: %w", err)
		}
		defer redisEvents.Close()
		log.Info("redis events connected", "addr", cfg.RedisEventsAddr())
	}

	// ── 6. Event sink (tool calls) ────────────────────────────────────────────
	// Mode: postgres (default), clickhouse (blocked), dual
	// Safety: ValidateEventsMode rejects unknown values and blocks clickhouse-only mode
	var toolEventSink events.EventSink
	eventsMode, err := events.ValidateEventsMode("THE_M_EVENTS_TOOLS")
	if err != nil {
		log.Error("invalid events mode", slog.String("error", err.Error()))
		return fmt.Errorf("startup: %w", err)
	}

	// Check Redis requirement for dual/clickhouse modes
	redisEventsAddr := ""
	if redisEvents != nil {
		redisEventsAddr = cfg.RedisEventsAddr()
	}
	if err := events.RequireRedisForMode(eventsMode, redisEventsAddr); err != nil {
		log.Error("events mode configuration error", slog.String("error", err.Error()))
		return fmt.Errorf("startup: %w", err)
	}

	switch eventsMode {
	case events.ModeClickhouse, events.ModeDual:
		// Clickhouse or Dual mode: emit to Redis Streams for event-writer to consume
		// MCP has no Postgres table yet, so both modes write to ClickHouse only
		toolEventSink = events.NewRedisSink(redisEvents.Client(), "them:events", 10000, 1*time.Second)
		log.Info("tool events sink: ClickHouse (via Redis Streams)",
			"mode", eventsMode, "redis", cfg.RedisEventsAddr())
	default:
		// ModePostgres: no event emission (no Postgres table for tool events yet)
		toolEventSink = nil
		log.Info("tool events sink: disabled (Postgres only)", "mode", eventsMode)
	}

	// ── 7. MCP internals ──────────────────────────────────────────────────────
	dal := mcp.NewDAL(database.Pool())
	registry := mcp.NewRegistry(redisCache.Client())
	leader := mcp.NewLeaderLock(redisCache.Client(), cfg.InstanceID)
	supervisor := mcp.NewSupervisor(dal, registry, leader, cfg.HealthIntervalSeconds, cfg.SecretKey, log)
	mcpMetrics := cache.NewMetricsRedisClient(redisCache.Client())
	executor := mcp.NewExecutor(dal, registry, cfg.SecretKey).
		WithMetricsRecorder(metrics.NewRedisRecorder(mcpMetrics)).
		WithEventSink(toolEventSink)

	// ── 8. HTTP server ────────────────────────────────────────────────────────
	healthHandler := health.New(cfg.InstanceID, database, redisCache)
	router := mcp.NewRouter(healthHandler, supervisor, executor, "1.0.0")

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// ── 9. Supervisor (per-server worker goroutines, leader-elected) ──────────
	go supervisor.Run(runCtx)
	log.Info("supervisor started")

	// ── 10. Serve with graceful shutdown ──────────────────────────────────────
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

	// Cancel background goroutines before draining HTTP
	runCancel()
	leader.Release(ctx)

	// Shutdown HTTP server first (drain in-flight requests)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("HTTP shutdown failed", "error", err)
		// Continue to drain event sink even if HTTP shutdown fails
	} else {
		log.Info("HTTP server drained")
	}

	// Drain event sink AFTER HTTP shutdown (all handlers finished, no new events)
	if toolEventSink != nil {
		sinkCtx, sinkCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer sinkCancel()
		if err := toolEventSink.Close(sinkCtx); err != nil {
			log.Error("failed to drain tool event sink", "error", err)
		} else {
			log.Info("tool event sink drained")
		}
	}
	log.Info("mcp-service stopped cleanly")
	return nil
}
