package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
)

// AgentsHandler handles /api/v1/admin/agents routes.
type AgentsHandler struct {
	db    DBQuerier
	cache CacheInvalidator
	dal   *dal.DB
}

// NewAgentsHandler creates an AgentsHandler.
func NewAgentsHandler(db DBQuerier, cache CacheInvalidator) *AgentsHandler {
	return &AgentsHandler{db: db, cache: cache, dal: dal.NewDB(db)}
}

// Routes mounts the agent CRUD endpoints.
func (h *AgentsHandler) Routes(r chi.Router) {
	r.Get("/agents", h.List)
	r.Post("/agents", h.Create)
	r.Get("/agents/{id}", h.Get)
	r.Put("/agents/{id}", h.Update)
	r.Patch("/agents/{id}", h.Update) // Python frontend sends PATCH; accept both
	r.Delete("/agents/{id}", h.Delete)
}

// List handles GET /api/v1/admin/agents.
func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	agents, err := h.dal.ListAgents(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, agents)
}

// Create handles POST /api/v1/admin/agents.
func (h *AgentsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input AgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.Slug == "" || input.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "slug and display_name are required")
		return
	}
	if input.Transport == "" {
		input.Transport = "a2a_async"
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.MaxConcurrency <= 0 {
		input.MaxConcurrency = 5
	}
	if input.MaxRetries <= 0 {
		input.MaxRetries = 2
	}
	if input.TimeoutSeconds <= 0 {
		input.TimeoutSeconds = 30
	}

	id, err := h.dal.CreateAgent(r.Context(), input, enabled)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create agent: "+err.Error())
		return
	}

	h.invalidateCache(r)

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/agents/%s", id))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// Get handles GET /api/v1/admin/agents/{id}.
func (h *AgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	a, err := h.dal.GetAgent(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// Update handles PUT/PATCH /api/v1/admin/agents/{id}.
func (h *AgentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	var input AgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.MaxConcurrency <= 0 {
		input.MaxConcurrency = 5
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	if err := h.dal.UpdateAgent(r.Context(), id, input, enabled); err != nil {
		writeError(w, http.StatusInternalServerError, "update agent: "+err.Error())
		return
	}

	h.invalidateCache(r)

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// Delete handles DELETE /api/v1/admin/agents/{id} (soft delete: enabled=false).
func (h *AgentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	if err := h.dal.DeleteAgent(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete agent: "+err.Error())
		return
	}

	h.invalidateCache(r)

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (h *AgentsHandler) invalidateCache(r *http.Request) {
	if h.cache != nil {
		_ = h.cache.Del(r.Context(), "them:agents:registry")
	}
}
