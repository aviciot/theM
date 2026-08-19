package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/tenantctx"
)

// AgentDefinition re-exported for handler use.
type AgentDefinition = dal.AgentDefinition

// AgentDefinitionsHandler handles /api/v1/admin/agent-definitions routes.
type AgentDefinitionsHandler struct {
	svc *service.AgentDefinitionService
}

// NewAgentDefinitionsHandler creates an AgentDefinitionsHandler backed by the given DB.
func NewAgentDefinitionsHandler(db DBQuerier) *AgentDefinitionsHandler {
	return &AgentDefinitionsHandler{svc: service.NewAgentDefinitionService(dal.NewDB(db))}
}

// agentDefinitionInput is the request body for POST and PUT agent definition endpoints.
type agentDefinitionInput struct {
	AgentSlug  string          `json:"agent_slug"`
	Definition json.RawMessage `json:"definition"`
}

// Routes mounts agent definition CRUD endpoints onto the provided router.
func (h *AgentDefinitionsHandler) Routes(r chi.Router) {
	r.Post("/agent-definitions", h.Create)
	r.Get("/agent-definitions", h.List)
	r.Get("/agent-definitions/{id}", h.Get)
	r.Put("/agent-definitions/{id}", h.Update)
	r.Delete("/agent-definitions/{id}", h.Delete)
}

// Create handles POST /api/v1/admin/agent-definitions.
func (h *AgentDefinitionsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input agentDefinitionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(input.Definition) == 0 {
		writeError(w, http.StatusBadRequest, "definition is required")
		return
	}
	if input.AgentSlug == "" {
		writeError(w, http.StatusBadRequest, "agent_slug is required")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	id, rev, err := h.svc.CreateDraft(r.Context(), tenantID, input.AgentSlug, input.Definition)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "create agent definition")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "revision": rev})
}

// List handles GET /api/v1/admin/agent-definitions.
func (h *AgentDefinitionsHandler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	defs, err := h.svc.ListDefinitions(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list agent definitions")
		return
	}
	writeJSON(w, http.StatusOK, defs)
}

// Get handles GET /api/v1/admin/agent-definitions/{id}.
func (h *AgentDefinitionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent definition id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	def, err := h.svc.GetDefinition(r.Context(), tenantID, id)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "get agent definition")
		return
	}
	writeJSON(w, http.StatusOK, def)
}

// Update handles PUT /api/v1/admin/agent-definitions/{id}.
func (h *AgentDefinitionsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent definition id")
		return
	}
	var input agentDefinitionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(input.Definition) == 0 {
		writeError(w, http.StatusBadRequest, "definition is required")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.UpdateDraft(r.Context(), tenantID, id, input.Definition); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "update agent definition")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// Delete handles DELETE /api/v1/admin/agent-definitions/{id}.
func (h *AgentDefinitionsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent definition id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.DeleteDraft(r.Context(), tenantID, id); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "delete agent definition")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
