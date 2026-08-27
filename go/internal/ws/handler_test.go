package ws_test

import (
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

	"github.com/gorilla/websocket"
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
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/transport"
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
	mu           sync.Mutex
	registered   []string
	ended        []string
	lastSession  session.SessionInfo
	failRegister bool
}

func (s *fakeSessionStore) Register(_ context.Context, info session.SessionInfo) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failRegister {
		return errors.New("redis: connection refused")
	}
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

type fakeEPLoader struct {
	cfg *epconfig.EPConfig
	err error
}

func (f *fakeEPLoader) Load(_ context.Context, _, _ string) (*epconfig.EPConfig, error) {
	return f.cfg, f.err
}

type fakeRunCreator struct{}

func (f *fakeRunCreator) CreateRun(_ context.Context, _ domain.Run) error { return nil }
func (f *fakeRunCreator) UpdateRunStatus(_ context.Context, _ string, _ domain.RunStatus, _ string) error {
	return nil
}
func (f *fakeRunCreator) UpdateRunGoal(_ context.Context, _, _ string) error { return nil }

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

func (c *captureRunCreator) UpdateRunStatus(_ context.Context, _ string, status domain.RunStatus, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, status)
	return nil
}
func (c *captureRunCreator) UpdateRunGoal(_ context.Context, _, _ string) error { return nil }

func (c *captureRunCreator) last() (domain.Run, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.runs) == 0 {
		return domain.Run{}, false
	}
	return c.runs[len(c.runs)-1], true
}

func (c *captureRunCreator) lastStatus() (domain.RunStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.updates) == 0 {
		return "", false
	}
	return c.updates[len(c.updates)-1], true
}

// ── Temporal fakes ────────────────────────────────────────────────────────────

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

type fakeTemporalClient struct {
	mu        sync.Mutex
	called    bool
	inputArgs []interface{}
}

func (f *fakeTemporalClient) ExecuteWorkflow(_ context.Context, opts temporalclient.StartWorkflowOptions, _ interface{}, args ...interface{}) (temporalclient.WorkflowRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.called = true
	f.inputArgs = args
	return &fakeWorkflowRun{id: opts.ID}, nil
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
	// Replay everything on the first read (start == "-"); return nothing after
	// the cursor has advanced so the reader transitions to the live XRead loop.
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

// wsBuilder assembles a WS Handler with injectable fakes via Lifecycle.
type wsBuilder struct {
	authn      transport.Authenticator
	epLoader   transport.EPConfigLoader
	gate       transport.GateStore
	sessions   transport.SessionStore
	recorder   execution.RunCreator
	temporal   transport.TemporalClientExecutor
	streamMsgs []string
}

func (b *wsBuilder) defaultEP() *epconfig.EPConfig {
	return &epconfig.EPConfig{
		EPSlug:            "ep1",
		EPType:            "websocket",
		AccessMode:        epconfig.AccessModeToken,
		EPEnabled:         true,
		AppEnabled:        true,
		TenantID:          "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:             "bbbbbbbb-0000-0000-0000-000000000001",
		// SEC-04: orchestrator resolved from EP binding, not from EP slug.
		AppOrchestratorID: "cccccccc-0000-0000-0000-000000000001",
		OrchestratorName:  "test-orchestrator",
	}
}

func (b *wsBuilder) build() (*wshandler.Handler, *fakeRunStreamer) {
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
	tc := b.temporal
	if tc == nil {
		tc = &fakeTemporalClient{}
	}

	lc := execution.NewLifecycleWithRecorder(b.authn, ep, b.gate, sess, rec, tc, nil)

	msgs := b.streamMsgs
	if len(msgs) == 0 {
		raw, _ := json.Marshal(map[string]any{"type": "done", "run_id": "mock-run"})
		msgs = []string{string(raw)}
	}
	rsStreamer := &fakeRunStreamer{messages: msgs}

	bus := event.New()
	h := wshandler.NewHandler(lc, bus, b.authn, "test-instance", nil)
	h.WithRunStreamer(rsStreamer)
	return h, rsStreamer
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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

// readUntilDone reads WS messages until a "done" or "error" type message or timeout/error.
func readUntilDone(t *testing.T, conn *websocket.Conn, timeout time.Duration) []map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout))
	var msgs []map[string]any
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var m map[string]any
		if json.Unmarshal(data, &m) == nil {
			msgs = append(msgs, m)
			if m["type"] == "done" || m["type"] == "error" {
				break
			}
		}
	}
	return msgs
}

