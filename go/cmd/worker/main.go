// Command worker is the dedicated Go Temporal worker for the-M (R-2C).
//
// It polls the Go-only task queue ("them-orchestration-go" by default, or
// WORKER_TASK_QUEUE env var) and executes OrchestrationWorkflow +
// RunOrchestratorActivity. It does NOT expose any HTTP/WS/SSE/admin routes.
//
// # Per-run orchestrator resolution
//
// Each WorkflowInput carries AppOrchestratorID — the UUID of the
// app_orchestrators row that governs the run. The worker loads config from
// DB on every activity execution: system_prompt, llm_provider, llm_model,
// max_iterations, history_window, budget_tokens, and allowed_agent_ids
// (resolved to agent slugs via agents.id = component_definitions.id).
// The provider API key is read from applications.provider_keys for the
// chosen provider. If no per-app key is stored, the global env-var key
// is used as a fallback.
//
// # Phase 3 — cross-process Redis Streams
//
// The worker and bridge run as separate OS processes. Events published by the
// worker's orchestrator to the in-process event bus must be forwarded to the
// run's Redis Stream so the Bridge can read them via StreamFromRedis.
//
// Wiring: after creating the event bus, the worker subscribes a wildcard ("*")
// listener and starts a goroutine that calls runstream.PublishEvent for every
// event received, writing a "data" → JSON entry to
// them:dash:run:{runID}:stream via RunStreamerWriterRedisClient.
//
// Graceful shutdown: SIGTERM or SIGINT → worker.Stop() → clean exit.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	temporalworker "go.temporal.io/sdk/worker"

	"github.com/aviciot/them/internal/agentregistry"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/telemetry"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/temporal/workerconfig"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
}

// run contains all startup and shutdown logic for the Go Temporal worker.
func run() error {
	// ── 1. Load and validate configuration ───────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// ── 2. Set up telemetry (structured logger) ───────────────────────────────
	tel := telemetry.New(cfg.LogLevel, cfg.LogFormat, cfg.InstanceID)
	log := tel.Logger

	log.Info("Go Worker: configuration loaded", "config", cfg.SafeString())

	// ── 3. Connect to PostgreSQL ──────────────────────────────────────────────
	ctx := context.Background()

	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		log.Error("failed to connect to postgres", slog.String("error", err.Error()))
		return fmt.Errorf("startup: postgres: %w", err)
	}
	defer database.Close()
	log.Info("postgres connected", "host", cfg.DBHost, "dbname", cfg.DBName)

	// ── 4. Connect to Redis ───────────────────────────────────────────────────
	redisCache, err := cache.New(ctx, cfg.RedisAddr(), cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		log.Error("failed to connect to redis", slog.String("error", err.Error()))
		return fmt.Errorf("startup: redis: %w", err)
	}
	log.Info("redis connected", "addr", cfg.RedisAddr(), "db", cfg.RedisDB)

	// ── 5. Create in-process event bus ───────────────────────────────────────
	bus := event.NewBus()
	log.Info("event bus initialised")

	// ── 6. Create run recorder ────────────────────────────────────────────────
	recorder := runrecorder.NewRecorder(runrecorder.NewPgxPoolQuerier(database.Pool()))

	// ── 7. Create global LLM provider (fallback when no per-app key is stored) ──
	var globalLLMProvider llm.Provider
	if cfg.AnthropicAPIKey != "" {
		globalLLMProvider = llm.NewAnthropicProvider(cfg.AnthropicAPIKey, "", 0)
		log.Info("LLM: Anthropic provider configured (global fallback)")
	} else {
		globalLLMProvider = &llm.MockProvider{}
		log.Warn("LLM: no ANTHROPIC_API_KEY set — using mock provider as global fallback")
	}

	// ── 8. Create agent registry ─────────────────────────────────────────────
	pool := database.Pool()
	agentDB := agentregistry.NewPgxQuerier(pool)
	agentCacheRedis := cache.NewAuthRedisClient(redisCache.Client())
	registry := agentregistry.New(agentDB, agentCacheRedis, log)

	// Start agent registry pub/sub invalidation listener.
	registryCtx, registryCancel := context.WithCancel(ctx)
	defer registryCancel()
	go registry.Subscribe(registryCtx)
	log.Info("agent registry initialised with pub/sub invalidation")

	// ── 9. Create per-run config loader ──────────────────────────────────────
	cfgLoader := workerconfig.NewPgxLoader(pool)

	// ── 10. Create orchestrator factory ──────────────────────────────────────
	// The factory builds a fresh orchestrator per activity execution using
	// config loaded from DB, so each run gets its own system_prompt, model,
	// history_window, budget, and agent tool list.
	factory := &runOrchestratorFactory{
		globalProvider: globalLLMProvider,
		globalAPIKey:   cfg.AnthropicAPIKey,
		registry:       registry,
		recorder:       recorder,
		bus:            bus,
		logger:         log,
	}

	// ── 10b. Phase 3 — forward bus events to Redis Streams ───────────────────
	streamWriter := cache.NewRunStreamerWriterRedisClient(redisCache.Client())
	log.Info("Redis Stream writer initialised — cross-process event forwarding active")

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	evCh, _, unsub := bus.Subscribe(streamCtx, "*", 512)
	defer unsub()

	go func() {
		for ev := range evCh {
			if ev.RunID == "" {
				continue
			}
			runstream.PublishEvent(streamCtx, streamWriter, log, ev)
		}
	}()

	// ── 11. Connect Temporal client ───────────────────────────────────────────
	if !cfg.TemporalEnabled {
		return fmt.Errorf("Go Worker requires TEMPORAL_ENABLED=true — worker cannot run without Temporal")
	}

	temporalCli, err := temporal.Connect(cfg.TemporalHostPort, log)
	if err != nil {
		log.Error("failed to connect to Temporal", slog.String("error", err.Error()))
		return fmt.Errorf("startup: temporal: %w", err)
	}
	defer temporalCli.Close()
	log.Info("Temporal client connected", "host_port", cfg.TemporalHostPort)

	// ── 12. Create and register Go Temporal worker ────────────────────────────
	taskQueue := cfg.WorkerTaskQueue
	goWorker := temporalworker.New(temporalCli, taskQueue, temporalworker.Options{})
	goWorker.RegisterWorkflow(temporal.OrchestrationWorkflow)
	acts := &temporal.Activities{
		Runner:       factory.buildFallback(), // static fallback for legacy/test paths
		ConfigLoader: cfgLoader,
		Factory:      factory,
	}
	goWorker.RegisterActivity(acts.RunOrchestratorActivity)

	if err := goWorker.Start(); err != nil {
		return fmt.Errorf("startup: temporal worker: %w", err)
	}

	log.Info("Go Worker polling", "task_queue", taskQueue)

	// ── 13. Block on SIGTERM / SIGINT ─────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info("Go Worker: shutdown signal received — draining worker")
	goWorker.Stop()
	log.Info("Go Worker stopped")

	return nil
}

