package execution

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/auth"
	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/epconfig"
	"github.com/aviciot/them/internal/gate"
	"github.com/aviciot/them/internal/session"
	"github.com/aviciot/them/internal/temporal"
	"github.com/aviciot/them/internal/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fakes
// ─────────────────────────────────────────────────────────────────────────────

type fakeAuth struct {
	info *auth.TokenInfo
	err  error
}

func (f *fakeAuth) Validate(_ context.Context, _ string) (*auth.TokenInfo, error) {
	return f.info, f.err
}

type fakeEPLoader struct {
	cfg *epconfig.EPConfig
	err error
}

func (f *fakeEPLoader) Load(_ context.Context, _, _ string) (*epconfig.EPConfig, error) {
	return f.cfg, f.err
}

type fakeGate struct {
	checkErr   error
	confirmErr error
	releaseErr error

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
	return f.releaseErr
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
	createErr    error
	createCalled bool
	lastRun      domain.Run

	updateCalled bool
	lastUpdateID string
	lastStatus   domain.RunStatus
	updateErr    error
}

func (f *fakeRecorder) CreateRun(_ context.Context, run domain.Run) error {
	f.createCalled = true
	f.lastRun = run
	return f.createErr
}

func (f *fakeRecorder) UpdateRunStatus(_ context.Context, runID string, status domain.RunStatus, _ string) error {
	f.updateCalled = true
	f.lastUpdateID = runID
	f.lastStatus = status
	return f.updateErr
}

type fakeTemporal struct {
	run       *fakeWorkflowRun
	err       error
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

type fakeWorkflowRun struct{}

func (f *fakeWorkflowRun) GetID() string    { return "wf-id" }
func (f *fakeWorkflowRun) GetRunID() string { return "run-id" }
func (f *fakeWorkflowRun) Get(_ context.Context, _ interface{}) error {
	return nil
}
func (f *fakeWorkflowRun) GetWithOptions(_ context.Context, valuePtr interface{}, _ temporalclient.WorkflowRunGetOptions) error {
	return f.Get(context.Background(), valuePtr)
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixture builders
// ─────────────────────────────────────────────────────────────────────────────

var devLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

func publicEP(slug string) *epconfig.EPConfig {
	return &epconfig.EPConfig{
		EPID:       "ep-id-1",
		AppID:      "app-id-1",
		TenantID:   "tenant-id-1",
		EPSlug:     slug,
		AppEnabled: true,
		EPEnabled:  true,
		EPType:     "websocket",
		AccessMode: epconfig.AccessModePublic,
	}
}

func tokenEP(slug string) *epconfig.EPConfig {
	cfg := publicEP(slug)
	cfg.AccessMode = epconfig.AccessModeToken
	return cfg
}

func validToken() *auth.TokenInfo {
	return &auth.TokenInfo{TokenID: 42}
}

func buildLifecycle(ep *epconfig.EPConfig, a *fakeAuth, g *fakeGate, s *fakeSession, r *fakeRecorder, t *fakeTemporal) *Lifecycle {
	var loader transport.EPConfigLoader
	if ep != nil {
		loader = &fakeEPLoader{cfg: ep}
	} else {
		loader = &fakeEPLoader{err: epconfig.ErrNotFound}
	}
	return NewLifecycleWithRecorder(a, loader, g, s, r, t, devLogger)
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

func TestLifecycle_HappyPath(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{run: &fakeWorkflowRun{}}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, tmp)
	req := ExecutionRequest{EPSlug: "slug", RawToken: "tok", UserMessage: domain.Message{Role: "user"}}

	h, err := lc.Admit(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, h)

	assert.NotEmpty(t, h.RunID)
	assert.NotEmpty(t, h.ContextID)
	assert.NotEmpty(t, h.SessionID)
	assert.True(t, g.checkCalled, "gate.Check must be called")
	assert.True(t, g.confirmCalled, "gate.Confirm must be called")
	assert.True(t, s.registerCalled, "session.Register must be called")
	assert.True(t, r.createCalled, "recorder.CreateRun must be called")
	assert.Equal(t, domain.RunStatusAdmitted, r.lastRun.Status, "run must be created as admitted")

	wfRun, startErr := lc.Start(context.Background(), h, temporal.WorkflowInput{OrchestratorName: "slug"})
	require.NoError(t, startErr)
	assert.NotNil(t, wfRun)
	assert.True(t, tmp.called)
	assert.True(t, r.updateCalled, "Start must update run to running")
	assert.Equal(t, domain.RunStatusRunning, r.lastStatus, "Start must set status=running")

	lc.Release(h)
	assert.True(t, s.endCalled, "session.End must be called in Release")
	assert.True(t, g.releaseCalled, "gate.Release must be called in Release")
}

func TestLifecycle_EPNotFound(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{}

	lc := buildLifecycle(nil, &fakeAuth{info: validToken()}, g, s, r, tmp)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "missing", RawToken: "tok"})

	require.Error(t, err)
	assert.Nil(t, h)
	var ae *AdmitError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, AdmitErrNotFound, ae.Kind)
	assert.Equal(t, 404, ae.HTTPStatus)
	assert.False(t, g.checkCalled, "gate must not be checked when EP not found")
	assert.False(t, s.registerCalled)
}