func sendMessage(t *testing.T, conn *websocket.Conn, content string) {
	t.Helper()
	msg, _ := json.Marshal(map[string]string{"type": "message", "content": content})
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, msg))
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// 1. Unauthenticated request to token-mode EP → 401 (before upgrade).
func TestWS_Unauthenticated(t *testing.T) {
	authn := &fakeAuth{token: "valid-token", info: &auth.TokenInfo{TokenID: 1}}
	b := &wsBuilder{authn: authn}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "")
	require.Error(t, err, "unauthenticated request must fail")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// 2. Valid bearer token + valid EP → connection upgrades (101).
func TestWS_AuthenticatedUpgrade(t *testing.T) {
	authn := &fakeAuth{token: "valid-token", info: &auth.TokenInfo{TokenID: 42}}
	b := &wsBuilder{authn: authn}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, resp, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "valid-token")
	require.NoError(t, err, "expected upgrade to succeed")
	defer conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
}

// 3. Message sent → "done" event received.
func TestWS_MessageAndDone(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	streamMsgs := []string{
		`{"type":"token","content":"hello"}`,
		`{"type":"done","run_id":"r1"}`,
	}
	b := &wsBuilder{authn: authn, streamMsgs: streamMsgs}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/app/ep1", "tok")
	require.NoError(t, err)
	defer conn.Close()

	sendMessage(t, conn, "hi")
	msgs := readUntilDone(t, conn, 5*time.Second)

	types := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if s, ok := m["type"].(string); ok {
			types = append(types, s)
		}
	}
	assert.Contains(t, types, "done", "expected done event")
}

// 4. Client disconnect → session.End called.
func TestWS_DisconnectEndsSession(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	sess := &fakeSessionStore{}
	b := &wsBuilder{authn: authn, sessions: sess}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/app/ep2", "tok")
	require.NoError(t, err)

	sendMessage(t, conn, "bye")
	conn.Close()
	time.Sleep(300 * time.Millisecond)

	assert.Equal(t, 1, len(sess.getRegistered()), "session must have been registered")
	assert.Equal(t, 1, len(sess.getEnded()), "session must have been ended on disconnect")
}

// 5. Gate cap exceeded → 429 before upgrade (Lifecycle.Admit returns CapExceeded).
// Note: Lifecycle maps cap-exceeded to 429 (Too Many Requests), consistent with SSE and A2A.
func TestWS_GateCapExceeded(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{checkErr: gate.ErrCapExceeded}
	sess := &fakeSessionStore{}
	b := &wsBuilder{authn: authn, gate: g, sessions: sess}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/app/ep1", "tok")
	require.Error(t, err, "dial must fail when gate rejects")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.Equal(t, 0, len(sess.getRegistered()), "session must not be registered when gate rejects")
}

// 6. Gate admitted → session registered + confirmed; on disconnect gate released.
func TestWS_GateAdmittedAndReleased(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	sess := &fakeSessionStore{}
	b := &wsBuilder{authn: authn, gate: g, sessions: sess}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/app/ep1", "tok")
	require.NoError(t, err)

	sendMessage(t, conn, "hi")
	readUntilDone(t, conn, 5*time.Second)
	conn.Close()
	time.Sleep(300 * time.Millisecond)

	check, confirm, rollback, release, _ := g.getCounts()
	assert.Equal(t, 1, check, "Gate.Check must be called once")
	assert.Equal(t, 1, confirm, "Gate.Confirm must be called after Register")
	assert.GreaterOrEqual(t, release, 1, "Gate.Release must be called on session end")
	assert.Equal(t, 0, rollback, "Gate.Rollback must not be called on success")
	assert.Equal(t, 1, len(sess.getRegistered()))
	assert.Equal(t, 1, len(sess.getEnded()))
}

// 7. Gate rollback called when session.Register fails.
// After migration: Register failure is inside Lifecycle.Admit (pre-upgrade).
// The client sees HTTP 500 — no WS upgrade occurs.
func TestWS_GateRollbackOnRegisterFailure(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	sess := &fakeSessionStore{failRegister: true}
	b := &wsBuilder{authn: authn, gate: g, sessions: sess}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/app/ep1", "tok")
	require.Error(t, err, "connection must not upgrade when Register fails")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	time.Sleep(200 * time.Millisecond)
	check, confirm, rollback, _, _ := g.getCounts()
	assert.Equal(t, 1, check)
	assert.Equal(t, 1, rollback, "Gate.Rollback must be called when Register fails")
	assert.Equal(t, 0, confirm, "Gate.Confirm must NOT be called when Register fails")
}

