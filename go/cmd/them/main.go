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

	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/a2a"
	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/agentregistry"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/health"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/ratelimit"
	"github.com/aviciot/them/internal/reconciler"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/runstream"
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
	// The run-events mode is injected once here so every new run row gets the
	// correct events_transport ('pubsub' or 'streams') without threading the mode
	// through call sites (Phase 11c-B).
	log.Info("run events mode", "mode", cfg.RunEventsMode)
	runstream.SetModeGauge(string(cfg.RunEventsMode))
	recorder := runrecorder.NewRecorder(runrecorder.NewPgxPoolQuerier(database.Pool())).
		WithRunEventsMode(cfg.RunEventsMode)

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
	_ = limiter // rate limiter available for future per-handler wiring

	// ── 10b. Create admission gate ────────────────────────────────────────────
	gateRedis := cache.NewGateRedisClient(redisCache.Client())
	admissionGate := gate.New(gateRedis)
	log.Info("admission gate initialised")

	// ── 10c. Create agent registry ────────────────────────────────────────────
	agentDB := agentregistry.NewPgxQuerier(database.Pool())
	agentCacheRedis := cache.NewAuthRedisClient(redisCache.Client())
	agentReg := agentregistry.New(agentDB, agentCacheRedis, log)
	go agentReg.Subscribe(ctx)
	log.Info("agent registry initialised with pub/sub cache invalidation")

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

	// ── 13c. Start run reconciler (Temporal path only) ───────────────────────
	// DryRun is read from RECONCILER_DRY_RUN env var; defaults to true (safe).
	// Set RECONCILER_DRY_RUN=false to enable actual DB writes.
	if cfg.TemporalEnabled && temporalCli != nil {
		recDB := reconciler.NewPgxQuerier(database.Pool())
		recCfg := reconciler.Config{DryRun: cfg.ReconcilerDryRun}
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

	// ── 15. Wire run-event dispatcher (Pub/Sub + Redis Streams) ──────────────
	// The dispatcher is built once and shared by the WS and SSE handlers. It
	// picks Pub/Sub or Streams per run based on RUN_EVENTS_MODE and the run's
	// events_transport value (Phase 11c-B).
	rsRedis := cache.NewRunStreamRedisClient(redisCache.Client())     // Pub/Sub subscriber
	rsStreamer := cache.NewRunStreamerRedisClient(redisCache.Client()) // Streams reader
	dispatcher := runstream.NewDispatcher(cfg.RunEventsMode, rsRedis, rsStreamer)

	// ── 16. Wire WebSocket handler (/ws/*) ───────────────────────────────────
	wsHandler := ws.NewHandler(sessionStore, recorder, orch, bus, authenticator, cfg.InstanceID, log).
		WithGate(admissionGate).
		WithEPConfig(epLoader).
		WithTemporal(temporalCli, rsRedis, cfg.TemporalEnabled).
		WithRunEvents(dispatcher, cfg.RunEventsMode)
	srv.MountWS(wsHandler.Routes())
	log.Info("WebSocket handler mounted", "prefix", "/ws")

	// ── 17. Wire SSE handler (/sse/*) ─────────────────────────────────────────
	sseHandler := sse.NewHandler(sessionStore, recorder, orch, bus, authenticator, cfg.InstanceID, log).
		WithGate(admissionGate).
		WithEPConfig(epLoader).
		WithTemporal(temporalCli, rsRedis, cfg.TemporalEnabled).
		WithRunEvents(dispatcher, cfg.RunEventsMode)
	srv.MountSSE(sseHandler.Routes())
	log.Info("SSE handler mounted", "prefix", "/sse")

	// ── 17b. Start pod heartbeat loop ─────────────────────────────────────────
	// Writes them:pod:{instance_id} every 15 s so the session reconciler knows
	// this replica is alive. Session TTL is 90 s so 15 s gives 6 misses before
	// a pod is considered dead — wide enough to survive transient Redis blips.
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := sessionStore.WriteHeartbeat(ctx); err != nil {
					log.Warn("pod heartbeat failed", "error", err)
				}
			}
		}
	}()
	log.Info("pod heartbeat loop started", "interval", "15s")

	// ── 17c. Mount /apps/{slug}/ws and /apps/{slug}/sse aliases ─────────────
	// These are the app entry-point URLs used by the frontend.
	// MountApps("/apps") strips the prefix, so sub-routes are /{slug}/ws etc.
	srv.MountApps(buildAppsHandler(wsHandler, sseHandler))
	log.Info("apps WS+SSE aliases mounted", "prefix", "/apps")

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

// buildAppsHandler creates a chi router for /apps/{slug}/ws and /apps/{slug}/sse.
// It is mounted at /apps by MountApps, so routes here are relative (/{slug}/ws).
// Each handler remaps {slug} → {entry_point_slug} for the shared ServeHTTP.
func buildAppsHandler(wsH *ws.Handler, sseH *sse.Handler) http.Handler {
	r := chi.NewRouter()

	r.Get("/{slug}/ws", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		rctx := chi.RouteContext(req.Context())
		rctx.URLParams.Add("app_slug", slug)
		rctx.URLParams.Add("entry_point_slug", slug)
		wsH.ServeHTTP(w, req)
	})

	r.Get("/{slug}/sse", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		rctx := chi.RouteContext(req.Context())
		rctx.URLParams.Add("app_slug", slug)
		rctx.URLParams.Add("entry_point_slug", slug)
		sseH.ServeHTTP(w, req)
	})
	r.Post("/{slug}/sse", func(w http.ResponseWriter, req *http.Request) {
		slug := chi.URLParam(req, "slug")
		rctx := chi.RouteContext(req.Context())
		rctx.URLParams.Add("app_slug", slug)
		rctx.URLParams.Add("entry_point_slug", slug)
		sseH.ServeHTTP(w, req)
	})

	return r
}
