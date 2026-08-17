package admin

import (
	"net/http"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/tenantctx"
)

// RegistryHandler handles component definition registry routes.
type RegistryHandler struct {
	db DBQuerier
}

// NewRegistryHandler creates a RegistryHandler backed by the given DB querier.
func NewRegistryHandler(db DBQuerier) *RegistryHandler {
	return &RegistryHandler{db: db}
}

// ListComponentDefinitions handles GET /admin/component-definitions.
func (h *RegistryHandler) ListComponentDefinitions(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	defs, err := dal.NewDB(h.db).ListComponentDefinitions(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list component definitions")
		return
	}
	writeJSON(w, http.StatusOK, defs)
}
