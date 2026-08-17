package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/tenantctx"
)

// AppDefinition re-exported for handler use.
type AppDefinition = dal.AppDefinition

// DefinitionsHandler handles /api/v1/admin/applications/{id}/definitions routes.
type DefinitionsHandler struct {
	svc *service.DefinitionService
}

// NewDefinitionsHandler creates a DefinitionsHandler backed by the given DB.
func NewDefinitionsHandler(db DBQuerier) *DefinitionsHandler {
	return &DefinitionsHandler{svc: service.NewDefinitionService(dal.NewDB(db))}
}

// definitionInput is the request body for POST and PUT definition endpoints.
type definitionInput struct {
	Definition json.RawMessage `json:"definition"`
}

// Routes mounts definition CRUD endpoints onto the provided router.
// The caller is responsible for mounting this under a tenant-scoped group so
// that AdminTenantMiddleware has already run.
func (h *DefinitionsHandler) Routes(r chi.Router) {
	r.Post("/applications/{id}/definitions", h.Create)
	r.Get("/applications/{id}/definitions", h.List)
	r.Get("/applications/{id}/definitions/{def_id}", h.Get)
	r.Put("/applications/{id}/definitions/{def_id}", h.Update)
	r.Delete("/applications/{id}/definitions/{def_id}", h.Delete)
}

// Create handles POST /api/v1/admin/applications/{id}/definitions.
func (h *DefinitionsHandler) Create(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var input definitionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(input.Definition) == 0 {
		writeError(w, http.StatusBadRequest, "definition is required")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	id, rev, err := h.svc.CreateDraft(r.Context(), tenantID, appID, input.Definition)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "create definition")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "revision": rev})
}

// List handles GET /api/v1/admin/applications/{id}/definitions.
func (h *DefinitionsHandler) List(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	defs, err := h.svc.ListDefinitions(r.Context(), tenantID, appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list definitions")
		return
	}
	writeJSON(w, http.StatusOK, defs)
}

// Get handles GET /api/v1/admin/applications/{id}/definitions/{def_id}.
func (h *DefinitionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	defID := chi.URLParam(r, "def_id")
	if appID == "" || defID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or definition id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	def, err := h.svc.GetDefinition(r.Context(), tenantID, appID, defID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "get definition")
		return
	}
	writeJSON(w, http.StatusOK, def)
}

// Update handles PUT /api/v1/admin/applications/{id}/definitions/{def_id}.
func (h *DefinitionsHandler) Update(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	defID := chi.URLParam(r, "def_id")
	if appID == "" || defID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or definition id")
		return
	}

	var input definitionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(input.Definition) == 0 {
		writeError(w, http.StatusBadRequest, "definition is required")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.UpdateDraft(r.Context(), tenantID, appID, defID, input.Definition); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "update definition")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": defID, "updated": true})
}

// Delete handles DELETE /api/v1/admin/applications/{id}/definitions/{def_id}.
func (h *DefinitionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	defID := chi.URLParam(r, "def_id")
	if appID == "" || defID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or definition id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.DeleteDraft(r.Context(), tenantID, appID, defID); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "delete definition")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
