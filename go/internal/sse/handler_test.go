package sse_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/runstream"
	"github.com/aviciot/them/internal/session"
	ssehandler "github.com/aviciot/them/internal/sse"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/transport"
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
	lastSession session.SessionInfo
	failRegister bool
}

func (s *fakeSessionStore) Register(_ context.Context, info session.SessionInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failRegister {
		return errors.New("redis: connection refused")
	}
	s.lastSession = info
	return nil
}
func (s *fakeSessionStore) End(_ context.Context, _, _, _ string) error { return nil }

func (s *fakeSessionStore) getLastSession() session.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastSession
}

type fakeGate struct {
	mu            sync.Mutex
	checkErr      error
	checkCalls    int
	confirmCalls  int
	rollbackCalls int
	releaseCalls  int
	lastConfig    gate.Config
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

type fakeDBQuerier struct{}

func (f *fakeDBQuerier) Exec(_ context.Context, _ string, _ ...any) error { return nil }
func (f *fakeDBQuerier) QueryRow(_ context.Context, _ string, _ ...any) runrecorder.SingleRowScanner {
	return &fakeRow{}
}

type fakeRow struct{}

func (f *fakeRow) Scan(dest ...any) error {
	if len(dest) > 0 {
		if sp, ok := dest[0].(*string); ok {
			*sp = "00000000-0000-0000-0000-000000000001"
		}
	}
	return nil
}

type fakeEPLoader struct {
	cfg *epconfig.EPConfig
	err error
}

func (f *fakeEPLoader) Load(_ context.Context, _, _, _ string) (*epconfig.EPConfig, error) {
	return f.cfg, f.err
}

type fakeRunCreator struct{}

func (f *fakeRunCreator) CreateRun(_ context.Context, _ domain.Run) error { return nil }
func (f *fakeRunCreator) UpdateRunGoal(_ context.Context, _, _ string) error { return nil }
func (f *fakeRunCreator) UpdateRunStatus(_ context.Context, _ string, _ domain.RunStatus, _ string) error {
	return nil
}

// captureRunCreator records CreateRun and UpdateRunStatus calls.
type captureRunCreator struct {
	mu      sync.Mutex
	runs    []domain.Run
	updates []domain.RunStatus
}

func (c *captureRunCreator) CreateRun(_ context.Context, run domain.Run) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.runs = append(c.runs, run)
	return nil
}

func (c *captureRunCreator) UpdateRunGoal(_ context.Context, _, _ string) error { return nil }
func (c *captureRunCreator) UpdateRunStatus(_ context.Context, _ string, status domain.RunStatus, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, status)
	return nil
}

func (c *captureRunCreator) last() (domain.Run, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.runs) == 0 {
		return domain.Run{}, false
	}
	return c.runs[len(c.runs)-1], true
}

// ── Temporal fakes ────────────────────────────────────────────────────────────

type fakeTemporalClient struct {
	mu     sync.Mutex
	called bool
	input  []interface{}
}

func (f *fakeTemporalClient) ExecuteWorkflow(_ context.Context, opts temporalclient.StartWorkflowOptions, _ interface{}, args ...interface{}) (temporalclient.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = true
	f.input = args
	return &fakeWorkflowRun{id: opts.ID}, nil
}

type fakeWorkflowRun struct{ id string }

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

// fakeRunStreamer implements runstream.RedisStreamer by replaying a fixed list
// of pre-loaded JSON messages as stream entries via XRange, then blocking on
// XRead until the context is cancelled. StreamFromRedis replays every entry and
// closes the output channel once it hits a terminal event ("done"/"error"), so
// the message list should end with one.
type fakeRunStreamer struct {
	messages []string
}

func (f *fakeRunStreamer) entries() []runstream.StreamEntry {
	out := make([]runstream.StreamEntry, 0, len(f.messages))
	for i, m := range f.messages {
		out = append(out, runstream.StreamEntry{
			ID:     fmt.Sprintf("%d-0", i+1),
			Values: map[string]interface{}{"data": m},
		})
	}
	return out
}

func (f *fakeRunStreamer) XRange(_ context.Context, _, start, _ string) ([]runstream.StreamEntry, error) {
	if start == "-" {
		return f.entries(), nil
	}
	return nil, nil
}

func (f *fakeRunStreamer) XRangeN(_ context.Context, _, _, _ string, _ int64) ([]runstream.StreamEntry, error) {
	return nil, nil
}

