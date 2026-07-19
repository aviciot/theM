// Package health provides liveness and readiness HTTP handlers.
//
// Liveness (/health/live) always returns 200 when the process is running.
// Readiness (/health/ready) probes PostgreSQL and Redis with a 2-second timeout
// and returns 200 when both are reachable or 503 when either fails.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const readinessTimeout = 2 * time.Second

// Pinger is implemented by any dependency that supports a Ping method.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Handler holds the dependencies required for health-check responses.
type Handler struct {
	instanceID string
	db         Pinger
	cache      Pinger
}

// New returns a Handler configured with the provided instance identifier and
// dependency pingers.
func New(instanceID string, db, cache Pinger) *Handler {
	return &Handler{
		instanceID: instanceID,
		db:         db,
		cache:      cache,
	}
}

// liveResponse is the JSON body for the liveness endpoint.
type liveResponse struct {
	Status   string `json:"status"`
	Instance string `json:"instance"`
}

// readyResponse is the JSON body for the readiness endpoint.
type readyResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Live handles GET /health/live. It always returns 200 with a small JSON body
// as long as the process is running; no dependency probes are performed.
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, liveResponse{
		Status:   "ok",
		Instance: h.instanceID,
	})
}

// Ready handles GET /health/ready. It probes PostgreSQL and Redis within a
// 2-second deadline. If both succeed it returns 200; if either fails it
// returns 503 with per-dependency error details.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()

	checks := map[string]string{}
	healthy := true

	if err := h.db.Ping(ctx); err != nil {
		checks["postgres"] = "error: " + err.Error()
		healthy = false
	} else {
		checks["postgres"] = "ok"
	}

	if err := h.cache.Ping(ctx); err != nil {
		checks["redis"] = "error: " + err.Error()
		healthy = false
	} else {
		checks["redis"] = "ok"
	}

	status := "ok"
	code := http.StatusOK
	if !healthy {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	writeJSON(w, code, readyResponse{Status: status, Checks: checks})
}

// writeJSON marshals v as JSON and writes it to w with the given status code.
// Content-Type is set to application/json.
func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// If encoding fails the headers are already sent; nothing more we can do.
		_ = err
	}
}
