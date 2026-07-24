package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
)

// RunsHandler handles /api/v1/runs routes.
type RunsHandler struct {
	db       DBQuerier
	temporal TemporalSignaler
	dal      *dal.DB
}

// NewRunsHandler creates a RunsHandler.
func NewRunsHandler(db DBQuerier, temporal TemporalSignaler) *RunsHandler {
	return &RunsHandler{db: db, temporal: temporal, dal: dal.NewDB(db)}
}

// Routes mounts the runs API endpoints.
func (h *RunsHandler) Routes(r chi.Router) {
	r.Get("/runs", h.List)
	r.Get("/runs/{run_id}", h.Get)
	r.Post("/runs/{run_id}/signal", h.Signal)
}

// List handles GET /api/v1/runs?context_id=&limit=50.
func (h *RunsHandler) List(w http.ResponseWriter, r *http.Request) {
	contextID := r.URL.Query().Get("context_id")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
			limit = n
		}
	}

	runs, err := h.dal.ListRuns(r.Context(), contextID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// Get handles GET /api/v1/runs/{run_id}.
func (h *RunsHandler) Get(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}

	run, err := h.dal.GetRun(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// Signal handles POST /api/v1/runs/{run_id}/signal (HITL).
// Forwards the human response payload to the Temporal workflow.
func (h *RunsHandler) Signal(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}

	var input SignalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	if h.temporal == nil {
		writeError(w, http.StatusServiceUnavailable, "temporal not configured")
		return
	}

	// context_id lives on them.tasks (not them.runs).
	// Python's OrchestrationWorkflow registers as "ctx-{context_id}".
	contextID, err := h.dal.GetRunContextID(r.Context(), runID)
	if err != nil {
		if IsNotFound(err) {
			writeError(w, http.StatusNotFound, "run not found or no root task")
		} else {
			writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		}
		return
	}
	workflowID := "ctx-" + contextID

	payload, err := json.Marshal(input.Payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid payload")
		return
	}

	if err := h.temporal.SignalRun(r.Context(), workflowID, payload); err != nil {
		writeError(w, http.StatusInternalServerError, "signal error: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "signaled": true})
}
