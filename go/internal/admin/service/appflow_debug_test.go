package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/execution"
)

// minimalDraftDoc is a one-entry-point, one-agent-node canvas definition that
// compiles and validates cleanly (mirrors internal/appflow/compiler_test.go's
// TestValidate_ValidSpec). Carries its own "_resolved_agent_ids" stamp —
// normally written by PublishDefinition at publish time
// (appflow.ResolveAgentByInstanceID's doc comment) — since a debug run must
// work against a draft that was published at least once, then edited; a
// draft that has NEVER been published has no such stamp and would correctly
// fail Validate with "unresolved_agent" (a real, documented limitation, not
// tested here).
const minimalDraftDoc = `{
	"schema_version": 2,
	"_resolved_agent_ids": {"agent_1": "agent-uuid-1"},
	"components": [
		{
			"instance_id": "agent_1",
			"definition_ref": {"kind":"agent","namespace":"default","name":"echo","version":1},
			"definition_id": "agent-uuid-1"
		}
	],
	"entry_points": [
		{"instance_id":"ep_ws_1","slug":"chat","protocol":"websocket","root":"agent_1"}
	],
	"connections": []
}`

type fakeAppFlowDebugDAL struct {
	app      dal.Application
	appErr   error
	draft    dal.AppDefinition
	draftErr error
}

func (f *fakeAppFlowDebugDAL) GetApplication(_ context.Context, _, _ string) (dal.Application, error) {
	return f.app, f.appErr
}

func (f *fakeAppFlowDebugDAL) GetLatestDraftDefinition(_ context.Context, _, _ string) (dal.AppDefinition, error) {
	return f.draft, f.draftErr
}

type fakeAppFlowDebugStarter struct {
	handle      *execution.ExecutionHandle
	admitErr    error
	startErr    error
	admitCalled bool
	startCalled bool
	lastInput   appflow.AppFlowWorkflowInput
	lastDebug   bool
}

func (f *fakeAppFlowDebugStarter) AdmitDebug(_ context.Context, _, _, _ string, _ int64) (*execution.ExecutionHandle, error) {
	f.admitCalled = true
	return f.handle, f.admitErr
}

func (f *fakeAppFlowDebugStarter) StartAppFlow(_ context.Context, _ *execution.ExecutionHandle, input appflow.AppFlowWorkflowInput, debug bool) (temporalclient.WorkflowRun, error) {
	f.startCalled = true
	f.lastInput = input
	f.lastDebug = debug
	return nil, f.startErr
}

func draftApp(epSlug string) dal.Application {
	return dal.Application{
		ID:          "app-1",
		Slug:        "my-app",
		EntryPoints: []dal.EntryPoint{{Slug: epSlug}},
	}
}

// Happy path: draft compiles, AdmitDebug + StartAppFlow(debug=true) both fire.
func TestAppFlowDebugService_Start_HappyPath(t *testing.T) {
	d := &fakeAppFlowDebugDAL{
		app:   draftApp("chat"),
		draft: dal.AppDefinition{Definition: json.RawMessage(minimalDraftDoc), Status: "draft"},
	}
	lc := &fakeAppFlowDebugStarter{handle: &execution.ExecutionHandle{RunID: "run-1"}}
	svc := NewAppFlowDebugService(d, lc)

	result, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hello", 7)
	require.NoError(t, err)
	assert.Equal(t, "run-1", result.RunID)
	assert.True(t, lc.admitCalled)
	assert.True(t, lc.startCalled)
	assert.True(t, lc.lastDebug, "must always start with debug=true")
	assert.Equal(t, "hello", lc.lastInput.UserMessage)
	require.NotNil(t, lc.lastInput.Spec)
	require.Len(t, lc.lastInput.Spec.EntryPoints, 1)
	assert.Equal(t, "chat", lc.lastInput.Spec.EntryPoints[0].Slug)
}

// Application not found (wrong tenant, or doesn't exist) → ErrNotFound.
func TestAppFlowDebugService_Start_AppNotFound(t *testing.T) {
	d := &fakeAppFlowDebugDAL{appErr: pgx.ErrNoRows}
	lc := &fakeAppFlowDebugStarter{}
	svc := NewAppFlowDebugService(d, lc)

	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
	assert.False(t, lc.admitCalled, "must not admit a run when the app can't be resolved")
}

// Entry point slug doesn't exist on the application → ErrNotFound, no draft fetch.
func TestAppFlowDebugService_Start_EPSlugNotFound(t *testing.T) {
	d := &fakeAppFlowDebugDAL{app: draftApp("other-slug")}
	lc := &fakeAppFlowDebugStarter{}
	svc := NewAppFlowDebugService(d, lc)

	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

// No draft saved yet → 422 unprocessable, not 404 (distinct from "app not found").
func TestAppFlowDebugService_Start_NoDraftSaved(t *testing.T) {
	d := &fakeAppFlowDebugDAL{app: draftApp("chat"), draftErr: pgx.ErrNoRows}
	lc := &fakeAppFlowDebugStarter{}
	svc := NewAppFlowDebugService(d, lc)

	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnprocessable))
}

// Entry point exists on the application row but not in the draft's own compiled
// entry points (draft edited since — the two sources can disagree) → unprocessable.
func TestAppFlowDebugService_Start_EPMissingFromCompiledDraft(t *testing.T) {
	d := &fakeAppFlowDebugDAL{
		app:   draftApp("chat"),
		draft: dal.AppDefinition{Definition: json.RawMessage(minimalDraftDoc), Status: "draft"},
	}
	lc := &fakeAppFlowDebugStarter{}
	svc := NewAppFlowDebugService(d, lc)

	// minimalDraftDoc only defines entry point "chat", but the application row
	// (draftApp) is stubbed with a matching slug — force a mismatch by asking
	// for a slug absent from the compiled spec.
	d.app = dal.Application{ID: "app-1", Slug: "my-app", EntryPoints: []dal.EntryPoint{{Slug: "stale-slug"}}}
	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "stale-slug", "hi", 7)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnprocessable))
	assert.False(t, lc.admitCalled)
}
