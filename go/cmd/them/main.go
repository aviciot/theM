// Command them is the Phase 6 entrypoint for the THEM v2 Go platform.
// It wires configuration, database, Redis, telemetry, health checks, the
// in-process event bus, the orchestration layer, and the WebSocket handler
// together, then blocks until a shutdown signal is received.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/health"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/server"
	"github.com/aviciot/them/internal/session"
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
	orch := orchestrator.New(orchCfg, llmProvider, nil /* agents wired in Phase 7 */, recorder, bus, log)

	// ── 10. Build health handler and HTTP server ──────────────────────────────
	healthHandler := health.New(cfg.InstanceID, database, redisCache)
	addr := fmt.Sprintf("%s:%d", cfg.AppHost, cfg.AppPort)

	authMW := server.AuthMiddlewares{}
	srv := server.NewWithBus(addr, healthHandler, authMW, bus, log, database, redisCache)

	// ── 11. Wire WebSocket handler ────────────────────────────────────────────
	// auth.Cache satisfies the ws.Authenticator interface but we don't have a
	// token cache wired here yet; use a pass-through stub for Phase 6.
	wsHandler := ws.NewHandler(sessionStore, recorder, orch, bus, &noopAuth{}, cfg.InstanceID, log)
	srv.MountWS(wsHandler.Routes())

	log.Info("starting server", "addr", addr, "env", cfg.AppEnv)

	return srv.ListenAndServe()
}

// noopAuth is a placeholder authenticator that accepts any non-empty token.
// Replace with auth.Cache when the bearer token infrastructure is wired.
type noopAuth struct{}

func (n *noopAuth) Validate(_ context.Context, token string) (*auth.TokenInfo, error) {
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}
	return &auth.TokenInfo{TokenID: 1}, nil
}
