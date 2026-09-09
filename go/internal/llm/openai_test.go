package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aviciot/them/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sseLines joins lines as an SSE stream with a [DONE] terminator.
func sseLines(lines ...string) string {
	return strings.Join(lines, "\n") + "\ndata: [DONE]\n"
}

// newTestOpenAIProvider creates an OpenAIProvider pointed at a test server URL.
func newTestOpenAIProvider(t *testing.T, serverURL string) *OpenAIProvider {
	t.Helper()
	p := NewOpenAIProvider("test-key", "gpt-4o", serverURL, 0)
	return p
}

// TestOpenAIProvider_defaults verifies that empty model/maxTokens/baseURL get defaults.
func TestOpenAIProvider_defaults(t *testing.T) {
	p := NewOpenAIProvider("key", "", "", 0)
	assert.Equal(t, openAIDefaultModel, p.model)
	assert.Equal(t, 4096, p.maxTokens)
	assert.Equal(t, openAIDefaultBaseURL, p.baseURL)
}

// TestOpenAIProvider_streamTextDelta verifies that text delta chunks are emitted.
func TestOpenAIProvider_streamTextDelta(t *testing.T) {
	body := sseLines(
		`data: {"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"content":", world"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body)) //nolint:errcheck
	}))
	defer srv.Close()

	p := newTestOpenAIProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), []domain.Message{
		{Role: "user", Parts: []domain.ContentPart{{Type: "text", Text: "hi"}}},
	}, nil, Options{})
	require.NoError(t, err)

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	require.Len(t, events, 3)
	assert.Equal(t, "text_delta", events[0].Type)
	assert.Equal(t, "Hello", events[0].Delta)
	assert.Equal(t, "text_delta", events[1].Type)
	assert.Equal(t, ", world", events[1].Delta)
	assert.Equal(t, "stop", events[2].Type)
	assert.Equal(t, "stop", events[2].StopReason)
	require.NotNil(t, events[2].Usage)
	assert.Equal(t, 5, events[2].Usage.InputTokens)
	assert.Equal(t, 2, events[2].Usage.OutputTokens)
}

// TestOpenAIProvider_toolCallEmitted verifies that finish_reason=tool_calls emits tool_calls event.
func TestOpenAIProvider_toolCallEmitted(t *testing.T) {
	body := sseLines(
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"my_tool","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"","type":"function","function":{"name":"","arguments":"{\"k\":\"v\"}"}}]},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":3}}`,
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body)) //nolint:errcheck
	}))
	defer srv.Close()

	p := newTestOpenAIProvider(t, srv.URL)
	ch, err := p.Stream(context.Background(), []domain.Message{
		{Role: "user", Parts: []domain.ContentPart{{Type: "text", Text: "call the tool"}}},
	}, []ToolDef{{Name: "my_tool", Description: "does stuff", InputSchema: map[string]any{}}}, Options{})
	require.NoError(t, err)

	var events []StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	require.Len(t, events, 1)
	assert.Equal(t, "tool_calls", events[0].Type)
	assert.Equal(t, "tool_use", events[0].StopReason)
	require.Len(t, events[0].ToolCalls, 1)
	assert.Equal(t, "my_tool", events[0].ToolCalls[0].Name)
}

// TestOpenAIProvider_httpErrorReturned verifies that a non-200 response is returned as an error.
func TestOpenAIProvider_httpErrorReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid key"}}`)) //nolint:errcheck
	}))
	defer srv.Close()

	p := newTestOpenAIProvider(t, srv.URL)
	_, err := p.Stream(context.Background(), []domain.Message{
		{Role: "user", Parts: []domain.ContentPart{{Type: "text", Text: "hi"}}},
	}, nil, Options{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

// TestOpenAIProvider_contextCancellation verifies stream stops on ctx cancel.
func TestOpenAIProvider_contextCancellation(t *testing.T) {
	// Server streams slowly — cancellation should stop reading.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		for i := 0; i < 1000; i++ {
			w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"},\"finish_reason\":null}]}\n\n")) //nolint:errcheck
			flusher.Flush()
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	p := newTestOpenAIProvider(t, srv.URL)
	ch, err := p.Stream(ctx, []domain.Message{
		{Role: "user", Parts: []domain.ContentPart{{Type: "text", Text: "hi"}}},
	}, nil, Options{})
	require.NoError(t, err)

	count := 0
	for ev := range ch {
		_ = ev
		count++
		if count >= 5 {
			cancel()
		}
	}
	assert.Less(t, count, 1000, "cancel should have stopped stream well before 1000 events")
}

// TestOpenAIProvider_systemPromptSentFirst verifies system prompt is prepended as role=system.
func TestOpenAIProvider_systemPromptSentFirst(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedBody, _ = json.Marshal(map[string]any{}) // parse body below
		// read
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		capturedBody = buf[:n]
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n")) //nolint:errcheck
	}))
	defer srv.Close()

	p := newTestOpenAIProvider(t, srv.URL)
	_, err := p.Stream(context.Background(), []domain.Message{
		{Role: "user", Parts: []domain.ContentPart{{Type: "text", Text: "hi"}}},
	}, nil, Options{SystemPrompt: "You are helpful"})
	require.NoError(t, err)

	var req openAIRequest
	require.NoError(t, json.Unmarshal(capturedBody, &req))
	require.GreaterOrEqual(t, len(req.Messages), 1)
	assert.Equal(t, "system", req.Messages[0].Role)
}

// TestOpenAIProvider_noAuthHeaderForOllama verifies Authorization header is skipped for "ollama" key.
func TestOpenAIProvider_noAuthHeaderForOllama(t *testing.T) {
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n")) //nolint:errcheck
	}))
	defer srv.Close()

	p := NewOpenAIProvider("ollama", "llama3", srv.URL, 0)
	_, _ = p.Stream(context.Background(), []domain.Message{
		{Role: "user", Parts: []domain.ContentPart{{Type: "text", Text: "hi"}}},
	}, nil, Options{})
	assert.Empty(t, authHeader, "Authorization header must be absent for 'ollama' key")
}

// TestDomainMessageToOpenAI_toolResult verifies tool result messages are converted correctly.
func TestDomainMessageToOpenAI_toolResult(t *testing.T) {
	m := domain.Message{
		Role: domain.RoleTool,
		Parts: []domain.ContentPart{{
			Type:       "tool_result",
			ToolUseID:  "call_abc",
			ToolResult: json.RawMessage(`"the result"`),
		}},
	}
	msg, ok := domainMessageToOpenAI(m)
	require.True(t, ok)
	assert.Equal(t, "tool", msg.Role)
	assert.Equal(t, "call_abc", msg.ToolCallID)
}

// TestDomainMessageToOpenAI_emptyUserSkipped verifies empty user messages return ok=false.
func TestDomainMessageToOpenAI_emptyUserSkipped(t *testing.T) {
	m := domain.Message{
		Role:  domain.RoleUser,
		Parts: []domain.ContentPart{{Type: "text", Text: ""}},
	}
	_, ok := domainMessageToOpenAI(m)
	assert.False(t, ok)
}
