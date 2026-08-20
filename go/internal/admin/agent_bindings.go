package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/service"
)

// appIDParam reads the application ID URL parameter, accepting both {app_id} (legacy
// standalone route) and {id} (when mounted inside ApplicationsHandler's sub-tree).
func appIDParam(r *http.Request) string {
	if v := chi.URLParam(r, "id"); v != "" {
		return v
	}
	return chi.URLParam(r, "app_id")
}

// AgentBindingsHandler handles /api/v1/admin/applications/{app_id}/agent-bindings routes.
// Credentials are AES-GCM encrypted before storage; responses return slot-set
// bool maps only — never ciphertext or plaintext.
type AgentBindingsHandler struct {
	svc *service.AgentDefinitionService
}

// NewAgentBindingsHandler creates an AgentBindingsHandler.
func NewAgentBindingsHandler(svc *service.AgentDefinitionService) *AgentBindingsHandler {
	return &AgentBindingsHandler{svc: svc}
}

// Routes mounts binding CRUD endpoints onto the given chi.Router.
// The router is assumed to be mounted at /admin/applications/{app_id}.
func (h *AgentBindingsHandler) Routes(r chi.Router) {
	h.MountOn(r)
}

// MountOn mounts binding routes onto r, implementing BindingRouter so the handler
// can be passed into ApplicationsHandler.Routes and share the /applications/{id} sub-tree.
// When mounted this way the app ID is in the {id} param; handlers fall back to {id}
// when {app_id} is absent.
func (h *AgentBindingsHandler) MountOn(r chi.Router) {
	r.Get("/agent-bindings", h.List)
	r.Get("/agent-bindings/{agent_id}", h.Get)
	r.Post("/agent-bindings/{agent_id}", h.Upsert)
	r.Put("/agent-bindings/{agent_id}", h.Upsert)
	r.Delete("/agent-bindings/{agent_id}", h.Delete)
}

// List handles GET /api/v1/admin/applications/{app_id}/agent-bindings.
func (h *AgentBindingsHandler) List(w http.ResponseWriter, r *http.Request) {
	appID := appIDParam(r)
	if appID == "" {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	bindings, err := h.svc.ListBindings(r.Context(), appID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list agent bindings")
		return
	}
	writeJSON(w, http.StatusOK, bindings)
}

// Get handles GET /api/v1/admin/applications/{app_id}/agent-bindings/{agent_id}.
func (h *AgentBindingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	appID := appIDParam(r)
	agentID := chi.URLParam(r, "agent_id")
	if appID == "" || agentID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or agent id")
		return
	}
	status, err := h.svc.GetBindingStatus(r.Context(), appID, agentID)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "get agent binding")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// Upsert handles POST and PUT /api/v1/admin/applications/{app_id}/agent-bindings/{agent_id}.
// Body: { definition_id?, credentials: {slot→plaintext}, config_overrides?, policies? }
// Credentials are encrypted before persisting — plaintext is never stored or returned.
func (h *AgentBindingsHandler) Upsert(w http.ResponseWriter, r *http.Request) {
	appID := appIDParam(r)
	agentID := chi.URLParam(r, "agent_id")
	if appID == "" || agentID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or agent id")
		return
	}

	var input service.AgentBindingUpsertInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.Credentials == nil {
		input.Credentials = map[string]string{}
	}

	if err := h.svc.UpsertBinding(r.Context(), appID, agentID, input); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "upsert agent binding")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"application_id": appID, "agent_id": agentID, "updated": true})
}

// Delete handles DELETE /api/v1/admin/applications/{app_id}/agent-bindings/{agent_id}.
func (h *AgentBindingsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	appID := appIDParam(r)
	agentID := chi.URLParam(r, "agent_id")
	if appID == "" || agentID == "" {
		writeError(w, http.StatusBadRequest, "invalid application or agent id")
		return
	}
	if err := h.svc.DeleteBinding(r.Context(), appID, agentID); err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "delete agent binding")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
