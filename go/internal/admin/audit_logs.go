package admin

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/db"
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