// 8. Unauthenticated request to public EP succeeds.
func TestWS_PublicEPNoTokenAllowed(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	ep := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:     "ep1",
		EPType:     "websocket",
		AccessMode: epconfig.AccessModePublic,
		EPEnabled:  true,
		AppEnabled: true,
		TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
	}}
	b := &wsBuilder{authn: authn, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, resp, err := dialWS(t, srv, "/orchestrate/myapp/public-ep", "")
	require.NoError(t, err, "unauthenticated request to public EP must upgrade")
	defer conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
}

// 9. Unauthenticated request to token-mode EP → 401.
func TestWS_TokenEPNoTokenRejected(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	ep := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:     "ep1",
		EPType:     "websocket",
		AccessMode: epconfig.AccessModeToken,
		EPEnabled:  true,
		AppEnabled: true,
		TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
	}}
	b := &wsBuilder{authn: authn, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/myapp/token-ep", "")
	require.Error(t, err, "unauthenticated request to token-mode EP must be rejected")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// 10. Anonymous public EP session: gate receives TokenHash="" (no per-token rate limit).
func TestWS_AnonymousSessionGateTokenHashEmpty(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	sess := &fakeSessionStore{}
	ep := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:     "ep1",
		EPType:     "websocket",
		AccessMode: epconfig.AccessModePublic,
		EPEnabled:  true,
		AppEnabled: true,
		TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
	}}
	b := &wsBuilder{authn: authn, gate: g, sessions: sess, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/public-ep", "")
	require.NoError(t, err)
	conn.Close()
	time.Sleep(200 * time.Millisecond)

	_, _, _, _, lastCfg := g.getCounts()
	assert.Equal(t, "", lastCfg.TokenHash,
		"anonymous session must pass TokenHash='' to gate (not sha256(''))")
}

// 11. Anonymous public EP session: session registered with UserID=0.
func TestWS_AnonymousSessionUserIDIsZero(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 1}}
	sess := &fakeSessionStore{}
	ep := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:     "ep1",
		EPType:     "websocket",
		AccessMode: epconfig.AccessModePublic,
		EPEnabled:  true,
		AppEnabled: true,
		TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
	}}
	b := &wsBuilder{authn: authn, sessions: sess, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/public-ep", "")
	require.NoError(t, err)
	conn.Close()
	time.Sleep(200 * time.Millisecond)

	require.Equal(t, 1, len(sess.getRegistered()), "session must be registered")
	assert.Equal(t, int64(0), sess.getLastSession().UserID,
		"anonymous session must store UserID=0")
}

// 12. Authenticated request to public EP also succeeds.
func TestWS_AuthenticatedRequestToPublicEP(t *testing.T) {
	authn := &fakeAuth{token: "valid", info: &auth.TokenInfo{TokenID: 42}}
	ep := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:     "ep1",
		EPType:     "websocket",
		AccessMode: epconfig.AccessModePublic,
		EPEnabled:  true,
		AppEnabled: true,
		TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
	}}
	b := &wsBuilder{authn: authn, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, resp, err := dialWS(t, srv, "/orchestrate/myapp/public-ep", "valid")
	require.NoError(t, err, "authenticated request to public EP must succeed")
	defer conn.Close()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
}

