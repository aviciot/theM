// Package admin provides REST API handlers for administrative operations:
// managing agents, orchestrators, applications, entry points, and runs.
// All admin endpoints require JWT authentication with the super_admin role.
package admin

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/aviciot/them/internal/auth"
)

// DBQuerier is the database interface required by all admin handlers.
type DBQuerier interface {
	// Query runs a SELECT and returns a RowScanner.
	Query(ctx context.Context, sql string, args ...any) (RowScanner, error)
	// QueryRow runs a SELECT and returns a single-row scanner.
	QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner
	// Exec executes a statement and discards the result.
	Exec(ctx context.Context, sql string, args ...any) error
	// ExecReturning executes a statement and scans the returned row.
	ExecReturning(ctx context.Context, sql string, args ...any) SingleRowScanner
}

// RowScanner iterates over query rows.
type RowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
}

// SingleRowScanner scans a single row.
type SingleRowScanner interface {
	Scan(dest ...any) error
}

// CacheInvalidator invalidates Redis caches on mutations.
type CacheInvalidator interface {
	Del(ctx context.Context, key string) error
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
