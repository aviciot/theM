// Package domain defines the canonical, provider-agnostic message and run types
// used throughout the THEM platform. These types are the internal representation
// stored in PostgreSQL and passed between packages — they deliberately do NOT
// mirror any LLM provider's wire format.
//
// This package fixes High finding #7: provider format must not leak into the DB.
// All adapters that translate to/from a specific LLM provider's format must do
// so at the boundary, keeping the interior of the system clean.
package domain

import (
	"encoding/json"
	"time"
)

// Role constants for Message.Role.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	RoleSystem    = "system"
)

// ContentPart is one unit of content in a message.
// Type is one of: "text", "tool_use", "tool_result", "image".
type ContentPart struct {
	Type string `json:"type"`

	// Text content — present when Type == "text".
	Text string `json:"text,omitempty"`

	// Tool use fields — present when Type == "tool_use".
	ToolUseID string          `json:"tool_use_id,omitempty"`
	ToolName  string          `json:"tool_name,omitempty"`
	ToolInput json.RawMessage `json:"tool_input,omitempty"`

	// Tool result fields — present when Type == "tool_result".
	ToolResult json.RawMessage `json:"tool_result,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
}

// Message is a canonical, provider-agnostic message in a conversation.
// Role must be one of the Role* constants.
type Message struct {
	Role      string        `json:"role"`
	Parts     []ContentPart `json:"parts"`
	CreatedAt time.Time     `json:"created_at"`
	Seq       int           `json:"seq"`
}

// ─── Task status ──────────────────────────────────────────────────────────────

// TaskStatus represents the lifecycle state of a task, matching the state
// machine defined in 08-state-machines.md.
type TaskStatus string

const (
	TaskSubmitted     TaskStatus = "submitted"
	TaskWorking       TaskStatus = "working"
	TaskCompleted     TaskStatus = "completed"
	TaskFailed        TaskStatus = "failed"
	TaskInputRequired TaskStatus = "input_required"
)

// ─── Run status ───────────────────────────────────────────────────────────────

// RunStatus represents the lifecycle state of an orchestration run.
type RunStatus string

const (
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
	RunStopped   RunStatus = "stopped"

	// Aliases used by the Phase 6 orchestration layer.
	RunStatusPending       = RunRunning   // treat pending as running for simplicity
	RunStatusRunning       = RunRunning
	RunStatusCompleted     = RunCompleted
	RunStatusFailed        = RunFailed
	RunStatusInputRequired RunStatus = "input_required"
	RunStatusCancelled     = RunCanceled
)

// ─── Message helpers ──────────────────────────────────────────────────────────

// TextMessage creates a single-part text message with the given role.
func TextMessage(role string, text string) Message {
	return Message{
		Role:      role,
		Parts:     []ContentPart{{Type: "text", Text: text}},
		CreatedAt: time.Now().UTC(),
	}
}

// Text returns the concatenated text of all "text" parts in the message.
// This is a convenience method for system/user messages that contain only text.
func (m Message) Text() string {
	var s string
	for _, p := range m.Parts {
		if p.Type == "text" {
			s += p.Text
		}
	}
	return s
}

// ─── Run ──────────────────────────────────────────────────────────────────────

// Run is the persistence record for one orchestration run. It maps to the
// them.runs table in PostgreSQL.
type Run struct {
	ID             string
	ContextID      string
	ApplicationID  int64
	EntryPointSlug string
	Status         RunStatus
	StartedAt      time.Time
	EndedAt        *time.Time
	InputTokens    int
	OutputTokens   int
	ErrorMessage   string
}
