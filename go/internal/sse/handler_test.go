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
	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/epconfig"
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

type fakeSessionStore struct {
	lastSession session.SessionInfo // captures the most recently registered SessionInfo
}

func (s *fakeSessionStore) Register(_ context.Context, info session.SessionInfo) error {
	s.lastSession = info
	return nil
}
func (s *fakeSessionStore) End(_ context.Context, _, _, _ string) error { return nil }

type fakeDBQuerier struct{}

func (f *fakeDBQuerier) Exec(_ context.Context, _ string, _ ...any) error { return nil }

// ── Helper ────────────────────────────────────────────────────────────────────

func newTestSSEHandler(mockEvents []llm.StreamEvent, authn ssehandler.Authenticator) *ssehandler.Handler {
	return newTestSSEHandlerWithStore(mockEvents, authn, &fakeSessionStore{})
}

func newTestSSEHandlerWithStore(mockEvents []llm.StreamEvent, authn ssehandler.Authenticator, store ssehandler.SessionStore) *ssehandler.Handler {
	bus := event.New()
	mock := llm.NewMockProvider(mockEvents)
	cfg := orchestrator.Config{MaxIterations: 5}
	recorder := runrecorder.New(&fakeDBQuerier{})
	orch := orchestrator.New(cfg, mock, nil, recorder, bus, nil)
	return ssehandler.NewHandler(store, recorder, orch, bus, authn, "test-instance", nil)
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
	lastConfig    gate.Config // records the Config passed to the most recent Check call
}

func (g *fakeSSEGate) Check(_ context.Context, cfg gate.Config) (gate.Result, error) {
	g.checkCalls++
	g.lastConfig = cfg
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

// ── EP config fake ─────────────────────────────────────────────────────────────

type fakeSSEEPLoader struct {
	cfg *epconfig.EPConfig
	err error
}

func (f *fakeSSEEPLoader) Load(_ context.Context, _ string) (*epconfig.EPConfig, error) {
	return f.cfg, f.err
}

// 7. Unauthenticated request to a public EP succeeds (receives SSE stream).
func TestSSEPublicEPNoTokenAllowed(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "hi"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h := newTestSSEHandler(mockEvents, authn)
	h.WithEPConfig(&fakeSSEEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	// No token supplied.
	resp, err := http.Get(srv.URL + "/orchestrate/app/public-ep?message=hello")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

// 8. Unauthenticated request to a token-mode EP returns 401.
func TestSSETokenEPNoTokenRejected(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	h := newTestSSEHandler(nil, authn)
	h.WithEPConfig(&fakeSSEEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModeToken,
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/orchestrate/app/token-ep?message=hello")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// 9. Anonymous session to public EP: gate receives TokenHash="" (no shared rate-limit bucket).
func TestSSEAnonymousSessionGateTokenHashEmpty(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeSSEGate{}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "hi"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h := newTestSSEHandler(mockEvents, authn)
	h.WithEPConfig(&fakeSSEEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
	}})
	h.WithGate(g)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(mustGet(srv.URL + "/orchestrate/app/public-ep?message=hi"))
	require.NoError(t, err)
	_ = collectSSE(t, resp, 2*time.Second)
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	// Gate must receive TokenHash="" so rlKey() returns "" and per-token rate
	// limiting is skipped — anonymous sessions must not share a single bucket.
	assert.Equal(t, "", g.lastConfig.TokenHash,
		"anonymous session must pass TokenHash='' to gate, not sha256('')")
}

// 10. Anonymous session to public EP: session is registered with UserID=0 (anonymous sentinel).
func TestSSEAnonymousSessionUserIDIsZero(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	store := &fakeSessionStore{}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "hi"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h := newTestSSEHandlerWithStore(mockEvents, authn, store)
	h.WithEPConfig(&fakeSSEEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(mustGet(srv.URL + "/orchestrate/app/public-ep?message=hi"))
	require.NoError(t, err)
	_ = collectSSE(t, resp, 2*time.Second)
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int64(0), store.lastSession.UserID,
		"anonymous session must store UserID=0, not a real user identity")
}

// 11. Authenticated request to a public EP also succeeds.
func TestSSEAuthenticatedRequestToPublicEP(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 42}}
	mockEvents := []llm.StreamEvent{
		{Type: "text_delta", Delta: "hi"},
		{Type: "stop", StopReason: "end_turn"},
	}
	h := newTestSSEHandler(mockEvents, authn)
	h.WithEPConfig(&fakeSSEEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/app/public-ep?message=hi")
	req.Header.Set("Authorization", "Bearer valid")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// 12. Voice EP with valid token returns 501 — must never enter the text orchestration path.
func TestSSEVoiceEPReturns501(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	store := &fakeSessionStore{}
	g := &fakeSSEGate{}
	h := newTestSSEHandlerWithStore(nil, authn, store)
	h.WithEPConfig(&fakeSSEEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModeToken,
		EPType:     "voice",
	}})
	h.WithGate(g)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/app/voice-ep?message=hi")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode, "voice EP must return 501")
	assert.Equal(t, 0, g.checkCalls, "gate must not be called for voice EP")
	assert.Equal(t, int64(0), store.lastSession.UserID,
		"session must not be registered for voice EP")
}

// 13. Voice EP with public access mode also returns 501.
func TestSSEVoiceEPPublicReturns501(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	store := &fakeSessionStore{}
	h := newTestSSEHandlerWithStore(nil, authn, store)
	h.WithEPConfig(&fakeSSEEPLoader{cfg: &epconfig.EPConfig{
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModePublic,
		EPType:     "voice",
	}})

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/orchestrate/app/voice-public?message=hi")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotImplemented, resp.StatusCode, "public voice EP must return 501")
	assert.Equal(t, int64(0), store.lastSession.UserID,
		"session must not be registered for public voice EP")
}

// mustGet returns a new GET *http.Request, panicking on error (test helper).
func mustGet(url string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		panic(err)
	}
	return req
}

