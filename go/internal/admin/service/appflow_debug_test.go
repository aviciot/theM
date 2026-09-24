package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	temporalclient "go.temporal.io/sdk/client"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/debugcred"
	"github.com/aviciot/them/internal/epconfig"
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

// twoLLMNodesDraftDoc has two independent inline LLM nodes on one entry point
// — used to prove docs/APPFLOW_RUNTIME_PARAMS_PLAN.md's per-node requirement:
// each node needs its own llm_overrides entry, never merged/deduped.
const twoLLMNodesDraftDoc = `{
	"schema_version": 2,
	"components": [
		{
			"instance_id": "llm_1",
			"definition_ref": {"kind":"inline","namespace":"builtin","name":"llm","version":1},
			"config": {"node_type":"llm","user_prompt":"{{.input}}","output_var":"a"}
		},
		{
			"instance_id": "llm_2",
			"definition_ref": {"kind":"inline","namespace":"builtin","name":"llm","version":1},
			"config": {"node_type":"llm","user_prompt":"{{.a}}","output_var":"b"}
		}
	],
	"entry_points": [
		{"instance_id":"ep1","slug":"chat","protocol":"websocket","root":"llm_1"}
	],
	"connections": [
		{"source":"ep1","target":"llm_1","type":"flow_control"},
		{"source":"llm_1","target":"llm_2","type":"flow_control"}
	]
}`

type fakeAppFlowDebugDAL struct {
	app      dal.Application
	appErr   error
	draft    dal.AppDefinition
	draftErr error

	// AppFlowDebugCredentialDAL fakes — unused by tests with no llm-kind
	// nodes in their draft (minimalDraftDoc has none), present so
	// fakeAppFlowDebugDAL satisfies AppFlowDebugDAL's embedded interface.
	provider    dal.LLMProvider
	providerErr error
	key         dal.LLMProviderKey
	keyErr      error

	// AgentExists fakes — see the AgentExists method below for defaults.
	agentExists    bool
	agentExistsSet bool
	agentExistsErr error
}

func (f *fakeAppFlowDebugDAL) GetApplication(_ context.Context, _, _ string) (dal.Application, error) {
	return f.app, f.appErr
}

func (f *fakeAppFlowDebugDAL) GetLatestDraftDefinition(_ context.Context, _, _ string) (dal.AppDefinition, error) {
	return f.draft, f.draftErr
}

func (f *fakeAppFlowDebugDAL) GetProviderByNameForTenant(_ context.Context, _, _ string) (dal.LLMProvider, error) {
	return f.provider, f.providerErr
}

func (f *fakeAppFlowDebugDAL) GetLLMProviderKey(_ context.Context, _ int64, _ *string) (dal.LLMProviderKey, error) {
	return f.key, f.keyErr
}

func (f *fakeAppFlowDebugDAL) GetDefaultLLMProviderKey(_ context.Context, _ int64, _ *string) (dal.LLMProviderKey, error) {
	return f.key, f.keyErr
}

// AgentExists is unused by every existing test in this file (all pass a nil
// registry to NewAppFlowDebugService, so resolveDraftAgentIDs never calls
// it) — defaults to "exists" so any incidental call is harmless. Set
// agentExists/agentExistsErr to override for a specific test. See
// appflow_debug_agent_resolve_test.go for the tests that actually exercise
// this.
func (f *fakeAppFlowDebugDAL) AgentExists(_ context.Context, _ string) (bool, error) {
	if f.agentExistsErr != nil {
		return false, f.agentExistsErr
	}
	if f.agentExistsSet {
		return f.agentExists, nil
	}
	return true, nil
}

// fakeAppFlowDebugCredentialStore records every Set call — used to assert
// which nodes got a credential written, and with what values.
type fakeAppFlowDebugCredentialStore struct {
	sets []credentialSetCall
	err  error
}

type credentialSetCall struct {
	tenantID, runID, nodeID string
	override                debugcred.Override
}

func (f *fakeAppFlowDebugCredentialStore) Set(_ context.Context, tenantID, runID, nodeID string, ov debugcred.Override) error {
	f.sets = append(f.sets, credentialSetCall{tenantID: tenantID, runID: runID, nodeID: nodeID, override: ov})
	return f.err
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
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	result, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hello", 7, nil, false)
	require.NoError(t, err)
	assert.Equal(t, "run-1", result.RunID)
	assert.True(t, lc.admitCalled)
	assert.True(t, lc.startCalled)
	assert.True(t, lc.lastDebug, "must always start with debug=true")
	assert.Equal(t, "hello", lc.lastInput.UserMessage)
	assert.False(t, lc.lastInput.StepMode, "stepMode=false must not set StepMode")
	require.NotNil(t, lc.lastInput.Spec)
	require.Len(t, lc.lastInput.Spec.EntryPoints, 1)
	assert.Equal(t, "chat", lc.lastInput.Spec.EntryPoints[0].Slug)
}