// 13. Voice EP with valid token → 400 (voice EPs use POST /voice/chat, not WS).
// Lifecycle.Admit succeeds (gate/session created) but the WS handler rejects after admit.
func TestWS_VoiceEPReturns501(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	g := &fakeGate{}
	sess := &fakeSessionStore{}
	ep := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:     "ep1",
		EPType:     "voice",
		AccessMode: epconfig.AccessModeToken,
		EPEnabled:  true,
		AppEnabled: true,
		TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
	}}
	b := &wsBuilder{authn: authn, gate: g, sessions: sess, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/myapp/voice-ep", "tok")
	require.Error(t, err, "voice EP must reject the WS upgrade")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// 14. Voice EP with public access mode also returns 400 (use POST /voice/chat).
func TestWS_VoiceEPPublicReturns501(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	sess := &fakeSessionStore{}
	ep := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:     "ep1",
		EPType:     "voice",
		AccessMode: epconfig.AccessModePublic,
		EPEnabled:  true,
		AppEnabled: true,
		TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
	}}
	b := &wsBuilder{authn: authn, sessions: sess, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/myapp/voice-public", "")
	require.Error(t, err, "public voice EP must also reject the WS upgrade")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// 15. Temporal path: ExecuteWorkflow is called; client receives done from run stream.
func TestWS_TemporalPathUsedWhenEnabled(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 42}}
	tc := &fakeTemporalClient{}
	streamMsgs := []string{
		`{"type":"token","content":"hello from temporal"}`,
		`{"type":"done","run_id":"test-run-id"}`,
	}
	b := &wsBuilder{authn: authn, temporal: tc, streamMsgs: streamMsgs}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "tok")
	require.NoError(t, err)
	defer conn.Close()

	sendMessage(t, conn, "hi")
	msgs := readUntilDone(t, conn, 5*time.Second)

	types := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if s, ok := m["type"].(string); ok {
			types = append(types, s)
		}
	}
	tc.mu.Lock()
	called := tc.called
	tc.mu.Unlock()
	assert.True(t, called, "ExecuteWorkflow must be called when temporal is wired")
	assert.Contains(t, types, "done", "client must receive done event from run stream")
}

// 16. replay_unavailable event is forwarded to the WS client (Phase 11c-B).
func TestWS_ReplayUnavailableForwardedToClient(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 42}}
	streamMsgs := []string{
		`{"type":"replay_unavailable","reason":"history_trimmed","run_id":"r1"}`,
		`{"type":"done","run_id":"r1"}`,
	}
	b := &wsBuilder{authn: authn, streamMsgs: streamMsgs}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "tok")
	require.NoError(t, err)
	defer conn.Close()

	sendMessage(t, conn, "hi")
	msgs := readUntilDone(t, conn, 5*time.Second)

	types := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if s, ok := m["type"].(string); ok {
			types = append(types, s)
		}
	}
	assert.Contains(t, types, "replay_unavailable", "replay_unavailable must be forwarded")
	assert.Contains(t, types, "done")
}

// 17. When Temporal is nil: upgrade succeeds (Admit does not check temporal),
// but Lifecycle.Start returns error → WS error event sent to client.
func TestWS_NoTemporalReturnsErrorEvent(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	// nil temporal → Lifecycle.Start returns StartError.
	lc := execution.NewLifecycleWithRecorder(
		authn,
		&fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "ep1",
			EPType:     "websocket",
			AccessMode: epconfig.AccessModeToken,
			EPEnabled:  true,
			AppEnabled: true,
			TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
			AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
		}},
		nil,
		&fakeSessionStore{},
		&fakeRunCreator{},
		nil, // temporal nil → Start will fail
		nil,
	)

	rsStreamer := &fakeRunStreamer{messages: []string{`{"type":"done","run_id":"r"}`}}
	bus := event.New()
	h := wshandler.NewHandler(lc, bus, authn, "test-instance", nil)
	h.WithRunStreamer(rsStreamer)

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "tok")
	require.NoError(t, err, "WS upgrade must succeed even when Temporal is not wired")
	defer conn.Close()

	sendMessage(t, conn, "hi")

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, readErr := conn.ReadMessage()
	require.NoError(t, readErr, "must receive an error message from the handler")

	var sm map[string]any
	require.NoError(t, json.Unmarshal(data, &sm))
	assert.Equal(t, "error", sm["type"],
		"handler must send error event when Temporal client is unavailable")
}

// ── R-4d: Tenant propagation tests ───────────────────────────────────────────

