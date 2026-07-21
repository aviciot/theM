package ws_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/gate"
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
	mu          sync.Mutex
	registered  []string
	ended       []string
	lastSession session.SessionInfo // captures the most recently registered SessionInfo
}

func (s *fakeSessionStore) Register(_ context.Context, info session.SessionInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registered = append(s.registered, info.SessionID)
	s.lastSession = info
	return nil
}

func (s *fakeSessionStore) End(_ context.Context, sessionID, _, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended = append(s.ended, sessionID)
	return nil
}

func (s *fakeSessionStore) getRegistered() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.registered))
	copy(out, s.registered)
	return out
}

func (s *fakeSessionStore) getEnded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.ended))
	copy(out, s.ended)
	return out
}

func (s *fakeSessionStore) getLastSession() session.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSession
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

	assert.Equal(t, 1, len(sessions.getRegistered()), "session should have been registered")
	assert.Equal(t, 1, len(sessions.getEnded()), "session should have been ended on disconnect")
}

// 5. Gate cap exceeded returns 503 before upgrade.
func TestGateCapExceeded(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	h, _ := newTestHandler(t, nil, authn, sessions)
	h.WithGate(&fakeGate{checkErr: gate.ErrCapExceeded})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/app/ep", "tok")
	require.Error(t, err, "expected dial to fail when gate rejects")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, 0, len(sessions.getRegistered()), "session must not be registered when gate rejects")
}

// 6. Gate admitted → session registered, confirmed; on disconnect gate released.
func TestGateAdmittedAndReleased(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	mockEvents := []llm.StreamEvent{
		{Type: "stop", StopReason: "end_turn"},
	}
	h, _ := newTestHandler(t, mockEvents, authn, sessions)
	h.WithGate(g)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/app/ep", "tok")
	require.NoError(t, err)

	msg, _ := json.Marshal(map[string]string{"type": "message", "content": "hi"})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msg))

	// Wait for done.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var sm map[string]any
		if json.Unmarshal(data, &sm) == nil && sm["type"] == "done" {
			break
		}
	}
	conn.Close()
	time.Sleep(300 * time.Millisecond)

	check, confirm, rollback, release, _ := g.getCounts()
	assert.Equal(t, 1, check, "Gate.Check must be called once")
	assert.Equal(t, 1, confirm, "Gate.Confirm must be called after Register")
	assert.GreaterOrEqual(t, release, 1, "Gate.Release must be called on session end")
	assert.Equal(t, 0, rollback, "Gate.Rollback must not be called on success")
	assert.Equal(t, 1, len(sessions.getRegistered()))
	assert.Equal(t, 1, len(sessions.getEnded()))
}

// 7. Gate rollback called when session.Register fails.
func TestGateRollbackOnRegisterFailure(t *testing.T) {
	sessions := &failingSessionStore{}
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	h, _ := newTestHandler(t, nil, authn, sessions)
	h.WithGate(g)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/app/ep", "tok")
	require.NoError(t, err, "WS upgrades before register failure is detected")
	defer conn.Close()

	// Handler writes an error event and returns.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, _ := conn.ReadMessage()
	var sm map[string]any
	_ = json.Unmarshal(data, &sm)
	assert.Equal(t, "error", sm["type"])

	time.Sleep(200 * time.Millisecond)
	check2, confirm2, rollback2, _, _ := g.getCounts()
	assert.Equal(t, 1, check2)
	assert.Equal(t, 1, rollback2, "Gate.Rollback must be called when Register fails")
	assert.Equal(t, 0, confirm2, "Gate.Confirm must NOT be called when Register fails")
}

// ── Gate fake ──────────────────────────────────────────────────────────────────

type fakeGate struct {
	mu            sync.Mutex
	checkErr      error
	checkCalls    int
	confirmCalls  int
	rollbackCalls int
	releaseCalls  int
	lastConfig    gate.Config // records the Config passed to the most recent Check call
}

func (g *fakeGate) Check(_ context.Context, cfg gate.Config) (gate.Result, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.checkCalls++
	g.lastConfig = cfg
	return gate.Result{Status: gate.StatusAdmitted}, g.checkErr
}

func (g *fakeGate) Confirm(_ context.Context, _ gate.Config) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.confirmCalls++
	return nil
}

func (g *fakeGate) Rollback(_ context.Context, _ gate.Config) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rollbackCalls++
	return nil
}

func (g *fakeGate) Release(_ context.Context, _ gate.Config) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.releaseCalls++
	return nil
}

func (g *fakeGate) getCounts() (check, confirm, rollback, release int, cfg gate.Config) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkCalls, g.confirmCalls, g.rollbackCalls, g.releaseCalls, g.lastConfig
}

// failingSessionStore always returns an error from Register.
type failingSessionStore struct{}

