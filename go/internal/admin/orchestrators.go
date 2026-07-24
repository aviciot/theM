package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// OrchestratorsHandler handles /api/v1/admin/orchestrators routes.
type OrchestratorsHandler struct {
	svc *service.OrchService
}

// NewOrchestratorsHandler creates an OrchestratorsHandler.
func NewOrchestratorsHandler(db DBQuerier, cache CacheInvalidator) *OrchestratorsHandler {
	return &OrchestratorsHandler{svc: service.NewOrchService(dal.NewDB(db), cache)}
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
	orchs, err := h.svc.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, orchs)
}

// Create handles POST /api/v1/admin/orchestrators.
func (h *OrchestratorsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input OrchestratorInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	id, err := h.svc.Create(r.Context(), input)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "create orchestrator: "+err.Error())
		return
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/orchestrators/%s", input.Name))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": input.Name})
}

// Get handles GET /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Get(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	o, err := h.svc.Get(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusNotFound, "orchestrator not found")
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// Update handles PUT/PATCH /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var input OrchestratorInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if err := h.svc.Update(r.Context(), name, input); err != nil {
		writeError(w, http.StatusInternalServerError, "update orchestrator: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"name": name, "updated": true})
}

// Delete handles DELETE /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	if err := h.svc.Delete(r.Context(), name); err != nil {
		writeError(w, http.StatusInternalServerError, "delete orchestrator: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true})
}
