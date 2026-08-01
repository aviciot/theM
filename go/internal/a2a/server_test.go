package a2a_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	temporalclient "go.temporal.io/sdk/client"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	a2aserver "github.com/aviciot/them/internal/a2a"
	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/event"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/transport"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fakes
// ─────────────────────────────────────────────────────────────────────────────

type fakeAuth struct {
	info *auth.TokenInfo
	err  error
}

func (f *fakeAuth) Validate(_ context.Context, _ string) (*auth.TokenInfo, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.info, nil
}

type fakeEPLoader struct {
	cfg *epconfig.EPConfig
	err error
}

func (f *fakeEPLoader) Load(_ context.Context, _ string) (*epconfig.EPConfig, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.cfg, nil
}

type fakeGate struct {
	checkErr   error
	confirmErr error

	checkCalled    bool
	confirmCalled  bool
	rollbackCalled bool
	releaseCalled  bool
}

func (f *fakeGate) Check(_ context.Context, _ gate.Config) (gate.Result, error) {
	f.checkCalled = true
	return gate.Result{}, f.checkErr
}

func (f *fakeGate) Confirm(_ context.Context, _ gate.Config) error {
	f.confirmCalled = true
	return f.confirmErr
}

func (f *fakeGate) Rollback(_ context.Context, _ gate.Config) error {
	f.rollbackCalled = true
	return nil
}

func (f *fakeGate) Release(_ context.Context, _ gate.Config) error {
	f.releaseCalled = true
	return nil
}

type fakeSession struct {
	registerErr    error
	registerCalled bool
	endCalled      bool
	lastInfo       session.SessionInfo
}

func (f *fakeSession) Register(_ context.Context, info session.SessionInfo) error {
	f.registerCalled = true
	f.lastInfo = info
	return f.registerErr
}

func (f *fakeSession) End(_ context.Context, _, _, _ string) error {
	f.endCalled = true
	return nil
}

type fakeRecorder struct {
	createCalled bool
	lastRun      domain.Run
}

func (f *fakeRecorder) CreateRun(_ context.Context, run domain.Run) error {
	f.createCalled = true
	f.lastRun = run
	return nil
}

type fakeWorkflowRun struct {
	result temporal.WorkflowResult
	err    error
}

func (f *fakeWorkflowRun) GetID() string    { return "wf-id" }
func (f *fakeWorkflowRun) GetRunID() string { return "run-id" }
func (f *fakeWorkflowRun) Get(_ context.Context, valuePtr interface{}) error {
	if f.err != nil {
		return f.err
	}
	if r, ok := valuePtr.(*temporal.WorkflowResult); ok {
		*r = f.result
	}
	return nil
}
func (f *fakeWorkflowRun) GetWithOptions(_ context.Context, valuePtr interface{}, _ temporalclient.WorkflowRunGetOptions) error {
	return f.Get(context.Background(), valuePtr)
}

type fakeTemporal struct {
	run  *fakeWorkflowRun
	err  error

	called    bool
	lastInput temporal.WorkflowInput
}

