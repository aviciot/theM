package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/aviciot/them/internal/domain"
)

const (
	openAIDefaultBaseURL = "https://api.openai.com/v1"
	openAIDefaultModel   = "gpt-4o"
)

// OpenAIProvider implements Provider against the OpenAI Chat Completions API.
// It is wire-compatible with any OpenAI-compatible endpoint: OpenAI, Ollama,
// vLLM, LMStudio, llama.cpp server, Groq, etc. The baseURL selects the
// endpoint; an empty or "ollama"-valued apiKey skips the Authorization header.
type OpenAIProvider struct {
	apiKey     string
	model      string
	maxTokens  int
	baseURL    string
	httpClient *http.Client
}

// NewOpenAIProvider creates an OpenAIProvider. Pass an empty baseURL to use
// the OpenAI public API. For local models pass e.g.
// "http://host.docker.internal:11434/v1" (Ollama) or "http://vllm:8000/v1".
// model defaults to "gpt-4o"; maxTokens defaults to 4096.
func NewOpenAIProvider(apiKey, model, baseURL string, maxTokens int) *OpenAIProvider {
	if model == "" {
		model = openAIDefaultModel
	}
	if maxTokens == 0 {
		maxTokens = 4096
	}
	if baseURL == "" {
		baseURL = openAIDefaultBaseURL
	}
	return &OpenAIProvider{
		apiKey:     apiKey,
		model:      model,
		maxTokens:  maxTokens,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: httpTimeout},
	}
}

// openAIMessage is one entry in the messages array.
type openAIMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

type openAIFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type openAIRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []openAIMessage `json:"messages"`
	Tools     []openAITool    `json:"tools,omitempty"`
	Stream    bool            `json:"stream"`
}