// StepMode=true (docs/APP_CANVAS_DEBUG_PLAN.md Phase 6) must propagate
// through to AppFlowWorkflowInput.StepMode unchanged — this is the only
// thing distinguishing a Step-controlled debug session from a Run-All one at
// the point the workflow is started.
func TestAppFlowDebugService_Start_StepModeTrue_PropagatesToWorkflowInput(t *testing.T) {
	d := &fakeAppFlowDebugDAL{
		app:   draftApp("chat"),
		draft: dal.AppDefinition{Definition: json.RawMessage(minimalDraftDoc), Status: "draft"},
	}
	lc := &fakeAppFlowDebugStarter{handle: &execution.ExecutionHandle{RunID: "run-1"}}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hello", 7, nil, true)
	require.NoError(t, err)
	assert.True(t, lc.lastInput.StepMode)
}

// ExpiresAt must be startedAt + appflow.DebugRunMaxLifetime, computed from a
// timestamp taken just before StartAppFlow — not an independently guessed
// duration — so it agrees exactly with the WorkflowRunTimeout Lifecycle sets
// on the same call. Bound the assertion between timestamps taken immediately
// before/after Start() to avoid a flaky exact-time comparison.
func TestAppFlowDebugService_Start_ExpiresAt_IsStartedAtPlusDebugRunMaxLifetime(t *testing.T) {
	d := &fakeAppFlowDebugDAL{
		app:   draftApp("chat"),
		draft: dal.AppDefinition{Definition: json.RawMessage(minimalDraftDoc), Status: "draft"},
	}
	lc := &fakeAppFlowDebugStarter{handle: &execution.ExecutionHandle{RunID: "run-1"}}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	before := time.Now()
	result, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hello", 7, nil, false)
	after := time.Now()
	require.NoError(t, err)

	minExpiry := before.Add(appflow.DebugRunMaxLifetime)
	maxExpiry := after.Add(appflow.DebugRunMaxLifetime)
	assert.False(t, result.ExpiresAt.Before(minExpiry), "ExpiresAt too early: %v < %v", result.ExpiresAt, minExpiry)
	assert.False(t, result.ExpiresAt.After(maxExpiry), "ExpiresAt too late: %v > %v", result.ExpiresAt, maxExpiry)
}

// Application not found (wrong tenant, or doesn't exist) → ErrNotFound.
func TestAppFlowDebugService_Start_AppNotFound(t *testing.T) {
	d := &fakeAppFlowDebugDAL{appErr: pgx.ErrNoRows}
	lc := &fakeAppFlowDebugStarter{}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7, nil, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
	assert.False(t, lc.admitCalled, "must not admit a run when the app can't be resolved")
}

// Entry point slug doesn't exist on the application → ErrNotFound, no draft fetch.
func TestAppFlowDebugService_Start_EPSlugNotFound(t *testing.T) {
	d := &fakeAppFlowDebugDAL{app: draftApp("other-slug")}
	lc := &fakeAppFlowDebugStarter{}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7, nil, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNotFound))
}

// No draft saved yet → 422 unprocessable, not 404 (distinct from "app not found").
func TestAppFlowDebugService_Start_NoDraftSaved(t *testing.T) {
	d := &fakeAppFlowDebugDAL{app: draftApp("chat"), draftErr: pgx.ErrNoRows}
	lc := &fakeAppFlowDebugStarter{}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7, nil, false)
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
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	// minimalDraftDoc only defines entry point "chat", but the application row
	// (draftApp) is stubbed with a matching slug — force a mismatch by asking
	// for a slug absent from the compiled spec.
	d.app = dal.Application{ID: "app-1", Slug: "my-app", EntryPoints: []dal.EntryPoint{{Slug: "stale-slug"}}}
	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "stale-slug", "hi", 7, nil, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnprocessable))
	assert.False(t, lc.admitCalled)
}

// ── AppFlow Runtime Params (docs/APPFLOW_RUNTIME_PARAMS_PLAN.md) ────────────

func twoLLMDraftDAL() *fakeAppFlowDebugDAL {
	return &fakeAppFlowDebugDAL{
		app:   draftApp("chat"),
		draft: dal.AppDefinition{Definition: json.RawMessage(twoLLMNodesDraftDoc), Status: "draft"},
	}
}

