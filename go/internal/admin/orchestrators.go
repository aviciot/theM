package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ── Orchestrator types ────────────────────────────────────────────────────────

// Orchestrator is the JSON representation of a them.orchestrators row.
// Field names match Python's OrchestratorOut schema exactly.
type Orchestrator struct {
	ID                      string   `json:"id"`
	Name                    string   `json:"name"`
	DisplayName             string   `json:"display_name"`
	SystemPrompt            string   `json:"system_prompt"`
	AllowedAgentIDs         []string `json:"allowed_agent_ids"`
	LLMProvider             string   `json:"llm_provider"`
	LLMModel                string   `json:"llm_model"`
	LLMAPIKeyHint           *string  `json:"llm_api_key_hint"`
	LLMBaseURL              *string  `json:"llm_base_url"`
	MaxIterations           int      `json:"max_iterations"`
	MaxParallelTools        int      `json:"max_parallel_tools"`
	RateLimitRPM            *int     `json:"rate_limit_rpm"`
	DailyBudgetUSD          *string  `json:"daily_budget_usd"`
	Enabled                 bool     `json:"enabled"`
	VoiceEnabled            bool     `json:"voice_enabled"`
	TranscriptionProvider   *string  `json:"transcription_provider"`
	TranscriptionModel      *string  `json:"transcription_model"`
	TranscriptionAPIKeyHint *string  `json:"transcription_api_key_hint"`
	TTSEnabled              bool     `json:"tts_enabled"`
	TTSProvider             *string  `json:"tts_provider"`
	TTSVoice                *string  `json:"tts_voice"`
	TTSAPIKeyHint           *string  `json:"tts_api_key_hint"`
	MemoryEnabled           bool     `json:"memory_enabled"`
	SummarizeEveryNCalls    int      `json:"summarize_every_n_calls"`
	MemoryRawFallbackN      int      `json:"memory_raw_fallback_n"`
	SummarizerProvider      *string  `json:"summarizer_provider"`
	SummarizerModel         *string  `json:"summarizer_model"`
	SummarizerAPIKeyHint    *string  `json:"summarizer_api_key_hint"`
	HistoryWindow           int      `json:"history_window"`
	BudgetTokens            *int     `json:"budget_tokens"`
}

// OrchestratorInput is the request body for create/update.
type OrchestratorInput struct {
	Name          string   `json:"name"`
	DisplayName   string   `json:"display_name"`
	SystemPrompt  string   `json:"system_prompt,omitempty"`
	AllowedAgents []string `json:"allowed_agent_ids,omitempty"`
	LLMProvider   string   `json:"llm_provider"`
	LLMModel      string   `json:"llm_model"`
	MaxIterations int      `json:"max_iterations"`
	HistoryWindow int      `json:"history_window"`
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

const orchSelectCols = `
	id::text, name, display_name, COALESCE(system_prompt, ''),
	COALESCE(allowed_agent_ids::text[], '{}'),
	COALESCE(llm_provider, ''), COALESCE(llm_model, ''),
	llm_base_url, max_iterations, max_parallel_tools,
	rate_limit_rpm, daily_budget_usd::text,
	enabled, voice_enabled,
	transcription_provider, transcription_model,
	tts_enabled, tts_provider, tts_voice,
	memory_enabled, summarize_every_n_calls, memory_raw_fallback_n,
	summarizer_provider, summarizer_model,
	history_window, budget_tokens`

func scanOrch(row SingleRowScanner) (Orchestrator, error) {
	var o Orchestrator
	var agentIDs []string
	var dailyBudget *string
	if err := row.Scan(
		&o.ID, &o.Name, &o.DisplayName, &o.SystemPrompt,
		&agentIDs, &o.LLMProvider, &o.LLMModel,
		&o.LLMBaseURL, &o.MaxIterations, &o.MaxParallelTools,
		&o.RateLimitRPM, &dailyBudget,
		&o.Enabled, &o.VoiceEnabled,
		&o.TranscriptionProvider, &o.TranscriptionModel,
		&o.TTSEnabled, &o.TTSProvider, &o.TTSVoice,
		&o.MemoryEnabled, &o.SummarizeEveryNCalls, &o.MemoryRawFallbackN,
		&o.SummarizerProvider, &o.SummarizerModel,
		&o.HistoryWindow, &o.BudgetTokens,
	); err != nil {
		return o, err
	}
	o.AllowedAgentIDs = agentIDs
	o.DailyBudgetUSD = dailyBudget
	return o, nil
}

// List handles GET /api/v1/admin/orchestrators.
func (h *OrchestratorsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := "SELECT " + orchSelectCols + " FROM them.orchestrators ORDER BY created_at"

	rows, err := h.db.Query(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	defer rows.Close()

	orchs := make([]Orchestrator, 0)
	for rows.Next() {
		o, err := scanOrch(rows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "scan error: "+err.Error())
			return
		}
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
	if input.HistoryWindow <= 0 {
		input.HistoryWindow = 20
	}

	const q = `
		INSERT INTO them.orchestrators
		  (name, display_name, system_prompt, llm_provider, llm_model,
		   max_iterations, history_window, enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id::text`

	row := h.db.ExecReturning(r.Context(), q,
		input.Name, input.DisplayName, input.SystemPrompt,
		input.LLMProvider, input.LLMModel,
		input.MaxIterations, input.HistoryWindow, enabled,
	)

	var id string
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

	q := "SELECT " + orchSelectCols + " FROM them.orchestrators WHERE name = $1"

	row := h.db.QueryRow(r.Context(), q, name)
	o, err := scanOrch(row)
	if err != nil {
		writeError(w, http.StatusNotFound, "orchestrator not found")
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// Update handles PUT/PATCH /api/v1/admin/orchestrators/{name}.
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
		SET display_name=$2, system_prompt=$3, llm_provider=$4, llm_model=$5,
		    max_iterations=$6, history_window=$7, enabled=$8, updated_at=now()
		WHERE name=$1`

	if err := h.db.Exec(r.Context(), q,
		name, input.DisplayName, input.SystemPrompt,
		input.LLMProvider, input.LLMModel,
		input.MaxIterations, input.HistoryWindow, enabled,
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
