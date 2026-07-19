//go:build integration

// Package integration_test exercises the full server stack with mocked
// dependencies. Run with:
//
//	go test -tags=integration ./...
//
// These tests are skipped in the normal test suite (go test ./...) to avoid
// requiring a live database or Redis.
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/health"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/server"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/ws"
)

// ── Minimal fakes ─────────────────────────────────────────────────────────────

type mockPinger struct{ err error }

func (m *mockPinger) Ping(_ context.Context) error { return m.err }
func (m *mockPinger) Close()                       {}

type mockAuth struct{}

func (m *mockAuth) Validate(_ context.Context, token string) (*auth.TokenInfo, error) {
	if token == "" {
		return nil, fmt.Errorf("empty")
	}
	return &auth.TokenInfo{TokenID: 1}, nil
}

type mockSessionStore struct{}

func (s *mockSessionStore) Register(_ context.Context, _ session.SessionInfo) error { return nil }
func (s *mockSessionStore) End(_ context.Context, _, _, _ string) error             { return nil }

type mockDB struct{}

func (d *mockDB) Exec(_ context.Context, _ string, _ ...any) error { return nil }

// ── Test setup ────────────────────────────────────────────────────────────────

// buildTestServer creates a full httptest.Server wired with all Phase 8
// handlers and mocked dependencies.
func buildTestServer(t *testing.T, mockEvents []llm.StreamEvent) *httptest.Server {
	t.Helper()

	bus := event.NewBus()
	dbPinger := &mockPinger{}
	cachePinger := &mockPinger{}

	healthHandler := health.New("test-instance", dbPinger, cachePinger)
	authMW := server.AuthMiddlewares{}

	// Build server with bus.
	srv := server.NewWithBus(":0", healthHandler, authMW, bus, nil, dbPinger, cachePinger)

	// Wire WS handler.
	recorder := runrecorder.New(&mockDB{})
	provider := llm.NewMockProvider(mockEvents)
	cfg := orchestrator.Config{MaxIterations: 5}
	orch := orchestrator.New(cfg, provider, nil, recorder, bus, nil)
	sessionStore := &mockSessionStore{}

	wsHandler := ws.NewHandler(sessionStore, recorder, orch, bus, &mockAuth{}, "integration-test", nil)
	srv.MountWS(wsHandler.Routes())

	// Wrap the server's handler in an httptest.Server.
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// ── Integration tests ─────────────────────────────────────────────────────────

// 1. GET /health/live → 200.
func TestIntegration_HealthLive(t *testing.T) {
	srv := buildTestServer(t, nil)

	resp, err := http.Get(srv.URL + "/health/live")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// 2. GET /health/ready with mock DB+Redis pingers → 200.
func TestIntegration_HealthReady(t *testing.T) {
	srv := buildTestServer(t, nil)

	resp, err := http.Get(srv.URL + "/health/ready")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "ok", body["status"])
}

// 3. WebSocket connection upgrades.
func TestIntegration_WSUpgrade(t *testing.T) {
	srv := buildTestServer(t, []llm.StreamEvent{
		{Type: "stop", StopReason: "end_turn"},
	})

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/orchestrate/myapp/myep"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer test-token")

	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	conn, resp, err := dialer.Dial(wsURL, headers)
	require.NoError(t, err, "WebSocket upgrade should succeed")
	defer conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
}

// 4. Send message → receive done event.
func TestIntegration_WSSendMessageGetDone(t *testing.T) {
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "integration test response"},
		{Type: "stop", StopReason: "end_turn"},
	}
	srv := buildTestServer(t, mockEvents)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/orchestrate/myapp/myep"
	headers := http.Header{}
	headers.Set("Authorization", "Bearer test-token")

	dialer := websocket.Dialer{HandshakeTimeout: 3 * time.Second}
	conn, _, err := dialer.Dial(wsURL, headers)
	require.NoError(t, err)
	defer conn.Close()

	// Send message.
	msg, _ := json.Marshal(map[string]string{"type": "message", "content": "hello"})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msg))

	// Collect events until done or timeout.
	done := false
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for !done {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var ev map[string]any
		if err := json.Unmarshal(data, &ev); err == nil && ev["type"] == "done" {
			done = true
		}
	}
	assert.True(t, done, "expected to receive done event")
}