func (f *fakeRunStreamer) XRead(ctx context.Context, _ runstream.XReadArgs) ([]runstream.StreamMessage, error) {
	<-ctx.Done()
	return nil, nil
}

// ── Builder ───────────────────────────────────────────────────────────────────

// sseBuilder assembles an SSE Handler with injectable fakes.
type sseBuilder struct {
	authn    transport.Authenticator
	epLoader transport.EPConfigLoader
	gate     transport.GateStore
	sessions transport.SessionStore
	recorder execution.RunCreator
	temporal transport.TemporalClientExecutor
	streamMsgs []string
}

func (b *sseBuilder) defaultEP() *epconfig.EPConfig {
	return &epconfig.EPConfig{
		EPSlug:            "ep",
		EPType:            "sse",
		AccessMode:        epconfig.AccessModeToken,
		EPEnabled:         true,
		AppEnabled:        true,
		TenantID:          "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:             "bbbbbbbb-0000-0000-0000-000000000001",
		AppOrchestratorID: "cccccccc-0000-0000-0000-000000000001",
		OrchestratorName:  "test-orchestrator",
	}
}

func (b *sseBuilder) build() (*ssehandler.Handler, *fakeRunStreamer) {
	ep := b.epLoader
	if ep == nil {
		ep = &fakeEPLoader{cfg: b.defaultEP()}
	}
	sess := b.sessions
	if sess == nil {
		sess = &fakeSessionStore{}
	}
	rec := b.recorder
	if rec == nil {
		rec = &fakeRunCreator{}
	}
	lc := execution.NewLifecycleWithRecorder(b.authn, ep, b.gate, sess, rec, b.temporal, nil)

	msgs := b.streamMsgs
	if len(msgs) == 0 {
		raw, _ := json.Marshal(map[string]any{"type": "done", "run_id": "mock-run"})
		msgs = []string{string(raw)}
	}
	rsStreamer := &fakeRunStreamer{messages: msgs}

	bus := event.New()
	recorder := runrecorder.New(&fakeDBQuerier{})
	h := ssehandler.NewHandler(lc, recorder, bus, b.authn, "test-instance", nil)
	h.WithRunStreamer(rsStreamer)
	return h, rsStreamer
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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

func mustGet(url string) *http.Request {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		panic(err)
	}
	return req
}

func defaultStreamMsgs() []string {
	raw, _ := json.Marshal(map[string]any{"type": "done", "run_id": "mock-run"})
	return []string{string(raw)}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// 1. Unauthenticated request to a token-mode EP → 401.
func TestSSEUnauthenticated(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{authn: authn, temporal: tc}
	h, _ := b.build()
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
	tc := &fakeTemporalClient{}
	raw1, _ := json.Marshal(map[string]any{"type": "token", "content": "hello world"})
	raw2, _ := json.Marshal(map[string]any{"type": "done", "run_id": "r"})
	b := &sseBuilder{authn: authn, temporal: tc, streamMsgs: []string{string(raw1), string(raw2)}}
	h, _ := b.build()
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
	tc := &fakeTemporalClient{}
	raw1, _ := json.Marshal(map[string]any{"type": "token", "content": "final"})
	raw2, _ := json.Marshal(map[string]any{"type": "done", "run_id": "done-run"})
	b := &sseBuilder{authn: authn, temporal: tc, streamMsgs: []string{string(raw1), string(raw2)}}
	h, _ := b.build()
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
	g := &fakeGate{checkErr: gate.ErrCapExceeded}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{authn: authn, gate: g, temporal: tc}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/orchestrate/app/ep?message=hi", nil)
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

// 5. Gate admitted → Confirm called; Release called on stream end.
func TestSSEGateAdmittedAndReleased(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{authn: authn, gate: g, temporal: tc, streamMsgs: defaultStreamMsgs()}
	h, _ := b.build()
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

	check, confirm, _, release, _ := g.getCounts()
	assert.Equal(t, 1, check)
	assert.Equal(t, 1, confirm)
	assert.GreaterOrEqual(t, release, 1)
	assert.Equal(t, 0, g.rollbackCalls)
}

// 6. Gate rollback called when session.Register fails.
func TestSSEGateRollbackOnRegisterFailure(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	sess := &fakeSessionStore{failRegister: true}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{authn: authn, gate: g, sessions: sess, temporal: tc}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/orchestrate/app/ep?message=hi", nil)
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// With SSE headers moved to AFTER Admit, a Register failure now returns a clean HTTP 500.
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	time.Sleep(200 * time.Millisecond)
	_, _, rollback, _, _ := g.getCounts()
	assert.Equal(t, 1, rollback, "Gate.Rollback must be called when Register fails")
}

// 7. Unauthenticated request to a public EP succeeds (receives SSE stream).
func TestSSEPublicEPNoTokenAllowed(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:    authn,
		temporal: tc,
		epLoader: &fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "public-ep",
			EPType:     "sse",
			EPEnabled:  true,
			AppEnabled: true,
			AccessMode: epconfig.AccessModePublic,
			TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
			AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
		}},
		streamMsgs: defaultStreamMsgs(),
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/orchestrate/app/public-ep?message=hello")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
}

