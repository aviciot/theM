package a2a_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

func (f *fakeEPLoader) Load(_ context.Context, _, _, _ string) (*epconfig.EPConfig, error) {
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

func (f *fakeRecorder) UpdateRunGoal(_ context.Context, _, _ string) error { return nil }
func (f *fakeRecorder) UpdateRunStatus(_ context.Context, _ string, _ domain.RunStatus, _ string) error {
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
	// bus and contextID are set by stream tests so ExecuteWorkflow can publish
	// the bus "done" event that handleMessageStream waits for as its terminal signal.
	bus       *event.InMemoryBus
	contextID string

	called    bool
	lastInput temporal.WorkflowInput
}

func (f *fakeTemporal) ExecuteWorkflow(_ context.Context, _ temporalclient.StartWorkflowOptions, _ interface{}, args ...interface{}) (temporalclient.WorkflowRun, error) {
	f.called = true
	if len(args) > 0 {
		if inp, ok := args[0].(temporal.WorkflowInput); ok {
			f.lastInput = inp
			// Capture the context ID stamped by Lifecycle.Start so the bus publish
			// reaches the correct subscriber in stream tests.
			if f.contextID == "" {
				f.contextID = inp.ContextID
			}
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	// Publish done on the bus so handleMessageStream terminates cleanly.
	// In non-stream tests the bus has no subscriber for this topic — safe to publish.
	if f.bus != nil && f.contextID != "" {
		raw, _ := json.Marshal(map[string]string{"run_id": f.lastInput.RunID})
		f.bus.Publish(context.Background(), event.Event{
			Topic:   f.contextID,
			Type:    "done",
			Payload: raw,
		})
	}
	return f.run, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture builders
// ─────────────────────────────────────────────────────────────────────────────

var devLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

func tokenEPConfig() *epconfig.EPConfig {
	return &epconfig.EPConfig{
		EPID:              "ep-uuid-1",
		AppID:             "app-uuid-1",
		TenantID:          "tenant-uuid-1",
		EPSlug:            "myapp",
		EPType:            "a2a",
		EPEnabled:         true,
		AppEnabled:        true,
		AccessMode:        epconfig.AccessModeToken,
		// SEC-04: OrchestratorName comes from app_orchestrators.name via EP binding,
		// never from the EP slug. Tests use a representative orchestrator name.
		AppOrchestratorID: "orch-uuid-1",
		OrchestratorName:  "test-orchestrator",
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

type builtServer struct {
	*a2aserver.Server
	bus *event.InMemoryBus
}

func (b *serverBuilder) build() builtServer {
	bus := event.New()
	lc := execution.NewLifecycleWithRecorder(
		b.auth, b.epLoader, b.gate, b.sessions,
		b.recorder, b.temporal, devLogger,
	)
	return builtServer{
		Server: a2aserver.NewServer(lc, bus, b.auth, "test-instance", devLogger),
		bus:    bus,
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// HTTP helpers
// ─────────────────────────────────────────────────────────────────────────────

func postRPC(t *testing.T, srv *httptest.Server, body any, token string) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/a2a/myapp/ep1", bytes.NewReader(data))
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
		"method":  "SendMessage",
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
			"messageId": "uuid-attacker-1", // required by A2A v1.0 spec
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
	// OrchestratorName must come from app_orchestrators.name via EP binding (SEC-04),
	// not from the EP slug.
	assert.Equal(t, "test-orchestrator", tc.lastInput.OrchestratorName)
	assert.Equal(t, "orch-uuid-1", tc.lastInput.AppOrchestratorID)
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

// A2A-14: Successful workflow → SDK Task with "completed" state + text artifact.
// The SDK wraps the result in StreamResponse: {"task": {id, contextId, status, artifacts}}.
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

	// SDK wraps result in StreamResponse: {"task": {...}}
	result := rpcResp["result"].(map[string]any)
	task := result["task"].(map[string]any)
	status := task["status"].(map[string]any)
	assert.Equal(t, "TASK_STATE_COMPLETED", status["state"])

	artifacts, _ := task["artifacts"].([]any)
	require.Len(t, artifacts, 1)
	parts := artifacts[0].(map[string]any)["parts"].([]any)
	require.Len(t, parts, 1)
	part := parts[0].(map[string]any)
	assert.Equal(t, "hello from orchestrator", part["text"])
	assert.Nil(t, part["kind"], "wire format must not include 'kind' field (non-spec)")
}

// A2A-14b: task id present in result (SDK Task uses "id" field, not "taskId")
func TestA2A_RPCResult_HasTaskID(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))

	// SDK wraps result in StreamResponse: {"task": {"id": "...", ...}}
	result := rpcResp["result"].(map[string]any)
	task := result["task"].(map[string]any)
	taskID, ok := task["id"]
	assert.True(t, ok, "task must contain id field")
	assert.NotEmpty(t, taskID, "task id must be non-empty")
}

// A2A-15: Failed workflow → SDK Task with status.state == "failed".
// With the SDK, the executor yields a failed status event; the SDK returns a Task
// (not a JSON-RPC error). The raw error is never exposed to callers.
func TestA2A_RPCError_WorkflowFailed(t *testing.T) {
	b := defaultBuilder()
	b.temporal = &fakeTemporal{run: &fakeWorkflowRun{err: errors.New("temporal internal error")}}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	// SDK returns HTTP 200 with a Task in failed state (not an HTTP-level error).
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))

	// No raw error in the JSON-RPC result.
	assert.Nil(t, rpcResp["error"])
	result := rpcResp["result"].(map[string]any)
	task := result["task"].(map[string]any)
	status := task["status"].(map[string]any)
	assert.Equal(t, "TASK_STATE_FAILED", status["state"], "failed workflow must produce task with state=failed")
	// Verify the raw error string is not leaked anywhere.
	respBytes, _ := json.Marshal(rpcResp)
	assert.NotContains(t, string(respBytes), "temporal internal error",
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
			"messageId": "uuid-ctx-test-1", // required by A2A v1.0 spec
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

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/a2a/myapp/ep1", bytes.NewReader([]byte(`{not valid json`)))
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

// A2A-22: Temporal client nil → executor yields failed status → SDK returns task with state=failed.
// With the SDK executor model, "temporal not configured" becomes a workflow start error
// that the executor maps to TaskStateFailed. The SDK returns HTTP 200 with a failed task
// (the HTTP-level 503 path was only present in the hand-rolled handler).
func TestA2A_TemporalNotConfigured_503(t *testing.T) {
	b := defaultBuilder()
	b.temporal = nil
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()
	// SDK path: HTTP 200 + task with state=failed (not HTTP 503).
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))
	assert.Nil(t, rpcResp["error"])
	result, _ := rpcResp["result"].(map[string]any)
	task, _ := result["task"].(map[string]any)
	status, _ := task["status"].(map[string]any)
	assert.Equal(t, "TASK_STATE_FAILED", status["state"])
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

// ─────────────────────────────────────────────────────────────────────────────
// message/stream tests
// ─────────────────────────────────────────────────────────────────────────────

func validStreamBody() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "SendStreamingMessage",
		"params": map[string]any{
			"message": map[string]any{
				"role":      "user",
				"parts":     []map[string]any{{"text": "hi"}},
				"messageId": "uuid-stream-1",
			},
		},
		"id": "stream-req-1",
	}
}

// postStream posts a message/stream request and returns the raw SSE body lines.
func postStream(t *testing.T, srv *httptest.Server, body any, token string) (int, []string) {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/a2a/myapp/ep1", bytes.NewReader(data))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}

	raw, _ := io.ReadAll(resp.Body)
	var lines []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return resp.StatusCode, lines
}

// streamBuilder returns a builtServer where fakeTemporal is wired to the bus
// so ExecuteWorkflow publishes the "done" bus event that handleMessageStream
// waits for as its terminal signal.
func streamBuilder() (*serverBuilder, func(bs builtServer)) {
	tc := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "hello from orchestrator", Status: domain.RunStatusCompleted},
	}}
	b := defaultBuilder()
	b.temporal = tc
	wire := func(bs builtServer) { tc.bus = bs.bus }
	return b, wire
}

