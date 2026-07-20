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
	registered  []string
	ended       []string
	lastSession session.SessionInfo // captures the most recently registered SessionInfo
}

func (s *fakeSessionStore) Register(_ context.Context, info session.SessionInfo) error {
	s.registered = append(s.registered, info.SessionID)
	s.lastSession = info
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
	assert.Equal(t, 0, len(sessions.registered), "session must not be registered when gate rejects")
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

	assert.Equal(t, 1, g.checkCalls, "Gate.Check must be called once")
	assert.Equal(t, 1, g.confirmCalls, "Gate.Confirm must be called after Register")
	assert.GreaterOrEqual(t, g.releaseCalls, 1, "Gate.Release must be called on session end")
	assert.Equal(t, 0, g.rollbackCalls, "Gate.Rollback must not be called on success")
	assert.Equal(t, 1, len(sessions.registered))
	assert.Equal(t, 1, len(sessions.ended))
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
	assert.Equal(t, 1, g.checkCalls)
	assert.Equal(t, 1, g.rollbackCalls, "Gate.Rollback must be called when Register fails")
	assert.Equal(t, 0, g.confirmCalls, "Gate.Confirm must NOT be called when Register fails")
}

// ── Gate fake ──────────────────────────────────────────────────────────────────

type fakeGate struct {
	checkErr      error
	checkCalls    int
	confirmCalls  int
	rollbackCalls int
	releaseCalls  int
	lastConfig    gate.Config // records the Config passed to the most recent Check call
}

func (g *fakeGate) Check(_ context.Context, cfg gate.Config) (gate.Result, error) {
	g.checkCalls++
	g.lastConfig = cfg
	return gate.Result{Status: gate.StatusAdmitted}, g.checkErr
}

func (g *fakeGate) Confirm(_ context.Context, _ gate.Config) error {
	g.confirmCalls++
	return nil
}

func (g *fakeGate) Rollback(_ context.Context, _ gate.Config) error {
	g.rollbackCalls++
	return nil
}

func (g *fakeGate) Release(_ context.Context, _ gate.Config) error {
	g.releaseCalls++
	return nil
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
	assert.Equal(t, "", g.lastConfig.TokenHash,
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

	require.Equal(t, 1, len(sessions.registered), "session must be registered")
	assert.Equal(t, int64(0), sessions.lastSession.UserID,
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
	assert.Equal(t, 0, g.checkCalls, "gate must not be called for voice EP")
	assert.Equal(t, 0, len(sessions.registered), "session must not be registered for voice EP")
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
	assert.Equal(t, 0, len(sessions.registered), "session must not be registered for voice EP")
}

// Ensure domain import is used.
var _ = domain.Message{}
