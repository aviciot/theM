// Command worker is the dedicated Go Temporal worker for the-M (R-2C).
//
// It polls the Go-only task queue ("them-orchestration-go" by default, or
// WORKER_TASK_QUEUE env var) and executes OrchestrationWorkflow +
// RunOrchestratorActivity. It does NOT expose any HTTP/WS/SSE/admin routes.
//
// The Bridge (cmd/them) connects to Temporal only as a client (starts workflows),
// while this binary is the sole Go worker registrant.
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
	recorder := runrecorder.NewRecorder(runrecorder.NewPgxPoolQuerier(database.Pool())).
		WithRunEventsMode(cfg.RunEventsMode)

	// ── 7. Create LLM provider ────────────────────────────────────────────────
	var llmProvider llm.Provider
	if cfg.AnthropicAPIKey != "" {
		llmProvider = llm.NewAnthropicProvider(cfg.AnthropicAPIKey, "", 0)
		log.Info("LLM: Anthropic provider configured")
	} else {
		llmProvider = &llm.MockProvider{}
		log.Warn("LLM: no ANTHROPIC_API_KEY set — using mock provider")
	}

	// ── 8. Create orchestrator ────────────────────────────────────────────────
	// agents (agent registry) is nil for now — tool invocations are wired in a
	// later phase. The orchestrator handles agent-less runs correctly.
	orchCfg := orchestrator.Config{
		MaxIterations: 10,
	}
	orch := orchestrator.New(orchCfg, llmProvider, nil, recorder, bus, log)

	// ── 8b. Phase 3 — forward bus events to Redis Streams ────────────────────
	// The worker and bridge are separate processes. Subscribe to all events on
	// the in-process bus with a wildcard topic ("*") and forward each to the
	// run's Redis Stream so StreamFromRedis in the bridge can receive them.
	//
	// We use a background context tied to the worker's lifetime; the goroutine
	// exits when the subscription channel is closed (on unsub() call below).
	streamWriter := cache.NewRunStreamerWriterRedisClient(redisCache.Client())
	log.Info("Redis Stream writer initialised — cross-process event forwarding active")

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	evCh, _, unsub := bus.Subscribe(streamCtx, "*", 512)
	defer unsub()

	go func() {
		for ev := range evCh {
			if ev.RunID == "" {
				// Skip events without a run ID — they cannot be routed to a stream.
				continue
			}
			runstream.PublishEvent(streamCtx, streamWriter, log, ev)
		}
	}()

	// ── 9. Connect Temporal client ────────────────────────────────────────────
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

	// ── 10. Create and register Go Temporal worker ────────────────────────────
	// The worker polls GoTaskQueue ("them-orchestration-go") exclusively.
	// Override the queue via WORKER_TASK_QUEUE for non-default deployments.
	taskQueue := cfg.WorkerTaskQueue
	goWorker := temporalworker.New(temporalCli, taskQueue, temporalworker.Options{})
	goWorker.RegisterWorkflow(temporal.OrchestrationWorkflow)
	acts := &temporal.Activities{Runner: orch}
	goWorker.RegisterActivity(acts.RunOrchestratorActivity)

	if err := goWorker.Start(); err != nil {
		return fmt.Errorf("startup: temporal worker: %w", err)
	}

	log.Info("Go Worker polling", "task_queue", taskQueue)

	// ── 11. Block on SIGTERM / SIGINT ─────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Info("Go Worker: shutdown signal received — draining worker")
	goWorker.Stop()
	log.Info("Go Worker stopped")

	return nil
}
