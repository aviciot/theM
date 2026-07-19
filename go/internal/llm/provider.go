// Package llm defines the provider-agnostic LLM interface used throughout
// the THEM orchestration engine. All LLM calls go through this interface,
// keeping provider-specific wire formats out of business logic.
//
// This fixes:
//   - High finding #7: canonical message format — providers translate at the
//     boundary, not inside orchestration code.
//   - High finding #8: streaming cancellation — ctx cancellation immediately
//     cancels the underlying HTTP request to the LLM provider.
//   - Medium finding #9: typed tool definitions replace the untyped NeutralTool.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aviciot/them/internal/domain"
)

// ── Tool definitions ──────────────────────────────────────────────────────────

// ToolDef is a typed tool definition (fixes Medium finding #9 — NeutralTool
// was untyped). InputSchema must be a valid JSON Schema object.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"` // JSON Schema object
}

// ErrToolDefInvalid is returned by ToolDef.Validate when the definition is
// structurally invalid.
var ErrToolDefInvalid = errors.New("llm: invalid tool definition")

// Validate checks that the ToolDef has a non-empty Name and Description.
// Returns ErrToolDefInvalid (wrapped) on failure.
func (t ToolDef) Validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("%w: Name must not be empty", ErrToolDefInvalid)
	}
	if strings.TrimSpace(t.Description) == "" {
		return fmt.Errorf("%w: Description must not be empty", ErrToolDefInvalid)
	}
	return nil
}

// ── Stream events ─────────────────────────────────────────────────────────────

// StreamEvent is one event in a streaming LLM response.
type StreamEvent struct {
	// Type is one of: "text_delta", "tool_use_start", "tool_use_delta",
	// "tool_use_end", "stop".
	Type string

	// Delta is the incremental text content (Type == "text_delta") or the
	// incremental tool input JSON fragment (Type == "tool_use_delta").
	Delta string

	// ToolUse carries tool-call metadata for "tool_use_start" and
	// "tool_use_end" events. Nil for all other event types.
	ToolUse *ToolUseEvent
}

// ToolUseEvent carries metadata about a single tool invocation.
type ToolUseEvent struct {
	ID    string
	Name  string
	Input json.RawMessage // accumulated JSON input (complete at "tool_use_end")
}

// ── Provider interface ────────────────────────────────────────────────────────

// Options configures a single LLM call.
type Options struct {
	Model        string
	MaxTokens    int
	Temperature  float64
	SystemPrompt string
}

// Provider is the interface every LLM provider must implement.
type Provider interface {
	// Stream sends messages to the LLM and streams back events.
	// The context carries cancellation — if ctx is cancelled the underlying
	// HTTP request to the LLM provider is cancelled immediately.
	//
	// The returned channel is closed when the stream ends (either with a
	// "stop" event, an error, or context cancellation). Errors during
	// streaming are delivered as a final event with Type == "error" and
	// Delta containing the error text, then the channel is closed.
	//
	// Callers that only care about cancellation may simply cancel ctx; the
	// channel will drain and close without leaking goroutines.
	Stream(ctx context.Context, messages []domain.Message, tools []ToolDef, opts Options) (<-chan StreamEvent, error)

	// Name returns the provider name for logging and metrics.
	Name() string
}

// ── Mock provider (for tests) ─────────────────────────────────────────────────

// mockProvider returns a pre-canned sequence of StreamEvents without any HTTP
// calls. Suitable for unit tests and offline development.
type mockProvider struct {
	responses []StreamEvent
}

// NewMockProvider creates a Provider that emits responses in order, then closes
// the channel. If ctx is cancelled before all responses are sent the channel is
// closed early (respects cancellation).
func NewMockProvider(responses []StreamEvent) Provider {
	return &mockProvider{responses: responses}
}

func (m *mockProvider) Name() string { return "mock" }

func (m *mockProvider) Stream(ctx context.Context, _ []domain.Message, _ []ToolDef, _ Options) (<-chan StreamEvent, error) {
	// Use a small fixed-size buffer (not len(responses)) so that ctx cancellation
	// is observable even when there are many responses. A buffer of 1 means
	// the goroutine blocks on the channel send and checks ctx.Done on each
	// iteration, ensuring cancellation is detected promptly.
	ch := make(chan StreamEvent, 1)

	go func() {
		defer close(ch)
		for _, ev := range m.responses {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()

	return ch, nil
}

// ── Anthropic provider ────────────────────────────────────────────────────────

const (
	anthropicAPIURL    = "https://api.anthropic.com/v1/messages"
	anthropicAPIVersion = "2023-06-01"
	anthropicTimeout   = 120 * time.Second
)

// anthropicProvider implements Provider using the Anthropic Messages API with
// server-sent event (SSE) streaming. Uses net/http directly — no SDK.
type anthropicProvider struct {
	apiKey string
	client *http.Client
}

// NewAnthropicProvider creates a Provider that calls the Anthropic Messages API.
// apiKey must be a valid Anthropic API key.
func NewAnthropicProvider(apiKey string) Provider {
	return &anthropicProvider{
		apiKey: apiKey,
		client: &http.Client{Timeout: anthropicTimeout},
	}
}

func (p *anthropicProvider) Name() string { return "anthropic" }

// anthropicMessage is the Anthropic wire format for a single message.
type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content"`
}

// anthropicContent is one content block in the Anthropic wire format.
type anthropicContent struct {
	Type       string          `json:"type"`
	Text       string          `json:"text,omitempty"`
	ID         string          `json:"id,omitempty"`
	Name       string          `json:"name,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	ToolUseID  string          `json:"tool_use_id,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
}

// anthropicTool is the Anthropic wire format for a tool definition.
type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// anthropicRequest is the full Anthropic Messages API request body.
type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
}

