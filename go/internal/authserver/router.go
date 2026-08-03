package authserver

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
)

// healthChecker is implemented by the store (Ping) for readiness probes.
type healthChecker interface {
	Ping(ctx context.Context) error
}

// NewRouter builds the full chi router. It registers the auth endpoints under
// BOTH /api/v1/auth (the path the frontend proxy uses via THE_M_AUTH_URL) and
// /auth/api/v1/auth (the Traefik-mirrored prefix the Python service exposed), so
// external routing parity is preserved. Health endpoints are registered at
// /health, /health/live, /health/ready, and mirrored under /auth.
func NewRouter(h *Handlers, hc healthChecker, version string) http.Handler {
	r := chi.NewRouter()

	registerHealth(r, hc, version)
	registerAuth(r, h, "/api/v1/auth")

	// Traefik mirror: the Python service mounted the same routers under /auth.
	r.Route("/auth", func(sub chi.Router) {
		registerHealth(sub, hc, version)
		registerAuth(sub, h, "/api/v1/auth")
	})

	return r
}

// registerAuth mounts the auth handlers under the given base path.
func registerAuth(r chi.Router, h *Handlers, base string) {
	r.Route(base, func(a chi.Router) {
		a.Post("/login", h.Login)
		a.Get("/me", h.Me)
		a.Post("/refresh", h.Refresh)
		a.Post("/logout", h.Logout)
		a.Post("/verify", h.Verify)
		a.Get("/validate", h.Validate)
	})
}

// registerHealth mounts liveness/readiness/health under r.
func registerHealth(r chi.Router, hc healthChecker, version string) {
	r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
		status := "ok"
		db := "connected"
		code := http.StatusOK
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := hc.Ping(ctx); err != nil {
			status, db, code = "degraded", "error", http.StatusOK // Python returns 200 degraded
		}
		writeJSON(w, code, map[string]any{
			"status": status, "service": "them-auth-go", "version": version, "database": db,
		})
	})
	r.Get("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	r.Get("/health/ready", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		if err := hc.Ping(ctx); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}
