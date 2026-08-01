package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/tenantctx"
)

// ApplicationsHandler handles /api/v1/admin/applications routes.
type ApplicationsHandler struct {
	svc *service.AppService
}

// NewApplicationsHandler creates an ApplicationsHandler.
func NewApplicationsHandler(db DBQuerier, cache CacheInvalidator) *ApplicationsHandler {
	return &ApplicationsHandler{svc: service.NewAppService(dal.NewDB(db), cache)}
}

// Routes mounts application and entry point CRUD endpoints.
func (h *ApplicationsHandler) Routes(r chi.Router) {
	r.Get("/applications", h.List)
	r.Post("/applications", h.Create)
	r.Get("/applications/{id}", h.Get)
	r.Put("/applications/{id}", h.Update)
	r.Patch("/applications/{id}", h.Update) // Python frontend sends PATCH; accept both
	r.Delete("/applications/{id}", h.Delete)

	r.Post("/applications/{id}/entry-points", h.CreateEntryPoint)
	r.Put("/applications/{id}/entry-points/{ep_id}", h.UpdateEntryPoint)
	r.Patch("/applications/{id}/entry-points/{ep_id}", h.UpdateEntryPoint) // Python sends PATCH
	r.Delete("/applications/{id}/entry-points/{ep_id}", h.DeleteEntryPoint)
}

// List handles GET /api/v1/admin/applications.
func (h *ApplicationsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	apps, err := h.svc.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, apps)
}

// Create handles POST /api/v1/admin/applications.
func (h *ApplicationsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input ApplicationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	id, err := h.svc.Create(r.Context(), tenantID, input.Name, input.Enabled)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "create application: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/applications/%s", id))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// Get handles GET /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	a, err := h.svc.Get(r.Context(), tenantID, id)
	if err != nil {
		writeError(w, http.StatusNotFound, "application not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// Update handles PUT/PATCH /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var input ApplicationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.Update(r.Context(), tenantID, id, input.Name, input.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "update application: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// Delete handles DELETE /api/v1/admin/applications/{id}.
func (h *ApplicationsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.Delete(r.Context(), tenantID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete application: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// CreateEntryPoint handles POST /api/v1/admin/applications/{id}/entry-points.
func (h *ApplicationsHandler) CreateEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if appID == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}

	var input EntryPointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	epID, err := h.svc.CreateEntryPoint(r.Context(), appID, input.Slug, input.EntryPointType, input.Enabled)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "create entry point: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/applications/%s/entry-points/%s", appID, epID))
	writeJSON(w, http.StatusCreated, map[string]any{"id": epID})
}

// UpdateEntryPoint handles PUT/PATCH /api/v1/admin/applications/{id}/entry-points/{ep_id}.
func (h *ApplicationsHandler) UpdateEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if appID == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or entry point id")
		return
	}

	var input EntryPointInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := h.svc.UpdateEntryPoint(r.Context(), epID, appID, input.Slug, input.EntryPointType, input.Enabled); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "update entry point: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "updated": true})
}

// DeleteEntryPoint handles DELETE /api/v1/admin/applications/{id}/entry-points/{ep_id}.
func (h *ApplicationsHandler) DeleteEntryPoint(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	epID := chi.URLParam(r, "ep_id")
	if appID == "" || epID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or entry point id")
		return
	}

	if err := h.svc.DeleteEntryPoint(r.Context(), epID, appID); err != nil {
		writeError(w, http.StatusInternalServerError, "delete entry point: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": epID, "deleted": true})
}