// A2A-S01: message/stream → 200 + text/event-stream content type
func TestA2AStream_ContentType(t *testing.T) {
	b, wire := streamBuilder()
	bs := b.build()
	wire(bs)
	srv := httptest.NewServer(bs.Routes())
	defer srv.Close()

	data, _ := json.Marshal(validStreamBody())
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/a2a/myapp/ep1", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer valid-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")
}

// A2A-S02: message/stream success → emits a statusUpdate completed event.
// SDK SSE format: data: {"jsonrpc":"2.0","id":...,"result":{"statusUpdate":{"status":{"state":"completed",...}}}}
func TestA2AStream_EmitsCompletedStatus(t *testing.T) {
	b, wire := streamBuilder()
	bs := b.build()
	wire(bs)
	srv := httptest.NewServer(bs.Routes())
	defer srv.Close()

	httpStatus, lines := postStream(t, srv, validStreamBody(), "valid-token")
	require.Equal(t, http.StatusOK, httpStatus)

	var foundCompleted bool
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			continue
		}
		// SDK SSE events are full JSON-RPC responses:
		// {"jsonrpc":"2.0","id":...,"result":{"statusUpdate":{...}}}
		result, _ := ev["result"].(map[string]any)
		statusUpdate, _ := result["statusUpdate"].(map[string]any)
		if statusUpdate != nil {
			s, _ := statusUpdate["status"].(map[string]any)
			if s["state"] == "TASK_STATE_COMPLETED" {
				foundCompleted = true
			}
		}
	}
	assert.True(t, foundCompleted, "expected a statusUpdate completed event in the SSE stream")
}

