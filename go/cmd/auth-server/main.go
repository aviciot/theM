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

	"github.com/aviciot/them/internal/authserver"
	"github.com/aviciot/them/internal/db"
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
	handlers := authserver.NewHandlers(svc, cfg, log)
	oidcStore := authserver.NewPgxOIDCStore(database.Pool())
	signer := authserver.NewTokenSigner(cfg)
	oidcHandlers := authserver.NewOIDCHandlers(oidcStore, signer, cfg, log)
	router := authserver.NewRouter(handlers, oidcHandlers, store, version)

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

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	log.Info("auth-server stopped cleanly")
	return nil
}
