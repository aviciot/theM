package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/db"
	"github.com/aviciot/them/internal/tenantctx"
)

// OrchestratorsHandler handles /api/v1/admin/orchestrators routes.
type OrchestratorsHandler struct {
	// legacySvc is the fallback service when pools is nil (unit tests only).
	// In production pools is always non-nil and openSvc uses TenantTx.
	legacySvc *service.OrchService
	pools     *db.Pools
	cache     CacheInvalidator
}

// NewOrchestratorsHandler creates an OrchestratorsHandler.
// legacyDB backs the unit-test fallback (pools=nil path).
func NewOrchestratorsHandler(legacyDB DBQuerier, pools *db.Pools, cache CacheInvalidator) *OrchestratorsHandler {
	return &OrchestratorsHandler{
		legacySvc: service.NewOrchService(dal.NewDB(legacyDB), cache),
		pools:     pools,
		cache:     cache,
	}
}

func (h *OrchestratorsHandler) openSvc(ctx context.Context, tenantID string) (svc *service.OrchService, commit func(context.Context) error, rollback func(), err error) {
	if h.pools == nil {
		return h.legacySvc, func(_ context.Context) error { return nil }, func() {}, nil
	}
	tenantUUID, uuidErr := uuid.Parse(tenantID)
	if uuidErr != nil {
		return nil, nil, nil, uuidErr
	}
	tx, txErr := h.pools.BeginTenantTx(ctx, tenantUUID)
	if txErr != nil {
		return nil, nil, nil, txErr
	}
	rb := func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		tx.Rollback(cleanupCtx)
	}
	return service.NewOrchService(dal.NewDBFromTenantQuerier(tx), h.cache), tx.Commit, rb, nil
}

// Routes mounts the orchestrator CRUD endpoints.
func (h *OrchestratorsHandler) Routes(r chi.Router) {
	r.Get("/orchestrators", h.List)
	r.Post("/orchestrators", h.Create)
	r.Get("/orchestrators/{name}", h.Get)
	r.Put("/orchestrators/{name}", h.Update)
	r.Patch("/orchestrators/{name}", h.Update) // Python frontend sends PATCH; accept both
	r.Delete("/orchestrators/{name}", h.Delete)
}

// List handles GET /api/v1/admin/orchestrators.
func (h *OrchestratorsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	orchs, err := svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, orchs)
}

// Create handles POST /api/v1/admin/orchestrators.
func (h *OrchestratorsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input OrchestratorInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	id, err := svc.Create(r.Context(), tenantID, input)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/orchestrators/%s", input.Name))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": input.Name})
}

// Get handles GET /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Get(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	o, err := svc.Get(r.Context(), tenantID, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "orchestrator not found")
		return
	}
	_ = commit(r.Context())
	writeJSON(w, http.StatusOK, o)
}

// Update handles PUT/PATCH /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var input OrchestratorInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.Update(r.Context(), tenantID, name, input); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "updated": true})
}

// Delete handles DELETE /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	svc, commit, rollback, err := h.openSvc(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	defer rollback()
	if err := svc.Delete(r.Context(), tenantID, name); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true})
}
