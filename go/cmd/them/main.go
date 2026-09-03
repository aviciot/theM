// Command them is the THEM v2 Go platform entrypoint.
// It wires configuration, database, Redis, telemetry, health checks, the
// in-process event bus, the orchestration layer, WebSocket, SSE, A2A, admin
// API, and rate limiting together, then blocks until a shutdown signal.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	temporalclient "go.temporal.io/sdk/client"

	"time"

	"github.com/aviciot/them/internal/a2a"
	"github.com/aviciot/them/internal/admin"
	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/agentregistry"
	"github.com/aviciot/them/internal/appliveness"
	"github.com/aviciot/them/internal/dashboard"
	"github.com/aviciot/them/internal/artifacts"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/cache"
	"github.com/aviciot/them/internal/config"
	"github.com/aviciot/them/internal/crypto"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/health"
	"github.com/aviciot/them/internal/middleware"
	"github.com/aviciot/them/internal/storage"
	"github.com/aviciot/them/internal/quota"
	"github.com/aviciot/them/internal/ratelimit"
	"github.com/aviciot/them/internal/reconciler"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/server"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/sse"
	"github.com/aviciot/them/internal/telemetry"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/tenantctx"
	"github.com/aviciot/them/internal/transport"
	"github.com/aviciot/them/internal/voice"
	"github.com/aviciot/them/internal/ws"

	"github.com/redis/rueidis"
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
	// ctx is used only for startup I/O (DB ping, Redis ping).
	// runCtx governs all long-lived goroutines so they are cancelled together
	// before the HTTP drain begins (R-0 L-2 + L-3 fix).
	ctx := context.Background()
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		log.Error("failed to connect to postgres", slog.String("error", err.Error()))
		return fmt.Errorf("startup: postgres: %w", err)
	}
	log.Info("postgres connected", "host", cfg.DBHost, "dbname", cfg.DBName)

	// ── 3b. Create RLS pools (Step 19) — optional until THEM_DB_URL_APP is configured ──
	var rlsPools *db.Pools
	if cfg.DBURLApp != "" && cfg.DBURLAdmin != "" {
		rlsPools, err = db.NewPools(ctx, cfg.DBURLApp, cfg.DBURLAdmin)
		if err != nil {
			database.Close()
			log.Error("failed to create RLS pools", slog.String("error", err.Error()))
			return fmt.Errorf("startup: rls pools: %w", err)
		}
		log.Info("RLS pools connected (them_app + them_admin)")
		defer rlsPools.Close()
	} else {
		log.Info("RLS pools not configured — THEM_DB_URL_APP/THEM_DB_URL_ADMIN not set")
	}

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
	// Every new run row records events_transport='streams'; the Go worker always
	// writes run events to Redis Streams and the bridge reads them from there.
	recorder := runrecorder.NewRecorder(runrecorder.NewPgxPoolQuerier(database.Pool()))

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
	go agentReg.Subscribe(runCtx)
	log.Info("agent registry initialised with pub/sub cache invalidation")

	// ── 11. Build auth middleware ─────────────────────────────────────────────
	// Priority: RS256 (JWT_PUBLIC_KEY_PEM) > HS256 (JWT_SECRET) > HS256 (SECRET_KEY) > disabled.
	// Production: auth service signs with JWT_SECRET (HS256). RS256 kept for test envs.
	// JWT_SECRET takes priority over SECRET_KEY for HS256 since it is the canonical
	// secret used by the auth service, while SECRET_KEY is a platform-level secret.
	var jwtMiddleware func(http.Handler) http.Handler
	if cfg.JWTPublicKey != nil {
		jwtMiddleware = auth.JWTMiddleware(cfg.JWTPublicKey)
		log.Info("JWT middleware enabled (RS256)")
	} else if cfg.JWTSecret != "" {
		jwtMiddleware = auth.HS256Middleware([]byte(cfg.JWTSecret))
		log.Info("JWT middleware enabled (HS256 via JWT_SECRET)")
	} else if cfg.SecretKey != "" {
		jwtMiddleware = auth.HS256Middleware([]byte(cfg.SecretKey))
		log.Info("JWT middleware enabled (HS256 via SECRET_KEY)")
	} else {
		log.Warn("JWT middleware disabled — neither JWT_PUBLIC_KEY_PEM nor JWT_SECRET nor SECRET_KEY is set")
	}

	// ── 12. Build health handler and HTTP server ──────────────────────────────
	healthHandler := health.New(cfg.InstanceID, database, redisCache)
	addr := fmt.Sprintf("%s:%d", cfg.AppHost, cfg.AppPort)

	authMW := server.AuthMiddlewares{}
	drainDuration := time.Duration(cfg.ShutdownDrainSeconds) * time.Second
	srv := server.NewWithBus(addr, healthHandler, authMW, bus, drainDuration, log, database, redisCache)

	// ── 13. Wire bearer token cache (L1 in-process → L2 Redis → PostgreSQL) ──
	tokenDB := auth.NewPgxQuerier(database.Pool())
	tokenRedis := cache.NewAuthRedisClient(redisCache.Client())
	tokenCache := auth.NewCache(tokenDB, tokenRedis, log)
	// Start cross-pod revocation listener. Blocks until runCtx is cancelled;
	// runCancel is called as the pre-drain hook before HTTP connections are
	// force-closed (R-0 L-3 fix).
	go tokenCache.Subscribe(runCtx)
	log.Info("bearer token cache initialised (L1+L2+pub/sub revocation)")

	// Chain authenticator: try opaque bearer token cache first, then fall back to
	// JWT session token validation. This allows the playground (which passes the
	// session JWT as ?token) to authenticate against token-mode EPs without
	// requiring a separate opaque access token.
	var authenticator transport.Authenticator = tokenCache
	if cfg.JWTSecret != "" {
		authenticator = &jwtFallbackAuthenticator{
			primary:   tokenCache,
			jwtSecret: []byte(cfg.JWTSecret),
		}
	} else if cfg.SecretKey != "" {
		authenticator = &jwtFallbackAuthenticator{
			primary:   tokenCache,
			jwtSecret: []byte(cfg.SecretKey),
		}
	}

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
		go reconciler.Run(runCtx, recCfg, recDB, temporalCli, log)
		log.Info("run reconciler started", "dry_run", recCfg.DryRun)
	}

	// ── 14. Wire EP config loader (shared by WS + SSE) ───────────────────────
	epDB := epconfig.NewPgxQuerier(database.Pool())
	epLoader := epconfig.NewLoader(epDB, log)
	// Subscribe for cross-pod cache invalidation. The session Redis client
	// already satisfies epconfig.RedisSubscriber (same Subscribe signature).
	epConfigSub := cache.NewSessionRedisClient(redisCache.Client())
	epLoader.Subscribe(runCtx, epConfigSub)
	log.Info("EP config loader initialised with pub/sub invalidation")

	// ── 15. Wire run-event reader (Redis Streams) ────────────────────────────
	// Shared by the WS and SSE handlers. The Go worker always writes run events
	// to them:dash:run:{runID}:stream; this reader replays + tails that stream.
	rsStreamer := cache.NewRunStreamerRedisClient(redisCache.Client())

	// ── 16. Build shared execution lifecycle (Admit/Start/Release) ──────────
	// Shared by SSE, A2A, and (future) WS. Owns: auth, EPConfig, gate, session,
	// CreateRun, ExecuteWorkflow identity enforcement. Protocol handlers keep only
	// their wire-format concerns.
	execLifecycle := execution.NewLifecycle(
		authenticator,
		epLoader,
		admissionGate,
		sessionStore,
		recorder,
		temporalCli,
		log,
	)

	// ── 16a. Wire quota enforcer ─────────────────────────────────────────────
	// Reuses the same RateLimitClient already constructed above for per-token RL.
	quotaDB := dal.NewDB(admin.NewPgxQuerier(database.Pool()))
	quotaRedis := cache.NewRateLimitClient(redisCache.Client())
	quotaEnf := quota.New(quotaDB, quotaRedis)
	quotaAdapter := &tenantQuotaAdapter{db: quotaDB, enforcer: quotaEnf}
	execLifecycle.WithQuotaEnforcer(quotaAdapter)
	log.Info("quota enforcer wired (max_concurrent_runs + runs_per_minute)")

	// ── 16b. Wire dashboard WebSocket handler (/ws/dashboard) ───────────────
	// Pure Redis pub/sub relay — multiplexes agent scan events, run events,
	// session events to browser clients. No Temporal, no recording.
	// Auth: session JWT (HS256) from ?token= query param.
	// MountDashboardWS must be called BEFORE MountWS (exact path beats prefix mount).
	dashJWTSecret := []byte(cfg.JWTSecret)
	if len(dashJWTSecret) == 0 {
		dashJWTSecret = []byte(cfg.SecretKey)
	}
	dashHandler := dashboard.New(redisCache.Client(), dashJWTSecret, log)
	srv.MountDashboardWS(dashHandler)
	log.Info("dashboard WebSocket handler mounted", "path", "/ws/dashboard")

	// ── 16c. Wire WebSocket handler (/ws/*) ──────────────────────────────────
	// Auth, EPConfig, gate, session, CreateRun, and temporal start are now owned by
	// execLifecycle. The WS handler retains only upgrade, frame I/O, and metrics.
	sessionPubRedis := cache.NewSessionRedisClient(redisCache.Client())
	sessionPub := dashboard.NewSessionPublisher(sessionPubRedis, log)
	wsHandler := ws.NewHandler(execLifecycle, bus, authenticator, cfg.InstanceID, log).
		WithRunStreamer(rsStreamer).
		WithSessionPublisher(sessionPub)
	srv.MountWS(wsHandler.Routes())
	log.Info("WebSocket handler mounted", "prefix", "/ws")

	// ── 17. Wire SSE handler (/sse/*) ─────────────────────────────────────────
	// Lifecycle handles auth, EPConfig, gate, session, CreateRun.
	// SSE handler retains: SSE headers, streaming, metrics.
	sseHandler := sse.NewHandler(execLifecycle, recorder, bus, authenticator, cfg.InstanceID, log).
		WithRunStreamer(rsStreamer)
	srv.MountSSE(sseHandler.Routes())
	log.Info("SSE handler mounted", "prefix", "/sse")

	// ── 17b. Start pod heartbeat loop ─────────────────────────────────────────
	// Writes them:pod:{instance_id} every 15 s so the session reconciler knows
	// this replica is alive. Session TTL is 90 s so 15 s gives 6 misses before
	// a pod is considered dead — wide enough to survive transient Redis blips.
	// Uses runCtx so the ticker stops before the HTTP drain begins (R-0 L-2 fix).
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				if err := sessionStore.WriteHeartbeat(runCtx); err != nil {
					log.Warn("pod heartbeat failed", "error", err)
				}
			}
		}
	}()
	log.Info("pod heartbeat loop started", "interval", "15s")

	// ── 17c. Start app liveness loop ─────────────────────────────────────────
	// Probes all enabled entry points immediately on startup then every 30s.
	// Publishes to them:dash:apps (live push) and caches at
	// them:dash:app_status_cache (snapshot for new WS subscribers).
	go appliveness.Loop(runCtx, database.Pool(), rlsPools, redisCache.Client(), cfg.AppPort, log)
	log.Info("app liveness loop started", "interval", "30s")

	// Register runCancel as the pre-drain hook so all subscriber goroutines
	// (token revocation, epconfig invalidation, agent registry, heartbeat,
	// reconciler) are cancelled before httpServer.Shutdown drains connections
	// (R-0 L-3 fix). runCancel is idempotent; defer above is a safety net.
	srv.WithPreDrainHook(runCancel)

	// /apps/* mounting is deferred to section 19 once adminDB and adminFernetKey
	// are available (voice handler requires the AppService for provider-key decryption).

	// ── 17b. Wire A2A server (/a2a/*, /.well-known/*) ────────────────────────
	// Uses the shared execLifecycle (constructed in section 16).
	a2aTaskRedis := cache.NewAuthRedisClient(redisCache.Client())
	a2aTaskStore := agentgen.NewRedisA2ATaskStore(a2aTaskRedis)

	// File gate: intercepts A2A file artifacts when security scanning is enabled.
	// Build MinIO storage client if S3 config is present; otherwise fail-open.
	// storageClient is the concrete MinIO client shared by the gate and artifact handler.
	// nil when S3 is not configured (both paths fail-open / serve from Postgres BYTEA).
	var storageClient *storage.Client
	var fileGateStore middleware.Store
	if cfg.S3Endpoint != "" {
		sc, scErr := storage.New(storage.Config{
			Endpoint:         cfg.S3Endpoint,
			AccessKey:        cfg.S3AccessKey,
			SecretKey:        cfg.S3SecretKey,
			QuarantineBucket: cfg.S3QuarantineBucket,
			ArtifactsBucket:  cfg.S3ArtifactsBucket,
		})
		if scErr != nil {
			log.Warn("storage client init failed — security gate will fail-open", "err", scErr)
		} else {
			storageClient = sc
			fileGateStore = sc
			log.Info("storage client initialised", "endpoint", cfg.S3Endpoint)
		}
	} else {
		log.Warn("THE_M_S3_ENDPOINT not set — security gate will fail-open for all apps")
	}
	fileGate := middleware.NewFileGate(middleware.NewPgxQuerier(database.Pool()), fileGateStore)
	// Subscribe to security config invalidation so the 30s cache is busted
	// immediately when an admin saves a new config via PUT /security-config.
	go func() {
		cmd := redisCache.Client().B().Subscribe().
			Channel("them:security_config:invalidated:*").Build()
		_ = redisCache.Client().Receive(ctx, cmd, func(msg rueidis.PubSubMessage) {
			appID := strings.TrimPrefix(msg.Channel, "them:security_config:invalidated:")
			fileGate.InvalidateCache(appID)
			log.Info("file gate: security config cache invalidated", "app_id", appID)
		})
	}()

	a2aServer := a2a.NewServer(
		execLifecycle,
		bus,
		authenticator,
		cfg.InstanceID,
		log,
	).WithRunStreamer(rsStreamer).
		WithPublicURL(cfg.PublicURL).
		WithCardLoader(a2a.NewPgxCardLoader(database.Pool())).
		WithSessionPublisher(sessionPub).
		WithTaskStore(a2aTaskStore).
		WithFileGate(&fileGateAdapter{gate: fileGate})
	srv.MountA2A(a2aServer.Routes())
	log.Info("A2A server mounted")

	// ── 18. Wire artifact download endpoint (Phase R-3) ─────────────────────
	// Route: GET /api/v1/runs/{run_id}/artifacts/{artifact_id}
	// Authentication: bearer token (same as WS/SSE — NOT RequireSuperAdmin JWT).
	// IMPORTANT: MountArtifacts must be called BEFORE MountAdmin. The artifact
	// route is a direct chi.Get registration at the full path; MountAdmin adds a
	// sub-router catch-all at /api/v1. Chi resolves direct routes before Mount
	// catch-alls, so registration order ensures the specific path wins.
	// Use Handler() (not Routes()) so no internal chi routing re-matches the path.
	artifactHandler := artifacts.NewWithFetcher(tokenCache, recorder, storageClient, log)
	srv.MountArtifacts(artifactHandler.Handler())
	log.Info("artifact download endpoint mounted", "path", "/api/v1/runs/{run_id}/artifacts/{artifact_id}")

	// ── 19. Wire admin API (/api/v1/admin/*, /api/v1/runs/*) ─────────────────
	adminDB := admin.NewPgxQuerier(database.Pool())
	adminCache := cache.NewAdminCacheClient(redisCache.Client())
	// Temporal signaler is optional — nil if Temporal is not enabled.
	var temporalSignaler admin.TemporalSignaler
	var temporalCanvasSignaler temporal.CanvasSignaler
	if temporalCli != nil {
		temporalSignaler = temporal.NewSignaler(temporalCli)
		temporalCanvasSignaler = temporal.NewTemporalExecutor(temporalCli, 0, 0, log)
	}
	// Derive Fernet key from SECRET_KEY for agent token decryption in action endpoints.
	adminFernetKey := crypto.DeriveKey(cfg.SecretKey)
	adminHITLRedis := cache.NewAuthRedisClient(redisCache.Client())
	adminHITLStore := agentgen.NewHITLStore(adminHITLRedis)
	adminRouter := admin.BuildRouter(adminDB, rlsPools, adminCache, temporalSignaler, sessionStore, jwtMiddleware, tokenCache, log, cfg.SecretKey, redisCache.Client(), adminFernetKey, cfg.MCPServiceURL, cfg.AnthropicAPIKey, adminHITLStore, temporalCanvasSignaler)
	srv.MountAdmin(adminRouter)
	log.Info("admin API mounted", "prefix", "/api/v1")

	// ── 19b. Mount /apps/* (WS + SSE + voice) ────────────────────────────────
	// Voice handler needs AppService (for provider-key decryption), which requires
	// adminDB and adminFernetKey — so it must be wired here, after section 19.
	voiceLoader := voice.NewPgxLoader(database.Pool())
	voiceAppsSvc := admin.NewApplicationsHandler(adminDB, nil, adminCache, adminFernetKey).Svc()
	voiceRunLoader := voice.NewWorkerConfigLoader(database.Pool(), adminFernetKey)
	voiceHandler := voice.NewHandler(voiceLoader, voiceAppsSvc, authenticator, voiceRunLoader, recorder, bus, tenantctx.BootstrapTenantID, log)
	srv.MountApps(appsDispatcher(wsHandler.AppsWSRoute(), sseHandler.AppsSSERoute(), voiceHandler.Routes()))
	log.Info("apps WS+SSE+voice mounted", "prefix", "/apps")

	log.Info("shutdown drain configured", "drain_seconds", cfg.ShutdownDrainSeconds)
	log.Info("starting server", "addr", addr, "env", cfg.AppEnv)

	return srv.ListenAndServe()
}