// A2A-S03: message/stream with missing token on token EP → 401 (clean HTTP, no SSE started)
func TestA2AStream_MissingToken_401(t *testing.T) {
	b := defaultBuilder()
	b.epLoader = &fakeEPLoader{cfg: tokenEPConfig()}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	status, _ := postStream(t, srv, validStreamBody(), "")
	assert.Equal(t, http.StatusUnauthorized, status)
}

// A2A-S04: message/stream with unknown slug → 404
func TestA2AStream_UnknownSlug_404(t *testing.T) {
	b := defaultBuilder()
	b.epLoader = &fakeEPLoader{err: epconfig.ErrNotFound}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	status, _ := postStream(t, srv, validStreamBody(), "valid-token")
	assert.Equal(t, http.StatusNotFound, status)
}

// A2A-S05: message/stream — gate cap exceeded → 429
func TestA2AStream_CapExceeded_429(t *testing.T) {
	b := defaultBuilder()
	b.gate = &fakeGate{checkErr: gate.ErrCapExceeded}
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	status, _ := postStream(t, srv, validStreamBody(), "valid-token")
	assert.Equal(t, http.StatusTooManyRequests, status)
}

// A2A-S06: no text in parts → JSON-RPC error before admission (HTTP 200, error body).
// The pre-check runs before the SDK, so it returns our custom error regardless of method.
func TestA2AStream_NoText_RPCError(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	body := map[string]any{
		"jsonrpc": "2.0",
		"method":  "SendStreamingMessage",
		"params": map[string]any{
			"message": map[string]any{
				"role":  "user",
				"parts": []map[string]any{},
			},
		},
		"id": "stream-req-2",
	}
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/a2a/myapp/ep1", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer valid-token")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var rpcResp map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rpcResp))
	assert.NotNil(t, rpcResp["error"])
}