// 18. WS-created run stores TenantID and ApplicationID from EPConfig — not from client.
func TestWS_RunStoresTenantID(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	tc := &fakeTemporalClient{}
	capRec := &captureRunCreator{}

	tenantID := "cccccccc-0000-0000-0000-000000000001"
	appID := "dddddddd-0000-0000-0000-000000000002"
	ep := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:            "ep1",
		EPType:            "websocket",
		AccessMode:        epconfig.AccessModeToken,
		EPEnabled:         true,
		AppEnabled:        true,
		TenantID:          tenantID,
		AppID:             appID,
		AppOrchestratorID: "orch-uuid-tenant-test",
		OrchestratorName:  "test-orchestrator",
	}}
	b := &wsBuilder{authn: authn, temporal: tc, recorder: capRec, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "tok")
	require.NoError(t, err)
	defer conn.Close()

	sendMessage(t, conn, "hello")
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
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

	run, ok := capRec.last()
	require.True(t, ok, "CreateRun must have been called")
	assert.Equal(t, tenantID, run.TenantID, "run must carry TenantID from EPConfig")
	assert.Equal(t, appID, run.ApplicationID, "run must carry ApplicationID from EPConfig")

	tc.mu.Lock()
	args := tc.inputArgs
	tc.mu.Unlock()
	require.True(t, tc.called, "ExecuteWorkflow must be called")
	require.Len(t, args, 1)
	wfInput, ok := args[0].(temporal.WorkflowInput)
	require.True(t, ok, "WorkflowInput arg must be temporal.WorkflowInput")
	assert.Equal(t, tenantID, wfInput.TenantID, "WorkflowInput.TenantID must match EPConfig")
	assert.Equal(t, appID, wfInput.ApplicationID, "WorkflowInput.ApplicationID must match EPConfig")
}