// 8. Unauthenticated request to a token-mode EP returns 401.
func TestSSETokenEPNoTokenRejected(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:    authn,
		temporal: tc,
		epLoader: &fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "token-ep",
			EPType:     "sse",
			EPEnabled:  true,
			AppEnabled: true,
			AccessMode: epconfig.AccessModeToken,
			TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
			AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
		}},
	}
	h, _ := b.build()
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
	g := &fakeGate{}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:    authn,
		gate:     g,
		temporal: tc,
		epLoader: &fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "public-ep",
			EPType:     "sse",
			EPEnabled:  true,
			AppEnabled: true,
			AccessMode: epconfig.AccessModePublic,
			TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
			AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
		}},
		streamMsgs: defaultStreamMsgs(),
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(mustGet(srv.URL + "/orchestrate/app/public-ep?message=hi"))
	require.NoError(t, err)
	_ = collectSSE(t, resp, 2*time.Second)
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	_, _, _, _, lastCfg := g.getCounts()
	assert.Equal(t, "", lastCfg.TokenHash,
		"anonymous session must pass TokenHash='' to gate, not sha256('')")
}

// 10. Anonymous session to public EP: session is registered with UserID=0.
func TestSSEAnonymousSessionUserIDIsZero(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	sess := &fakeSessionStore{}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:    authn,
		sessions: sess,
		temporal: tc,
		epLoader: &fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "public-ep",
			EPType:     "sse",
			EPEnabled:  true,
			AppEnabled: true,
			AccessMode: epconfig.AccessModePublic,
			TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
			AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
		}},
		streamMsgs: defaultStreamMsgs(),
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(mustGet(srv.URL + "/orchestrate/app/public-ep?message=hi"))
	require.NoError(t, err)
	_ = collectSSE(t, resp, 2*time.Second)
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int64(0), sess.getLastSession().UserID,
		"anonymous session must store UserID=0, not a real user identity")
}

// 11. Authenticated request to a public EP also succeeds.
func TestSSEAuthenticatedRequestToPublicEP(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 42}}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:    authn,
		temporal: tc,
		epLoader: &fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "public-ep",
			EPType:     "sse",
			EPEnabled:  true,
			AppEnabled: true,
			AccessMode: epconfig.AccessModePublic,
			TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
			AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
		}},
		streamMsgs: defaultStreamMsgs(),
	}
	h, _ := b.build()
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
// Voice EP on SSE path → 400 (voice EPs use POST /voice/chat, not SSE).
// Lifecycle.Admit succeeds but SSE handler rejects after admit.
func TestSSEVoiceEPReturns501(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	sess := &fakeSessionStore{}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:    authn,
		gate:     g,
		sessions: sess,
		temporal: tc,
		epLoader: &fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "voice-ep",
			EPType:     "voice",
			EPEnabled:  true,
			AppEnabled: true,
			AccessMode: epconfig.AccessModeToken,
			TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
			AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
		}},
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/app/voice-ep?message=hi")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "voice EP must return 400 on SSE — use POST /voice/chat")
}

// Public voice EP on SSE path → 400.
func TestSSEVoiceEPPublicReturns501(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	sess := &fakeSessionStore{}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:    authn,
		sessions: sess,
		temporal: tc,
		epLoader: &fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "voice-public",
			EPType:     "voice",
			EPEnabled:  true,
			AppEnabled: true,
			AccessMode: epconfig.AccessModePublic,
			TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
			AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
		}},
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/orchestrate/app/voice-public?message=hi")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "public voice EP must return 400 on SSE — use POST /voice/chat")
}

