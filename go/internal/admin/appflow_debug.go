package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/tenantctx"
)

// AppFlowDebugHandler handles starting a debug run of an application's draft
// canvas (docs/APP_CANVAS_DEBUG_PLAN.md Phase 5) — a new, dedicated route so
// production run-start traffic (ws/sse) is never touched by debug concerns.
type AppFlowDebugHandler struct {
	db       DBQuerier
	svc      *service.AppFlowDebugService
	temporal TemporalSignaler
}

// NewAppFlowDebugHandler creates an AppFlowDebugHandler. credStore persists
// per-node LLM credential overrides for debug runs
// (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md); fernetKey decrypts General-mode
// tenant provider keys — pass the same key used elsewhere in this package
// (e.g. NewAgentsHandler). temporal sends the debug Step signal
// (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6) — same TemporalSignaler
// HILApprovalsHandler already uses; nil disables the Step route only (Start
// still works, since Run-All debug sessions never need a signal). reg is the
// same RegistryResolver NewDefinitionsHandlerWithRegistry uses — passing nil
// means an unpublished draft with agent nodes still fails with
// unresolved_agent (pre-existing behavior); passing a real resolver lets
// Start resolve agent nodes live, without requiring a publish first.
func NewAppFlowDebugHandler(db DBQuerier, lc service.AppFlowDebugStarter, credStore service.AppFlowDebugCredentialStore, fernetKey []byte, temporal TemporalSignaler, reg service.RegistryResolver) *AppFlowDebugHandler {
	return &AppFlowDebugHandler{db: db, svc: service.NewAppFlowDebugService(dal.NewDB(db), lc, credStore, fernetKey, reg), temporal: temporal}
}

// AppRoutes mounts the debug-start and debug-step routes. Must be registered
// under a RequireTenantAdmin group with {id} = application UUID.
func (h *AppFlowDebugHandler) AppRoutes(r chi.Router) {
	r.Post("/debug/start", h.Start)
	r.Post("/debug/{run_id}/step", h.Step)
}

type debugStartBody struct {
	EntryPointSlug string `json:"entry_point_slug"`
	UserMessage    string `json:"user_message"`
	// LLMOverrides maps canvas node_id -> the per-node LLM credential choice
	// for THIS debug run only — docs/APPFLOW_RUNTIME_PARAMS_PLAN.md. Every
	// llm-kind node in the compiled draft must have an entry here; the
	// service validates this server-side before admitting the run.
	LLMOverrides map[string]llmOverrideBody `json:"llm_overrides,omitempty"`
	// StepMode starts the run paused before every node's tick, releasing one
	// tick per POST .../debug/{run_id}/step call instead of running straight
	// through (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6). Defaults to false —
	// today's Run-All behavior, unchanged.
	StepMode bool `json:"step_mode,omitempty"`
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
	// ExpiresAt is when this debug run will be forcibly terminated
	// (WorkflowRunTimeout enforcement) — the debug panel should display this
	// so the user knows the bounded lifetime, not just discover it via a
	// timeout error later.
	ExpiresAt string `json:"expires_at"`
	// WorkflowID lets the frontend deep-link to this run in the Temporal Web
	// UI (docs/APP_CANVAS_DEBUG_PLAN.md) without duplicating the ID format
	// client-side.
	WorkflowID string `json:"workflow_id"`
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

	result, err := h.svc.Start(r.Context(), tenantID, appID, body.EntryPointSlug, body.UserMessage, userID, overrides, body.StepMode)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "debug start failed")
		return
	}
	writeJSON(w, http.StatusOK, debugStartResponse{RunID: result.RunID, ExpiresAt: result.ExpiresAt.UTC().Format(time.RFC3339), WorkflowID: result.WorkflowID})
}

// Step handles POST /admin/applications/{id}/debug/{run_id}/step — sends one
// AppFlowSignalStep signal to a running debug workflow
// (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6). One call = one tick: every node
// currently paused in this run (including every node in every currently
// active fork branch) advances by exactly one node, then pauses again — see
// appflow.stepTick's doc comment for why one signal is sufficient to release
// an arbitrary number of paused branches at once, rather than needing one
// signal per branch.
//
// {id} (application UUID) is not otherwise used beyond routing — the
// signal target is derived entirely from {run_id} + the caller's own tenant,
// via GetRun's tenant-scoped lookup, so this can never be used to signal a
// run belonging to another tenant or another application (same cross-tenant
// IDOR class fixed elsewhere in this plan — never trust a URL param alone).
func (h *AppFlowDebugHandler) Step(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if _, err := uuid.Parse(runID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid run id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	d := dal.NewDB(h.db)
	if _, err := d.GetRun(r.Context(), tenantID, runID); err != nil {
		if dal.IsNoRows(err) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	if h.temporal == nil {
		writeError(w, http.StatusServiceUnavailable, "temporal signaling not configured")
		return
	}

	workflowID := appflow.WorkflowIDForRun(tenantID, runID)
	if err := h.temporal.SignalNamedWorkflow(r.Context(), workflowID, appflow.AppFlowSignalStep, nil); err != nil {
		// Best-effort, same as HIL: the run may have already completed, hit
		// its debug lifetime ceiling, or never been started in step mode —
		// none of those are a caller error worth surfacing as 4xx.
		writeError(w, http.StatusConflict, "step signal failed: run may have completed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "status": "stepped"})
}
