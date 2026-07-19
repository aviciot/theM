// Package server configures the chi HTTP router, mounts all application routes,
// and implements graceful shutdown. The HTTP server stops accepting connections
// when a SIGTERM or SIGINT signal is received, allowing in-flight requests up
// to a configurable drain timeout before the process exits.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/aviciot/them/internal/health"
)

const (
	drainTimeout      = 5 * time.Second
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
)

// Closer groups the Close/shutdown calls for all long-lived dependencies so
// the server can release them after the HTTP server drains.
type Closer interface {
	Close()
}

// AuthMiddlewares carries the optional JWT and bearer token HTTP middlewares.
// Both fields are optional: a nil middleware is not registered.
// Use WithAuth() to construct this.
type AuthMiddlewares struct {
	JWT    func(http.Handler) http.Handler
	Bearer func(http.Handler) http.Handler
}

// WithAuth returns an AuthMiddlewares value for the provided middleware
// functions. Either argument may be nil (nil = disabled).
func WithAuth(jwt, bearer func(http.Handler) http.Handler) AuthMiddlewares {
	return AuthMiddlewares{JWT: jwt, Bearer: bearer}
}

// Server wraps an http.Server and the chi router, and owns the shutdown logic.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
	closers    []Closer
}

// buildRouter constructs and returns the chi router with all routes mounted.
// Extracted so it can be shared by New (production) and NewRouter (tests).
//
// auth is optional: health and metrics routes are always unauthenticated.
// Authenticated application routes (added in later phases) receive the auth
// middlewares via sub-routers. Passing a zero-value AuthMiddlewares is safe.
func buildRouter(h *health.Handler, auth AuthMiddlewares) *chi.Mux {
	r := chi.NewRouter()

	// Standard middleware — applies to all routes.
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	// Health and metrics routes — always unauthenticated.
	r.Get("/health/live", h.Live)
	r.Get("/health/ready", h.Ready)

	// Prometheus metrics — served from the default global registry which
	// includes Go runtime and process collectors out of the box.
	r.Handle("/metrics", promhttp.Handler())

	// Protected API sub-router — auth middlewares applied when provided.
	// Routes requiring authentication are mounted here in later phases.
	r.Group(func(protected chi.Router) {
		if auth.JWT != nil {
			protected.Use(auth.JWT)
		}
		if auth.Bearer != nil {
			protected.Use(auth.Bearer)
		}
		// TODO Phase 3+: mount protected API routes here.
	})

	return r
}

// New builds and returns a Server with all routes mounted. healthHandler handles
// the /health/* routes. auth carries optional JWT/bearer middleware (use
// WithAuth to construct; pass zero value to disable). closers are called in
// order during graceful shutdown after the HTTP server stops.
func New(addr string, healthHandler *health.Handler, auth AuthMiddlewares, logger *slog.Logger, closers ...Closer) *Server {
	r := buildRouter(healthHandler, auth)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	return &Server{
		httpServer: httpSrv,
		logger:     logger,
		closers:    closers,
	}
}

// NewRouter returns the chi router with all routes mounted, without starting a
// server. Intended for use in tests that need to probe routes via httptest.
// auth carries optional JWT/bearer middleware; pass zero value to disable.
func NewRouter(healthHandler *health.Handler, auth AuthMiddlewares) http.Handler {
	return buildRouter(healthHandler, auth)
}

// ListenAndServe starts the HTTP server and blocks until a SIGTERM or SIGINT is
// received. After the signal it attempts a graceful drain for up to drainTimeout
// seconds, then closes all registered Closers before returning.
func (s *Server) ListenAndServe() error {
	// Channel to receive OS signals.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	// Start server in a goroutine so ListenAndServe doesn't block this one.
	serverErr := make(chan error, 1)
	go func() {
		s.logger.Info("server starting", "addr", s.httpServer.Addr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- fmt.Errorf("server: listen: %w", err)
		}
	}()

	// Block until a signal or a fatal server error.
	select {
	case err := <-serverErr:
		return err
	case sig := <-quit:
		s.logger.Info("shutdown signal received", "signal", sig.String())
	}

	// Graceful drain.
	ctx, cancel := context.WithTimeout(context.Background(), drainTimeout)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		s.logger.Error("server shutdown error", "error", err)
	}

	// Release dependencies in registration order.
	for _, c := range s.closers {
		c.Close()
	}

	s.logger.Info("shutdown complete")
	return nil
}
