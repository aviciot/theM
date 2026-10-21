// Command auth-server is the-M's user-facing authentication service in Go.
// It replaces the Python them-auth-service for the UI-facing contract
// (login/me/refresh/logout + verify/validate) while reading the existing
// auth_service schema and issuing HS256 JWTs the Go bridge already validates.
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

	"github.com/redis/rueidis"

	"github.com/aviciot/them/internal/authserver"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/events"
	"github.com/aviciot/them/internal/idpcrypto"
	"github.com/aviciot/them/internal/telemetry"
)

const version = "1.0.0"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ── 1. Config ─────────────────────────────────────────────────────────────
	cfg, err := authserver.LoadConfig()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// ── 2. Telemetry ──────────────────────────────────────────────────────────
	tel := telemetry.New(cfg.LogLevel, cfg.LogFormat, cfg.InstanceID)
	log := tel.Logger
	log.Info("auth-server configuration loaded", "config", cfg.SafeString())

	// ── 3. Database (them DB, auth_service schema) ───────────────────────────
	ctx := context.Background()
	database, err := db.New(ctx, cfg.DSN())
	if err != nil {
		return fmt.Errorf("startup: postgres: %w", err)
	}
	defer database.Close()
	log.Info("postgres connected", "host", cfg.DBHost, "dbname", cfg.DBName)

	// ── 4. Wire service + handlers + router ──────────────────────────────────
	store := authserver.NewPgxStore(database.Pool())
	svc := authserver.NewService(store, cfg, log)

	idpKey, err := idpcrypto.ParseKey(cfg.IDPEncryptionKey)
	if err != nil {
		return fmt.Errorf("IDP_ENCRYPTION_KEY: %w", err)
	}

	// ── 4a. Redis (optional — OIDC debug records only) ────────────────────────
	var redisClient rueidis.Client
	if addr := cfg.RedisAddr(); addr != "" {
		opts := rueidis.ClientOption{
			InitAddress: []string{addr},
		}
		if cfg.RedisPassword != "" {
			opts.Password = cfg.RedisPassword
		}
		rc, err := rueidis.NewClient(opts)
		if err != nil {
			log.Warn("redis unavailable — OIDC debug writes disabled", "err", err)
		} else {
			redisClient = rc
			defer rc.Close()
			log.Info("redis connected for OIDC debug", "addr", addr)
		}
	}

	// ── 4b. Redis Events (dedicated queue for observability events) ──────────
	var redisEventsClient rueidis.Client
	if cfg.RedisEventsAddr() != "" {
		opts := rueidis.ClientOption{
			InitAddress:  []string{cfg.RedisEventsAddr()},
			SelectDB:     0,
			DisableCache: true,
		}
		if cfg.RedisEventsPassword != "" {
			opts.Password = cfg.RedisEventsPassword
		}
		rc, err := rueidis.NewClient(opts)
		if err != nil {
			database.Close()
			log.Error("failed to connect to redis events", "err", err)
			return fmt.Errorf("startup: redis events: %w", err)
		}
		redisEventsClient = rc
		defer rc.Close()
		log.Info("redis events connected", "addr", cfg.RedisEventsAddr())
	}

	// ── 4c. Event sink (user activity) ────────────────────────────────────────
	// Mode: postgres (default), clickhouse (blocked), dual
	// Safety: ValidateEventsMode rejects unknown values and blocks clickhouse-only mode
	var authEventSink events.EventSink
	eventsMode, err := events.ValidateEventsMode("THE_M_EVENTS_AUTH")
	if err != nil {
		log.Error("invalid events mode", slog.String("error", err.Error()))
		return fmt.Errorf("startup: %w", err)
	}

	// Check Redis requirement for dual/clickhouse modes
	redisEventsAddr := ""
	if redisEventsClient != nil {
		redisEventsAddr = cfg.RedisEventsAddr()
	}
	if err := events.RequireRedisForMode(eventsMode, redisEventsAddr); err != nil {
		log.Error("events mode configuration error", slog.String("error", err.Error()))
		return fmt.Errorf("startup: %w", err)
	}

	switch eventsMode {
	case events.ModeDual:
		// Dual mode: Postgres (authoritative) + ClickHouse (mirror)
		// Mirror failures are logged but don't block requests
		authEventSink = events.NewRedisSink(redisEventsClient, "them:events", 10000, 1*time.Second)
		log.Info("auth events sink: dual mode (Postgres authoritative, ClickHouse mirror)",
			"mode", eventsMode, "redis", cfg.RedisEventsAddr())
	default:
		// ModePostgres: no event emission, Postgres writes only
		authEventSink = nil
		log.Info("auth events sink: disabled (Postgres only)", "mode", eventsMode)
	}

	handlers := authserver.NewHandlers(svc, cfg, log).WithEventSink(authEventSink)

	oidcStore := authserver.NewPgxOIDCStoreWithKeyAndRedis(database.Pool(), idpKey, redisClient)
	signer := authserver.NewTokenSigner(cfg)
	oidcHandlers := authserver.NewOIDCHandlers(oidcStore, signer, cfg, log)
	userMgmt := authserver.NewUserMgmtHandlers(store, cfg, log)
	router := authserver.NewRouterWithAdmin(handlers, oidcHandlers, userMgmt, store, version)

	srv := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// ── 5. Serve with graceful shutdown ──────────────────────────────────────
	serverErr := make(chan error, 1)
	go func() {
		log.Info("auth-server listening", "addr", cfg.Addr())
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
	if authEventSink != nil {
		sinkCtx, sinkCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer sinkCancel()
		if err := authEventSink.Close(sinkCtx); err != nil {
			log.Error("failed to drain auth event sink", "error", err)
		} else {
			log.Info("auth event sink drained")
		}
	}

	log.Info("auth-server stopped cleanly")
	return nil
}
