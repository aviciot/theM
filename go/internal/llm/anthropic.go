package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aviciot/them/internal/domain"
)

const (
	anthropicMessagesURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion     = "2023-06-01"
	defaultModel         = "claude-sonnet-4-6"
	httpTimeout          = 10 * time.Minute
)

// AnthropicProvider implements Provider against the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey     string
	model      string
	maxTokens  int
	httpClient *http.Client
}

// NewAnthropicProvider creates an AnthropicProvider. model defaults to
// "claude-sonnet-4-6" when empty. maxTokens defaults to 4096 when zero.
func NewAnthropicProvider(apiKey, model string, maxTokens int) *AnthropicProvider {
	if model == "" {
		model = defaultModel
	}
	if maxTokens == 0 {
		maxTokens = 4096
	}
	return &AnthropicProvider{
		apiKey:    apiKey,
		model:     model,
		maxTokens: maxTokens,
		httpClient: &http.Client{Timeout: httpTimeout},
	}
}

type anthropicMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
	Stream    bool               `json:"stream"`
	System    string             `json:"system,omitempty"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

// Stream sends messages to Anthropic and returns a channel of StreamEvents.
func (p *AnthropicProvider) Stream(ctx context.Context, messages []domain.Message, tools []ToolDef, opts Options) (<-chan StreamEvent, error) {
	// Apply per-call overrides.
	model := p.model
	if opts.Model != "" {
		model = opts.Model
	}
	maxTokens := p.maxTokens
	if opts.MaxTokens > 0 {
		maxTokens = opts.MaxTokens
	}
	var systemPrompt string
	if opts.SystemPrompt != "" {
		systemPrompt = opts.SystemPrompt
	}
	var apiMsgs []anthropicMessage

	for _, m := range messages {
		if m.Role == domain.RoleSystem {
			if systemPrompt == "" {
				systemPrompt = m.Text()
			}
			continue
		}
		content, err := domainPartsToAnthropicContent(m.Parts)
		if err != nil {
			return nil, fmt.Errorf("llm: anthropic: marshal message: %w", err)
		}
		apiMsgs = append(apiMsgs, anthropicMessage{Role: m.Role, Content: content})
	}

	var apiTools []anthropicTool
	for _, t := range tools {
		apiTools = append(apiTools, anthropicTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		})
	}

	reqBody := anthropicRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  apiMsgs,
		Tools:     apiTools,
		Stream:    true,
		System:    systemPrompt,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("llm: anthropic: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicMessagesURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("llm: anthropic: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: anthropic: http: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("llm: anthropic: status %d: %s", resp.StatusCode, string(body))
	}

	out := make(chan StreamEvent, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		p.parseSSE(ctx, resp.Body, out)
	}()

	return out, nil
}

func (p *AnthropicProvider) parseSSE(ctx context.Context, r io.Reader, out chan<- StreamEvent) {
	scanner := bufio.NewScanner(r)
	var eventType string
	toolInputBuf := map[int]*bytes.Buffer{}
	toolCallAccum := map[int]ToolCall{}

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Text()
		if line == "" {
			eventType = ""
			continue
		}
		if after, ok := cutPrefix(line, "event: "); ok {
			eventType = after
			continue
		}
		if after, ok := cutPrefix(line, "data: "); ok {
			p.handleSSEData(ctx, eventType, after, out, toolInputBuf, toolCallAccum)
		}
	}
}

func (p *AnthropicProvider) handleSSEData(
	ctx context.Context,
	eventType, data string,
	out chan<- StreamEvent,
	toolInputBuf map[int]*bytes.Buffer,
	toolCallAccum map[int]ToolCall,
) {
	switch eventType {
	case "content_block_start":
		var ev struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return
		}
		if ev.ContentBlock.Type == "tool_use" {
			toolCallAccum[ev.Index] = ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
			toolInputBuf[ev.Index] = &bytes.Buffer{}
		}

	case "content_block_delta":
		var ev struct {
			Index int `json:"index"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return
		}
		if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
			sendEvent(ctx, out, StreamEvent{Type: "text_delta", Delta: ev.Delta.Text})
		}
		if ev.Delta.Type == "input_json_delta" {
			if buf, ok := toolInputBuf[ev.Index]; ok {
				buf.WriteString(ev.Delta.PartialJSON)
			}
		}

	case "content_block_stop":
		var ev struct {
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return
		}
		if buf, ok := toolInputBuf[ev.Index]; ok {
			tc := toolCallAccum[ev.Index]
			var input map[string]any
			_ = json.Unmarshal(buf.Bytes(), &input)
			tc.Input = input
			toolCallAccum[ev.Index] = tc
		}

	case "message_delta":
		var ev struct {
			Delta struct {
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return
		}
		if ev.Delta.StopReason == "tool_use" {
			var calls []ToolCall
			for _, tc := range toolCallAccum {
				calls = append(calls, tc)
			}
			if len(calls) > 0 {
				sendEvent(ctx, out, StreamEvent{Type: "tool_calls", ToolCalls: calls, StopReason: "tool_use"})
			}
		} else {
			sendEvent(ctx, out, StreamEvent{Type: "stop", StopReason: ev.Delta.StopReason})
		}
	}
}

func domainPartsToAnthropicContent(parts []domain.ContentPart) (json.RawMessage, error) {
	type textBlock struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type toolUseBlock struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	type toolResultBlock struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id"`
		Content   string `json:"content"`
	}

	var blocks []any
	for _, p := range parts {
		switch p.Type {
		case "text":
			blocks = append(blocks, textBlock{Type: "text", Text: p.Text})
		case "tool_use":
			blocks = append(blocks, toolUseBlock{
				Type:  "tool_use",
				ID:    p.ToolUseID,
				Name:  p.ToolName,
				Input: p.ToolInput,
			})
		case "tool_result":
			var content string
			if len(p.ToolResult) > 0 {
				content = string(p.ToolResult)
			}
			blocks = append(blocks, toolResultBlock{
				Type:      "tool_result",
				ToolUseID: p.ToolUseID,
				Content:   content,
			})
		}
	}

	if len(blocks) == 0 {
		return json.Marshal("")
	}
	return json.Marshal(blocks)
}

func sendEvent(ctx context.Context, out chan<- StreamEvent, ev StreamEvent) {
	select {
	case out <- ev:
	case <-ctx.Done():
	}
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return "", false
}
