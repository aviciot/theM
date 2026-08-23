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
// cache and fernetKey may be nil (disables publish pipeline; CRUD still works).
func NewAgentDefinitionsHandler(db DBQuerier, cache CacheInvalidator, fernetKey []byte) *AgentDefinitionsHandler {
	return &AgentDefinitionsHandler{svc: service.NewAgentDefinitionService(dal.NewDB(db), cache, fernetKey)}
}

// Svc returns the underlying AgentDefinitionService for use by sibling handlers.
func (h *AgentDefinitionsHandler) Svc() *service.AgentDefinitionService { return h.svc }

// agentDefinitionInput is the request body for POST and PUT agent definition endpoints.
type agentDefinitionInput struct {
	AgentSlug  string          `json:"agent_slug"`
	Definition json.RawMessage `json:"definition"`
}

// Routes mounts agent definition CRUD + publish endpoints onto the provided router.
func (h *AgentDefinitionsHandler) Routes(r chi.Router) {
	r.Post("/agent-definitions", h.Create)
	r.Get("/agent-definitions", h.List)
	r.Get("/agent-definitions/{id}", h.Get)
	r.Put("/agent-definitions/{id}", h.Update)
	r.Delete("/agent-definitions/{id}", h.Delete)
	r.Post("/agent-definitions/{id}/validate", h.Validate)
	r.Post("/agent-definitions/{id}/publish", h.Publish)
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

// validateInput is the optional request body for the Validate endpoint.
// If Definition is non-nil it is used directly; otherwise the saved DB definition is loaded.
type validateInput struct {
	Definition json.RawMessage `json:"definition,omitempty"`
}

// Validate handles POST /api/v1/admin/agent-definitions/{id}/validate.
// Accepts an optional {"definition": <json>} body. When provided, the body
// definition is validated directly without reading from the DB — this lets the
// frontend validate the current in-memory canvas state without requiring a save.
// When the body is absent or definition is null, falls back to the saved DB definition.
// Returns 200 AgentValidationReport on success, 422 with errors on compile failure.
func (h *AgentDefinitionsHandler) Validate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent definition id")
		return
	}

	var input validateInput
	// Decode body leniently — empty body is valid (means "use DB definition").
	_ = json.NewDecoder(r.Body).Decode(&input)

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	report, err := h.svc.ValidateAgentDefinition(r.Context(), tenantID, id, input.Definition)
	if err != nil {
		var compErr *service.AgentCompileError
		if err != nil && isAgentCompileError(err, &compErr) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"valid":  false,
				"errors": compErr.Errors,
			})
			return
		}
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "validate agent definition")
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// Publish handles POST /api/v1/admin/agent-definitions/{id}/publish.
// Compiles, atomically writes runtime tables, marks definition published.
// Returns 200 with AgentPublishResult on success.
func (h *AgentDefinitionsHandler) Publish(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent definition id")
		return
	}
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	result, err := h.svc.PublishAgentDefinition(r.Context(), tenantID, id)
	if err != nil {
		var compErr *service.AgentCompileError
		if isAgentCompileError(err, &compErr) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
				"errors": compErr.Errors,
			})
			return
		}
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "publish agent definition")
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// isAgentCompileError reports whether err is an *AgentCompileError and sets target.
func isAgentCompileError(err error, target **service.AgentCompileError) bool {
	if err == nil {
		return false
	}
	if ce, ok := err.(*service.AgentCompileError); ok {
		*target = ce
		return true
	}
	return false
}