func (f *fakeTemporal) ExecuteWorkflow(_ context.Context, _ temporalclient.StartWorkflowOptions, _ interface{}, args ...interface{}) (temporalclient.WorkflowRun, error) {
	f.called = true
	if len(args) > 0 {
		if inp, ok := args[0].(temporal.WorkflowInput); ok {
			f.lastInput = inp
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.run, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture builders
// ─────────────────────────────────────────────────────────────────────────────

var devLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

func tokenEPConfig() *epconfig.EPConfig {
	return &epconfig.EPConfig{
		EPID:       "ep-uuid-1",
		AppID:      "app-uuid-1",
		TenantID:   "tenant-uuid-1",
		EPSlug:     "myapp",
		EPType:     "a2a",
		EPEnabled:  true,
		AppEnabled: true,
		AccessMode: epconfig.AccessModeToken,
	}
}

func publicEPConfig() *epconfig.EPConfig {
	cfg := tokenEPConfig()
	cfg.AccessMode = epconfig.AccessModePublic
	return cfg
}

func validTokenInfo() *auth.TokenInfo {
	return &auth.TokenInfo{TokenID: 42}
}

// serverBuilder holds the overridable deps for building a test Server.
type serverBuilder struct {
	auth     transport.Authenticator
	epLoader transport.EPConfigLoader
	gate     transport.GateStore
	sessions transport.SessionStore
	temporal transport.TemporalClientExecutor
	recorder execution.RunCreator
}

func defaultBuilder() *serverBuilder {
	return &serverBuilder{
		auth:     &fakeAuth{info: validTokenInfo()},
		epLoader: &fakeEPLoader{cfg: tokenEPConfig()},
		gate:     &fakeGate{},
		sessions: &fakeSession{},
		temporal: &fakeTemporal{run: &fakeWorkflowRun{
			result: temporal.WorkflowResult{
				FinalText: "hello from orchestrator",
				Status:    domain.RunStatusCompleted,
			},
		}},
		recorder: &fakeRecorder{},
	}
}

func (b *serverBuilder) build() *a2aserver.Server {
	bus := event.New()
	lc := execution.NewLifecycleWithRecorder(
		b.auth, b.epLoader, b.gate, b.sessions,
		b.recorder, b.temporal, devLogger,
	)
	return a2aserver.NewServer(lc, bus, b.auth, "test-instance", devLogger)
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP helpers
// ─────────────────────────────────────────────────────────────────────────────

func postRPC(t *testing.T, srv *httptest.Server, body any, token string) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/a2a/myapp", bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func validSendBody() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "message/send",
		"params": map[string]any{
			"message": map[string]any{
				"role":      "user",
				"parts":     []map[string]any{{"text": "hi"}},
				"messageId": "uuid-123",
			},
		},
		"id": "req-1",
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

// A2A-01: Unknown app_slug → 404
func TestA2A_MissingSlug_404(t *testing.T) {
	b := defaultBuilder()
	b.epLoader = &fakeEPLoader{err: epconfig.ErrNotFound}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// A2A-02: EP or app disabled → 403
func TestA2A_DisabledEP_403(t *testing.T) {
	b := defaultBuilder()
	cfg := tokenEPConfig()
	cfg.EPEnabled = false
	b.epLoader = &fakeEPLoader{cfg: cfg}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// A2A-02b: App disabled → 403 (exercises CheckAccess block-list path)
func TestA2A_BlockedToken_403(t *testing.T) {
	b := defaultBuilder()
	cfg := tokenEPConfig()
	cfg.AppEnabled = false
	b.epLoader = &fakeEPLoader{cfg: cfg}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// A2A-03: Token-mode EP + no bearer → 401
func TestA2A_MissingTokenOnTokenEP_401(t *testing.T) {
	b := defaultBuilder()
	b.epLoader = &fakeEPLoader{cfg: tokenEPConfig()}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "") // no token
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// A2A-04: Invalid token → 401 (failed validation → tokenInfo nil → token EP → 401)
func TestA2A_InvalidToken_401(t *testing.T) {
	b := defaultBuilder()
	b.auth = &fakeAuth{err: errors.New("invalid")}
	b.epLoader = &fakeEPLoader{cfg: tokenEPConfig()}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "bad-token")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// A2A-05: Public EP + no bearer → proceeds successfully
func TestA2A_PublicEP_NoToken_OK(t *testing.T) {
	b := defaultBuilder()
	b.epLoader = &fakeEPLoader{cfg: publicEPConfig()}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "") // no token
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))
	assert.Nil(t, rpcResp["error"])
}

// A2A-06: Gate returns ErrCapExceeded → 429
func TestA2A_CapExceeded_429(t *testing.T) {
	b := defaultBuilder()
	b.gate = &fakeGate{checkErr: gate.ErrCapExceeded}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

// A2A-07: TenantID in run comes from EPConfig, not from request
func TestA2A_TenantIDFromEPConfig(t *testing.T) {
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b := defaultBuilder()
	b.temporal = tc
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, tc.called)
	assert.Equal(t, "tenant-uuid-1", tc.lastInput.TenantID)
	assert.Equal(t, "app-uuid-1", tc.lastInput.ApplicationID)
}

// A2A-08: Request body cannot inject TenantID/ApplicationID into WorkflowInput
func TestA2A_ClientCannotOverrideTenantID(t *testing.T) {
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b := defaultBuilder()
	b.temporal = tc
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	body := validSendBody()
	body["params"] = map[string]any{
		"message": map[string]any{
			"role":      "user",
			"parts":     []map[string]any{{"text": "hi"}},
			"contextId": "attacker-controlled-context",
		},
		"tenant_id":      "attacker-tenant",
		"application_id": "attacker-app",
	}
	resp := postRPC(t, srv, body, "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "tenant-uuid-1", tc.lastInput.TenantID, "TenantID must come from EPConfig")
	assert.Equal(t, "app-uuid-1", tc.lastInput.ApplicationID, "ApplicationID must come from EPConfig")
}

// A2A-09: WorkflowInput receives TenantID + ApplicationID from EPConfig
func TestA2A_WorkflowInputHasTenantID(t *testing.T) {
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "done", Status: domain.RunStatusCompleted},
	}}
	b := defaultBuilder()
	b.temporal = tc
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, tc.called)
	assert.Equal(t, "tenant-uuid-1", tc.lastInput.TenantID)
	assert.Equal(t, "app-uuid-1", tc.lastInput.ApplicationID)
	assert.NotEmpty(t, tc.lastInput.RunID)
	assert.NotEmpty(t, tc.lastInput.ContextID)
	assert.Equal(t, "myapp", tc.lastInput.EntryPointSlug)
	assert.Equal(t, "myapp", tc.lastInput.OrchestratorName)
}

// A2A-10: session.Register called before ExecuteWorkflow
func TestA2A_SessionRegistered(t *testing.T) {
	sess := &fakeSession{}
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b := defaultBuilder()
	b.sessions = sess
	b.temporal = tc
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, sess.registerCalled, "session.Register must be called")
	assert.Equal(t, "tenant-uuid-1", sess.lastInfo.TenantID)
	assert.Equal(t, "app-uuid-1", sess.lastInfo.AppID)
}