// Stream translates domain messages and tools to Anthropic wire format, sends
// the request, and parses the SSE stream into StreamEvents. The ctx is
// propagated to the HTTP request so cancellation immediately closes the
// underlying connection.
func (p *anthropicProvider) Stream(ctx context.Context, messages []domain.Message, tools []ToolDef, opts Options) (<-chan StreamEvent, error) {
	// Translate messages to Anthropic format.
	var wireMessages []anthropicMessage
	for _, m := range messages {
		// Skip system messages — Anthropic takes system as a separate field.
		if m.Role == domain.RoleSystem {
			continue
		}
		var contents []anthropicContent
		for _, part := range m.Parts {
			switch part.Type {
			case "text":
				contents = append(contents, anthropicContent{Type: "text", Text: part.Text})
			case "tool_use":
				contents = append(contents, anthropicContent{
					Type:  "tool_use",
					ID:    part.ToolUseID,
					Name:  part.ToolName,
					Input: part.ToolInput,
				})
			case "tool_result":
				contents = append(contents, anthropicContent{
					Type:      "tool_result",
					ToolUseID: part.ToolUseID,
					Content:   part.ToolResult,
					IsError:   part.IsError,
				})
			}
		}
		wireMessages = append(wireMessages, anthropicMessage{Role: m.Role, Content: contents})
	}

	// Translate tools.
	var wireTools []anthropicTool
	for _, t := range tools {
		wireTools = append(wireTools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	// Build system prompt from system messages or opts.
	system := opts.SystemPrompt
	for _, m := range messages {
		if m.Role == domain.RoleSystem && len(m.Parts) > 0 {
			for _, p := range m.Parts {
				if p.Type == "text" && p.Text != "" {
					system = p.Text
					break
				}
			}
		}
	}

	reqBody := anthropicRequest{
		Model:     opts.Model,
		MaxTokens: opts.MaxTokens,
		System:    system,
		Messages:  wireMessages,
		Tools:     wireTools,
		Stream:    true,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	// Create the HTTP request with the caller's context so cancellation
	// propagates to the network layer immediately.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: http: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("anthropic: unexpected status %d", resp.StatusCode)
	}

	ch := make(chan StreamEvent, 64)

	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		// Track current tool-use accumulation state.
		var currentToolID, currentToolName string
		var currentToolInput strings.Builder

		for scanner.Scan() {
			line := scanner.Text()

			// SSE: data lines carry JSON events; blank lines are separators.
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var sseEvent map[string]json.RawMessage
			if err := json.Unmarshal([]byte(data), &sseEvent); err != nil {
				continue
			}

			var evType string
			if t, ok := sseEvent["type"]; ok {
				_ = json.Unmarshal(t, &evType)
			}

			switch evType {
			case "content_block_start":
				// Detect tool_use blocks.
				var cb struct {
					ContentBlock struct {
						Type string `json:"type"`
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"content_block"`
				}
				if err := json.Unmarshal([]byte(data), &cb); err == nil && cb.ContentBlock.Type == "tool_use" {
					currentToolID = cb.ContentBlock.ID
					currentToolName = cb.ContentBlock.Name
					currentToolInput.Reset()
					select {
					case ch <- StreamEvent{
						Type: "tool_use_start",
						ToolUse: &ToolUseEvent{
							ID:   currentToolID,
							Name: currentToolName,
						},
					}:
					case <-ctx.Done():
						return
					}
				}

			case "content_block_delta":
				var delta struct {
					Delta struct {
						Type  string `json:"type"`
						Text  string `json:"text"`
						Input string `json:"partial_json"`
					} `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &delta); err != nil {
					continue
				}
				switch delta.Delta.Type {
				case "text_delta":
					select {
					case ch <- StreamEvent{Type: "text_delta", Delta: delta.Delta.Text}:
					case <-ctx.Done():
						return
					}
				case "input_json_delta":
					currentToolInput.WriteString(delta.Delta.Input)
					select {
					case ch <- StreamEvent{Type: "tool_use_delta", Delta: delta.Delta.Input}:
					case <-ctx.Done():
						return
					}
				}

			case "content_block_stop":
				if currentToolID != "" {
					inputJSON := json.RawMessage(currentToolInput.String())
					if len(inputJSON) == 0 {
						inputJSON = json.RawMessage("{}")
					}
					select {
					case ch <- StreamEvent{
						Type: "tool_use_end",
						ToolUse: &ToolUseEvent{
							ID:    currentToolID,
							Name:  currentToolName,
							Input: inputJSON,
						},
					}:
					case <-ctx.Done():
						return
					}
					currentToolID = ""
					currentToolName = ""
					currentToolInput.Reset()
				}

			case "message_stop":
				select {
				case ch <- StreamEvent{Type: "stop"}:
				case <-ctx.Done():
				}
				return
			}
		}

		if err := scanner.Err(); err != nil {
			if ctx.Err() == nil {
				select {
				case ch <- StreamEvent{Type: "error", Delta: err.Error()}:
				default:
				}
			}
		}
	}()

	return ch, nil
}
