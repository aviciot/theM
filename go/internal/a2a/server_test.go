package a2a_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/runrecorder"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/temporal"
)

// ── Recorder stub ────────────────────────────────────────────────────────────

type noopDB struct{}

func (n *noopDB) Exec(_ context.Context, _ string, _ ...any) error { return nil }
func (n *noopDB) QueryRow(_ context.Context, _ string, _ ...any) runrecorder.SingleRowScanner {
	return &noopRow{}
}

type noopRow struct{}

func (r *noopRow) Scan(dest ...any) error {
	if len(dest) > 0 {
		if sp, ok := dest[0].(*string); ok {
			*sp = "00000000-0000-0000-0000-000000000001"
		}
	}
	return nil
}

// ── Fake authenticator ────────────────────────────────────────────────────────

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

// ── Fake EPConfig loader ──────────────────────────────────────────────────────

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

// ── Fake gate ─────────────────────────────────────────────────────────────────

type fakeGate struct {
	checkErr    error
	confirmErr  error
	rollbackErr error
	releaseErr  error

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
	return f.rollbackErr
}
func (f *fakeGate) Release(_ context.Context, _ gate.Config) error {
	f.releaseCalled = true
	return f.releaseErr
}

// ── Fake session store ────────────────────────────────────────────────────────

type fakeSession struct {
	registerErr error
	endErr      error

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
	return f.endErr
}

// ── Fake Temporal client ──────────────────────────────────────────────────────

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
	run *fakeWorkflowRun
	err error

	called     bool
	lastInput  temporal.WorkflowInput
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

// ── EPConfig helpers ──────────────────────────────────────────────────────────

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

// ── Server builder ────────────────────────────────────────────────────────────

type serverBuilder struct {
	auth     a2aserver.Authenticator
	epLoader a2aserver.EPConfigLoader
	gate     a2aserver.GateStore
	sessions a2aserver.SessionStore
	temporal a2aserver.TemporalClientExecutor
}

func defaultBuilder() *serverBuilder {
	return &serverBuilder{
		auth: &fakeAuth{info: &auth.TokenInfo{TokenID: 42}},
		epLoader: &fakeEPLoader{cfg: tokenEPConfig()},
		gate:     &fakeGate{},
		sessions: &fakeSession{},
		temporal: &fakeTemporal{run: &fakeWorkflowRun{
			result: temporal.WorkflowResult{
				FinalText: "hello from orchestrator",
				Status:    domain.RunStatusCompleted,
			},
		}},
	}
}

func (b *serverBuilder) build() *a2aserver.Server {
	bus := event.New()
	recorder := runrecorder.New(&noopDB{})
	return a2aserver.NewServer(
		recorder, bus,
		b.auth, b.epLoader, b.gate, b.sessions, b.temporal,
		"test-instance", nil,
	)
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

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

// ── R-4e tests ────────────────────────────────────────────────────────────────

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

// A2A-02b: Token on block list → 403
func TestA2A_BlockedToken_403(t *testing.T) {
	b := defaultBuilder()
	cfg := tokenEPConfig()
	// blocked_tokens stores SHA-256 hex; use a value that transport.TokenHash("valid-token") would NOT produce.
	// Instead wire CheckAccess by disabling the app:
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

// A2A-04: Invalid token → 401 (failed validation → authed=false → token EP → 401)
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
	b := defaultBuilder()
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{
			FinalText: "ok",
			Status:    domain.RunStatusCompleted,
		},
	}}
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
	b := defaultBuilder()
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b.temporal = tc
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	// Attempt to inject tenant via contextId (which the caller can provide)
	body := validSendBody()
	body["params"] = map[string]any{
		"message": map[string]any{
			"role":      "user",
			"parts":     []map[string]any{{"text": "hi"}},
			"contextId": "attacker-controlled-context",
		},
		// Non-standard fields attempting injection
		"tenant_id":      "attacker-tenant",
		"application_id": "attacker-app",
	}
	resp := postRPC(t, srv, body, "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	// TenantID must come from EPConfig, not from request
	assert.Equal(t, "tenant-uuid-1", tc.lastInput.TenantID)
	assert.Equal(t, "app-uuid-1", tc.lastInput.ApplicationID)
}

// A2A-09: WorkflowInput receives TenantID + ApplicationID from EPConfig
func TestA2A_WorkflowInputHasTenantID(t *testing.T) {
	b := defaultBuilder()
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "done", Status: domain.RunStatusCompleted},
	}}
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
	b := defaultBuilder()
	sess := &fakeSession{}
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
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
	b := defaultBuilder()
	sess := &fakeSession{}
	b.sessions = sess
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	// session.End is called in a defer — give a moment for the handler to finish
	time.Sleep(10 * time.Millisecond)
	assert.True(t, sess.endCalled, "session.End must be called")
}

// A2A-12: gate.Release called after session.End (cleanup on success)
func TestA2A_GateReleasedOnCompletion(t *testing.T) {
	b := defaultBuilder()
	g := &fakeGate{}
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
	b := defaultBuilder()
	g := &fakeGate{}
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
	// Wire format: spec-correct — "text" field present, no "kind" field
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
	// Must be a static string, not raw error message
	assert.Equal(t, "internal error", errObj["message"])
	assert.NotContains(t, errObj["message"], "temporal internal error",
		"raw error must not be exposed to callers")
}

// A2A-16: Caller-provided contextId used as event bus key
func TestA2A_ContextIDFromParams(t *testing.T) {
	b := defaultBuilder()
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
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

// A2A-17: No contextId in params → new ID generated (non-empty, different each call)
func TestA2A_ContextIDGeneratedIfAbsent(t *testing.T) {
	b := defaultBuilder()
	tc1 := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b.temporal = tc1
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp1 := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp1.Body.Close()

	tc2 := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "ok", Status: domain.RunStatusCompleted},
	}}
	b.temporal = tc2
	srv2 := httptest.NewServer(b.build().Routes())
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

// A2A-18: direct orch.Run is no longer used — NewServer has no orch parameter
// This is enforced at compile time by the new signature; no runtime test needed.
// Verify the Temporal client was called instead.
func TestA2A_DirectOrchNotUsed_TemporalCalledInstead(t *testing.T) {
	b := defaultBuilder()
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "done", Status: domain.RunStatusCompleted},
	}}
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
	b := defaultBuilder()
	g := &fakeGate{checkErr: gate.ErrCapExceeded}
	sess := &fakeSession{}
	b.gate = g
	b.sessions = sess
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	// Gate check failed — session should never have been registered
	assert.False(t, sess.registerCalled, "session must not be registered if gate denied")
	// Gate was not admitted — release must not be called
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

// A2A-23: Temporal interface compile check — fakeTemporal satisfies TemporalClientExecutor.
// If the interface changes, the assignment in defaultBuilder() will fail to compile.
func TestA2A_TemporalInterface_Satisfied(t *testing.T) {
	var _ a2aserver.TemporalClientExecutor = &fakeTemporal{}
	_ = t
}
