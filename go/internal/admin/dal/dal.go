// Package dal (Data Access Layer) contains all SQL query strings, row-scan
// helpers, and result types for the admin package.
//
// The Querier / RowScanner / SingleRowScanner interfaces defined here are the
// canonical definitions; the admin package re-exports them as type aliases so
// existing callers and tests continue to compile unchanged.
package dal

import (
	"context"
	"encoding/json"
)

// Querier is the database interface required by all dal functions.
// admin.DBQuerier is a type alias of this interface.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (RowScanner, error)
	QueryRow(ctx context.Context, sql string, args ...any) SingleRowScanner
	Exec(ctx context.Context, sql string, args ...any) error
	ExecReturning(ctx context.Context, sql string, args ...any) SingleRowScanner
}

// RowScanner iterates over query rows.
// admin.RowScanner is a type alias of this interface.
type RowScanner interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
}

// SingleRowScanner scans a single row.
// admin.SingleRowScanner is a type alias of this interface.
type SingleRowScanner interface {
	Scan(dest ...any) error
}

// DB wraps a Querier and exposes all dal query methods.
type DB struct {
	q Querier
}

// NewDB wraps a Querier for use by dal query functions.
func NewDB(q Querier) *DB {
	return &DB{q: q}
}

// ── Agent types ───────────────────────────────────────────────────────────────

// Agent is the JSON representation of a them.agents row.
// Field names match Python's AgentOut schema exactly.
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

// AgentInput is the request body for agent create/update.
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

// OrchestratorInput is the request body for orchestrator create/update.
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

// ── Application types ─────────────────────────────────────────────────────────

// Application is the JSON representation of a them.applications row.
type Application struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Enabled     bool         `json:"enabled"`
	EntryPoints []EntryPoint `json:"entry_points,omitempty"`
}

// EntryPoint is one access door for an application.
type EntryPoint struct {
	ID             string `json:"id"`
	ApplicationID  string `json:"application_id"`
	Slug           string `json:"slug"`
	EntryPointType string `json:"entry_point_type"`
	Enabled        bool   `json:"enabled"`
}

// ApplicationInput is the request body for application create/update.
type ApplicationInput struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// EntryPointInput is the request body for entry point create/update.
type EntryPointInput struct {
	Slug           string `json:"slug"`
	EntryPointType string `json:"entry_point_type"`
	Enabled        *bool  `json:"enabled,omitempty"`
}

// ── Run types ─────────────────────────────────────────────────────────────────

// Run is the JSON representation of a them.runs row.
// context_id is NOT a column on them.runs (it lives on them.tasks).
// Field names match Python's RunOut schema exactly.
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
