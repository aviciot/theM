package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/admin/service"
	"github.com/aviciot/them/internal/tenantctx"
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
// Static routes (e.g. /runs/stats, /runs/bulk-delete) must be registered before
// dynamic ones (e.g. /runs/{run_id}) so chi does not treat them as run_id values.
func (h *RunsHandler) Routes(r chi.Router) {
	r.Get("/runs", h.List)
	r.Get("/runs/stats", h.Stats)
	r.Post("/runs/bulk-delete", h.BulkDelete)
	r.Get("/runs/{run_id}", h.Get)
	r.Patch("/runs/{run_id}/cancel", h.Cancel)
	r.Delete("/runs/{run_id}", h.Delete)
	r.Get("/runs/{run_id}/tasks", h.Tasks)
	r.Get("/runs/{run_id}/artifacts", h.Artifacts)
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

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	runs, err := h.svc.List(r.Context(), tenantID, contextID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// Get handles GET /api/v1/runs/{run_id} — returns RunDetail (run + steps + usage + children).
func (h *RunsHandler) Get(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	detail, err := h.svc.GetDetail(r.Context(), tenantID, runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// Stats handles GET /api/v1/runs/stats.
func (h *RunsHandler) Stats(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	stats, err := h.svc.Stats(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// Tasks handles GET /api/v1/runs/{run_id}/tasks.
func (h *RunsHandler) Tasks(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}
	tasks, err := h.svc.GetTasks(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

// Artifacts handles GET /api/v1/runs/{run_id}/artifacts.
func (h *RunsHandler) Artifacts(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}
	artifacts, err := h.svc.GetArtifacts(r.Context(), runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	writeJSON(w, http.StatusOK, artifacts)
}

// bulkDeleteRunsInput is the request body for POST /api/v1/runs/bulk-delete.
type bulkDeleteRunsInput struct {
	RunIDs []string `json:"run_ids"`
}

// Cancel handles PATCH /api/v1/runs/{run_id}/cancel.
// Force-cancels a running run; returns 409 if the run is not in "running" state.
func (h *RunsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	run, err := h.svc.Cancel(r.Context(), tenantID, runID)
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		if errors.Is(err, service.ErrConflict) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "cancel error")
		return
	}
	writeJSON(w, http.StatusOK, run)
}

// Delete handles DELETE /api/v1/runs/{run_id}.
func (h *RunsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	runID := chi.URLParam(r, "run_id")
	if runID == "" {
		writeError(w, http.StatusBadRequest, "run_id is required")
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	if err := h.svc.Delete(r.Context(), tenantID, runID); err != nil {
		if errors.Is(err, service.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "delete error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// BulkDelete handles POST /api/v1/runs/bulk-delete.
func (h *RunsHandler) BulkDelete(w http.ResponseWriter, r *http.Request) {
	var body bulkDeleteRunsInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if len(body.RunIDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"deleted": 0})
		return
	}

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
	n, err := h.svc.BulkDelete(r.Context(), tenantID, body.RunIDs)
	if err != nil {
		if writeServiceError(w, err) {
			return
		}
		writeError(w, http.StatusInternalServerError, "bulk delete error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": n})
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

	tenantID := tenantctx.MustTenantIDFromCtx(r.Context())
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
