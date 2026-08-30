package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/agentgen"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/tenantctx"
)

// CanvasTaskSignaler delivers a HITL signal to a paused CanvasAgentWorkflow.
// Implemented by temporal.TemporalExecutor; nil when Temporal is disabled.
type CanvasTaskSignaler interface {
	HITLStore() *agentgen.HITLStore
	SignalCanvasStep(ctx context.Context, workflowID, runID, signalName string, payload agentgen.PipelineVars) error
}

// CanvasTasksHandler exposes POST /admin/canvas-tasks/{task_id}/signal behind RequireSuperAdmin + JWT.
// It reads the HITL handle from Redis, validates tenant ownership, and signals the Temporal workflow.
type CanvasTasksHandler struct {
	hitlStore *agentgen.HITLStore
	signaler  temporal.CanvasSignaler
}

// NewCanvasTasksHandler creates a CanvasTasksHandler.
// Returns nil when hitlStore or signaler is nil (Temporal not enabled).
func NewCanvasTasksHandler(hitlStore *agentgen.HITLStore, signaler temporal.CanvasSignaler) *CanvasTasksHandler {
	if hitlStore == nil || signaler == nil {
		return nil
	}
	return &CanvasTasksHandler{hitlStore: hitlStore, signaler: signaler}
}

// Routes mounts the canvas-tasks signal endpoint.
func (h *CanvasTasksHandler) Routes(r chi.Router) {
	r.Post("/canvas-tasks/{task_id}/signal", h.signal)
}

// signal handles POST /admin/canvas-tasks/{task_id}/signal.
// Body: {"reply_var": "value", ...} — merged into workflow vars at the waiting step.
// Returns 200 OK on success, 404 when the handle has expired, 409 when wrong token.
func (h *CanvasTasksHandler) signal(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "task_id")
	if taskID == "" {
		writeError(w, http.StatusBadRequest, "missing task_id")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())

	handle, err := h.hitlStore.Get(r.Context(), taskID)
	if err != nil {
		if errors.Is(err, agentgen.ErrHITLNotFound) {
			writeError(w, http.StatusNotFound, "task not found or expired")
			return
		}
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Tenant ownership check — reject cross-tenant signal attempts.
	if handle.TenantID != tenantID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	if handle.State != agentgen.HITLStateWaiting {
		writeError(w, http.StatusConflict, "task is not waiting for human input")
		return
	}

	// Optional: validate wait_token header for idempotent re-submissions.
	var body struct {
		WaitToken string                 `json:"wait_token"` // optional; if provided must match
		Payload   agentgen.PipelineVars  `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.WaitToken != "" && body.WaitToken != handle.WaitToken {
		writeError(w, http.StatusConflict, "stale wait_token — task may have already been signalled")
		return
	}

	// Use TrySignal for atomic CAS — prevents duplicate signal delivery.
	token := handle.WaitToken
	if token == "" {
		token = body.WaitToken // fallback: submitted state without wait_token yet
	}
	if token != "" {
		if _, sigErr := h.hitlStore.TrySignal(r.Context(), taskID, token); sigErr != nil {
			if errors.Is(sigErr, agentgen.ErrHITLWrongToken) || errors.Is(sigErr, agentgen.ErrHITLNotWaiting) {
				writeError(w, http.StatusConflict, "signal rejected: "+sigErr.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
	}

	signalName := temporal.SignalHumanInputPrefix + handle.StepID
	payload := body.Payload
	if payload == nil {
		payload = agentgen.PipelineVars{}
	}
	if err := h.signaler.SignalCanvasStep(r.Context(), handle.WorkflowID, handle.RunID, signalName, payload); err != nil {
		writeError(w, http.StatusInternalServerError, "signal delivery failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"signaled": true, "step_id": handle.StepID})
}