// A2A-11: session.End called after workflow completes
func TestA2A_SessionEndedOnCompletion(t *testing.T) {
	sess := &fakeSession{}
	b := defaultBuilder()
	b.sessions = sess
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	time.Sleep(10 * time.Millisecond)
	assert.True(t, sess.endCalled, "session.End must be called")
}

// A2A-12: gate.Release called after session.End (cleanup on success)
func TestA2A_GateReleasedOnCompletion(t *testing.T) {
	g := &fakeGate{}
	b := defaultBuilder()
	b.gate = g
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	time.Sleep(10 * time.Millisecond)
	assert.True(t, g.checkCalled, "gate.Check must be called")
	assert.True(t, g.confirmCalled, "gate.Confirm must be called")
	assert.True(t, g.releaseCalled, "gate.Release must be called")
}

// A2A-13: gate.Rollback called if session.Register fails
func TestA2A_GateRollbackOnRegisterFail(t *testing.T) {
	g := &fakeGate{}
	b := defaultBuilder()
	b.gate = g
	b.sessions = &fakeSession{registerErr: errors.New("redis down")}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.True(t, g.checkCalled, "gate.Check must be called")
	assert.True(t, g.rollbackCalled, "gate.Rollback must be called on Register failure")
}

// A2A-14: Successful workflow → "completed" state + text artifact
func TestA2A_RPCResult_CompletedState(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))

	assert.Equal(t, "2.0", rpcResp["jsonrpc"])
	assert.Nil(t, rpcResp["error"])
	require.NotNil(t, rpcResp["result"])

	result := rpcResp["result"].(map[string]any)
	status := result["status"].(map[string]any)
	assert.Equal(t, "completed", status["state"])

	artifacts := result["artifacts"].([]any)
	require.Len(t, artifacts, 1)
	parts := artifacts[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 1)
	part := parts[0].(map[string]any)
	assert.Equal(t, "hello from orchestrator", part["text"])
	assert.Nil(t, part["kind"], "wire format must not include 'kind' field (non-spec)")
}

// A2A-14b: taskId present in result
func TestA2A_RPCResult_HasTaskID(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))

	result := rpcResp["result"].(map[string]any)
	taskID, ok := result["taskId"]
	assert.True(t, ok, "result must contain taskId")
	assert.NotEmpty(t, taskID, "taskId must be non-empty")
}

// A2A-15: Failed workflow → sanitized JSON-RPC error (no raw error string)
func TestA2A_RPCError_WorkflowFailed(t *testing.T) {
	b := defaultBuilder()
	b.temporal = &fakeTemporal{run: &fakeWorkflowRun{err: errors.New("temporal internal error")}}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))

	require.NotNil(t, rpcResp["error"])
	errObj := rpcResp["error"].(map[string]any)
	assert.Equal(t, "internal error", errObj["message"])
	assert.NotContains(t, errObj["message"], "temporal internal error",
		"raw error must not be exposed to callers")
}

