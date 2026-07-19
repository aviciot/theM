package ws_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/session"
	wshandler "github.com/aviciot/them/internal/ws"
)

// ── Fakes ─────────────────────────────────────────────────────────────────────

type fakeAuth struct {
	token string
	info  *auth.TokenInfo
}

func (f *fakeAuth) Validate(_ context.Context, token string) (*auth.TokenInfo, error) {
	if token == f.token {
		return f.info, nil
	}
	return nil, errors.New("invalid token")
}

type fakeSessionStore struct {
	registered []string
	ended      []string
}

func (s *fakeSessionStore) Register(_ context.Context, info session.SessionInfo) error {
	s.registered = append(s.registered, info.SessionID)
	return nil
}

func (s *fakeSessionStore) End(_ context.Context, sessionID, _, _ string) error {
	s.ended = append(s.ended, sessionID)
	return nil
}

type fakeDBQuerier struct{}

func (f *fakeDBQuerier) Exec(_ context.Context, _ string, _ ...any) error { return nil }

// ── Helpers ───────────────────────────────────────────────────────────────────

func newTestHandler(t *testing.T, mockEvents []llm.StreamEvent, authn wshandler.Authenticator, sessions wshandler.SessionStore) (*wshandler.Handler, *event.InMemoryBus) {
	t.Helper()
	bus := event.New()
	mock := llm.NewMockProvider(mockEvents)
	cfg := orchestrator.Config{
		MaxIterations: 5,
		SystemPrompt:  "test",
	}
	recorder := runrecorder.New(&fakeDBQuerier{})
	orch := orchestrator.New(cfg, mock, nil, recorder, bus, nil)
	h := wshandler.NewHandler(sessions, recorder, orch, bus, authn, "test-instance", nil)
	return h, bus
}

func dialWS(t *testing.T, server *httptest.Server, path, token string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + path
	headers := http.Header{}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	return dialer.Dial(wsURL, headers)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// 1. Unauthenticated connection returns 401.
func TestUnauthenticated(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "valid-token", info: &auth.TokenInfo{TokenID: 1}}
	h, _ := newTestHandler(t, nil, authn, sessions)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "")
	if err == nil {
		t.Fatal("expected dial to fail for unauthenticated request")
	}
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// 2. Valid bearer token + valid app/entry_point — connection upgrades.
func TestAuthenticatedUpgrade(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "valid-token", info: &auth.TokenInfo{TokenID: 42}}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "hello"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h, _ := newTestHandler(t, mockEvents, authn, sessions)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, resp, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "valid-token")
	require.NoError(t, err, "expected upgrade to succeed with valid token")
	defer conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
}

// 3. Message sent → run created → done event received.
func TestMessageAndDone(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "world"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h, _ := newTestHandler(t, mockEvents, authn, sessions)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/app/ep", "tok")
	require.NoError(t, err)
	defer conn.Close()

	// Send user message.
	msg, _ := json.Marshal(map[string]string{"type": "message", "content": "hi"})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msg))

	// Collect server messages until "done" or timeout.
	done := false
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for !done {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var sm map[string]any
		require.NoError(t, json.Unmarshal(data, &sm))
		if sm["type"] == "done" {
			done = true
		}
	}
	assert.True(t, done, "expected done event from orchestrator")
}

// 4. Client disconnect → session ended.
func TestDisconnectEndsSession(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	mockEvents := []llm.StreamEvent{
		{Type: "stop", StopReason: "end_turn"},
	}
	h, _ := newTestHandler(t, mockEvents, authn, sessions)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/app/ep2", "tok")
	require.NoError(t, err)

	// Send a message then immediately close.
	msg, _ := json.Marshal(map[string]string{"type": "message", "content": "bye"})
	_ = conn.WriteMessage(websocket.TextMessage, msg)
	conn.Close()

	// Give the server a moment to process the disconnect.
	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, 1, len(sessions.registered), "session should have been registered")
	assert.Equal(t, 1, len(sessions.ended), "session should have been ended on disconnect")
}

// Ensure domain import is used.
var _ = domain.Message{}
