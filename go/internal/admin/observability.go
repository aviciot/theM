package admin

import (
	"encoding/json"
	"net/http"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/db"
)

// ObservabilityHandler serves GET /admin/observability/summary.
//
// The handler uses rlsPools.Admin (BYPASSRLS) to execute a cross-tenant
// aggregate query — this is intentional for the super-admin observability view.
// Protected by RequireSuperAdmin; no AdminTenantMiddleware is applied.
type ObservabilityHandler struct {
	// adminDB is a DBQuerier backed by the Admin (BYPASSRLS) pool.
	// In production this is NewPgxQuerier(pools.Admin); in tests a fakeDB.
	adminDB DBQuerier
}

// NewObservabilityHandler creates an ObservabilityHandler backed by the Admin pool.
// pools must be non-nil.
func NewObservabilityHandler(pools *db.Pools) *ObservabilityHandler {
	return &ObservabilityHandler{adminDB: NewPgxQuerier(pools.Admin)}
}

// NewObservabilityHandlerForTest creates an ObservabilityHandler with an
// injected DBQuerier. Used only in unit tests — production code calls
// NewObservabilityHandler(pools) which uses the Admin (BYPASSRLS) pool.
func NewObservabilityHandlerForTest(q DBQuerier) *ObservabilityHandler {
	return &ObservabilityHandler{adminDB: q}
}

// Routes mounts the observability endpoints on r.
func (h *ObservabilityHandler) Routes(r interface {
	Get(string, http.HandlerFunc)
}) {
	r.Get("/observability/summary", h.Summary)
}

// Summary handles GET /api/v1/admin/observability/summary.
// Returns per-tenant aggregate: run count (30d), total LLM tokens (30d),
// quota limits (max_agents, max_apps), and current resource counts.
func (h *ObservabilityHandler) Summary(w http.ResponseWriter, r *http.Request) {
	rows, err := dal.ListObservabilitySummary(r.Context(), h.adminDB)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rows)
}
