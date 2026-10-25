// Package events provides pluggable observability storage (Postgres, ClickHouse, dual).
// Design: docs/_archive/OBSERVABILITY_STORAGE_DESIGN.md
//
// Producers emit events via EventSink (non-blocking, buffered).
// Consumers query via EventStore (tenant-scoped, interface-defined methods).
// Backend selection: THE_M_EVENTS_<AREA>=postgres|clickhouse|dual (default: postgres)
package events

import (
	"context"
	"time"
)

// Event is the top-level envelope for all event types.
// Every event carries tenant_id for isolation and event_id for deduplication.
type Event struct {
	EventID       string    `json:"EventID"`       // UUID v4 — dedup key (at-least-once delivery)
	SchemaVersion uint16    `json:"SchemaVersion"` // schema version — allows writer to route unknown versions to DLQ
	Timestamp     time.Time `json:"Timestamp"`      // event occurrence time (not insertion time)
	TenantID      string    `json:"TenantID"`      // UUID — tenant boundary
	Kind          EventKind `json:"Kind"`           // discriminator for payload type

	// Payload — exactly one must be non-nil
	GatewayRequest *GatewayRequestEvent `json:"GatewayRequest,omitempty"`
	ToolCall       *ToolCallEvent       `json:"ToolCall,omitempty"`
	UserActivity   *UserActivityEvent   `json:"UserActivity,omitempty"`
	Audit          *AuditEvent          `json:"Audit,omitempty"`
	// Conversation, RunStep, etc. added in Stage B
}

// EventKind discriminates payload types.
type EventKind string

const (
	EventKindGatewayRequest EventKind = "gateway_request"
	EventKindToolCall       EventKind = "tool_call"
	EventKindUserActivity   EventKind = "user_activity"
	EventKindAudit          EventKind = "audit"
)

// GatewayRequestEvent — LLM Gateway request metadata (bodies stored separately via BodyStore).
// Maps to ClickHouse events_gateway_request + Postgres gateway_requests.
type GatewayRequestEvent struct {
	ApplicationID  string  `json:"ApplicationID"`           // UUID
	ClientID       string  `json:"ClientID"`                // UUID — may be empty (direct call, not via gateway client)
	UserID         string  `json:"UserID"`                  // external_user_id or user_id as string
	RequestID      string  `json:"RequestID"`               // UUID — correlates with bodies
	TraceID        string  `json:"TraceID,omitempty"`       // distributed trace ID
	Provider       string  `json:"Provider"`                 // 'anthropic', 'openai', etc.
	ModelRequested string  `json:"ModelRequested"`          // client's model string
	ModelServed    string  `json:"ModelServed"`             // actual model sent upstream
	Status         string  `json:"Status"`                   // 'ok', 'error', 'blocked', 'rate_limited'
	HTTPStatus     uint16  `json:"HTTPStatus,omitempty"`    //
	ErrorCode      string  `json:"ErrorCode,omitempty"`     //
	TokensIn       uint32  `json:"TokensIn"`                //
	TokensOut      uint32  `json:"TokensOut"`               //
	CostUSD        float64 `json:"CostUSD"`                 // stored as Decimal(12,8) in ClickHouse
	LatencyMS      uint32  `json:"LatencyMS"`               //
	TTFBMS         uint32  `json:"TTFBMS,omitempty"`        // time-to-first-byte for streaming
	Streamed       bool    `json:"Streamed,omitempty"`       //
	BodyRef        string  `json:"BodyRef,omitempty"`       // UUID — points to events_bodies (optional)
}

// ToolCallEvent — MCP or agent tool invocation.
type ToolCallEvent struct {
	ApplicationID string
	RunID         string
	RequestID     string // correlates with gateway request or workflow
	TraceID       string
	UserID        string
	ToolName      string
	MCPServer     string // empty if not MCP
	Status        string // 'ok', 'error', 'timeout'
	DurationMS    uint32
	ArgsRef       string // UUID → events_bodies
	ResultRef     string // UUID → events_bodies
}

