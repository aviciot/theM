package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
)

// OrchestratorsHandler handles /api/v1/admin/orchestrators routes.
type OrchestratorsHandler struct {
	db    DBQuerier
	cache CacheInvalidator
	dal   *dal.DB
}

// NewOrchestratorsHandler creates an OrchestratorsHandler.
func NewOrchestratorsHandler(db DBQuerier, cache CacheInvalidator) *OrchestratorsHandler {
	return &OrchestratorsHandler{db: db, cache: cache, dal: dal.NewDB(db)}
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
	orchs, err := h.dal.ListOrchestrators(r.Context())
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
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.MaxIterations <= 0 {
		input.MaxIterations = 10
	}
	if input.HistoryWindow <= 0 {
		input.HistoryWindow = 20
	}

	id, err := h.dal.CreateOrchestrator(r.Context(), input, enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create orchestrator: "+err.Error())
		return
	}

	h.invalidateCache(r, input.Name)

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/orchestrators/%s", input.Name))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": input.Name})
}

// Get handles GET /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Get(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	o, err := h.dal.GetOrchestrator(r.Context(), name)
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
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	if err := h.dal.UpdateOrchestrator(r.Context(), name, input, enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "update orchestrator: "+err.Error())
		return
	}

	h.invalidateCache(r, name)

	writeJSON(w, http.StatusOK, map[string]any{"name": name, "updated": true})
}

// Delete handles DELETE /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	if err := h.dal.DeleteOrchestrator(r.Context(), name); err != nil {
		writeError(w, http.StatusInternalServerError, "delete orchestrator: "+err.Error())
		return
	}

	h.invalidateCache(r, name)

	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true})
}

func (h *OrchestratorsHandler) invalidateCache(r *http.Request, name string) {
	if h.cache == nil {
		return
	}
	_ = h.cache.Del(r.Context(), fmt.Sprintf("them:orchestrators:%s", name))
}
