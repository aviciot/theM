// Package llm defines the LLM provider interface and associated types.
// Implementations live in sub-files: anthropic.go, mock.go.
package llm

import (
	"context"
	"errors"

	"github.com/aviciot/them/internal/domain"
)

// ErrToolDefInvalid is returned by ToolDef.Validate when required fields are missing.
var ErrToolDefInvalid = errors.New("llm: invalid tool definition")

// ToolDef describes a tool the LLM can call. Each agent exposed to the
// orchestrator maps to one ToolDef named "agent__<slug>".
type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Validate returns ErrToolDefInvalid when required fields are empty.
func (t ToolDef) Validate() error {
	if t.Name == "" {
		return ErrToolDefInvalid
	}
	if t.Description == "" {
		return ErrToolDefInvalid
	}
	return nil
}

// ToolCall is a single tool invocation requested by the LLM.
type ToolCall struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// StreamEvent is a single streamed LLM event. Events arrive in order from
// Provider.Stream and are published to the event bus as they arrive.
type StreamEvent struct {
	// Type is one of: "text_delta", "tool_calls", "stop", "error".
	Type string

	// Delta holds the text delta for type=="text_delta".
	Delta string

	// ToolCalls holds the requested tool calls for type=="tool_calls".
	ToolCalls []ToolCall

	// StopReason indicates why the stream stopped: "end_turn", "tool_use",
	// "max_tokens", "stop_sequence".
	StopReason string

	// Error is set for type=="error".
	Error error

	// Usage is populated on the final event (type=="stop").
	Usage *Usage
}

// Usage holds token counts from the LLM response.
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// Options holds per-call options that override provider defaults.
type Options struct {
	Model       string
	MaxTokens   int
	Temperature float64
	SystemPrompt string
}

// Provider is the interface all LLM backends implement.
type Provider interface {
	// Stream sends messages to the LLM and streams back events.
	// tools may be nil when no tools are available.
	// The channel is closed when the stream ends or ctx is cancelled.
	Stream(ctx context.Context, messages []domain.Message, tools []ToolDef, opts Options) (<-chan StreamEvent, error)
}