// appsDispatcher routes /apps/{app_slug}/{ep_slug}/ws to wsApps,
// /apps/{app_slug}/{ep_slug}/sse to sseApps,
// /apps/{app_slug}/{ep_slug}/voice/* to voiceApps, and returns 404 for anything else.
// Each sub-handler owns its own chi router; this function only dispatches.
// voiceApps may be nil (voice is disabled/not wired).
func appsDispatcher(wsApps, sseApps, voiceApps http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/ws"):
			wsApps.ServeHTTP(w, r)
		case strings.HasSuffix(r.URL.Path, "/sse"):
			sseApps.ServeHTTP(w, r)
		case strings.Contains(r.URL.Path, "/voice/") && voiceApps != nil:
			voiceApps.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// fileGateAdapter bridges middleware.FileGate to the a2a.FileInterceptor interface.
type fileGateAdapter struct {
	gate *middleware.FileGate
}

func (a *fileGateAdapter) Intercept(ctx context.Context, in a2a.FileInterceptInput) (a2a.FileInterceptResult, error) {
	gr, err := a.gate.Intercept(ctx, middleware.GateInput{
		DownloadURL:   in.DownloadURL,
		FileName:      in.FileName,
		ContentType:   in.ContentType,
		ApplicationID: in.ApplicationID,
		RunID:         in.RunID,
		SessionID:     in.SessionID,
		TenantID:      in.TenantID,
	})
	if err != nil {
		return a2a.FileInterceptResult{ScanStatus: "error"}, err
	}
	return a2a.FileInterceptResult{
		ArtifactID: gr.ArtifactID,
		ScanStatus: gr.ScanStatus,
	}, nil
}

// jwtFallbackAuthenticator tries the opaque bearer token cache first.
// If the token is not found (not an opaque bearer token), it falls back to
// validating it as an HS256 JWT session token (issued by the auth service).
// This allows the playground to pass its session JWT as a WS ?token param
// against token-mode EPs without requiring a separate access token.
type jwtFallbackAuthenticator struct {
	primary   transport.Authenticator
	jwtSecret []byte
}

// tenantQuotaAdapter implements execution.QuotaEnforcer. It loads the tenant's
// quota row from the DB and delegates enforcement to quota.Enforcer. When no
// quota row exists the check is skipped (fail-open).
type tenantQuotaAdapter struct {
	db       *dal.DB
	enforcer *quota.Enforcer
}

func (a *tenantQuotaAdapter) CheckQuota(ctx context.Context, tenantID string) error {
	q, err := a.db.GetQuota(ctx, tenantID)
	if err != nil {
		// No quota row → no enforcement. Any other DB error → also fail-open.
		return nil
	}
	qe := quota.Quota{
		MaxConcurrentRuns: q.MaxConcurrentRuns,
		RunsPerMinute:     q.RunsPerMinute,
		MonthlyRuns:       q.MonthlyRuns,
	}
	enforceErr := a.enforcer.Check(ctx, tenantID, qe)
	switch {
	case errors.Is(enforceErr, quota.ErrConcurrentRunsExceeded):
		return execution.ErrQuotaConcurrentRuns
	case errors.Is(enforceErr, quota.ErrRunsRateLimited):
		return execution.ErrQuotaRunsPerMinute
	case errors.Is(enforceErr, quota.ErrMonthlyRunsExceeded):
		return execution.ErrQuotaMonthlyRuns
	default:
		return enforceErr
	}
}

func (a *jwtFallbackAuthenticator) Validate(ctx context.Context, rawToken string) (*auth.TokenInfo, error) {
	// Try opaque bearer token first.
	if info, err := a.primary.Validate(ctx, rawToken); err == nil {
		return info, nil
	}
	// Fall back to JWT session token.
	claims, err := auth.ValidateHS256JWT(rawToken, a.jwtSecret)
	if err != nil {
		return nil, err
	}
	return &auth.TokenInfo{
		TokenID:     claims.UserID,
		TenantID:    claims.TenantID,
		Permissions: []string{},
	}, nil
}