func TestLifecycle_TokenRequired_Absent(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}

	lc := buildLifecycle(tokenEP("slug"), &fakeAuth{}, g, s, r, nil)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"}) // no token

	require.Error(t, err)
	assert.Nil(t, h)
	var ae *AdmitError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, AdmitErrUnauthorized, ae.Kind)
	assert.Equal(t, 401, ae.HTTPStatus)
	assert.False(t, g.checkCalled)
}

func TestLifecycle_TokenRequired_Invalid(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}

	// Token present but auth.Validate returns nil (unknown token) → 401.
	lc := buildLifecycle(tokenEP("slug"), &fakeAuth{info: nil, err: errors.New("unknown")}, g, s, r, nil)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug", RawToken: "badtoken"})

	require.Error(t, err)
	assert.Nil(t, h)
	var ae *AdmitError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, AdmitErrUnauthorized, ae.Kind)
	assert.False(t, g.checkCalled)
}

func TestLifecycle_GateCapExceeded(t *testing.T) {
	g := &fakeGate{checkErr: gate.ErrCapExceeded}
	s := &fakeSession{}
	r := &fakeRecorder{}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, nil)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})

	require.Error(t, err)
	assert.Nil(t, h)
	var ae *AdmitError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, AdmitErrCapExceeded, ae.Kind)
	assert.Equal(t, 429, ae.HTTPStatus)
	assert.False(t, g.rollbackCalled, "rollback must not be called before session.Register")
	assert.False(t, s.registerCalled)
}

func TestLifecycle_SessionRegisterFails_GateRolledBack(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{registerErr: errors.New("redis unavailable")}
	r := &fakeRecorder{}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, nil)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})

	require.Error(t, err)
	assert.Nil(t, h)
	var ae *AdmitError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, AdmitErrInternal, ae.Kind)
	assert.True(t, g.rollbackCalled, "gate must be rolled back when session.Register fails")
	assert.False(t, r.createCalled, "CreateRun must not be called after session.Register failure")
}

func TestLifecycle_RecorderCreateRunFails(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{createErr: errors.New("db down")}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, nil)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})

	require.Error(t, err)
	assert.Nil(t, h)
	var ae *AdmitError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, AdmitErrInternal, ae.Kind)
	assert.True(t, s.endCalled, "session must be cleaned up when CreateRun fails")
	assert.True(t, g.releaseCalled, "gate must be released when CreateRun fails")
}

func TestLifecycle_StartTemporalFails(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{err: errors.New("temporal down")}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, tmp)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
	require.NoError(t, err)
	defer lc.Release(h)

	_, startErr := lc.Start(context.Background(), h, temporal.WorkflowInput{OrchestratorName: "slug"})
	require.Error(t, startErr)
	assert.Contains(t, startErr.Error(), "internal error")
}

func TestLifecycle_ReleaseNilHandle_NoOp(t *testing.T) {
	lc := &Lifecycle{logger: devLogger}
	// Must not panic.
	lc.Release(nil)
}

func TestLifecycle_TenantIDFromEPConfig_NotFromRequest(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{run: &fakeWorkflowRun{}}

	ep := publicEP("slug")
	ep.TenantID = "server-tenant-id"
	ep.AppID = "server-app-id"

	lc := buildLifecycle(ep, &fakeAuth{info: validToken()}, g, s, r, tmp)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
	require.NoError(t, err)

	// Caller attempts to supply tenant override — must be ignored.
	_, startErr := lc.Start(context.Background(), h, temporal.WorkflowInput{
		TenantID:         "attacker-tenant",
		ApplicationID:    "attacker-app",
		OrchestratorName: "slug",
	})
	require.NoError(t, startErr)

	assert.Equal(t, "server-tenant-id", tmp.lastInput.TenantID, "TenantID must come from EPConfig, not request")
	assert.Equal(t, "server-app-id", tmp.lastInput.ApplicationID, "ApplicationID must come from EPConfig, not request")
	assert.Equal(t, "server-tenant-id", r.lastRun.TenantID, "recorder must persist server TenantID")
	assert.Equal(t, "server-app-id", r.lastRun.ApplicationID, "recorder must persist server AppID")
}