// A2A-S07: agent card advertises streaming: true
func TestA2A_AgentCard_StreamingTrue(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/a2a/myapp/ep1/.well-known/agent.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var card map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&card))
	caps, _ := card["capabilities"].(map[string]any)
	assert.Equal(t, true, caps["streaming"], "agent card must advertise streaming: true")
}

// A2A-S08: agent card URL uses WithPublicURL when set.
// SDK-built cards use supportedInterfaces[0].url per A2A v1.0 spec.
func TestA2A_AgentCard_WithPublicURL(t *testing.T) {
	b := defaultBuilder()
	s := b.build().WithPublicURL("https://example.com")
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/a2a/myapp/ep1/.well-known/agent.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	var card map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&card))
	// SDK-built cards: URL is in supportedInterfaces[0].url, not top-level "url".
	ifaces, _ := card["supportedInterfaces"].([]any)
	require.NotEmpty(t, ifaces, "card must have at least one supportedInterface")
	iface, _ := ifaces[0].(map[string]any)
	assert.Equal(t, "https://example.com/a2a/myapp/ep1", iface["url"])
}

// A2A-S09: agent card URL is derived from request host when publicURL is unset.
// SDK-built cards use supportedInterfaces[0].url per A2A v1.0 spec.
func TestA2A_AgentCard_DerivedURL(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/a2a/myapp/ep1/.well-known/agent.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	var card map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&card))
	// SDK-built cards: URL is in supportedInterfaces[0].url, not top-level "url".
	ifaces, _ := card["supportedInterfaces"].([]any)
	require.NotEmpty(t, ifaces, "card must have at least one supportedInterface")
	iface, _ := ifaces[0].(map[string]any)
	u, _ := iface["url"].(string)
	assert.Contains(t, u, "/a2a/myapp/ep1", "URL must contain app and ep slugs")
	assert.Contains(t, u, "http://", "URL must include http scheme when no X-Forwarded-Proto")
}

// ─── card loader tests ────────────────────────────────────────────────────────

// fakeCardLoader is a test double for CardLoader.
type fakeCardLoader struct {
	row a2aserver.EPCardRow
	err error
}

func (f *fakeCardLoader) LoadEPCard(_ context.Context, _, _ string) (a2aserver.EPCardRow, error) {
	return f.row, f.err
}

// A2A-S10: synthesized card served when CardLoader returns a stored card.
func TestA2A_AgentCard_SynthesizedCard(t *testing.T) {
	synthesized := map[string]any{
		"name":        "My App",
		"description": "Does cool things",
		"skills":      []any{},
	}
	cardBytes, _ := json.Marshal(synthesized)

	b := defaultBuilder()
	s := b.build().WithCardLoader(&fakeCardLoader{
		row: a2aserver.EPCardRow{AgentCardJSON: cardBytes, OrchestratorDisplayName: "ignored", AppName: "ignored"},
	})
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/a2a/myapp/ep1/.well-known/agent.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	var card map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&card))
	assert.Equal(t, "My App", card["name"])
	assert.Equal(t, "Does cool things", card["description"])
	// URL is always injected regardless of stored card content.
	assert.Contains(t, card["url"].(string), "/a2a/myapp/ep1")
	// Capabilities always present.
	caps, _ := card["capabilities"].(map[string]any)
	assert.Equal(t, true, caps["streaming"])
}

// A2A-S11: fallback card uses OrchestratorDisplayName when no card is synthesized yet.
// SDK-built fallback cards put URL in supportedInterfaces[0].url.
func TestA2A_AgentCard_FallbackToOrchName(t *testing.T) {
	b := defaultBuilder()
	s := b.build().WithCardLoader(&fakeCardLoader{
		row: a2aserver.EPCardRow{AgentCardJSON: nil, OrchestratorDisplayName: "My Orchestrator", AppName: "My App"},
	})
	srv := httptest.NewServer(s.Routes())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/a2a/myapp/ep1/.well-known/agent.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	var card map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&card))
	assert.Equal(t, "My Orchestrator", card["name"])
	// SDK-built fallback: URL in supportedInterfaces[0].url.
	ifaces, _ := card["supportedInterfaces"].([]any)
	require.NotEmpty(t, ifaces)
	iface, _ := ifaces[0].(map[string]any)
	assert.Contains(t, iface["url"].(string), "/a2a/myapp/ep1")
}