// A required llm_credential param with NO llm_overrides entry at all must
// fail validation BEFORE admitting a run — "validate required settings
// server-side before starting the run," and a validation failure must never
// consume a debug run slot.
func TestAppFlowDebugService_Start_MissingRequiredOverride_FailsBeforeAdmit(t *testing.T) {
	d := twoLLMDraftDAL()
	lc := &fakeAppFlowDebugStarter{handle: &execution.ExecutionHandle{RunID: "run-1"}}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	// Only llm_1 has an override — llm_2 does not.
	overrides := map[string]LLMOverrideInput{
		"llm_1": {Mode: "custom", Provider: "anthropic", APIKey: "sk-test"},
	}
	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7, overrides, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnprocessable))
	assert.False(t, lc.admitCalled, "must not admit a run when a required override is missing")
	assert.Empty(t, credStore.sets, "must not write any credential when validation fails")
}

// Custom mode never touches the DAL — the literal api_key from the request
// is what gets stored, per node.
func TestAppFlowDebugService_Start_CustomMode_UsesLiteralKey_PerNode(t *testing.T) {
	d := twoLLMDraftDAL()
	lc := &fakeAppFlowDebugStarter{handle: &execution.ExecutionHandle{RunID: "run-1", EPConfig: &epconfig.EPConfig{TenantID: "tenant-1"}}}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	overrides := map[string]LLMOverrideInput{
		"llm_1": {Mode: "custom", Provider: "anthropic", Model: "claude-haiku-4-5-20251001", APIKey: "sk-node-1"},
		"llm_2": {Mode: "custom", Provider: "openai", Model: "gpt-4o", APIKey: "sk-node-2"},
	}
	result, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7, overrides, false)
	require.NoError(t, err)
	assert.Equal(t, "run-1", result.RunID)

	require.Len(t, credStore.sets, 2, "one Set call per node — never merged into one")
	byNode := map[string]credentialSetCall{}
	for _, c := range credStore.sets {
		byNode[c.nodeID] = c
	}
	require.Contains(t, byNode, "llm_1")
	require.Contains(t, byNode, "llm_2")
	assert.Equal(t, "sk-node-1", byNode["llm_1"].override.APIKey)
	assert.Equal(t, "sk-node-2", byNode["llm_2"].override.APIKey, "llm_2 must keep its own key, not llm_1's")
	assert.Equal(t, "anthropic", byNode["llm_1"].override.Provider)
	assert.Equal(t, "openai", byNode["llm_2"].override.Provider)
	assert.Equal(t, "tenant-1", byNode["llm_1"].tenantID)
	assert.Equal(t, "run-1", byNode["llm_1"].runID)
}

// General mode with an unusable key (no provider configured for this
// tenant) must fail clearly, not silently proceed with an empty key.
func TestAppFlowDebugService_Start_GeneralMode_NoUsableKey_FailsClearly(t *testing.T) {
	d := twoLLMDraftDAL()
	d.providerErr = pgx.ErrNoRows // "no anthropic provider configured for this tenant"
	lc := &fakeAppFlowDebugStarter{}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	overrides := map[string]LLMOverrideInput{
		"llm_1": {Mode: "general", Provider: "anthropic"},
		"llm_2": {Mode: "general", Provider: "anthropic"},
	}
	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7, overrides, false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnprocessable))
	assert.False(t, lc.admitCalled)
	assert.Empty(t, credStore.sets)
}

// Credential store write failure must surface as an error, not be swallowed
// (a swallowed write would leave the workflow starting with no override the
// activity can find, silently regressing to normal key resolution).
func TestAppFlowDebugService_Start_CredentialStoreWriteFails_SurfacesError(t *testing.T) {
	d := twoLLMDraftDAL()
	lc := &fakeAppFlowDebugStarter{handle: &execution.ExecutionHandle{RunID: "run-1", EPConfig: &epconfig.EPConfig{TenantID: "tenant-1"}}}
	credStore := &fakeAppFlowDebugCredentialStore{err: assert.AnError}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	overrides := map[string]LLMOverrideInput{
		"llm_1": {Mode: "custom", Provider: "anthropic", APIKey: "sk-test"},
		"llm_2": {Mode: "custom", Provider: "anthropic", APIKey: "sk-test"},
	}
	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hi", 7, overrides, false)
	require.Error(t, err)
	assert.False(t, lc.startCalled, "must not start the workflow if a credential write failed")
}
