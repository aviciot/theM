// Package admin provides REST API handlers for administrative operations:
// managing agents, orchestrators, applications, entry points, and runs.
// All admin endpoints require JWT authentication with the super_admin role.
//
// SQL query strings and row-scan logic live in the dal sub-package.
// Handler files are thin HTTP translators that call dal functions.
package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
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
// Alias for service.Cache — defined in the service package where it is consumed.
type CacheInvalidator = service.Cache

// TemporalSignaler sends HITL signals to Temporal workflows.
// Alias for service.Temporal — defined in the service package where it is consumed.
type TemporalSignaler = service.Temporal

// SessionReader is the admin service's view of the session store.
// Alias for service.SessionReader — keeps router.go and main.go free of a direct
// service import and consistent with the CacheInvalidator / TemporalSignaler pattern.
type SessionReader = service.SessionReader

// Token types re-exported for handler use.
type Token = dal.Token
type TokenCreatedOut = dal.TokenCreatedOut
type TokenCreateRow = dal.TokenCreateRow
type TokenPatchRow = dal.TokenPatchRow

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

// writeServiceError maps a typed service error to the appropriate HTTP status code.
// For untyped errors handlers write their own prefix-prefixed 500 inline, so this
// helper covers only the typed sentinel cases.
func writeServiceError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, service.ErrValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrUnprocessable):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, service.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, service.ErrTemporalUnavailable):
		writeError(w, http.StatusServiceUnavailable, err.Error())
	default:
		return false
	}
	return true
}