// A2A-S12: static fallback served when no CardLoader is configured.
func TestA2A_AgentCard_FallbackNoLoader(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes()) // no WithCardLoader
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/a2a/myapp/ep1/.well-known/agent.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	var card map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&card))
	assert.Equal(t, "the-M Orchestrator", card["name"])
}

// A2A-S13: "file" bus event is forwarded as an artifactUpdate SSE frame.
// SDK SSE format: data: {"jsonrpc":"2.0","id":...,"result":{"artifactUpdate":{"artifact":{...}}}}
func TestA2AStream_FileEventForwardedAsArtifactUpdate(t *testing.T) {
	inner := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "done", Status: domain.RunStatusCompleted},
	}}
	fileFake := &filePublishTemporal{delegate: inner}
	b := defaultBuilder()
	b.temporal = fileFake
	bs := b.build()
	fileFake.bus = bs.bus

	srv := httptest.NewServer(bs.Routes())
	defer srv.Close()

	httpStatus, lines := postStream(t, srv, validStreamBody(), "valid-token")
	require.Equal(t, http.StatusOK, httpStatus)

	var foundArtifact bool
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			continue
		}
		// SDK SSE events: {"jsonrpc":"2.0","id":...,"result":{"artifactUpdate":{"artifact":{...}}}}
		result, _ := ev["result"].(map[string]any)
		artifactUpdate, _ := result["artifactUpdate"].(map[string]any)
		if artifactUpdate == nil {
			continue
		}
		artifact, _ := artifactUpdate["artifact"].(map[string]any)
		if artifact == nil {
			continue
		}
		parts, _ := artifact["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			// SDK serializes URL parts as {"url": "..."} directly in the part object.
			if part["url"] == "https://example.com/report.pdf" {
				foundArtifact = true
			}
		}
	}
	assert.True(t, foundArtifact, "expected an artifactUpdate SSE frame with the file URL")
}

// filePublishTemporal publishes a "file" bus event then "done" when ExecuteWorkflow is called.
type filePublishTemporal struct {
	delegate *fakeTemporal
	bus      *event.InMemoryBus
}

func (f *filePublishTemporal) ExecuteWorkflow(ctx context.Context, opts temporalclient.StartWorkflowOptions, wf interface{}, args ...interface{}) (temporalclient.WorkflowRun, error) {
	// Suppress the delegate's auto-done publish so we control event ordering.
	origBus := f.delegate.bus
	f.delegate.bus = nil
	run, err := f.delegate.ExecuteWorkflow(ctx, opts, wf, args...)
	f.delegate.bus = origBus
	if err != nil || f.bus == nil {
		return run, err
	}
	contextID := f.delegate.contextID
	runID := f.delegate.lastInput.RunID
	bus := f.bus
	// Use a blocking workflow run so that wfRun.Get() waits until after bus events
	// are published and consumed. This prevents the wfCh path from firing first.
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		// Give handleMessageStream a moment to subscribe before publishing events.
		time.Sleep(20 * time.Millisecond)
		fileRaw, _ := json.Marshal(map[string]any{
			"artifact_id":  "art-001",
			"filename":     "report.pdf",
			"content_type": "application/pdf",
			"download_url": "https://example.com/report.pdf",
			"run_id":       runID,
		})
		doneRaw, _ := json.Marshal(map[string]string{"run_id": runID})
		bus.Publish(context.Background(), event.Event{Topic: contextID, Type: "file", Payload: fileRaw})
		bus.Publish(context.Background(), event.Event{Topic: contextID, Type: "done", Payload: doneRaw})
		// Small pause to let the executor consume the bus "done" event before unblocking Get.
		time.Sleep(10 * time.Millisecond)
	}()
	return &blockingWorkflowRun{inner: f.delegate.run, doneCh: doneCh}, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// A2A v1.0 wire format compliance tests (new — 3 required)