// UserActivityEvent — login, logout, SSO, resource access.
type UserActivityEvent struct {
	ApplicationID string
	UserID        string
	Action        string // 'login', 'logout', 'sso_success', 'access_denied'
	ResourceType  string // 'application', 'agent', 'run'
	ResourceID    string
	Outcome       string // 'success', 'failure'
	IP            string
	UserAgent     string
	TraceID       string
}

// AuditEvent — admin actions, policy changes, tamper-evident trail.
type AuditEvent struct {
	UserID       string
	Action       string // 'create', 'update', 'delete'
	ResourceType string // 'agent', 'application', 'policy'
	ResourceID   string
	BeforeRef    string // UUID → events_bodies (snapshot before change)
	AfterRef     string // UUID → events_bodies (snapshot after change)
	PrevHash     string // SHA256 of previous audit event (hash chain)
	Hash         string // SHA256 of this event (for tamper detection)
}

// EventSink is the write-side interface. Producers emit events; the sink
// handles buffering, queueing, and delivery. Emit is non-blocking.
type EventSink interface {
	Emit(ctx context.Context, event Event) error
	Close(ctx context.Context) error // flush buffers, close connections
}

// EventStore is the read-side interface. One method per product use case.
// Every method takes tenantID explicitly (never ambient/GUC isolation).
type EventStore interface {
	// Gateway requests
	ListGatewayRequests(ctx context.Context, tenantID string, filter GatewayRequestFilter) ([]GatewayRequestEvent, error)
	GetGatewayRequestByID(ctx context.Context, tenantID string, requestID string) (*GatewayRequestEvent, error)

	// Usage aggregates
	UsageByDay(ctx context.Context, tenantID string, filter UsageFilter) ([]UsagePoint, error)

	// Tool calls
	ToolCallsForRun(ctx context.Context, tenantID string, runID string) ([]ToolCallEvent, error)

	// User activity
	UserActivity(ctx context.Context, tenantID string, filter ActivityFilter) ([]UserActivityEvent, error)

	// Health check
	Ping(ctx context.Context) error
}

// GatewayRequestFilter — query parameters for ListGatewayRequests.
type GatewayRequestFilter struct {
	ApplicationID string
	ClientID      string
	UserID        string
	Status        string    // 'ok', 'error', etc.
	StartTime     time.Time // inclusive
	EndTime       time.Time // exclusive
	Limit         int       // max rows (default 100, max 1000)
	Offset        int
}

// UsageFilter — parameters for aggregate queries.
type UsageFilter struct {
	ApplicationID string
	StartTime     time.Time
	EndTime       time.Time
	GroupBy       UsageGroupBy // 'day', 'hour', 'application', 'model'
}

type UsageGroupBy string

const (
	UsageGroupByDay         UsageGroupBy = "day"
	UsageGroupByHour        UsageGroupBy = "hour"
	UsageGroupByApplication UsageGroupBy = "application"
	UsageGroupByModel       UsageGroupBy = "model"
)

// UsagePoint — one aggregate row.
type UsagePoint struct {
	Timestamp     time.Time // bucket start time (for time-based grouping)
	ApplicationID string
	Model         string
	Requests      int64
	TokensIn      int64
	TokensOut     int64
	CostUSD       float64
}

// ActivityFilter — query parameters for UserActivity.
type ActivityFilter struct {
	UserID       string
	Action       string
	ResourceType string
	StartTime    time.Time
	EndTime      time.Time
	Limit        int
	Offset       int
}

// BodyStore — pluggable storage for prompt/completion/tool-result content.
// Default: ClickHouse events_bodies table. Optional: S3/MinIO.
// Bodies are encrypted by the producer before WriteBody (Fernet, per LLM_GATEWAY_DESIGN §12).
type BodyStore interface {
	WriteBody(ctx context.Context, body Body) (bodyID string, err error)
	ReadBody(ctx context.Context, tenantID string, bodyID string) (*Body, error)
}

// Body — content payload (encrypted by producer before write).
type Body struct {
	BodyID        string
	TenantID      string
	ApplicationID string
	Kind          string // 'prompt', 'completion', 'tool_args', 'tool_result', 'snapshot'
	Encrypted     bool
	SizeBytes     uint32
	SHA256        string // hex-encoded
	Content       []byte // encrypted if Encrypted=true
}