// 19. Client-supplied X-Tenant-ID header must NOT override EPConfig.TenantID.
func TestWS_ClientTenantHeaderIgnored(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	tc := &fakeTemporalClient{}
	capRec := &captureRunCreator{}

	serverTenantID := "eeeeeeee-0000-0000-0000-000000000001"
	ep := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:     "ep1",
		EPType:     "websocket",
		AccessMode: epconfig.AccessModeToken,
		EPEnabled:  true,
		AppEnabled: true,
		TenantID:   serverTenantID,
		AppID:      "ffffffff-0000-0000-0000-000000000002",
	}}
	b := &wsBuilder{authn: authn, temporal: tc, recorder: capRec, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/orchestrate/myapp/ep1"
	headers := http.Header{
		"Authorization": []string{"Bearer tok"},
		"X-Tenant-ID":   []string{"attacker-00000000-0000-0000-0000-ffff"},
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	require.NoError(t, err)
	defer conn.Close()

	sendMessage(t, conn, "hi")
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
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

	run, ok := capRec.last()
	require.True(t, ok, "CreateRun must have been called")
	assert.Equal(t, serverTenantID, run.TenantID,
		"tenant_id must be server-resolved (from EPConfig), not client-supplied header")
}

// 20. IDs generated by Lifecycle.Admit are UUID v4 (required by Python Temporal worker).
func TestWS_IDsAreUUIDv4(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	capRec := &captureRunCreator{}
	b := &wsBuilder{authn: authn, recorder: capRec}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "tok")
	require.NoError(t, err)
	defer conn.Close()

	sendMessage(t, conn, "hi")
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var sm map[string]any
		if json.Unmarshal(data, &sm) == nil && (sm["type"] == "done" || sm["type"] == "error") {
			break
		}
	}

	run, ok := capRec.last()
	require.True(t, ok, "CreateRun must have been called")
	assert.Regexp(t, `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
		run.ID, "RunID must be UUID v4")
}

// 21. Lifecycle.Admit runs before upgrade: EP not found → 404 HTTP (not a WS error frame).
func TestWS_AdmitBeforeUpgrade_EPNotFound(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	ep := &fakeEPLoader{err: epconfig.ErrNotFound}
	b := &wsBuilder{authn: authn, epLoader: ep}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	_, resp, err := dialWS(t, srv, "/orchestrate/myapp/unknown-ep", "tok")
	require.Error(t, err, "must fail when EP not found")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode,
		"EP not found must return 404 HTTP (not WS error event)")
}

// ── Execution Core Hardening — WS failure-path tests ─────────────────────────

// 22. WS upgrade failure: plain HTTP GET to a public EP (no WS Upgrade header)
// after Admit succeeds — run must be marked failed by Release, not left "admitted".
func TestWS_UpgradeFailure_RunMarkedFailed(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	capRec := &captureRunCreator{}
	// Public EP so Admit passes even without a WS upgrade (no token required).
	publicEPLoader := &fakeEPLoader{cfg: &epconfig.EPConfig{
		EPSlug:     "ep1",
		EPType:     "websocket",
		AccessMode: epconfig.AccessModePublic,
		EPEnabled:  true,
		AppEnabled: true,
		TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
		AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
	}}
	b := &wsBuilder{authn: authn, recorder: capRec, epLoader: publicEPLoader}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	// Plain HTTP GET without WS Upgrade header → Admit passes, upgrade fails.
	// gorilla returns 400 Bad Request when the Upgrade header is missing.
	resp, err := http.Get(srv.URL + "/orchestrate/myapp/ep1")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// Give Release goroutine time to run.
	time.Sleep(200 * time.Millisecond)

	run, ok := capRec.last()
	require.True(t, ok, "CreateRun must have been called")
	assert.Equal(t, domain.RunStatusAdmitted, run.Status, "run must be initially created as admitted")

	finalStatus, hasUpdate := capRec.lastStatus()
	require.True(t, hasUpdate, "UpdateRunStatus must be called after upgrade failure")
	assert.Equal(t, domain.RunStatusFailed, finalStatus, "run must be marked failed after upgrade failure")
}

// 23. WS first-message timeout/error: connect but never send a message.
// The handler reads with a 30s deadline — we close before sending to trigger
// a read error; run must be marked failed.
func TestWS_FirstMessageError_RunMarkedFailed(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	capRec := &captureRunCreator{}
	b := &wsBuilder{authn: authn, recorder: capRec}
	h, _ := b.build()

	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "tok")
	require.NoError(t, err)
	// Close immediately without sending a message → readClientMessage fails.
	conn.Close()

	time.Sleep(300 * time.Millisecond)

	_, ok := capRec.last()
	require.True(t, ok, "CreateRun must have been called")

	finalStatus, hasUpdate := capRec.lastStatus()
	require.True(t, hasUpdate, "UpdateRunStatus must be called after first-message failure")
	assert.Equal(t, domain.RunStatusFailed, finalStatus, "run must be marked failed on first-message error")
}

// 24. Temporal Start failure: run must be marked failed.
func TestWS_StartFailure_RunMarkedFailed(t *testing.T) {
	authn := &fakeAuth{token: "tok", info: &auth.TokenInfo{TokenID: 1}}
	capRec := &captureRunCreator{}

	// Temporal client that fails ExecuteWorkflow.
	failTemporal := &failingTemporalClient{}

	lc := execution.NewLifecycleWithRecorder(
		authn,
		&fakeEPLoader{cfg: &epconfig.EPConfig{
			EPSlug:     "ep1",
			EPType:     "websocket",
			AccessMode: epconfig.AccessModeToken,
			EPEnabled:  true,
			AppEnabled: true,
			TenantID:   "aaaaaaaa-0000-0000-0000-000000000001",
			AppID:      "bbbbbbbb-0000-0000-0000-000000000001",
		}},
		&fakeGate{},
		&fakeSessionStore{},
		capRec,
		failTemporal,
		nil,
	)

	rsStreamer := &fakeRunStreamer{messages: []string{`{"type":"done","run_id":"r"}`}}
	bus := event.New()
	wsH := wshandler.NewHandler(lc, bus, authn, "test-instance", nil)
	wsH.WithRunStreamer(rsStreamer)

	srv := httptest.NewServer(wsH.Routes())
	defer srv.Close()

	conn, _, err := dialWS(t, srv, "/orchestrate/myapp/ep1", "tok")
	require.NoError(t, err)
	defer conn.Close()

	sendMessage(t, conn, "hi")

	// Expect an error event from the handler.
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, readErr := conn.ReadMessage()
	require.NoError(t, readErr)
	var sm map[string]any
	require.NoError(t, json.Unmarshal(data, &sm))
	assert.Equal(t, "error", sm["type"])

	conn.Close()
	time.Sleep(300 * time.Millisecond)

	finalStatus, hasUpdate := capRec.lastStatus()
	require.True(t, hasUpdate, "UpdateRunStatus must be called after Start failure")
	assert.Equal(t, domain.RunStatusFailed, finalStatus, "run must be marked failed after Start failure")
}

// failingTemporalClient always returns an error from ExecuteWorkflow.
type failingTemporalClient struct{}

func (f *failingTemporalClient) ExecuteWorkflow(_ context.Context, _ temporalclient.StartWorkflowOptions, _ interface{}, _ ...interface{}) (temporalclient.WorkflowRun, error) {
	return nil, errors.New("temporal: unavailable")
}

// Ensure domain import is used.
var _ = domain.Message{}