func (s *failingSessionStore) Register(_ context.Context, _ session.SessionInfo) error {
	return errors.New("redis: connection refused")
}

func (s *failingSessionStore) End(_ context.Context, _, _, _ string) error { return nil }

// ── EP config fake ─────────────────────────────────────────────────────────────

type fakeEPLoader struct {
	cfg *epconfig.EPConfig
	err error
}

func (f *fakeEPLoader) Load(_ context.Context, _ string) (*epconfig.EPConfig, error) {
	return f.cfg, f.err
}

// 8. Unauthenticated request to a public EP succeeds (upgrades to WS).
func TestPublicEPNoTokenAllowed(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "valid-token", info: &auth.TokenInfo{TokenID: 1}}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "hi"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h, _ := newTestHandler(t, mockEvents, authn, sessions)
	h.WithEPConfig(&fakeEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	// No token supplied.
	conn, resp, err := dialWS(t, srv, "/orchestrate/myapp/public-ep", "")
	require.NoError(t, err, "unauthenticated request to public EP should upgrade")
	defer conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
}

// 9. Unauthenticated request to a token-mode EP returns 401.
func TestTokenEPNoTokenRejected(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "valid-token", info: &auth.TokenInfo{TokenID: 1}}
	h, _ := newTestHandler(t, nil, authn, sessions)
	h.WithEPConfig(&fakeEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModeToken,
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/myapp/token-ep", "")
	require.Error(t, err, "unauthenticated request to token-mode EP should be rejected")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// 10. Anonymous session to public EP: gate receives TokenHash="" (no shared rate-limit bucket).
func TestAnonymousSessionGateTokenHashEmpty(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	mockEvents := []llm.StreamEvent{
		{Type: "stop", StopReason: "end_turn"},
	}
	h, _ := newTestHandler(t, mockEvents, authn, sessions)
	h.WithEPConfig(&fakeEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
	}})
	h.WithGate(g)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/public-ep", "")
	require.NoError(t, err)
	conn.Close()
	time.Sleep(200 * time.Millisecond)

	// Gate must receive TokenHash="" so that rlKey() returns "" and
	// per-token rate limiting is skipped for public/anonymous sessions.
	_, _, _, _, lastCfg := g.getCounts()
	assert.Equal(t, "", lastCfg.TokenHash,
		"anonymous session must pass TokenHash='' to gate, not sha256('')")
}

// 11. Anonymous session to public EP: session is registered with UserID=0 (anonymous sentinel).
func TestAnonymousSessionUserIDIsZero(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	mockEvents := []llm.StreamEvent{
		{Type: "stop", StopReason: "end_turn"},
	}
	h, _ := newTestHandler(t, mockEvents, authn, sessions)
	h.WithEPConfig(&fakeEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/public-ep", "")
	require.NoError(t, err)
	conn.Close()
	time.Sleep(200 * time.Millisecond)

	require.Equal(t, 1, len(sessions.getRegistered()), "session must be registered")
	assert.Equal(t, int64(0), sessions.getLastSession().UserID,
		"anonymous session must store UserID=0, not a real user identity")
}

// 12. Authenticated request to a public EP also succeeds (public EPs accept both).
func TestAuthenticatedRequestToPublicEP(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 42}}
	mockEvents := []llm.StreamEvent{
		{Type: "stop", StopReason: "end_turn"},
	}
	h, _ := newTestHandler(t, mockEvents, authn, sessions)
	h.WithEPConfig(&fakeEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, resp, err := dialWS(t, srv, "/orchestrate/myapp/public-ep", "valid")
	require.NoError(t, err, "authenticated request to public EP must succeed")
	defer conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
}

// 13. Voice EP with valid token returns 501 — must never enter the text orchestration path.
func TestVoiceEPReturns501(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	h, _ := newTestHandler(t, nil, authn, sessions)
	h.WithEPConfig(&fakeEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModeToken,
		EPType:     "voice",
	}})
	h.WithGate(g)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/myapp/voice-ep", "tok")
	require.Error(t, err, "voice EP must reject the WS upgrade")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode, "voice EP must return 501")
	check3, _, _, _, _ := g.getCounts()
	assert.Equal(t, 0, check3, "gate must not be called for voice EP")
	assert.Equal(t, 0, len(sessions.getRegistered()), "session must not be registered for voice EP")
}

// 14. Voice EP with public access mode also returns 501.
func TestVoiceEPPublicReturns501(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	h, _ := newTestHandler(t, nil, authn, sessions)
	h.WithEPConfig(&fakeEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
		EPType:     "voice",
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/myapp/voice-public", "")
	require.Error(t, err, "public voice EP must also reject the WS upgrade")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode, "public voice EP must return 501")
	assert.Equal(t, 0, len(sessions.getRegistered()), "session must not be registered for voice EP")
}

// ── Temporal path fakes ────────────────────────────────────────────────────────

