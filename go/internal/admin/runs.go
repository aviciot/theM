package admin

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ── Run types ─────────────────────────────────────────────────────────────────

// Run is the JSON representation of a them.runs row.
// context_id is NOT a column on them.runs (it lives on them.tasks).
// Do not add it here — the signal endpoint resolves it via tasks.
// Field names match Python's RunOut schema so the frontend works without changes.
type Run struct {
	ID               string  `json:"id"`
	OrchestratorID   string  `json:"orchestrator_id,omitempty"`
	OrchestratorName string  `json:"orchestrator_name,omitempty"`
	EntryPointSlug   string  `json:"entry_point_slug,omitempty"`
	UserID           *int64  `json:"user_id,omitempty"`
	SessionID        string  `json:"session_id,omitempty"`
	Goal             string  `json:"goal,omitempty"`
	Status           string  `json:"status"`
	FinalOutput      string  `json:"final_output,omitempty"`
	Error            string  `json:"error,omitempty"`
	ParentRunID      string  `json:"parent_run_id,omitempty"`
	Iterations       int     `json:"iterations"`
	TotalTokensIn    int     `json:"total_tokens_in"`
	TotalTokensOut   int     `json:"total_tokens_out"`
	TotalTokens      int     `json:"total_tokens"`
	TotalCostUSD     string  `json:"total_cost_usd,omitempty"`
	StartedAt        string  `json:"started_at"`
	EndedAt          string  `json:"ended_at,omitempty"`
	DurationMS       *int64  `json:"duration_ms,omitempty"`
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

const runSelectCols = `
	id::text,
	COALESCE(orchestrator_id::text, ''), COALESCE(orchestrator_name, ''),
	COALESCE(entry_point_slug, ''), user_id, COALESCE(session_id::text, ''),
	COALESCE(goal, ''), status,
	COALESCE(final_output, ''), COALESCE(error, ''), COALESCE(parent_run_id::text, ''),
	iterations, total_tokens_in, total_tokens_out,
	COALESCE(total_cost_usd::text, '0'),
	started_at::text, COALESCE(ended_at::text, '')`

func scanRun(row SingleRowScanner) (Run, error) {
	var r Run
	if err := row.Scan(
		&r.ID, &r.OrchestratorID, &r.OrchestratorName,
		&r.EntryPointSlug, &r.UserID, &r.SessionID,
		&r.Goal, &r.Status,
		&r.FinalOutput, &r.Error, &r.ParentRunID,
		&r.Iterations, &r.TotalTokensIn, &r.TotalTokensOut,
		&r.TotalCostUSD,
		&r.StartedAt, &r.EndedAt,
	); err != nil {
		return r, err
	}
	r.TotalTokens = r.TotalTokensIn + r.TotalTokensOut
	return r, nil
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
		// context_id lives on them.tasks, not them.runs. Resolve run IDs via tasks.
		q := "SELECT " + runSelectCols + `
			FROM them.runs r
			JOIN them.tasks t ON t.run_id = r.id AND t.kind = 'root'
			WHERE t.context_id = $1::uuid
			ORDER BY r.started_at DESC LIMIT $2`
		rows, err = h.db.Query(r.Context(), q, contextID, limit)
	} else {
		q := "SELECT " + runSelectCols + `
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
		run, err := scanRun(rows)
		if err != nil {
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

	q := "SELECT " + runSelectCols + " FROM them.runs WHERE id = $1::uuid"

	row := h.db.QueryRow(r.Context(), q, runID)
	run, err := scanRun(row)
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
	var contextID string
	row := h.db.QueryRow(r.Context(),
		`SELECT context_id::text FROM them.tasks WHERE run_id = $1::uuid AND kind = 'root' LIMIT 1`,
		runID)
	if err := row.Scan(&contextID); err != nil {
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
