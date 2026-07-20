// Command them is the THEM v2 Go platform entrypoint.
// It wires configuration, database, Redis, telemetry, health checks, the
// in-process event bus, the orchestration layer, WebSocket, SSE, A2A, admin
// API, and rate limiting together, then blocks until a shutdown signal.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/a2a"
	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/health"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/ratelimit"
	"github.com/aviciot/them/internal/reconciler"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/server"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/sse"
	"github.com/aviciot/them/internal/telemetry"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/ws"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
}

// run contains all startup and shutdown logic.
func run() error {
	// ── 1. Load and validate configuration ───────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
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

	// ── 5. Create in-process event bus ───────────────────────────────────────
	bus := event.NewBus()
	log.Info("event bus initialised")

	// ── 6. Create session store ───────────────────────────────────────────────
	sessionRedis := cache.NewSessionRedisClient(redisCache.Client())
	sessionStore := session.NewStore(sessionRedis, cfg.InstanceID, log)
	log.Info("session store initialised")

	// ── 7. Create run recorder ────────────────────────────────────────────────
	recorder := runrecorder.NewRecorder(runrecorder.NewPgxPoolQuerier(database.Pool()))

	// ── 8. Create LLM provider ────────────────────────────────────────────────
	var llmProvider llm.Provider
	if cfg.AnthropicAPIKey != "" {
		llmProvider = llm.NewAnthropicProvider(cfg.AnthropicAPIKey, "", 0)
		log.Info("LLM: Anthropic provider configured")
	} else {
		llmProvider = &llm.MockProvider{}
		log.Warn("LLM: no ANTHROPIC_API_KEY set — using mock provider")
	}

	// ── 9. Create orchestrator ────────────────────────────────────────────────
	orchCfg := orchestrator.Config{
		MaxIterations: 10,
	}
	orch := orchestrator.New(orchCfg, llmProvider, nil, recorder, bus, log)

	// ── 10. Create rate limiter ───────────────────────────────────────────────
	rlRedis := cache.NewRateLimitClient(redisCache.Client())
	limiter := ratelimit.New(rlRedis)
	_ = limiter // rate limiter available for wiring into handlers

	// ── 11. Build auth middleware ─────────────────────────────────────────────
	var jwtMiddleware func(http.Handler) http.Handler
	if cfg.JWTPublicKey != nil {
		jwtMiddleware = auth.JWTMiddleware(cfg.JWTPublicKey)
		log.Info("JWT middleware enabled")
	} else {
		log.Info("JWT middleware disabled — JWT_PUBLIC_KEY_PEM not set")
	}

	// ── 12. Build health handler and HTTP server ──────────────────────────────
	healthHandler := health.New(cfg.InstanceID, database, redisCache)
	addr := fmt.Sprintf("%s:%d", cfg.AppHost, cfg.AppPort)

	authMW := server.AuthMiddlewares{}
	srv := server.NewWithBus(addr, healthHandler, authMW, bus, log, database, redisCache)

	// ── 13. Wire bearer token cache (L1 in-process → L2 Redis → PostgreSQL) ──
	tokenDB := auth.NewPgxQuerier(database.Pool())
	tokenRedis := cache.NewAuthRedisClient(redisCache.Client())
	tokenCache := auth.NewCache(tokenDB, tokenRedis, log)
	// Start cross-pod revocation listener. Blocks until ctx is cancelled; the
	// context is derived from the process lifetime so it stops on SIGTERM.
	go tokenCache.Subscribe(ctx)
	log.Info("bearer token cache initialised (L1+L2+pub/sub revocation)")

	authenticator := tokenCache

	// ── 13b. Conditionally wire Temporal client (gated on TEMPORAL_ENABLED) ──
	var temporalCli temporalclient.Client
	if cfg.TemporalEnabled {
		tc, tcErr := temporal.Connect(cfg.TemporalHostPort, log)
		if tcErr != nil {
			log.Error("failed to connect to Temporal", slog.String("error", tcErr.Error()))
			return fmt.Errorf("startup: temporal: %w", tcErr)
		}
		temporalCli = tc
		defer temporalCli.Close()
		log.Info("Temporal client connected", "host_port", cfg.TemporalHostPort)
	} else {
		log.Info("Temporal disabled — using Go-inline orchestration path")
	}

	// ── 13c. Start run reconciler (Temporal path only, dry-run by default) ───
	if cfg.TemporalEnabled && temporalCli != nil {
		recDB := reconciler.NewPgxQuerier(database.Pool())
		recCfg := reconciler.Config{DryRun: true} // safe default; set DryRun=false to enable writes
		go reconciler.Run(ctx, recCfg, recDB, temporalCli, log)
		log.Info("run reconciler started", "dry_run", recCfg.DryRun)
	}

	// ── 14. Wire EP config loader (shared by WS + SSE) ───────────────────────
	epDB := epconfig.NewPgxQuerier(database.Pool())
	epLoader := epconfig.NewLoader(epDB, log)
	// Subscribe for cross-pod cache invalidation. The session Redis client
	// already satisfies epconfig.RedisSubscriber (same Subscribe signature).
	epConfigSub := cache.NewSessionRedisClient(redisCache.Client())
	epLoader.Subscribe(ctx, epConfigSub)
	log.Info("EP config loader initialised with pub/sub invalidation")

	// ── 15. Wire WebSocket handler (/ws/*) ───────────────────────────────────
	rsRedis := cache.NewRunStreamRedisClient(redisCache.Client())
	wsHandler := ws.NewHandler(sessionStore, recorder, orch, bus, authenticator, cfg.InstanceID, log).
		WithEPConfig(epLoader).
		WithTemporal(temporalCli, rsRedis, cfg.TemporalEnabled)
	srv.MountWS(wsHandler.Routes())
	log.Info("WebSocket handler mounted", "prefix", "/ws")

	// ── 16. Wire SSE handler (/sse/*) ─────────────────────────────────────────
	sseHandler := sse.NewHandler(sessionStore, recorder, orch, bus, authenticator, cfg.InstanceID, log).
		WithEPConfig(epLoader).
		WithTemporal(temporalCli, rsRedis, cfg.TemporalEnabled)
	srv.MountSSE(sseHandler.Routes())
	log.Info("SSE handler mounted", "prefix", "/sse")

	// ── 17. Wire A2A server (/a2a/*, /.well-known/*) ─────────────────────────
	a2aServer := a2a.NewServer(recorder, orch, bus, log)
	srv.MountA2A(a2aServer.Routes())
	log.Info("A2A server mounted")

	// ── 18. Wire admin API (/api/v1/admin/*, /api/v1/runs/*) ─────────────────
	adminDB := admin.NewPgxQuerier(database.Pool())
	adminCache := cache.NewAdminCacheClient(redisCache.Client())
	// Temporal signaler is optional — nil if Temporal is not enabled.
	var temporalSignaler admin.TemporalSignaler
	if temporalCli != nil {
		temporalSignaler = temporal.NewSignaler(temporalCli)
	}
	adminRouter := admin.BuildRouter(adminDB, adminCache, temporalSignaler, jwtMiddleware, log)
	srv.MountAdmin(adminRouter)
	log.Info("admin API mounted", "prefix", "/api/v1")

	log.Info("starting server", "addr", addr, "env", cfg.AppEnv)

	return srv.ListenAndServe()
}
