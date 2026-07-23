package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ── Agent types ───────────────────────────────────────────────────────────────

// Agent is the JSON representation of a them.agents row.
type Agent struct {
	ID             int64   `json:"id"`
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	AdapterType    string  `json:"adapter_type"`
	EndpointURL    string  `json:"endpoint_url,omitempty"`
	MaxConcurrency int     `json:"max_concurrency"`
	Enabled        bool    `json:"enabled"`
	LLMProviderID  *int64  `json:"llm_provider_id,omitempty"`
}

// AgentInput is the request body for create/update.
type AgentInput struct {
	Slug           string  `json:"slug"`
	Name           string  `json:"name"`
	Description    string  `json:"description"`
	AdapterType    string  `json:"adapter_type"`
	EndpointURL    string  `json:"endpoint_url,omitempty"`
	MaxConcurrency int     `json:"max_concurrency"`
	Enabled        *bool   `json:"enabled,omitempty"`
	LLMProviderID  *int64  `json:"llm_provider_id,omitempty"`
}

// ── Agents handler ────────────────────────────────────────────────────────────

// AgentsHandler handles /api/v1/admin/agents routes.
type AgentsHandler struct {
	db    DBQuerier
	cache CacheInvalidator
}

// NewAgentsHandler creates an AgentsHandler.
func NewAgentsHandler(db DBQuerier, cache CacheInvalidator) *AgentsHandler {
	return &AgentsHandler{db: db, cache: cache}
}

// Routes mounts the agent CRUD endpoints.
func (h *AgentsHandler) Routes(r chi.Router) {
	r.Get("/agents", h.List)
	r.Post("/agents", h.Create)
	r.Get("/agents/{id}", h.Get)
	r.Put("/agents/{id}", h.Update)
	r.Patch("/agents/{id}", h.Update) // Python frontend sends PATCH; accept both
	r.Delete("/agents/{id}", h.Delete)
}

// List handles GET /api/v1/admin/agents.
func (h *AgentsHandler) List(w http.ResponseWriter, r *http.Request) {
	const q = `
		SELECT id, slug, name, description, adapter_type,
		       COALESCE(endpoint_url, ''), max_concurrency, enabled, llm_provider_id
		FROM them.agents
		ORDER BY id`

	rows, err := h.db.Query(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	defer rows.Close()

	agents := make([]Agent, 0) // never null — always returns []
	for rows.Next() {
		var a Agent
		if err := rows.Scan(
			&a.ID, &a.Slug, &a.Name, &a.Description,
			&a.AdapterType, &a.EndpointURL, &a.MaxConcurrency,
			&a.Enabled, &a.LLMProviderID,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error: "+err.Error())
			return
		}
		agents = append(agents, a)
	}

	writeJSON(w, http.StatusOK, agents)
}

// Create handles POST /api/v1/admin/agents.
func (h *AgentsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var input AgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.Slug == "" || input.Name == "" {
		writeError(w, http.StatusBadRequest, "slug and name are required")
		return
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.MaxConcurrency <= 0 {
		input.MaxConcurrency = 1
	}

	const q = `
		INSERT INTO them.agents (slug, name, description, adapter_type, endpoint_url, max_concurrency, enabled, llm_provider_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`

	row := h.db.ExecReturning(r.Context(), q,
		input.Slug, input.Name, input.Description, input.AdapterType,
		input.EndpointURL, input.MaxConcurrency, enabled, input.LLMProviderID,
	)

	var id int64
	if err := row.Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "create agent: "+err.Error())
		return
	}

	// Invalidate agent registry cache.
	if h.cache != nil {
		_ = h.cache.Del(r.Context(), "them:agents:registry")
	}

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/agents/%d", id))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// Get handles GET /api/v1/admin/agents/{id}.
func (h *AgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	const q = `
		SELECT id, slug, name, description, adapter_type,
		       COALESCE(endpoint_url, ''), max_concurrency, enabled, llm_provider_id
		FROM them.agents WHERE id = $1`

	row := h.db.QueryRow(r.Context(), q, id)
	var a Agent
	if err := row.Scan(
		&a.ID, &a.Slug, &a.Name, &a.Description,
		&a.AdapterType, &a.EndpointURL, &a.MaxConcurrency,
		&a.Enabled, &a.LLMProviderID,
	); err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}

	writeJSON(w, http.StatusOK, a)
}

// Update handles PUT /api/v1/admin/agents/{id}.
func (h *AgentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	var input AgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.MaxConcurrency <= 0 {
		input.MaxConcurrency = 1
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	const q = `
		UPDATE them.agents
		SET slug=$2, name=$3, description=$4, adapter_type=$5,
		    endpoint_url=$6, max_concurrency=$7, enabled=$8, llm_provider_id=$9,
		    updated_at=now()
		WHERE id=$1`

	if err := h.db.Exec(r.Context(), q,
		id, input.Slug, input.Name, input.Description, input.AdapterType,
		input.EndpointURL, input.MaxConcurrency, enabled, input.LLMProviderID,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "update agent: "+err.Error())
		return
	}

	if h.cache != nil {
		_ = h.cache.Del(r.Context(), "them:agents:registry")
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// Delete handles DELETE /api/v1/admin/agents/{id} (soft delete: enabled=false).
func (h *AgentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	const q = `UPDATE them.agents SET enabled=false, updated_at=now() WHERE id=$1`
	if err := h.db.Exec(r.Context(), q, id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete agent: "+err.Error())
		return
	}

	if h.cache != nil {
		_ = h.cache.Del(r.Context(), "them:agents:registry")
	}

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}
