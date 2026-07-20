package sse_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/llm"
	"github.com/aviciot/them/internal/orchestrator"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/session"
	ssehandler "github.com/aviciot/them/internal/sse"
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

type fakeSessionStore struct{}

func (s *fakeSessionStore) Register(_ context.Context, _ session.SessionInfo) error { return nil }
func (s *fakeSessionStore) End(_ context.Context, _, _, _ string) error             { return nil }

type fakeDBQuerier struct{}

func (f *fakeDBQuerier) Exec(_ context.Context, _ string, _ ...any) error { return nil }

// ── Helper ────────────────────────────────────────────────────────────────────

func newTestSSEHandler(mockEvents []llm.StreamEvent, authn ssehandler.Authenticator) *ssehandler.Handler {
	bus := event.New()
	mock := llm.NewMockProvider(mockEvents)
	cfg := orchestrator.Config{MaxIterations: 5}
	recorder := runrecorder.New(&fakeDBQuerier{})
	orch := orchestrator.New(cfg, mock, nil, recorder, bus, nil)
	return ssehandler.NewHandler(&fakeSessionStore{}, recorder, orch, bus, authn, "test-instance", nil)
}

// collectSSE reads SSE events from the response body until the stream closes or
// deadline exceeds. Returns the parsed JSON event maps.
func collectSSE(t *testing.T, resp *http.Response, deadline time.Duration) []map[string]any {
	t.Helper()
	var events []map[string]any
	done := make(chan struct{})
	go func() {
		defer close(done)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")
				var m map[string]any
				if err := json.Unmarshal([]byte(data), &m); err == nil {
					events = append(events, m)
				}
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(deadline):
	}
	return events
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// 1. Unauthenticated request → 401.
func TestSSEUnauthenticated(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	h := newTestSSEHandler(nil, authn)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/orchestrate/app/ep?message=hello")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// 2. Valid auth + message → receives token events as SSE.
func TestSSETokenEvents(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "hello world"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h := newTestSSEHandler(mockEvents, authn)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/orchestrate/app/ep?message=hi", nil)
	req.Header.Set("Authorization", "Bearer tok")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	events := collectSSE(t, resp, 3*time.Second)

	hasToken := false
	for _, ev := range events {
		if ev["type"] == "token" {
			hasToken = true
			break
		}
	}
	assert.True(t, hasToken, "expected at least one token event")
}

// 3. Done event closes the stream.
func TestSSEDoneClosesStream(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "final"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h := newTestSSEHandler(mockEvents, authn)
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/orchestrate/app/ep?message=go", nil)
	req.Header.Set("Authorization", "Bearer tok")

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	events := collectSSE(t, resp, 3*time.Second)

	hasDone := false
	for _, ev := range events {
		if ev["type"] == "done" {
			hasDone = true
			_, hasRunID := ev["run_id"]
			assert.True(t, hasRunID, "done event should contain run_id")
			break
		}
	}
	assert.True(t, hasDone, "expected done event to close stream")
}

// 4. Gate cap exceeded returns 503 before SSE stream is opened.
func TestSSEGateCapExceeded(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	h := newTestSSEHandler(nil, authn)
	h.WithGate(&fakeSSEGate{checkErr: gate.ErrCapExceeded})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/orchestrate/app/ep?message=hi", nil)
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// 5. Gate admitted → Confirm called; Release called on stream end.
func TestSSEGateAdmittedAndReleased(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeSSEGate{}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "hi"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h := newTestSSEHandler(mockEvents, authn)
	h.WithGate(g)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/orchestrate/app/ep?message=hi", nil)
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)

	_ = collectSSE(t, resp, 3*time.Second)
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, 1, g.checkCalls)
	assert.Equal(t, 1, g.confirmCalls)
	assert.GreaterOrEqual(t, g.releaseCalls, 1)
	assert.Equal(t, 0, g.rollbackCalls)
}

// 6. Gate rollback called when session.Register fails.
func TestSSEGateRollbackOnRegisterFailure(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeSSEGate{}
	bus := event.New()
	mock := llm.NewMockProvider(nil)
	cfg := orchestrator.Config{MaxIterations: 5}
	recorder := runrecorder.New(&fakeDBQuerier{})
	orch := orchestrator.New(cfg, mock, nil, recorder, bus, nil)
	h := ssehandler.NewHandler(&failingSSESessionStore{}, recorder, orch, bus, authn, "test", nil)
	h.WithGate(g)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/orchestrate/app/ep?message=hi", nil)
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The handler sends an SSE error event (headers already written).
	events := collectSSE(t, resp, 2*time.Second)
	hasError := false
	for _, ev := range events {
		if ev["type"] == "error" {
			hasError = true
		}
	}
	assert.True(t, hasError, "expected SSE error event on Register failure")
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 1, g.rollbackCalls, "Gate.Rollback must be called when Register fails")
	assert.Equal(t, 0, g.confirmCalls)
}

// ── Gate fake ──────────────────────────────────────────────────────────────────

type fakeSSEGate struct {
	checkErr      error
	checkCalls    int
	confirmCalls  int
	rollbackCalls int
	releaseCalls  int
}

func (g *fakeSSEGate) Check(_ context.Context, _ gate.Config) (gate.Result, error) {
	g.checkCalls++
	return gate.Result{Status: gate.StatusAdmitted}, g.checkErr
}

func (g *fakeSSEGate) Confirm(_ context.Context, _ gate.Config) error {
	g.confirmCalls++
	return nil
}

func (g *fakeSSEGate) Rollback(_ context.Context, _ gate.Config) error {
	g.rollbackCalls++
	return nil
}

func (g *fakeSSEGate) Release(_ context.Context, _ gate.Config) error {
	g.releaseCalls++
	return nil
}

// failingSSESessionStore always returns an error from Register.
type failingSSESessionStore struct{}

func (s *failingSSESessionStore) Register(_ context.Context, _ session.SessionInfo) error {
	return errors.New("redis: connection refused")
}

func (s *failingSSESessionStore) End(_ context.Context, _, _, _ string) error { return nil }