// ─────────────────────────────────────────────────────────────────────────────

// A2A-WF01: SendMessage returns a compliant A2A v1.0 task object.
// The result must be: {"jsonrpc":"2.0","id":...,"result":{"task":{...}}}
// with a Task that has id, contextId, status.state, and artifacts fields.
// The "kind" field must NOT appear anywhere in the response (non-spec invention).
func TestA2ASend_ResultIsSpecCompliant(t *testing.T) {
	b := defaultBuilder()
	srv := httptest.NewServer(b.build().Routes())
	defer srv.Close()

	resp := postRPC(t, srv, validSendBody(), "valid-token")
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var rpcResp map[string]any
	require.NoError(t, json.Unmarshal(body, &rpcResp))

	// Top-level JSON-RPC envelope.
	assert.Equal(t, "2.0", rpcResp["jsonrpc"])
	assert.Nil(t, rpcResp["error"], "compliant result must not have an error key")
	require.NotNil(t, rpcResp["result"])
	assert.NotNil(t, rpcResp["id"])

	// result must be {"task": {...}} — A2A v1.0 StreamResponse.
	result := rpcResp["result"].(map[string]any)
	require.NotNil(t, result["task"], "result must contain 'task' key per A2A v1.0 spec")

	task := result["task"].(map[string]any)
	assert.NotEmpty(t, task["id"], "task must have an id field")
	assert.NotEmpty(t, task["contextId"], "task must have a contextId field")

	status := task["status"].(map[string]any)
	assert.Equal(t, "TASK_STATE_COMPLETED", status["state"])

	// The non-spec "kind" field must not appear anywhere.
	assert.NotContains(t, string(body), `"kind"`,
		"'kind' is not an A2A v1.0 wire format field")
}

// A2A-WF02: SendStreamingMessage token event → artifactUpdate SSE frame.
// SDK SSE format: data: {"jsonrpc":"2.0","id":...,"result":{"artifactUpdate":{...}}}
// Tokens published via the bus must arrive as artifactUpdate events with text parts.
func TestA2AStream_TokenIsSpecCompliant(t *testing.T) {
	inner := &fakeTemporal{run: &fakeWorkflowRun{
		result: temporal.WorkflowResult{FinalText: "hello", Status: domain.RunStatusCompleted},
	}}
	tokenFake := &tokenPublishTemporal{delegate: inner}
	b := defaultBuilder()
	b.temporal = tokenFake
	bs := b.build()
	tokenFake.bus = bs.bus

	srv := httptest.NewServer(bs.Routes())
	defer srv.Close()

	httpStatus, lines := postStream(t, srv, validStreamBody(), "valid-token")
	require.Equal(t, http.StatusOK, httpStatus)

	var foundArtifact bool
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) != nil {
			continue
		}
		// Token events must produce artifactUpdate frames.
		result, _ := ev["result"].(map[string]any)
		artifactUpdate, _ := result["artifactUpdate"].(map[string]any)
		if artifactUpdate == nil {
			continue
		}
		artifact, _ := artifactUpdate["artifact"].(map[string]any)
		parts, _ := artifact["parts"].([]any)
		for _, p := range parts {
			part, _ := p.(map[string]any)
			if part["text"] == "streaming-token" {
				foundArtifact = true
			}
		}
	}
	assert.True(t, foundArtifact, "token bus event must produce an artifactUpdate SSE frame with the token text")
}