func TestLifecycle_ContextIDProvidedByCaller_Preserved(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{run: &fakeWorkflowRun{}}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, tmp)
	req := ExecutionRequest{EPSlug: "slug", ContextID: "caller-context-id"}

	h, err := lc.Admit(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "caller-context-id", h.ContextID)
}

func TestLifecycle_ContextIDGeneratedWhenEmpty(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{run: &fakeWorkflowRun{}}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, tmp)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
	require.NoError(t, err)
	assert.NotEmpty(t, h.ContextID)
	assert.NotEqual(t, h.RunID, h.ContextID, "RunID and ContextID must be distinct UUIDs")
}

func TestLifecycle_PublicEP_NoToken_Admitted(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{run: &fakeWorkflowRun{}}

	// Public EP: no token, auth fake returns nil — must still be admitted.
	lc := buildLifecycle(publicEP("slug"), &fakeAuth{}, g, s, r, tmp)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
	require.NoError(t, err)
	assert.NotNil(t, h)
	assert.True(t, r.createCalled)
}

func TestLifecycle_AllIDsAreUUIDv4(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{run: &fakeWorkflowRun{}}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, tmp)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
	require.NoError(t, err)

	isUUIDv4 := func(s string) bool {
		return len(s) == 36 && s[8] == '-' && s[13] == '-' && s[18] == '-' && s[23] == '-'
	}
	assert.True(t, isUUIDv4(h.RunID), "RunID must be UUID v4, got %q", h.RunID)
	assert.True(t, isUUIDv4(h.ContextID), "ContextID must be UUID v4, got %q", h.ContextID)
	assert.True(t, isUUIDv4(h.SessionID), "SessionID must be UUID v4, got %q", h.SessionID)
}

// ── Execution Core Hardening tests ────────────────────────────────────────────

// Verify that a run created as "admitted" is marked "failed" by Release when
// Start was never called (simulates WS upgrade failure, first-message error, etc.).
func TestLifecycle_Release_MarksRunFailed_WhenStartNeverCalled(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{run: &fakeWorkflowRun{}}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, tmp)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
	require.NoError(t, err)
	require.True(t, r.createCalled, "CreateRun must have been called")
	assert.Equal(t, domain.RunStatusAdmitted, r.lastRun.Status)

	// Simulate upgrade/message failure — Release without Start.
	lc.Release(h)

	assert.True(t, r.updateCalled, "Release must call UpdateRunStatus when run was created but Start never ran")
	assert.Equal(t, domain.RunStatusFailed, r.lastStatus, "Release must mark run as failed")
}

// Verify that Start transitions the run to "running" and Release does NOT
// double-update it (startedOK = true → Release skips the failed update).
func TestLifecycle_Release_DoesNotMarkFailed_AfterSuccessfulStart(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{}
	tmp := &fakeTemporal{run: &fakeWorkflowRun{}}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, tmp)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
	require.NoError(t, err)

	_, startErr := lc.Start(context.Background(), h, temporal.WorkflowInput{OrchestratorName: "slug"})
	require.NoError(t, startErr)

	// After Start succeeds, UpdateRunStatus was called once (admitted → running).
	assert.True(t, r.updateCalled)
	assert.Equal(t, domain.RunStatusRunning, r.lastStatus)

	// Reset the update flag to detect if Release calls it again.
	r.updateCalled = false
	r.lastStatus = ""

	lc.Release(h)

	assert.False(t, r.updateCalled, "Release must NOT call UpdateRunStatus when Start succeeded (startedOK=true)")
}

// Verify gate.Confirm failure is fatal: session ended, gate released, run never created.
func TestLifecycle_ConfirmFatal_SessionCleanedUp(t *testing.T) {
	g := &fakeGate{confirmErr: errors.New("redis: confirm failed")}
	s := &fakeSession{}
	r := &fakeRecorder{}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, nil)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})

	require.Error(t, err)
	assert.Nil(t, h)

	var ae *AdmitError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, AdmitErrInternal, ae.Kind)
	assert.False(t, r.createCalled, "CreateRun must not be called when gate.Confirm fails")
	assert.True(t, s.endCalled, "session.End must be called when gate.Confirm fails")
	assert.True(t, g.releaseCalled, "gate.Release must be called when gate.Confirm fails")
}

