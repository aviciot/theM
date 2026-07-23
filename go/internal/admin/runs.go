package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ── Run types ─────────────────────────────────────────────────────────────────

// Run is the JSON representation of a them.runs row.
type Run struct {
	ID             string `json:"id"`
	ContextID      string `json:"context_id"`
	ApplicationID  int64  `json:"application_id,omitempty"`
	EntryPointSlug string `json:"entry_point_slug,omitempty"`
	Status         string `json:"status"`
	StartedAt      string `json:"started_at"`
	EndedAt        string `json:"ended_at,omitempty"`
	ErrorMessage   string `json:"error_message,omitempty"`
}

// SignalInput is the request body for POST /api/v1/runs/{run_id}/signal.
type SignalInput struct {
	Payload json.RawMessage `json:"payload"`
}

// ── Runs handler ──────────────────────────────────────────────────────────────

// RunsHandler handles /api/v1/runs routes.
type RunsHandler struct {
	db       DBQuerier
	temporal TemporalSignaler
}

// NewRunsHandler creates a RunsHandler.
func NewRunsHandler(db DBQuerier, temporal TemporalSignaler) *RunsHandler {
	return &RunsHandler{db: db, temporal: temporal}
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

	var (
		rows RowScanner
		err  error
	)

	if contextID != "" {
		const q = `
			SELECT id, context_id, COALESCE(application_id, 0), COALESCE(entry_point_slug, ''),
			       status, started_at::text, COALESCE(ended_at::text, ''), COALESCE(error_message, '')
			FROM them.runs
			WHERE context_id = $1
			ORDER BY started_at DESC LIMIT $2`
		rows, err = h.db.Query(r.Context(), q, contextID, limit)
	} else {
		const q = `
			SELECT id, context_id, COALESCE(application_id, 0), COALESCE(entry_point_slug, ''),
			       status, started_at::text, COALESCE(ended_at::text, ''), COALESCE(error_message, '')
			FROM them.runs
			ORDER BY started_at DESC LIMIT $1`
		rows, err = h.db.Query(r.Context(), q, limit)
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	defer rows.Close()

	runs := make([]Run, 0)
	for rows.Next() {
		var run Run
		if err := rows.Scan(
			&run.ID, &run.ContextID, &run.ApplicationID, &run.EntryPointSlug,
			&run.Status, &run.StartedAt, &run.EndedAt, &run.ErrorMessage,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error: "+err.Error())
			return
		}
		runs = append(runs, run)
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

	const q = `
		SELECT id, context_id, COALESCE(application_id, 0), COALESCE(entry_point_slug, ''),
		       status, started_at::text, COALESCE(ended_at::text, ''), COALESCE(error_message, '')
		FROM them.runs WHERE id = $1`

	row := h.db.QueryRow(r.Context(), q, runID)
	var run Run
	if err := row.Scan(
		&run.ID, &run.ContextID, &run.ApplicationID, &run.EntryPointSlug,
		&run.Status, &run.StartedAt, &run.EndedAt, &run.ErrorMessage,
	); err != nil {
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

	// Look up context_id so we can target the correct Temporal workflow.
	// Python registers OrchestrationWorkflow with ID "ctx-{context_id}".
	var contextID string
	row := h.db.QueryRow(r.Context(), `SELECT context_id FROM them.runs WHERE id = $1`, runID)
	if err := row.Scan(&contextID); err != nil {
		if IsNotFound(err) {
			writeError(w, http.StatusNotFound, "run not found")
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
