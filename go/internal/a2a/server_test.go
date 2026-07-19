package a2a_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	a2aserver "github.com/aviciot/them/internal/a2a"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

type noopDB struct{}

func (n *noopDB) Exec(_ context.Context, _ string, _ ...any) error { return nil }

// ── Helpers ───────────────────────────────────────────────────────────────────

func newTestA2AServer(mockEvents []llm.StreamEvent) *a2aserver.Server {
	bus := event.New()
	mock := llm.NewMockProvider(mockEvents)
	cfg := orchestrator.Config{MaxIterations: 3}
	recorder := runrecorder.New(&noopDB{})
	orch := orchestrator.New(cfg, mock, nil, recorder, bus, nil)
	return a2aserver.NewServer(recorder, orch, bus, nil)
}

func postRPC(t *testing.T, srv *httptest.Server, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	resp, err := http.Post(srv.URL+"/a2a/myapp", "application/json", bytes.NewReader(data))
	require.NoError(t, err)
	return resp
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// 1. Valid message/send → correct JSON-RPC response with completed status.
func TestA2AMessageSend(t *testing.T) {
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "hello from orchestrator"},
		{Type: "stop", StopReason: "end_turn"},
	}
	s := newTestA2AServer(mockEvents)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"role":      "user",
				"parts":     []map[string]any{{"kind": "text", "text": "hi"}},
				"messageId": "uuid-123",
			},
		},
		"id": "req-1",
	}
	resp := postRPC(t, srv, reqBody)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))

	assert.Equal(t, "2.0", rpcResp["jsonrpc"])
	assert.Nil(t, rpcResp["error"], "expected no error")
	require.NotNil(t, rpcResp["result"])

	result := rpcResp["result"].(map[string]any)
	status := result["status"].(map[string]any)
	assert.Equal(t, "completed", status["state"])
}

// 2. Unknown method → JSON-RPC error -32601.
func TestA2AUnknownMethod(t *testing.T) {
	s := newTestA2AServer(nil)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  "unknown/method",
		"id":      "req-2",
	}
	resp := postRPC(t, srv, reqBody)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))

	assert.Nil(t, rpcResp["result"])
	require.NotNil(t, rpcResp["error"])

	errObj := rpcResp["error"].(map[string]any)
	assert.Equal(t, float64(-32601), errObj["code"])
}

// 3. Malformed JSON → JSON-RPC error -32700.
func TestA2AMalformedJSON(t *testing.T) {
	s := newTestA2AServer(nil)
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/a2a/myapp", "application/json",
		bytes.NewReader([]byte(`{not valid json`)))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))

	require.NotNil(t, rpcResp["error"])
	errObj := rpcResp["error"].(map[string]any)
	assert.Equal(t, float64(-32700), errObj["code"])
}