// 14. Temporal path: ExecuteWorkflow is called; client receives events from run stream.
func TestSSETemporalPathUsedWhenEnabled(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 42}}
	tc := &fakeTemporalClient{}
	raw1, _ := json.Marshal(map[string]any{"type": "token", "content": "from temporal"})
	raw2, _ := json.Marshal(map[string]any{"type": "done", "run_id": "test-run-id"})
	b := &sseBuilder{authn: authn, temporal: tc, streamMsgs: []string{string(raw1), string(raw2)}}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := collectSSE(t, resp, 3*time.Second)
	assert.True(t, tc.called, "ExecuteWorkflow must be called")
	var types []string
	for _, ev := range events {
		if t2, ok := ev["type"].(string); ok {
			types = append(types, t2)
		}
	}
	assert.Contains(t, types, "done", "client must receive done event from run stream")
}

// 15. replay_unavailable event is forwarded as SSE to the client.
func TestSSEReplayUnavailableForwardedToClient(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 42}}
	tc := &fakeTemporalClient{}
	raw1, _ := json.Marshal(map[string]any{"type": "replay_unavailable", "reason": "history_trimmed", "run_id": "r1"})
	raw2, _ := json.Marshal(map[string]any{"type": "done", "run_id": "r1"})
	b := &sseBuilder{authn: authn, temporal: tc, streamMsgs: []string{string(raw1), string(raw2)}}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := collectSSE(t, resp, 3*time.Second)
	var types []string
	for _, ev := range events {
		if evType, ok := ev["type"].(string); ok {
			types = append(types, evType)
		}
	}
	assert.Contains(t, types, "replay_unavailable", "replay_unavailable must be forwarded as SSE")
	assert.Contains(t, types, "done", "done must follow replay_unavailable")
}

// 16. When Temporal is nil (Lifecycle has no temporal), Start fails; SSE error event is emitted.
// Headers are already sent (200 OK); the error is delivered as an SSE data event.
func TestSSENoTemporalReturns503(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	// temporal = nil → Lifecycle.Start returns *StartError → handler writes SSE error event
	b := &sseBuilder{authn: authn, temporal: nil}
	h, _ := b.build()
	// build() already wired the run streamer; events would flow if Start succeeded.
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "SSE headers sent before Start check")
	events := collectSSE(t, resp, 3*time.Second)
	hasError := false
	for _, ev := range events {
		if ev["type"] == "error" {
			hasError = true
			break
		}
	}
	assert.True(t, hasError, "SSE handler must emit an error event when Temporal is unavailable")
}

// ── R-4d: Tenant propagation tests ───────────────────────────────────────────

// 17. SSE-created run carries TenantID and ApplicationID from EPConfig, not from client.
func TestSSE_RunStoresTenantID(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	capRec := &captureRunCreator{}
	capTC := &fakeTemporalClient{}

	tenantID := "aaaabbbb-0000-0000-0000-000000000001"
	appID := "ccccdddd-0000-0000-0000-000000000002"

	b := &sseBuilder{
		authn:    authn,
		recorder: capRec,
		temporal: capTC,
		epLoader: &fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:            "ep1",
			EPType:            "sse",
			EPEnabled:         true,
			AppEnabled:        true,
			AccessMode:        epconfig.AccessModeToken,
			TenantID:          tenantID,
			AppID:             appID,
			AppOrchestratorID: "orch-uuid-tenant-test",
			OrchestratorName:  "test-orchestrator",
		}},
		streamMsgs: []string{`{"type":"done","run_id":"r"}`},
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep1?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	collectSSE(t, resp, 3*time.Second)

	run, ok := capRec.last()
	require.True(t, ok, "CreateRun must have been called")
	assert.Equal(t, tenantID, run.TenantID, "run must carry TenantID from EPConfig")
	assert.Equal(t, appID, run.ApplicationID, "run must carry ApplicationID from EPConfig")

	require.True(t, capTC.called, "ExecuteWorkflow must be called")
	require.Len(t, capTC.input, 1)
	wfInput, ok := capTC.input[0].(temporal.WorkflowInput)
	require.True(t, ok)
	assert.Equal(t, tenantID, wfInput.TenantID, "WorkflowInput.TenantID must match EPConfig")
	assert.Equal(t, appID, wfInput.ApplicationID, "WorkflowInput.ApplicationID must match EPConfig")
}