// fakeWorkflowRun blocks Get until the context is cancelled, simulating a
// long-running workflow that ends when the handler's context is cancelled.
// This ensures all stream events are delivered before orchDone fires.
type fakeWorkflowRun struct {
	id string
}

func (f *fakeWorkflowRun) GetID() string    { return f.id }
func (f *fakeWorkflowRun) GetRunID() string { return f.id }
func (f *fakeWorkflowRun) Get(ctx context.Context, _ interface{}) error {
	<-ctx.Done()
	return nil
}
func (f *fakeWorkflowRun) GetWithOptions(ctx context.Context, _ interface{}, _ temporalclient.WorkflowRunGetOptions) error {
	<-ctx.Done()
	return nil
}

// fakeTemporalClient records ExecuteWorkflow calls.
type fakeTemporalClient struct {
	called bool
	runID  string
}

func (f *fakeTemporalClient) ExecuteWorkflow(_ context.Context, opts temporalclient.StartWorkflowOptions, _ interface{}, _ ...interface{}) (temporalclient.WorkflowRun, error) {
	f.called = true
	f.runID = opts.ID
	return &fakeWorkflowRun{id: opts.ID}, nil
}

// fakeRunStreamSub returns a channel pre-loaded with messages then closes.
// The handler subscribes once (to them:dash:run:{runID}:tokens); the channel
// key is not inspected here because Go passes runID to Python, so Python
// publishes to the same channel Go subscribed to.
type fakeRunStreamSub struct {
	messages []string
}

func (f *fakeRunStreamSub) Subscribe(_ context.Context, _ string) (<-chan string, error) {
	ch := make(chan string, len(f.messages)+1)
	for _, m := range f.messages {
		ch <- m
	}
	close(ch)
	return ch, nil
}

// 15. Temporal path: when temporalEnabled=true, ExecuteWorkflow is called and
// orch.Run is NOT called. The client receives events directly from the Redis
// run stream. Go passes runID to Python so both sides use the same channel key.
func TestTemporalPathUsedWhenEnabled(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 42}}

	// Build handler with a mock orchestrator that has no mock events.
	// If orch.Run were called, it would complete without emitting a "done" event
	// on the bus — the test would time out rather than succeed.
	h, _ := newTestHandler(t, nil, authn, sessions)

	tc := &fakeTemporalClient{}
	rsSub := &fakeRunStreamSub{
		messages: []string{
			`{"type":"token","content":"hello from temporal"}`,
			`{"type":"done","run_id":"test-run-id"}`,
		},
	}
	h.WithTemporal(tc, rsSub, true)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "tok")
	require.NoError(t, err)
	defer conn.Close()

	// Send user message.
	msg, _ := json.Marshal(map[string]string{"type": "message", "content": "hi"})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msg))

	// Collect events until "done" or timeout.
	var receivedTypes []string
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		var sm map[string]any
		if json.Unmarshal(data, &sm) == nil {
			if t, ok := sm["type"].(string); ok {
				receivedTypes = append(receivedTypes, t)
				if t == "done" {
					break
				}
			}
		}
	}

	assert.True(t, tc.called, "ExecuteWorkflow must be called when temporalEnabled=true")
	assert.Contains(t, receivedTypes, "done", "client must receive done event from run stream")
}

// 16. replay_unavailable event from Redis Streams is forwarded to the WS client
// (Phase 11c-B). The event is emitted when last_event_id was trimmed by MAXLEN;
// it must not be silently dropped by the handler's writeEvent switch.
func TestReplayUnavailableForwardedToClient(t *testing.T) {
	sessions := &fakeSessionStore{}
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 42}}
	h, _ := newTestHandler(t, nil, authn, sessions)

	tc := &fakeTemporalClient{}
	rsSub := &fakeRunStreamSub{
		messages: []string{
			`{"type":"replay_unavailable","reason":"history_trimmed","run_id":"r1"}`,
			`{"type":"done","run_id":"r1"}`,
		},
	}
	h.WithTemporal(tc, rsSub, true)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "tok")
	require.NoError(t, err)
	defer conn.Close()

	msg, _ := json.Marshal(map[string]string{"type": "message", "content": "hi"})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msg))

	var receivedTypes []string
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, data, readErr := conn.ReadMessage()
		if readErr != nil {
			break
		}
		var sm map[string]any
		if json.Unmarshal(data, &sm) == nil {
			if evType, ok := sm["type"].(string); ok {
				receivedTypes = append(receivedTypes, evType)
				if evType == "done" {
					break
				}
			}
		}
	}

	assert.Contains(t, receivedTypes, "replay_unavailable", "replay_unavailable must be forwarded to WS client")
	assert.Contains(t, receivedTypes, "done", "done must arrive after replay_unavailable")
}

// Ensure domain import is used.
var _ = domain.Message{}