// tokenPublishTemporal publishes a "token" bus event then "done" after a short delay.
// The returned WorkflowRun blocks until the bus events have been published and consumed.
type tokenPublishTemporal struct {
	delegate *fakeTemporal
	bus      *event.InMemoryBus
}

// blockingWorkflowRun wraps fakeWorkflowRun.Get() to block until a signal is given.
// This ensures bus events are published and consumed before wfRun.Get() returns.
type blockingWorkflowRun struct {
	inner  *fakeWorkflowRun
	doneCh <-chan struct{}
}

func (b *blockingWorkflowRun) GetID() string    { return b.inner.GetID() }
func (b *blockingWorkflowRun) GetRunID() string { return b.inner.GetRunID() }
func (b *blockingWorkflowRun) Get(ctx context.Context, valuePtr interface{}) error {
	select {
	case <-b.doneCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	return b.inner.Get(ctx, valuePtr)
}
func (b *blockingWorkflowRun) GetWithOptions(ctx context.Context, valuePtr interface{}, opts temporalclient.WorkflowRunGetOptions) error {
	return b.Get(ctx, valuePtr)
}

func (f *tokenPublishTemporal) ExecuteWorkflow(ctx context.Context, opts temporalclient.StartWorkflowOptions, wf interface{}, args ...interface{}) (temporalclient.WorkflowRun, error) {
	// Suppress delegate's auto-done publish so we control event ordering.
	origBus := f.delegate.bus
	f.delegate.bus = nil
	_, err := f.delegate.ExecuteWorkflow(ctx, opts, wf, args...)
	f.delegate.bus = origBus
	if err != nil || f.bus == nil {
		return f.delegate.run, err
	}
	contextID := f.delegate.contextID
	runID := f.delegate.lastInput.RunID
	bus := f.bus

	// doneCh is closed after bus events are published — unblocks wfRun.Get().
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		time.Sleep(20 * time.Millisecond)
		tokenRaw, _ := json.Marshal(map[string]string{"content": "streaming-token"})
		doneRaw, _ := json.Marshal(map[string]string{"run_id": runID})
		bus.Publish(context.Background(), event.Event{Topic: contextID, Type: "token", Payload: tokenRaw})
		bus.Publish(context.Background(), event.Event{Topic: contextID, Type: "done", Payload: doneRaw})
		// Small pause to allow the executor to process the bus events before Get returns.
		time.Sleep(10 * time.Millisecond)
	}()
	return &blockingWorkflowRun{inner: f.delegate.run, doneCh: doneCh}, nil
}

// A2A-WF03: SendStreamingMessage final frame must be a statusUpdate completed event.
// After all artifact frames, the SDK must emit a statusUpdate with state=completed.
// This verifies proper stream termination per the A2A v1.0 spec.
func TestA2AStream_ArtifactUpdateIsSpecCompliant(t *testing.T) {
	b, wire := streamBuilder()
	bs := b.build()
	wire(bs)
	srv := httptest.NewServer(bs.Routes())
	defer srv.Close()

	httpStatus, lines := postStream(t, srv, validStreamBody(), "valid-token")
	require.Equal(t, http.StatusOK, httpStatus)

	var lastResult map[string]any
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) != nil {
			continue
		}
		result, _ := ev["result"].(map[string]any)
		if result != nil {
			lastResult = result
		}
	}
	require.NotNil(t, lastResult, "must receive at least one SSE result frame")

	// The final frame in the stream must be a statusUpdate with state=completed.
	statusUpdate, _ := lastResult["statusUpdate"].(map[string]any)
	require.NotNil(t, statusUpdate, "final SSE frame must be a statusUpdate (not artifactUpdate or raw task)")
	s, _ := statusUpdate["status"].(map[string]any)
	assert.Equal(t, "TASK_STATE_COMPLETED", s["state"],
		"final statusUpdate must have state=TASK_STATE_COMPLETED per A2A v1.0 spec")
}