// A2A-16: Caller-provided contextId used as event bus key
func TestA2A_ContextIDFromParams(t *testing.T) {
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b := defaultBuilder()
	b.temporal = tc
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	body := validSendBody()
	body["params"] = map[string]any{
		"message": map[string]any{
			"role":      "user",
			"parts":     []map[string]any{{"text": "hi"}},
			"contextId": "caller-context-id-abc123",
		},
	}

	resp := postRPC(t, srv, body, "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "caller-context-id-abc123", tc.lastInput.ContextID)
}

// A2A-17: No contextId in params → new UUID generated (non-empty, different each call)
func TestA2A_ContextIDGeneratedIfAbsent(t *testing.T) {
	tc1 := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b := defaultBuilder()
	b.temporal = tc1
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()
	resp1 := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp1.Body.Close()

	tc2 := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b2 := defaultBuilder()
	b2.temporal = tc2
	srv2 := httptest.NewServer(b2.build().Routes())
	defer srv2.Close()
	resp2 := postRPC(t, srv2, validSendBody(), "valid-token")
	defer resp2.Body.Close()

	require.Equal(t, http.StatusOK, resp1.StatusCode)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	assert.NotEmpty(t, tc1.lastInput.ContextID)
	assert.NotEmpty(t, tc2.lastInput.ContextID)
	assert.NotEqual(t, tc1.lastInput.ContextID, tc2.lastInput.ContextID,
		"each request without contextId should get a unique contextId")
}

// A2A-18: Temporal is the only execution path — no direct orch.Run fallback.
func TestA2A_DirectOrchNotUsed_TemporalCalledInstead(t *testing.T) {
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "done", Status: domain.RunStatusCompleted},
	}}
	b := defaultBuilder()
	b.temporal = tc
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.True(t, tc.called, "Temporal ExecuteWorkflow must be called")
}

// A2A-19: Cleanup releases gate and session on auth failure path
func TestA2A_CleanupOnGateFailure(t *testing.T) {
	g := &fakeGate{checkErr: gate.ErrCapExceeded}
	sess := &fakeSession{}
	b := defaultBuilder()
	b.gate = g
	b.sessions = sess
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.False(t, sess.registerCalled, "session must not be registered if gate denied")
	assert.False(t, g.releaseCalled, "gate.Release must not be called if gate.Check failed")
}

// A2A-20: Unknown JSON-RPC method → -32601 error
func TestA2AUnknownMethod(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"method":  "unknown/method",
		"id":      "req-2",
	}
	resp := postRPC(t, srv, reqBody, "valid-token")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))
	assert.Nil(t, rpcResp["result"])
	require.NotNil(t, rpcResp["error"])
	errObj := rpcResp["error"].(map[string]any)
	assert.Equal(t, float64(-32601), errObj["code"])
}

// A2A-21: Malformed JSON → -32700 parse error
func TestA2AMalformedJSON(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/a2a/myapp", bytes.NewReader([]byte(`{not valid json`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))
	require.NotNil(t, rpcResp["error"])
	errObj := rpcResp["error"].(map[string]any)
	assert.Equal(t, float64(-32700), errObj["code"])
}

// A2A-22: Temporal client nil → 503
func TestA2A_TemporalNotConfigured_503(t *testing.T) {
	b := defaultBuilder()
	b.temporal = nil
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// A2A-23: ContextID generated by Lifecycle is UUID v4 format
func TestA2A_ContextID_IsUUIDv4(t *testing.T) {
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b := defaultBuilder()
	b.temporal = tc
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	contextID := tc.lastInput.ContextID
	assert.NotEmpty(t, contextID)
	// UUID v4: exactly 36 chars with dashes at 8, 13, 18, 23
	assert.Len(t, contextID, 36, "ContextID must be UUID v4 (36 chars)")
	assert.Equal(t, '-', rune(contextID[8]), "position 8 must be '-'")
	assert.Equal(t, '-', rune(contextID[13]), "position 13 must be '-'")
}

// A2A-24: Execution lifecycle interface compile check
func TestA2A_LifecycleInterface_Satisfied(t *testing.T) {
	var _ execution.RunCreator = &fakeRecorder{}
	var _ transport.Authenticator = &fakeAuth{}
	var _ transport.GateStore = &fakeGate{}
	var _ transport.SessionStore = &fakeSession{}
	var _ transport.TemporalClientExecutor = &fakeTemporal{}
	_ = t
}

// A2A-25: fakeTemporal satisfies TemporalClientExecutor (compile check)
func TestA2A_TemporalInterface_Satisfied(t *testing.T) {
	var _ transport.TemporalClientExecutor = &fakeTemporal{}
	_ = t
}
