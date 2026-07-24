// Package admin provides REST API handlers for administrative operations:
// managing agents, orchestrators, applications, entry points, and runs.
// All admin endpoints require JWT authentication with the super_admin role.
//
// SQL query strings and row-scan logic live in the dal sub-package.
// Handler files are thin HTTP translators that call dal functions.
package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/auth"
)

// ── Type aliases — re-export dal types so existing callers and tests compile
// unchanged after the SQL moved to the dal sub-package. ──────────────────────

// DBQuerier is the database interface required by all admin handlers.
// It is satisfied by admin.PgxQuerier and by the fakeDB used in tests.
type DBQuerier = dal.Querier

// RowScanner iterates over query rows.
type RowScanner = dal.RowScanner

// SingleRowScanner scans a single row.
type SingleRowScanner = dal.SingleRowScanner

// Agent, AgentInput, Orchestrator, OrchestratorInput, Application,
// EntryPoint, ApplicationInput, EntryPointInput, Run, SignalInput are defined
// in the dal package and re-exported here for backward compatibility.
type Agent = dal.Agent
type AgentInput = dal.AgentInput
type Orchestrator = dal.Orchestrator
type OrchestratorInput = dal.OrchestratorInput
type Application = dal.Application
type EntryPoint = dal.EntryPoint
type ApplicationInput = dal.ApplicationInput
type EntryPointInput = dal.EntryPointInput
type Run = dal.Run
type SignalInput = dal.SignalInput

// CacheInvalidator invalidates Redis caches on mutations.
type CacheInvalidator interface {
	Del(ctx context.Context, key string) error
	// Publish broadcasts an invalidation message to a Redis pub/sub channel.
	// Used to propagate EP config cache evictions across pods.
	Publish(ctx context.Context, channel, message string) error
}

// TemporalSignaler sends HITL signals to Temporal workflows.
type TemporalSignaler interface {
	SignalRun(ctx context.Context, runID string, payload []byte) error
}

// RequireSuperAdmin returns a middleware that requires a valid JWT with the
// super_admin role. Relies on auth.ClaimsFromCtx (set by JWTMiddleware).
func RequireSuperAdmin(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := auth.ClaimsFromCtx(r.Context())
			if !ok {
				writeError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			isSuperAdmin := false
			for _, role := range claims.Roles {
				if role == "super_admin" {
					isSuperAdmin = true
					break
				}
			}
			if !isSuperAdmin {
				writeError(w, http.StatusForbidden, "super_admin role required")
				return
			}

			if logger != nil {
				logger.Debug("admin: authorized",
					"user", claims.Username,
					"path", r.URL.Path,
					"method", r.Method)
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeJSON marshals v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
