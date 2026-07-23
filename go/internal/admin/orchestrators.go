package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ── Orchestrator types ────────────────────────────────────────────────────────

// Orchestrator is the JSON representation of a them.orchestrators row.
type Orchestrator struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	LLMProvider   string   `json:"llm_provider"`
	Model         string   `json:"model"`
	MaxIterations int      `json:"max_iterations"`
	MaxTokens     int      `json:"max_tokens"`
	Temperature   float64  `json:"temperature"`
	SystemPrompt  string   `json:"system_prompt,omitempty"`
	HistoryWindow int      `json:"history_window"`
	AllowedAgents []string `json:"allowed_agents"`
	Enabled       bool     `json:"enabled"`
}

// OrchestratorInput is the request body for create/update.
type OrchestratorInput struct {
	Name          string   `json:"name"`
	LLMProvider   string   `json:"llm_provider"`
	Model         string   `json:"model"`
	MaxIterations int      `json:"max_iterations"`
	MaxTokens     int      `json:"max_tokens"`
	Temperature   float64  `json:"temperature"`
	SystemPrompt  string   `json:"system_prompt,omitempty"`
	HistoryWindow int      `json:"history_window"`
	AllowedAgents []string `json:"allowed_agents"`
	Enabled       *bool    `json:"enabled,omitempty"`
}

// ── Orchestrators handler ─────────────────────────────────────────────────────

// OrchestratorsHandler handles /api/v1/admin/orchestrators routes.
type OrchestratorsHandler struct {
	db    DBQuerier
	cache CacheInvalidator
}

// NewOrchestratorsHandler creates an OrchestratorsHandler.
func NewOrchestratorsHandler(db DBQuerier, cache CacheInvalidator) *OrchestratorsHandler {
	return &OrchestratorsHandler{db: db, cache: cache}
}

// Routes mounts the orchestrator CRUD endpoints.
func (h *OrchestratorsHandler) Routes(r chi.Router) {
	r.Get("/orchestrators", h.List)
	r.Post("/orchestrators", h.Create)
	r.Get("/orchestrators/{name}", h.Get)
	r.Put("/orchestrators/{name}", h.Update)
	r.Patch("/orchestrators/{name}", h.Update) // Python frontend sends PATCH; accept both
	r.Delete("/orchestrators/{name}", h.Delete)
}

// List handles GET /api/v1/admin/orchestrators.
func (h *OrchestratorsHandler) List(w http.ResponseWriter, r *http.Request) {
	const q = `
		SELECT id, name, llm_provider, model, max_iterations, max_tokens,
		       temperature, COALESCE(system_prompt, ''), history_window, enabled
		FROM them.orchestrators ORDER BY id`

	rows, err := h.db.Query(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	defer rows.Close()

	orchs := make([]Orchestrator, 0)
	for rows.Next() {
		var o Orchestrator
		if err := rows.Scan(
			&o.ID, &o.Name, &o.LLMProvider, &o.Model, &o.MaxIterations,
			&o.MaxTokens, &o.Temperature, &o.SystemPrompt, &o.HistoryWindow,
			&o.Enabled,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error: "+err.Error())
			return
		}
		o.AllowedAgents = make([]string, 0)
		orchs = append(orchs, o)
	}

	writeJSON(w, http.StatusOK, orchs)
}

// Create handles POST /api/v1/admin/orchestrators.
func (h *OrchestratorsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input OrchestratorInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.MaxIterations <= 0 {
		input.MaxIterations = 10
	}
	if input.MaxTokens <= 0 {
		input.MaxTokens = 4096
	}

	const q = `
		INSERT INTO them.orchestrators
		  (name, llm_provider, model, max_iterations, max_tokens, temperature, system_prompt, history_window, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`

	row := h.db.ExecReturning(r.Context(), q,
		input.Name, input.LLMProvider, input.Model, input.MaxIterations,
		input.MaxTokens, input.Temperature, input.SystemPrompt, input.HistoryWindow,
		enabled,
	)

	var id int64
	if err := row.Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "create orchestrator: "+err.Error())
		return
	}

	h.invalidateCache(r, input.Name)

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/orchestrators/%s", input.Name))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": input.Name})
}

// Get handles GET /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Get(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	const q = `
		SELECT id, name, llm_provider, model, max_iterations, max_tokens,
		       temperature, COALESCE(system_prompt, ''), history_window, enabled
		FROM them.orchestrators WHERE name = $1`

	row := h.db.QueryRow(r.Context(), q, name)
	var o Orchestrator
	if err := row.Scan(
		&o.ID, &o.Name, &o.LLMProvider, &o.Model, &o.MaxIterations,
		&o.MaxTokens, &o.Temperature, &o.SystemPrompt, &o.HistoryWindow,
		&o.Enabled,
	); err != nil {
		writeError(w, http.StatusNotFound, "orchestrator not found")
		return
	}
	o.AllowedAgents = make([]string, 0)
	writeJSON(w, http.StatusOK, o)
}

// Update handles PUT /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var input OrchestratorInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	const q = `
		UPDATE them.orchestrators
		SET llm_provider=$2, model=$3, max_iterations=$4, max_tokens=$5,
		    temperature=$6, system_prompt=$7, history_window=$8, enabled=$9,
		    updated_at=now()
		WHERE name=$1`

	if err := h.db.Exec(r.Context(), q,
		name, input.LLMProvider, input.Model, input.MaxIterations,
		input.MaxTokens, input.Temperature, input.SystemPrompt, input.HistoryWindow,
		enabled,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "update orchestrator: "+err.Error())
		return
	}

	h.invalidateCache(r, name)

	writeJSON(w, http.StatusOK, map[string]any{"name": name, "updated": true})
}

// Delete handles DELETE /api/v1/admin/orchestrators/{name}.
func (h *OrchestratorsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	const q = `UPDATE them.orchestrators SET enabled=false, updated_at=now() WHERE name=$1`
	if err := h.db.Exec(r.Context(), q, name); err != nil {
		writeError(w, http.StatusInternalServerError, "delete orchestrator: "+err.Error())
		return
	}

	h.invalidateCache(r, name)

	writeJSON(w, http.StatusOK, map[string]any{"name": name, "deleted": true})
}

func (h *OrchestratorsHandler) invalidateCache(r *http.Request, name string) {
	if h.cache == nil {
		return
	}
	_ = h.cache.Del(r.Context(), fmt.Sprintf("them:orchestrators:%s", name))
}