// 18. A client-supplied X-Tenant-ID header must NOT override the server-resolved TenantID.
func TestSSE_ClientTenantHeaderIgnored(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	capRec := &captureRunCreator{}
	capTC := &fakeTemporalClient{}
	serverTenantID := "eeeeffff-0000-0000-0000-000000000001"

	b := &sseBuilder{
		authn:    authn,
		recorder: capRec,
		temporal: capTC,
		epLoader: &fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "ep1",
			EPType:     "sse",
			EPEnabled:  true,
			AppEnabled: true,
			AccessMode: epconfig.AccessModeToken,
			TenantID:   serverTenantID,
			AppID:      "11112222-0000-0000-0000-000000000002",
		}},
		streamMsgs: []string{`{"type":"done","run_id":"r"}`},
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep1?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("X-Tenant-ID", "attacker-00000000-0000-0000-ffff-000000000000")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	collectSSE(t, resp, 3*time.Second)

	run, ok := capRec.last()
	require.True(t, ok, "CreateRun must have been called")
	assert.Equal(t, serverTenantID, run.TenantID,
		"tenant_id must be server-resolved, not client-supplied")
}

// 19. SSE-created runs record events_transport="streams" — the Go worker always
// writes run events to Redis Streams (Python retired; no Pub/Sub path remains).
// Lifecycle.Admit leaves EventsTransport empty so the recorder stamps "streams".
func TestSSE_EventsTransportIsStreams(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	capRec := &captureRunCreator{}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:      authn,
		recorder:   capRec,
		temporal:   tc,
		streamMsgs: defaultStreamMsgs(),
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	collectSSE(t, resp, 3*time.Second)

	run, ok := capRec.last()
	require.True(t, ok)
	// Admit leaves EventsTransport empty; the recorder stamps "streams".
	assert.Empty(t, run.EventsTransport, "Admit must not set EventsTransport on the handle")
}

// 20. R-5: SSE handler satisfies the Lifecycle interface — Admit/Start/Release are called.
func TestSSE_LifecycleCallSequence(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	sess := &fakeSessionStore{}
	capRec := &captureRunCreator{}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:      authn,
		gate:       g,
		sessions:   sess,
		recorder:   capRec,
		temporal:   tc,
		streamMsgs: defaultStreamMsgs(),
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = collectSSE(t, resp, 3*time.Second)
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	// Admit: gate.Check + session.Register + CreateRun
	check, _, _, release, _ := g.getCounts()
	assert.Equal(t, 1, check, "gate.Check must be called exactly once")
	assert.GreaterOrEqual(t, release, 1, "gate.Release must be called on cleanup")

	_, sessionOK := capRec.last()
	assert.True(t, sessionOK, "CreateRun must be called")
	assert.True(t, tc.called, "ExecuteWorkflow (Start) must be called")
}

// 21. Missing message → 400 before any Lifecycle call.
func TestSSE_MissingMessage(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{authn: authn, temporal: tc}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep") // no ?message=
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// orderingStreamer records whether its stream reader was opened (first XRange)
// before ExecuteWorkflow ran, and delivers a terminal "done" on replay.
type orderingStreamer struct {
	mu        sync.Mutex
	readCalled bool
	tc        *orderingTemporalClient
}

func (s *orderingStreamer) XRange(_ context.Context, _, start, _ string) ([]runstream.StreamEntry, error) {
	s.mu.Lock()
	s.readCalled = true
	s.mu.Unlock()
	if start != "-" {
		return nil, nil
	}
	raw, _ := json.Marshal(map[string]any{"type": "done", "run_id": "r"})
	return []runstream.StreamEntry{{ID: "1-0", Values: map[string]interface{}{"data": string(raw)}}}, nil
}

func (s *orderingStreamer) XRangeN(_ context.Context, _, _, _ string, _ int64) ([]runstream.StreamEntry, error) {
	return nil, nil
}

func (s *orderingStreamer) XRead(ctx context.Context, _ runstream.XReadArgs) ([]runstream.StreamMessage, error) {
	<-ctx.Done()
	return nil, nil
}

func (s *orderingStreamer) opened() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readCalled
}

// orderingTemporalClient records that ExecuteWorkflow was called.
type orderingTemporalClient struct {
	mu     sync.Mutex
	called bool
}