// ── runOrchestratorFactory ────────────────────────────────────────────────────

// runOrchestratorFactory implements temporal.OrchestratorFactory.
// It builds a per-run *orchestrator.Orchestrator from a loaded RunConfig,
// wiring the correct LLM provider (per-app key → global fallback).
type runOrchestratorFactory struct {
	globalProvider llm.Provider
	globalAPIKey   string
	registry       *agentregistry.Registry
	recorder       *runrecorder.Recorder
	bus            event.Bus
	logger         *slog.Logger
}

// Build creates a per-run orchestrator from the loaded RunConfig.
// LLM provider selection: if RunConfig.LLMAPIKey is set, use it for the
// chosen provider; otherwise fall back to the global provider.
func (f *runOrchestratorFactory) Build(cfg workerconfig.RunConfig) temporal.OrchestratorRunner {
	provider := f.resolveProvider(cfg)
	orch := orchestrator.New(cfg.OrchestratorConfig, provider, f.registry, f.recorder, f.bus, f.logger)
	return orch
}

// buildFallback returns a static orchestrator used when AppOrchestratorID is
// absent (legacy paths, unit tests). Uses global provider + default config.
func (f *runOrchestratorFactory) buildFallback() temporal.OrchestratorRunner {
	cfg := orchestrator.Config{MaxIterations: 10}
	return orchestrator.New(cfg, f.globalProvider, nil, f.recorder, f.bus, f.logger)
}

// resolveProvider selects and constructs the LLM provider for one run.
// Priority: per-app key for the named provider → global provider.
func (f *runOrchestratorFactory) resolveProvider(cfg workerconfig.RunConfig) llm.Provider {
	if cfg.LLMAPIKey != "" && cfg.LLMProvider != "" {
		switch cfg.LLMProvider {
		case "anthropic":
			return llm.NewAnthropicProvider(cfg.LLMAPIKey, cfg.OrchestratorConfig.Model, 0)
		// Future: openai, gemini, groq — add cases here as providers are implemented
		default:
			f.logger.Warn("workerconfig: unknown provider — falling back to global",
				"provider", cfg.LLMProvider)
		}
	}
	return f.globalProvider
}
