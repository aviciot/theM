package admin

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/tenantctx"
)

// AppFlowDebugHandler handles starting a debug run of an application's draft
// canvas (docs/APP_CANVAS_DEBUG_PLAN.md Phase 5) — a new, dedicated route so
// production run-start traffic (ws/sse) is never touched by debug concerns.
type AppFlowDebugHandler struct {
	svc *service.AppFlowDebugService
}

// NewAppFlowDebugHandler creates an AppFlowDebugHandler. credStore persists
// per-node LLM credential overrides for debug runs
// (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md); fernetKey decrypts General-mode
// tenant provider keys — pass the same key used elsewhere in this package
// (e.g. NewSystemAgentsHandler).
func NewAppFlowDebugHandler(db DBQuerier, lc service.AppFlowDebugStarter, credStore service.AppFlowDebugCredentialStore, fernetKey []byte) *AppFlowDebugHandler {
	return &AppFlowDebugHandler{svc: service.NewAppFlowDebugService(dal.NewDB(db), lc, credStore, fernetKey)}
}

// AppRoutes mounts the debug-start route. Must be registered under a
// RequireTenantAdmin group with {id} = application UUID.
func (h *AppFlowDebugHandler) AppRoutes(r chi.Router) {
	r.Post("/debug/start", h.Start)
}

type debugStartBody struct {
	EntryPointSlug string `json:"entry_point_slug"`
	UserMessage    string `json:"user_message"`
	// LLMOverrides maps canvas node_id -> the per-node LLM credential choice
	// for THIS debug run only — docs/APPFLOW_RUNTIME_PARAMS_PLAN.md. Every
	// llm-kind node in the compiled draft must have an entry here; the
	// service validates this server-side before admitting the run.
	LLMOverrides map[string]llmOverrideBody `json:"llm_overrides,omitempty"`
}

type llmOverrideBody struct {
	Mode     string `json:"mode"` // "general" | "custom"
	Provider string `json:"provider,omitempty"`
	KeyID    *int64 `json:"key_id,omitempty"`
	Model    string `json:"model,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
}

type debugStartResponse struct {
	RunID string `json:"run_id"`
}

// Start handles POST /admin/applications/{id}/debug/start. Compiles and runs
// the application's latest saved draft definition — never the published
// active_definition_id — on the isolated debug Temporal task queue.
func (h *AppFlowDebugHandler) Start(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "id")
	if _, err := uuid.Parse(appID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid application id")
		return
	}
	var body debugStartBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if body.EntryPointSlug == "" {
		writeError(w, http.StatusBadRequest, "entry_point_slug is required")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	var userID int64
	if claims, ok := auth.ClaimsFromCtx(r.Context()); ok {
		userID = claims.UserID
	}

	overrides := make(map[string]service.LLMOverrideInput, len(body.LLMOverrides))
	for nodeID, in := range body.LLMOverrides {
		overrides[nodeID] = service.LLMOverrideInput{
			Mode: in.Mode, Provider: in.Provider, KeyID: in.KeyID,
			Model: in.Model, APIKey: in.APIKey, BaseURL: in.BaseURL,
		}
	}

	result, err := h.svc.Start(r.Context(), tenantID, appID, body.EntryPointSlug, body.UserMessage, userID, overrides)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "debug start failed")
		return
	}
	writeJSON(w, http.StatusOK, debugStartResponse{RunID: result.RunID})
}