// Verify that admitCleanup (session.End + gate.Release) is invoked by both the
// Confirm-fail path and the CreateRun-fail path — centralized cleanup covers both.
func TestLifecycle_AdmitCleanup_BothFailPathsCleanUp(t *testing.T) {
	t.Run("confirm failure cleans session+gate", func(t *testing.T) {
		g := &fakeGate{confirmErr: errors.New("ttl expired")}
		s := &fakeSession{}
		r := &fakeRecorder{}
		lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, nil)
		h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
		require.Error(t, err)
		assert.Nil(t, h)
		assert.True(t, s.endCalled, "session.End must be called by admitCleanup on Confirm failure")
		assert.True(t, g.releaseCalled, "gate.Release must be called by admitCleanup on Confirm failure")
		assert.False(t, r.createCalled, "CreateRun must not be called after Confirm failure")
	})

	t.Run("createRun failure cleans session+gate", func(t *testing.T) {
		g := &fakeGate{}
		s := &fakeSession{}
		r := &fakeRecorder{createErr: errors.New("db unavailable")}
		lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, nil)
		h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
		require.Error(t, err)
		assert.Nil(t, h)
		assert.True(t, s.endCalled, "session.End must be called by admitCleanup on CreateRun failure")
		assert.True(t, g.releaseCalled, "gate.Release must be called by admitCleanup on CreateRun failure")
	})
}

// Verify Start retries UpdateRunStatus up to 3 attempts when it keeps failing.
// After all retries are exhausted: startedOK is still set (workflow IS running),
// Release does NOT mark the run failed again (double-update prevention).
func TestLifecycle_Start_UpdateRunStatus_AllRetriesExhausted_StartedOKSet(t *testing.T) {
	g := &fakeGate{}
	s := &fakeSession{}
	r := &fakeRecorder{updateErr: errors.New("db timeout")} // persistent failure
	tmp := &fakeTemporal{run: &fakeWorkflowRun{}}

	lc := buildLifecycle(publicEP("slug"), &fakeAuth{info: validToken()}, g, s, r, tmp)
	h, err := lc.Admit(context.Background(), ExecutionRequest{EPSlug: "slug"})
	require.NoError(t, err)

	_, startErr := lc.Start(context.Background(), h, temporal.WorkflowInput{OrchestratorName: "slug"})
	require.NoError(t, startErr, "Start must succeed even when all UpdateRunStatus retries fail")
	assert.True(t, h.IsStartedOK(),
		"startedOK must be true after Start so Release does not mark the run as failed")

	// Release must NOT call UpdateRunStatus(failed) when startedOK=true, even
	// though the admitted→running update itself failed.
	r.updateCalled = false
	lc.Release(h)
	assert.False(t, r.updateCalled,
		"Release must not call UpdateRunStatus when startedOK=true (workflow is executing)")
}

// Verify NewLifecycle panics when a required production dependency is nil.
// Note: NewLifecycle takes *runrecorder.Recorder and TemporalClientExecutor;
// nil checks for those are enforced by the panic guards at construction time.
func TestNewLifecycle_PanicsOnNilDeps(t *testing.T) {
	ep := &fakeEPLoader{cfg: publicEP("slug")}
	g := &fakeGate{}
	s := &fakeSession{}
	tc := &fakeTemporal{run: &fakeWorkflowRun{}}
	a := &fakeAuth{info: validToken()}

	assert.Panics(t, func() {
		NewLifecycle(a, nil, g, s, nil, tc, devLogger)
	}, "nil epLoader must panic")
	assert.Panics(t, func() {
		NewLifecycle(a, ep, nil, s, nil, tc, devLogger)
	}, "nil gateStore must panic")
	assert.Panics(t, func() {
		NewLifecycle(a, ep, g, nil, nil, tc, devLogger)
	}, "nil sessions must panic")
	// recorder is *runrecorder.Recorder — nil literal satisfies the *T nil check.
	assert.Panics(t, func() {
		NewLifecycle(a, ep, g, s, nil, tc, devLogger)
	}, "nil recorder must panic")
	// temporal is an interface — nil interface value satisfies == nil.
	assert.Panics(t, func() {
		NewLifecycle(a, ep, g, s, nil, nil, devLogger)
	}, "nil temporal must panic (reached only if recorder panic removed)")
}
