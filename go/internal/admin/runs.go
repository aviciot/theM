package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
)

// RunsHandler handles /api/v1/runs routes.
type RunsHandler struct {
	svc *service.RunService
}

// NewRunsHandler creates a RunsHandler.
func NewRunsHandler(db DBQuerier, temporal TemporalSignaler) *RunsHandler {
	return &RunsHandler{svc: service.NewRunService(dal.NewDB(db), temporal)}
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

	tenantID := tenantIDFromCtxOrBootstrap(r.Context())
	runs, err := h.svc.List(r.Context(), tenantID, contextID, limit)
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

	tenantID := tenantIDFromCtxOrBootstrap(r.Context())
	run, err := h.svc.Get(r.Context(), tenantID, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// Signal handles POST /api/v1/runs/{run_id}/signal (HITL).
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

	tenantID := tenantIDFromCtxOrBootstrap(r.Context())
	if err := h.svc.Signal(r.Context(), tenantID, runID, input.Payload); err != nil {
		if writeServiceError(w, err) {
			return
		}
		if errors.Is(err, service.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "signal error: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"run_id": runID, "signaled": true})
}
