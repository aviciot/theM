// Package main is the them-agent-runtime — a generic stateless A2A agent server.
// It reads AgentSpec definitions from PostgreSQL (cached locally, TTL 60s) and
// serves any canvas-designed agent over the A2A JSON-RPC 2.0 and streaming wire
// protocol, backed by the official github.com/a2aproject/a2a-go/v2 SDK.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/temporal"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	ctx := context.Background()

	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		logger.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer database.Close()

	var rlsPools *db.Pools
	if cfg.DBURLApp != "" && cfg.DBURLAdmin != "" {
		rlsPools, err = db.NewPools(ctx, cfg.DBURLApp, cfg.DBURLAdmin)
		if err != nil {
			logger.Error("rls pools connect failed", "err", err)
			os.Exit(1)
		}
		defer rlsPools.Close()
	}
	_ = rlsPools

	redisCache, err := cache.New(ctx, cfg.RedisAddr(), cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		logger.Error("redis connect failed", "err", err)
		os.Exit(1)
	}

	taskRedis := cache.NewAuthRedisClient(redisCache.Client())
	cryptoKey := crypto.DeriveKey(cfg.SecretKey)

	interpBase := agentgen.NewInterpreter(
		&http.Client{Timeout: 60 * time.Second},
		&multiLLMFactory{platformKey: cfg.AnthropicAPIKey},
		cfg.AnthropicAPIKey,
	)
	if cfg.MCPServiceURL != "" {
		interpBase.WithMCPCaller(agentgen.NewHTTPMCPCaller(cfg.MCPServiceURL, &http.Client{Timeout: 30 * time.Second}))
	}

	// A2A inter-agent call support: resolve target endpoint from DB, decrypt auth token.
	a2aResolver := agentgen.NewDBAgentEndpointResolver(
		&pgxAgentEndpointQueryer{pool: database.Pool()},
		func(ct string) (string, error) { return crypto.DecryptStored(cryptoKey, ct) },
	)
	interpBase.WithA2ACaller(agentgen.NewHTTPA2ACaller(a2aResolver, &http.Client{Timeout: 5 * time.Minute}))

	rt := &Runtime{
		pool:      database.Pool(),
		cryptoKey: cryptoKey,
		taskStore: agentgen.NewRedisA2ATaskStore(taskRedis),
		hitlStore: agentgen.NewHITLStore(taskRedis),
		specCache: &specCache{entries: make(map[string]*cachedSpec)},
		logger:    logger,
		interp:    interpBase,
	}

	// When Temporal is enabled, create a TemporalExecutor so canvas agents with
	// execution_backend=="temporal" can be routed to the DAG worker.
	if cfg.TemporalEnabled {
		temporalCli, err := temporal.Connect(cfg.TemporalHostPort, logger)
		if err != nil {
			logger.Error("temporal connect failed", "err", err)
			os.Exit(1)
		}
		te := temporal.NewTemporalExecutor(temporalCli, 0, 0, logger)
		rt.temporalExecutor = te
		rt.canvasSubmitter = te
		rt.canvasSignaler = te
		rt.canvasAwaiter = te
		rt.canvasCanceler = te
		rt.canvasHITLQuerier = te
		logger.Info("temporal executor configured", "host_port", cfg.TemporalHostPort)
	}

	port := "9300"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	r := chi.NewRouter()
	r.Get("/healthz", rt.healthz)
	// SDK card handler: NewStaticAgentCardHandler requires the spec to build the card.
	// We serve it via a thin wrapper that loads the spec then delegates to the SDK handler.
	r.Get("/agents/{slug}/.well-known/agent-card.json", rt.agentCard)
	// A2A JSON-RPC endpoint: auth + spec + binding resolution happens here in middleware,
	// then the SDK's NewJSONRPCHandler dispatches message/send, tasks/get, tasks/cancel,
	// message/stream, tasks/resubscribe and all other A2A methods.
	r.Post("/agents/{slug}", rt.handle)

	logger.Info("them-agent-runtime starting", "port", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		logger.Error("server error", "err", err)
		os.Exit(1)
	}
}
