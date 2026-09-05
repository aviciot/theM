package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/metrics"
	"github.com/aviciot/them/internal/tenantctx"
)

// AuditLogsHandler handles GET /admin/audit-logs.
// Uses the admin pool (BYPASSRLS) because them.audit_logs has INSERT-only
// RLS for them_app; reads require the admin role.
type AuditLogsHandler struct {
	pools *db.Pools
	legacyDB DBQuerier // fallback when pools is nil (tests)
}

// NewAuditLogsHandler creates an AuditLogsHandler.
func NewAuditLogsHandler(legacyDB DBQuerier, pools *db.Pools) *AuditLogsHandler {
	return &AuditLogsHandler{pools: pools, legacyDB: legacyDB}
}

// Routes mounts the audit log endpoints.
func (h *AuditLogsHandler) Routes(r chi.Router) {
	r.Get("/audit-logs", h.List)
}

const (
	auditLogsDefaultLimit = 50
	auditLogsMaxLimit     = 200
)

// List handles GET /api/v1/admin/audit-logs?limit=50&offset=0.
func (h *AuditLogsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())

	limit := auditLogsDefaultLimit
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			if n > auditLogsMaxLimit {
				n = auditLogsMaxLimit
			}
			limit = n
		}
	}
	offset := 0
	if s := r.URL.Query().Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	var db *dal.DB
	if h.pools != nil {
		db = dal.NewDBFromAdminQuerier(h.pools.NewAdminQuerier())
	} else {
		db = dal.NewDB(h.legacyDB)
	}

	logs, err := db.ListAuditLogs(r.Context(), tenantID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, logs)
}

// ── AuditWriter ───────────────────────────────────────────────────────────────

// AuditWriter writes audit log entries synchronously after successful operations.
// It uses the admin pool (BYPASSRLS) so it bypasses the INSERT-only RLS on audit_logs.
type AuditWriter struct {
	pools       *db.Pools
	testQuerier DBQuerier // non-nil only in tests (set by NewAuditWriterForTest)
}

// NewAuditWriter creates an AuditWriter. Pass nil pools in tests — writes are no-ops.
func NewAuditWriter(pools *db.Pools) *AuditWriter {
	return &AuditWriter{pools: pools}
}

// NewAuditWriterForTest creates an AuditWriter backed by the given querier.
// It exercises the real Write → WriteAuditLog code path without a live pool,
// allowing tests to capture and inspect written audit entries via a mock querier.
func NewAuditWriterForTest(q DBQuerier) *AuditWriter {
	return &AuditWriter{testQuerier: q}
}

func (aw *AuditWriter) writeViaTestQuerier(ctx context.Context, e dal.AuditEntry) {
	adb := dal.NewDB(aw.testQuerier)
	_ = adb.WriteAuditLog(ctx, e)
}

// ChangesOf converts any JSON-serializable value into a map[string]any suitable
// for AuditEntry.Changes. Nil-safe: returns nil on marshal/unmarshal failure.
// Exported for use in tests; call the unexported alias changesOf in handlers.
func ChangesOf(v any) map[string]any { return changesOf(v) }

func changesOf(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// Write persists one audit entry with a 3-second timeout.
// On failure it logs a warning and increments them_audit_write_errors_total.
// It never changes the HTTP response — audit failures must not affect the primary operation.
func (aw *AuditWriter) Write(ctx context.Context, e dal.AuditEntry) {
	if aw == nil {
		return
	}
	if aw.testQuerier != nil {
		aw.writeViaTestQuerier(ctx, e)
		return
	}
	if aw.pools == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	adb := dal.NewDBFromAdminQuerier(aw.pools.NewAdminQuerier())
	if err := adb.WriteAuditLog(writeCtx, e); err != nil {
		slog.WarnContext(ctx, "audit write failed",
			"action", e.Action,
			"entity_type", e.EntityType,
			"entity_id", e.EntityID,
			"error", err,
		)
		metrics.AuditWriteErrors.Inc()
	}
}

// actorFromRequest extracts a human-readable actor string from JWT claims.
// Returns email if present, "user:{id}" if only user ID is known, or "token" for bearer calls.
func actorFromRequest(r *http.Request) string {
	if claims, ok := auth.ClaimsFromCtx(r.Context()); ok {
		if claims.Email != "" {
			return claims.Email
		}
		return fmt.Sprintf("user:%d", claims.UserID)
	}
	return "token"
}

// userIDPtr extracts the UserID from JWT claims as a *int64.
// Returns nil for bearer-token-authenticated requests (no user claims).
func userIDPtr(r *http.Request) *int64 {
	if claims, ok := auth.ClaimsFromCtx(r.Context()); ok && claims.UserID != 0 {
		id := claims.UserID
		return &id
	}
	return nil
}
