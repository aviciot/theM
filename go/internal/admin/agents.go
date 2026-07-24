package admin

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ── Agent types ───────────────────────────────────────────────────────────────

// Agent is the JSON representation of a them.agents row.
// Field names match Python's AgentOut schema exactly so the frontend works
// without changes.
type Agent struct {
	ID               string   `json:"id"`
	Slug             string   `json:"slug"`
	DisplayName      string   `json:"display_name"`
	Description      string   `json:"description"`
	Transport        string   `json:"transport"`
	EndpointURL      string   `json:"endpoint_url,omitempty"`
	AuthTokenSet     bool     `json:"auth_token_set"`
	AuthTokenMasked  *string  `json:"auth_token_masked"`
	InputSchema      any      `json:"input_schema"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	MaxConcurrency   int      `json:"max_concurrency"`
	MaxRetries       int      `json:"max_retries"`
	Enabled          bool     `json:"enabled"`
	Tags             []string `json:"tags"`
	AgentCard        any      `json:"agent_card"`
	AgentCardURL     *string  `json:"agent_card_url"`
	Skills           any      `json:"skills"`
	SupportsStreaming bool     `json:"supports_streaming"`
	SupportsPush     bool     `json:"supports_push"`
	Icon             *string  `json:"icon"`
	Category         *string  `json:"category"`
	CardFetchedAt    *string  `json:"card_fetched_at"`
	LastScanAt       *string  `json:"last_scan_at"`
	LastScanResult   any      `json:"last_scan_result"`
}

// AgentInput is the request body for create/update.
// Accepts both old (name/adapter_type) and new (display_name/transport) field
// names so existing API clients keep working.
type AgentInput struct {
	Slug             string   `json:"slug"`
	DisplayName      string   `json:"display_name"`
	Description      string   `json:"description"`
	Transport        string   `json:"transport"`
	EndpointURL      string   `json:"endpoint_url,omitempty"`
	AuthToken        string   `json:"auth_token,omitempty"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	MaxConcurrency   int      `json:"max_concurrency"`
	MaxRetries       int      `json:"max_retries"`
	Enabled          *bool    `json:"enabled,omitempty"`
	Tags             []string `json:"tags,omitempty"`
	SupportsStreaming bool     `json:"supports_streaming"`
	SupportsPush     bool     `json:"supports_push"`
	Icon             *string  `json:"icon,omitempty"`
	Category         *string  `json:"category,omitempty"`
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
		SELECT id::text, slug, display_name, description, transport,
		       COALESCE(endpoint_url, ''),
		       auth_token_encrypted IS NOT NULL AND auth_token_encrypted <> '',
		       input_schema, timeout_seconds, max_concurrency, max_retries,
		       enabled, COALESCE(tags, '{}'), agent_card, agent_card_url,
		       skills, supports_streaming, supports_push, icon, category,
		       card_fetched_at::text, last_scan_at::text, last_scan_result
		FROM them.agents
		ORDER BY created_at`

	rows, err := h.db.Query(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error: "+err.Error())
		return
	}
	defer rows.Close()

	agents := make([]Agent, 0) // never null — always returns []
	for rows.Next() {
		var a Agent
		var tagsArr []string
		var inputSchema, agentCard, skills, lastScanResult []byte
		var cardFetchedAt, lastScanAt *string
		if err := rows.Scan(
			&a.ID, &a.Slug, &a.DisplayName, &a.Description, &a.Transport,
			&a.EndpointURL, &a.AuthTokenSet,
			&inputSchema, &a.TimeoutSeconds, &a.MaxConcurrency, &a.MaxRetries,
			&a.Enabled, &tagsArr, &agentCard, &a.AgentCardURL,
			&skills, &a.SupportsStreaming, &a.SupportsPush, &a.Icon, &a.Category,
			&cardFetchedAt, &lastScanAt, &lastScanResult,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "scan error: "+err.Error())
			return
		}
		a.Tags = tagsArr
		if len(inputSchema) > 0 {
			_ = json.Unmarshal(inputSchema, &a.InputSchema)
		} else {
			a.InputSchema = map[string]any{}
		}
		if len(agentCard) > 0 {
			_ = json.Unmarshal(agentCard, &a.AgentCard)
		}
		if len(skills) > 0 {
			_ = json.Unmarshal(skills, &a.Skills)
		} else {
			a.Skills = []any{}
		}
		if len(lastScanResult) > 0 {
			_ = json.Unmarshal(lastScanResult, &a.LastScanResult)
		}
		a.CardFetchedAt = cardFetchedAt
		a.LastScanAt = lastScanAt
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
	if input.Slug == "" || input.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "slug and display_name are required")
		return
	}
	if input.Transport == "" {
		input.Transport = "a2a_async"
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.MaxConcurrency <= 0 {
		input.MaxConcurrency = 5
	}
	if input.MaxRetries <= 0 {
		input.MaxRetries = 2
	}
	if input.TimeoutSeconds <= 0 {
		input.TimeoutSeconds = 30
	}

	const q = `
		INSERT INTO them.agents
		  (slug, display_name, description, transport, endpoint_url,
		   max_concurrency, max_retries, timeout_seconds, enabled,
		   supports_streaming, supports_push, icon, category)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		RETURNING id::text`

	row := h.db.ExecReturning(r.Context(), q,
		input.Slug, input.DisplayName, input.Description, input.Transport,
		input.EndpointURL, input.MaxConcurrency, input.MaxRetries,
		input.TimeoutSeconds, enabled,
		input.SupportsStreaming, input.SupportsPush,
		input.Icon, input.Category,
	)

	var id string
	if err := row.Scan(&id); err != nil {
		writeError(w, http.StatusInternalServerError, "create agent: "+err.Error())
		return
	}

	h.invalidateCache(r)

	w.Header().Set("Location", fmt.Sprintf("/api/v1/admin/agents/%s", id))
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

// Get handles GET /api/v1/admin/agents/{id}.
func (h *AgentsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	const q = `
		SELECT id::text, slug, display_name, description, transport,
		       COALESCE(endpoint_url, ''),
		       auth_token_encrypted IS NOT NULL AND auth_token_encrypted <> '',
		       input_schema, timeout_seconds, max_concurrency, max_retries,
		       enabled, COALESCE(tags, '{}'), agent_card, agent_card_url,
		       skills, supports_streaming, supports_push, icon, category,
		       card_fetched_at::text, last_scan_at::text, last_scan_result
		FROM them.agents WHERE id = $1::uuid`

	row := h.db.QueryRow(r.Context(), q, id)
	var a Agent
	var tagsArr []string
	var inputSchema, agentCard, skills, lastScanResult []byte
	var cardFetchedAt, lastScanAt *string
	if err := row.Scan(
		&a.ID, &a.Slug, &a.DisplayName, &a.Description, &a.Transport,
		&a.EndpointURL, &a.AuthTokenSet,
		&inputSchema, &a.TimeoutSeconds, &a.MaxConcurrency, &a.MaxRetries,
		&a.Enabled, &tagsArr, &agentCard, &a.AgentCardURL,
		&skills, &a.SupportsStreaming, &a.SupportsPush, &a.Icon, &a.Category,
		&cardFetchedAt, &lastScanAt, &lastScanResult,
	); err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	a.Tags = tagsArr
	if len(inputSchema) > 0 {
		_ = json.Unmarshal(inputSchema, &a.InputSchema)
	} else {
		a.InputSchema = map[string]any{}
	}
	if len(agentCard) > 0 {
		_ = json.Unmarshal(agentCard, &a.AgentCard)
	}
	if len(skills) > 0 {
		_ = json.Unmarshal(skills, &a.Skills)
	} else {
		a.Skills = []any{}
	}
	if len(lastScanResult) > 0 {
		_ = json.Unmarshal(lastScanResult, &a.LastScanResult)
	}
	a.CardFetchedAt = cardFetchedAt
	a.LastScanAt = lastScanAt

	writeJSON(w, http.StatusOK, a)
}

// Update handles PUT/PATCH /api/v1/admin/agents/{id}.
func (h *AgentsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	var input AgentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if input.MaxConcurrency <= 0 {
		input.MaxConcurrency = 5
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	const q = `
		UPDATE them.agents
		SET display_name=$2, description=$3, transport=$4,
		    endpoint_url=NULLIF($5, ''), max_concurrency=$6, max_retries=$7,
		    timeout_seconds=$8, enabled=$9,
		    supports_streaming=$10, supports_push=$11,
		    icon=$12, category=$13, updated_at=now()
		WHERE id=$1::uuid`

	if err := h.db.Exec(r.Context(), q,
		id, input.DisplayName, input.Description, input.Transport,
		input.EndpointURL, input.MaxConcurrency, input.MaxRetries,
		input.TimeoutSeconds, enabled,
		input.SupportsStreaming, input.SupportsPush,
		input.Icon, input.Category,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "update agent: "+err.Error())
		return
	}

	h.invalidateCache(r)

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "updated": true})
}

// Delete handles DELETE /api/v1/admin/agents/{id} (soft delete: enabled=false).
func (h *AgentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid agent id")
		return
	}

	const q = `UPDATE them.agents SET enabled=false, updated_at=now() WHERE id=$1::uuid`
	if err := h.db.Exec(r.Context(), q, id); err != nil {
		writeError(w, http.StatusInternalServerError, "delete agent: "+err.Error())
		return
	}

	h.invalidateCache(r)

	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

func (h *AgentsHandler) invalidateCache(r *http.Request) {
	if h.cache != nil {
		_ = h.cache.Del(r.Context(), "them:agents:registry")
	}
}