// ── SSE Temporal path fakes ────────────────────────────────────────────────────

// fakeSSEWorkflowRun blocks Get until the context is cancelled, simulating a
// long-running workflow that ends when the handler's context is cancelled.
// This ensures all run-stream events are delivered before orchDone fires.
type fakeSSEWorkflowRun struct {
	id string
}

func (f *fakeSSEWorkflowRun) GetID() string    { return f.id }
func (f *fakeSSEWorkflowRun) GetRunID() string { return f.id }
func (f *fakeSSEWorkflowRun) Get(ctx context.Context, _ interface{}) error {
	<-ctx.Done()
	return nil
}
func (f *fakeSSEWorkflowRun) GetWithOptions(ctx context.Context, _ interface{}, _ temporalclient.WorkflowRunGetOptions) error {
	<-ctx.Done()
	return nil
}

// fakeSSETemporalClient records ExecuteWorkflow calls.
type fakeSSETemporalClient struct {
	called bool
}

func (f *fakeSSETemporalClient) ExecuteWorkflow(_ context.Context, opts temporalclient.StartWorkflowOptions, _ interface{}, _ ...interface{}) (temporalclient.WorkflowRun, error) {
	f.called = true
	return &fakeSSEWorkflowRun{id: opts.ID}, nil
}

// fakeSSERunStreamSub returns a channel pre-loaded with messages then closes.
// The handler subscribes once (to them:dash:run:{runID}:tokens); the channel
// key is not inspected here because Go passes runID to Python, so Python
// publishes to the same channel Go subscribed to.
type fakeSSERunStreamSub struct {
	messages []string
}

func (f *fakeSSERunStreamSub) Subscribe(_ context.Context, _ string) (<-chan string, error) {
	ch := make(chan string, len(f.messages)+1)
	for _, m := range f.messages {
		ch <- m
	}
	close(ch)
	return ch, nil
}

// 14. Temporal path: when temporalEnabled=true, ExecuteWorkflow is called and
// orch.Run is NOT called. The client receives events directly from the Redis
// run stream. Go passes runID to Python so both sides use the same channel key.
func TestSSETemporalPathUsedWhenEnabled(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 42}}

	// Build handler with no mock events — if orch.Run were called,
	// the SSE stream would close without a "done" event.
	h := newTestSSEHandler(nil, authn)

	tc := &fakeSSETemporalClient{}
	rsSub := &fakeSSERunStreamSub{
		messages: []string{
			`{"type":"token","content":"from temporal"}`,
			`{"type":"done","run_id":"test-run-id"}`,
		},
	}
	h.WithTemporal(tc, rsSub, true)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep1?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := collectSSE(t, resp, 3*time.Second)

	assert.True(t, tc.called, "ExecuteWorkflow must be called when temporalEnabled=true")

	var types []string
	for _, ev := range events {
		if t2, ok := ev["type"].(string); ok {
			types = append(types, t2)
		}
	}
	assert.Contains(t, types, "done", "client must receive done event from run stream")
}