// Stream sends messages to an OpenAI-compatible endpoint and returns a channel
// of StreamEvents. The event format is normalized to match the llm.Provider
// contract (same as AnthropicProvider) so callers are provider-agnostic.
func (p *OpenAIProvider) Stream(ctx context.Context, messages []domain.Message, tools []ToolDef, opts Options) (<-chan StreamEvent, error) {
	model := p.model
	if opts.Model != "" {
		model = opts.Model
	}
	maxTokens := p.maxTokens
	if opts.MaxTokens > 0 {
		maxTokens = opts.MaxTokens
	}

	var apiMsgs []openAIMessage

	// Collect system prompt — OpenAI uses role "system" as a regular message.
	systemPrompt := opts.SystemPrompt
	for _, m := range messages {
		if m.Role == domain.RoleSystem {
			if systemPrompt == "" {
				systemPrompt = m.Text()
			}
			continue
		}
		msg, ok := domainMessageToOpenAI(m)
		if !ok {
			continue
		}
		apiMsgs = append(apiMsgs, msg)
	}
	if systemPrompt != "" {
		sys := openAIMessage{Role: "system", Content: jsonString(systemPrompt)}
		apiMsgs = append([]openAIMessage{sys}, apiMsgs...)
	}

	var apiTools []openAITool
	for _, t := range tools {
		apiTools = append(apiTools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	reqBody := openAIRequest{
		Model:     model,
		MaxTokens: maxTokens,
		Messages:  apiMsgs,
		Tools:     apiTools,
		Stream:    true,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("llm: openai: marshal request: %w", err)
	}

	url := p.baseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("llm: openai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Skip auth header for local models that don't require it.
	if p.apiKey != "" && p.apiKey != "ollama" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: openai: http: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("llm: openai: status %d: %s", resp.StatusCode, string(body))
	}

	out := make(chan StreamEvent, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		p.parseSSE(ctx, resp.Body, out)
	}()

	return out, nil
}

// parseSSE reads OpenAI streaming SSE lines and emits normalized StreamEvents.
func (p *OpenAIProvider) parseSSE(ctx context.Context, r io.Reader, out chan<- StreamEvent) {
	scanner := bufio.NewScanner(r)

	// Accumulators for tool call deltas (indexed by tool_calls array position).
	toolCallNames := map[int]string{}
	toolCallIDs := map[int]string{}
	toolCallArgs := map[int]*bytes.Buffer{}

	var inputTokens, outputTokens int

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Text()
		if line == "" {
			continue
		}
		data, ok := cutPrefix(line, "data: ")
		if !ok {
			continue
		}
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   *string          `json:"content"`
					ToolCalls []openAIToolCall  `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		// Usage may appear in any chunk (stream_options or final chunk).
		if chunk.Usage != nil {
			inputTokens = chunk.Usage.PromptTokens
			outputTokens = chunk.Usage.CompletionTokens
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		// Text delta.
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			sendEvent(ctx, out, StreamEvent{Type: "text_delta", Delta: *choice.Delta.Content})
		}

		// Tool call deltas — OpenAI streams them incrementally.
		for _, tc := range choice.Delta.ToolCalls {
			idx := 0
			// The index field in the delta tells us which tool call slot this is.
			// We use it directly since it's the position in the array.
			if tc.ID != "" {
				toolCallIDs[idx] = tc.ID
			}
			if tc.Function.Name != "" {
				toolCallNames[idx] = tc.Function.Name
			}
			if tc.Function.Arguments != "" {
				if toolCallArgs[idx] == nil {
					toolCallArgs[idx] = &bytes.Buffer{}
				}
				toolCallArgs[idx].WriteString(tc.Function.Arguments)
			}
		}

		// Finish reason — emit final events.
		if choice.FinishReason != nil {
			usage := &Usage{InputTokens: inputTokens, OutputTokens: outputTokens}
			switch *choice.FinishReason {
			case "tool_calls":
				calls := p.buildToolCalls(toolCallIDs, toolCallNames, toolCallArgs)
				if len(calls) > 0 {
					sendEvent(ctx, out, StreamEvent{
						Type:       "tool_calls",
						ToolCalls:  calls,
						StopReason: "tool_use",
						Usage:      usage,
					})
				}
			default:
				sendEvent(ctx, out, StreamEvent{
					Type:       "stop",
					StopReason: *choice.FinishReason,
					Usage:      usage,
				})
			}
		}
	}
}

func (p *OpenAIProvider) buildToolCalls(
	ids map[int]string,
	names map[int]string,
	args map[int]*bytes.Buffer,
) []ToolCall {
	var calls []ToolCall
	for i := range names {
		var input map[string]any
		if buf := args[i]; buf != nil {
			_ = json.Unmarshal(buf.Bytes(), &input)
		}
		if input == nil {
			input = map[string]any{}
		}
		calls = append(calls, ToolCall{
			ID:    ids[i],
			Name:  names[i],
			Input: input,
		})
	}
	return calls
}

// domainMessageToOpenAI converts a domain.Message to an openAIMessage.
// Returns (msg, false) when the message has no usable content (caller skips it).
func domainMessageToOpenAI(m domain.Message) (openAIMessage, bool) {
	switch m.Role {
	case domain.RoleTool:
		// Tool results — one message per part.
		for _, p := range m.Parts {
			if p.Type == "tool_result" {
				return openAIMessage{
					Role:       "tool",
					ToolCallID: p.ToolUseID,
					Content:    jsonString(string(p.ToolResult)),
				}, true
			}
		}
		return openAIMessage{}, false

	case domain.RoleAssistant:
		// Assistant may have text and/or tool_use parts.
		var textContent string
		var toolCalls []openAIToolCall
		for _, p := range m.Parts {
			switch p.Type {
			case "text":
				textContent += p.Text
			case "tool_use":
				args := p.ToolInput
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				toolCalls = append(toolCalls, openAIToolCall{
					ID:   p.ToolUseID,
					Type: "function",
					Function: struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					}{
						Name:      p.ToolName,
						Arguments: string(args),
					},
				})
			}
		}
		if textContent == "" && len(toolCalls) == 0 {
			return openAIMessage{}, false
		}
		msg := openAIMessage{Role: "assistant", ToolCalls: toolCalls}
		if textContent != "" {
			msg.Content = jsonString(textContent)
		}
		return msg, true

	default: // user
		text := m.Text()
		if text == "" {
			return openAIMessage{}, false
		}
		return openAIMessage{Role: "user", Content: jsonString(text)}, true
	}
}

// jsonString encodes a plain string as a JSON string literal.
func jsonString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}