func (c *orderingTemporalClient) ExecuteWorkflow(_ context.Context, opts temporalclient.StartWorkflowOptions, _ interface{}, args ...interface{}) (temporalclient.WorkflowRun, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.called = true
	return &fakeWorkflowRun{id: opts.ID}, nil
}

// 23. The run-stream reader must be opened (StreamFromRedis called) before
// Lifecycle.Start (ExecuteWorkflow). With the durable Redis stream the reader
// replays from cursor 0-0, so no event emitted after workflow start is lost;
// this test verifies the handler still opens the reader before starting and
// that events flow through to the client.
func TestSSE_RunStreamOpenedBeforeStart(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	tc := &orderingTemporalClient{}
	rs := &orderingStreamer{tc: tc}

	ep := &epconfig.EPConfig{
		EPSlug:            "ep",
		EPType:            "sse",
		EPEnabled:         true,
		AppEnabled:        true,
		AccessMode:        epconfig.AccessModePublic,
		TenantID:          "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:             "bbbbbbbb-0000-0000-0000-000000000001",
		AppOrchestratorID: "orch-uuid-ordering-test",
		OrchestratorName:  "test-orchestrator",
	}
	lc := execution.NewLifecycleWithRecorder(authn, &fakeEPLoader{cfg: ep}, nil, nil, &fakeRunCreator{}, tc, nil)
	bus := event.New()
	recorder := runrecorder.New(&fakeDBQuerier{})
	h := ssehandler.NewHandler(lc, recorder, bus, authn, "test-instance", nil)
	h.WithRunStreamer(rs)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep?message=hello")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_ = collectSSE(t, resp, 3*time.Second)

	assert.True(t, tc.called, "ExecuteWorkflow must be called")
	assert.True(t, rs.opened(), "run-stream reader must be opened (StreamFromRedis called)")
}

// 22. SSE handler: all run/session/context IDs are UUID v4.
func TestSSE_IDsAreUUIDv4(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	sess := &fakeSessionStore{}
	capRec := &captureRunCreator{}
	tc := &fakeTemporalClient{}
	b := &sseBuilder{
		authn:      authn,
		sessions:   sess,
		recorder:   capRec,
		temporal:   tc,
		streamMsgs: defaultStreamMsgs(),
	}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = collectSSE(t, resp, 3*time.Second)
	resp.Body.Close()
	time.Sleep(200 * time.Millisecond)

	run, ok := capRec.last()
	require.True(t, ok)

	// UUID v4 format: 8-4-4-4-12 hex chars.
	uuidRe := func(s string) bool {
		parts := strings.Split(s, "-")
		return len(parts) == 5 &&
			len(parts[0]) == 8 && len(parts[1]) == 4 &&
			len(parts[2]) == 4 && len(parts[3]) == 4 &&
			len(parts[4]) == 12
	}
	assert.True(t, uuidRe(run.ID), "RunID must be UUID v4: %s", run.ID)
	assert.True(t, uuidRe(run.ContextID), "ContextID must be UUID v4: %s", run.ContextID)
	assert.True(t, uuidRe(sess.getLastSession().SessionID), "SessionID must be UUID v4")
}

// 21. "file" bus event is forwarded as an artifact-update SSE event.
func TestSSEFileEventForwardedAsArtifactUpdate(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	tc := &fakeTemporalClient{}
	raw1, _ := json.Marshal(map[string]any{
		"type":         "file",
		"artifact_id":  "art-123",
		"filename":     "report.pdf",
		"content_type": "application/pdf",
		"download_url": "https://example.com/files/report.pdf",
		"run_id":       "run-1",
	})
	raw2, _ := json.Marshal(map[string]any{"type": "done", "run_id": "run-1"})
	b := &sseBuilder{authn: authn, temporal: tc, streamMsgs: []string{string(raw1), string(raw2)}}
	h, _ := b.build()
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	req := mustGet(srv.URL + "/orchestrate/myapp/ep?message=hello")
	req.Header.Set("Authorization", "Bearer tok")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	events := collectSSE(t, resp, 3*time.Second)
	var artifactEv map[string]any
	for _, ev := range events {
		if ev["type"] == "artifact-update" {
			artifactEv = ev
			break
		}
	}
	require.NotNil(t, artifactEv, "expected an artifact-update SSE event")
	assert.Equal(t, "report.pdf", artifactEv["filename"])
	assert.Equal(t, "application/pdf", artifactEv["content_type"])
	assert.Equal(t, "https://example.com/files/report.pdf", artifactEv["url"])
}
