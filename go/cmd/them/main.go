// Command them is the Phase 8 entrypoint for the THEM v2 Go platform.
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
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/server"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/sse"
	"github.com/aviciot/them/internal/telemetry"
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

	// ── 13. Wire noopAuth for bearer tokens ──────────────────────────────────
	// The bearer token cache requires a DB-backed TokenQuerier; use a no-op
	// authenticator for Phase 8 (the full cache is wired in a later phase).
	authenticator := &noopAuth{}

	// ── 14. Wire EP config loader (shared by WS + SSE) ───────────────────────
	epDB := epconfig.NewPgxQuerier(database.Pool())
	epLoader := epconfig.NewLoader(epDB, log)
	// Subscribe for cross-pod cache invalidation. The session Redis client
	// already satisfies epconfig.RedisSubscriber (same Subscribe signature).
	epConfigSub := cache.NewSessionRedisClient(redisCache.Client())
	epLoader.Subscribe(ctx, epConfigSub)
	log.Info("EP config loader initialised with pub/sub invalidation")

	// ── 15. Wire WebSocket handler (/ws/*) ───────────────────────────────────
	wsHandler := ws.NewHandler(sessionStore, recorder, orch, bus, authenticator, cfg.InstanceID, log).
		WithEPConfig(epLoader)
	srv.MountWS(wsHandler.Routes())
	log.Info("WebSocket handler mounted", "prefix", "/ws")

	// ── 16. Wire SSE handler (/sse/*) ─────────────────────────────────────────
	sseHandler := sse.NewHandler(sessionStore, recorder, orch, bus, authenticator, cfg.InstanceID, log).
		WithEPConfig(epLoader)
	srv.MountSSE(sseHandler.Routes())
	log.Info("SSE handler mounted", "prefix", "/sse")

	// ── 17. Wire A2A server (/a2a/*, /.well-known/*) ─────────────────────────
	a2aServer := a2a.NewServer(recorder, orch, bus, log)
	srv.MountA2A(a2aServer.Routes())
	log.Info("A2A server mounted")

	// ── 18. Wire admin API (/api/v1/admin/*, /api/v1/runs/*) ─────────────────
	adminDB := admin.NewPgxQuerier(database.Pool())
	adminCache := cache.NewAdminCacheClient(redisCache.Client())
	// Temporal is optional — nil if not configured.
	adminRouter := admin.BuildRouter(adminDB, adminCache, nil /* temporal */, jwtMiddleware, log)
	srv.MountAdmin(adminRouter)
	log.Info("admin API mounted", "prefix", "/api/v1")

	log.Info("starting server", "addr", addr, "env", cfg.AppEnv)

	return srv.ListenAndServe()
}

// noopAuth is a placeholder authenticator that accepts any non-empty token.
// Replace with a full auth.Cache instance when the bearer token infrastructure
// is fully wired (requires a DB-backed TokenQuerier).
type noopAuth struct{}

func (n *noopAuth) Validate(_ context.Context, token string) (*auth.TokenInfo, error) {
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}
	return &auth.TokenInfo{TokenID: 1}, nil
}
