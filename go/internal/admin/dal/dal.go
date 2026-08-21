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
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// IsNoRows reports whether err represents a "no rows" result from PostgreSQL.
// Used by the service layer to map DAL errors to service.ErrNotFound without
// importing pgx directly.
func IsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// IsUniqueViolation reports whether err is a PostgreSQL unique-constraint
// violation (SQLSTATE 23505). Used by the service layer to map duplicate-key
// errors to service.ErrUnprocessable without importing pgx/pgconn directly.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
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
	CardFetchedAt        *string  `json:"card_fetched_at"`
	LastScanAt           *string  `json:"last_scan_at"`
	LastScanResult       any      `json:"last_scan_result"`
	RuntimeDefinitionID  *string  `json:"runtime_definition_id,omitempty"`
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
	// Discovery fields — populated by Discover + Apply flow.
	AgentCard    any     `json:"agent_card,omitempty"`
	AgentCardURL *string `json:"agent_card_url,omitempty"`
	Skills       any     `json:"skills,omitempty"`
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

// AppOrchestratorSummary is the lightweight orchestrator summary returned with
// the application list / get responses so the card UI can display name + model
// without a separate API call.
type AppOrchestratorSummary struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	DisplayName string  `json:"display_name"`
	LLMProvider *string `json:"llm_provider,omitempty"`
	LLMModel    *string `json:"llm_model,omitempty"`
}

// Application is the JSON representation of a them.applications row.
type Application struct {
	ID               string                   `json:"id"`
	Name             string                   `json:"name"`
	Enabled          bool                     `json:"enabled"`
	ActiveRevision   *int                     `json:"active_revision,omitempty"`
	ActiveStatus     *string                  `json:"active_status,omitempty"`
	EntryPoints      []EntryPoint             `json:"entry_points"`
	AppOrchestrators []AppOrchestratorSummary `json:"app_orchestrators"`
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
	CostUSD          string  `json:"cost_usd,omitempty"`          // alias for frontend compat
	UserMessage      string  `json:"user_message,omitempty"`      // alias for goal, for frontend compat
	StartedAt        string  `json:"started_at"`
	EndedAt          string  `json:"ended_at,omitempty"`
	DurationMS       *int64  `json:"duration_ms,omitempty"`
}

// SignalInput is the request body for POST /api/v1/runs/{run_id}/signal.
type SignalInput struct {
	Payload json.RawMessage `json:"payload"`
}

// RunStep is one step in a run from them.run_steps.
type RunStep struct {
	ID         string `json:"id"`
	Iteration  int    `json:"iteration"`
	AgentSlug  string `json:"agent_slug,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Input      any    `json:"input,omitempty"`
	Output     string `json:"output,omitempty"`
	Status     string `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`
	LatencyMS  *int64 `json:"latency_ms,omitempty"`
	StartedAt  string `json:"started_at"`
	EndedAt    string `json:"ended_at,omitempty"`
}

// RunUsage is one usage row from them.run_usage.
type RunUsage struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	TokensIn  int    `json:"tokens_input"`
	TokensOut int    `json:"tokens_output"`
	CostUSD   string `json:"cost_usd,omitempty"`
}

// RunDetail extends Run with steps, usage, and child run IDs.
type RunDetail struct {
	Run
	Steps    []RunStep  `json:"steps"`
	Usage    []RunUsage `json:"usage"`
	Children []Run      `json:"children"`
}

// Task is one task row from them.tasks.
type Task struct {
	ID             string `json:"id"`
	ParentTaskID   string `json:"parent_task_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentName      string `json:"agent_name,omitempty"`
	AgentSlug      string `json:"agent_slug,omitempty"`
	OrchestratorID string `json:"orchestrator_id,omitempty"`
	ContextID      string `json:"context_id,omitempty"`
	State          string `json:"state"`
	Kind           string `json:"kind"`
	RemoteTaskID   string `json:"remote_task_id,omitempty"`
	BudgetTokens   *int   `json:"budget_tokens,omitempty"`
	TokensUsed     *int   `json:"tokens_used,omitempty"`
	Error          string `json:"error,omitempty"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	DurationMS     *int64 `json:"duration_ms,omitempty"`
}

// ArtifactPart is one element in an artifact's parts array.
type ArtifactPart struct {
	Kind      string `json:"kind,omitempty"`
	Text      string `json:"text,omitempty"`
	Filename  string `json:"filename,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// Artifact is one artifact row from them.artifacts.
type Artifact struct {
	ID          string         `json:"id"`
	TaskID      string         `json:"task_id"`
	ContextID   string         `json:"context_id,omitempty"`
	ArtifactID  string         `json:"artifact_id,omitempty"`
	Name        string         `json:"name,omitempty"`
	Parts       []ArtifactPart `json:"parts"`
	AppendIndex int            `json:"append_index"`
	LastChunk   bool           `json:"last_chunk"`
	CreatedAt   string         `json:"created_at"`
}

// RunStats is the summary returned by GET /runs/stats.
type RunStats struct {
	Total        int            `json:"total"`
	ByStatus     map[string]int `json:"by_status"`
	TotalCostUSD string         `json:"total_cost_usd"`
}

// ── Token types ───────────────────────────────────────────────────────────────

// Token is the JSON representation of a them.access_tokens row.
// TokenHash is never serialized to JSON — internal use only (cache invalidation).
// Field names match Python's TokenOut schema exactly.
type Token struct {
	ID             string  `json:"id"`
	Label          string  `json:"label"`
	UserID         int64   `json:"user_id"`
	OrchestratorID *string `json:"orchestrator_id"` // null in JSON when unset
	Enabled        bool    `json:"enabled"`
	ExpiresAt      *string `json:"expires_at"`   // RFC3339 or null
	LastUsedAt     *string `json:"last_used_at"` // RFC3339 or null
	CreatedAt      string  `json:"created_at"`
	TokenHash      string  `json:"-"` // never exposed; used for cache invalidation
}

// TokenCreatedOut is returned by POST /admin/tokens — Token fields + one-time plaintext.
type TokenCreatedOut struct {
	Token
	Plaintext string `json:"token"`
}

// TokenCreateRow is the persisted shape for CreateToken (hash computed in service).
type TokenCreateRow struct {
	TokenHash      string
	Label          string
	UserID         int64
	OrchestratorID *string // nil → DB NULL
	ExpiresAt      *string // ISO8601 string or nil → DB NULL
}

// TokenPatchRow carries PATCH fields; nil pointer = field absent (leave unchanged).
type TokenPatchRow struct {
	Label     *string
	Enabled   *bool
	ExpiresAt *string
}
