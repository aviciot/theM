package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aviciot/them/internal/admin/dal"
	"github.com/aviciot/them/internal/execution"
	"github.com/aviciot/them/internal/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unpublishedAgentDraftDoc is the same one-agent-node canvas shape as
// minimalDraftDoc (appflow_debug_test.go) but with NO "_resolved_agent_ids"
// stamp — i.e. a draft that has never been published. Before this fix,
// starting a debug run against this exact shape failed Validate with
// "unresolved_agent" (docs/APP_CANVAS_DEBUG_PLAN.md's documented "known
// limitation"). resolveDraftAgentIDs exists to resolve it live instead.
const unpublishedAgentDraftDoc = `{
	"schema_version": 2,
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

// fakeRegistryResolver is a minimal RegistryResolver fake for
// resolveDraftAgentIDs' tests — Resolve is never called by this code path
// (only ResolveForPublish is), but both are implemented to satisfy the
// interface.
type fakeRegistryResolver struct {
	resolveForPublishFn func(ctx context.Context, tenantID string, ref registry.DefinitionRef, definitionID string) (*registry.ComponentDefinition, error)
}

func (f *fakeRegistryResolver) Resolve(ctx context.Context, tenantID string, ref registry.DefinitionRef, definitionID string) (*registry.ComponentDefinition, error) {
	return f.resolveForPublishFn(ctx, tenantID, ref, definitionID)
}

func (f *fakeRegistryResolver) ResolveForPublish(ctx context.Context, tenantID string, ref registry.DefinitionRef, definitionID string) (*registry.ComponentDefinition, error) {
	return f.resolveForPublishFn(ctx, tenantID, ref, definitionID)
}

func TestResolveDraftAgentIDs_NilRegistry_NoOp(t *testing.T) {
	svc := &AppFlowDebugService{registry: nil}
	agentByInstanceID := map[string]string{}
	err := svc.resolveDraftAgentIDs(context.Background(), "tenant-1", []byte(unpublishedAgentDraftDoc), agentByInstanceID)
	require.NoError(t, err)
	assert.Empty(t, agentByInstanceID, "nil registry must never mutate the map — unresolved_agent still fires downstream, same as before this fix")
}

func TestResolveDraftAgentIDs_UnpublishedDraft_ResolvesLive(t *testing.T) {
	reg := &fakeRegistryResolver{
		resolveForPublishFn: func(_ context.Context, tenantID string, ref registry.DefinitionRef, _ string) (*registry.ComponentDefinition, error) {
			assert.Equal(t, "tenant-1", tenantID)
			assert.Equal(t, registry.KindAgent, ref.Kind)
			assert.Equal(t, "echo", ref.Name)
			return &registry.ComponentDefinition{ID: "agent-uuid-1", Kind: registry.KindAgent}, nil
		},
	}
	dal := &fakeAppFlowDebugDAL{}
	svc := &AppFlowDebugService{registry: reg, dal: dal}

	agentByInstanceID := map[string]string{}
	err := svc.resolveDraftAgentIDs(context.Background(), "tenant-1", []byte(unpublishedAgentDraftDoc), agentByInstanceID)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"agent_1": "agent-uuid-1"}, agentByInstanceID,
		"an unpublished draft's agent node must resolve to a real agent ID live, without requiring a publish first")
}

func TestResolveDraftAgentIDs_AlreadyStamped_NeverOverwritten(t *testing.T) {
	called := false
	reg := &fakeRegistryResolver{
		resolveForPublishFn: func(context.Context, string, registry.DefinitionRef, string) (*registry.ComponentDefinition, error) {
			called = true
			return &registry.ComponentDefinition{ID: "wrong-id"}, nil
		},
	}
	svc := &AppFlowDebugService{registry: reg, dal: &fakeAppFlowDebugDAL{}}

	agentByInstanceID := map[string]string{"agent_1": "already-resolved-uuid"}
	err := svc.resolveDraftAgentIDs(context.Background(), "tenant-1", []byte(unpublishedAgentDraftDoc), agentByInstanceID)
	require.NoError(t, err)
	assert.False(t, called, "an instance_id already present (from a real publish stamp) must never be re-resolved or overwritten")
	assert.Equal(t, "already-resolved-uuid", agentByInstanceID["agent_1"])
}

func TestResolveDraftAgentIDs_RegistryResolveFails_LeavesUnresolved(t *testing.T) {
	reg := &fakeRegistryResolver{
		resolveForPublishFn: func(context.Context, string, registry.DefinitionRef, string) (*registry.ComponentDefinition, error) {
			return nil, registry.ErrNotFound
		},
	}
	svc := &AppFlowDebugService{registry: reg, dal: &fakeAppFlowDebugDAL{}}

	agentByInstanceID := map[string]string{}
	err := svc.resolveDraftAgentIDs(context.Background(), "tenant-1", []byte(unpublishedAgentDraftDoc), agentByInstanceID)
	require.NoError(t, err, "a resolve failure must not abort the whole debug-start call — appflow.Validate reports unresolved_agent with its own clear message")
	assert.Empty(t, agentByInstanceID)
}

func TestResolveDraftAgentIDs_AgentRowMissing_LeavesUnresolved(t *testing.T) {
	reg := &fakeRegistryResolver{
		resolveForPublishFn: func(context.Context, string, registry.DefinitionRef, string) (*registry.ComponentDefinition, error) {
			return &registry.ComponentDefinition{ID: "agent-uuid-1", Kind: registry.KindAgent}, nil
		},
	}
	// component_definitions row resolved fine, but the matching agents row
	// doesn't exist (e.g. deleted) — must not be treated as resolved.
	dal := &fakeAppFlowDebugDAL{agentExistsSet: true, agentExists: false}
	svc := &AppFlowDebugService{registry: reg, dal: dal}

	agentByInstanceID := map[string]string{}
	err := svc.resolveDraftAgentIDs(context.Background(), "tenant-1", []byte(unpublishedAgentDraftDoc), agentByInstanceID)
	require.NoError(t, err)
	assert.Empty(t, agentByInstanceID, "a resolved component_definitions row with no matching agents row must not be reported as resolved")
}

func TestResolveDraftAgentIDs_AgentExistsCheckErrors_PropagatesError(t *testing.T) {
	reg := &fakeRegistryResolver{
		resolveForPublishFn: func(context.Context, string, registry.DefinitionRef, string) (*registry.ComponentDefinition, error) {
			return &registry.ComponentDefinition{ID: "agent-uuid-1", Kind: registry.KindAgent}, nil
		},
	}
	dal := &fakeAppFlowDebugDAL{agentExistsErr: errors.New("db down")}
	svc := &AppFlowDebugService{registry: reg, dal: dal}

	agentByInstanceID := map[string]string{}
	err := svc.resolveDraftAgentIDs(context.Background(), "tenant-1", []byte(unpublishedAgentDraftDoc), agentByInstanceID)
	assert.Error(t, err, "a real DB error checking AgentExists must propagate, not be silently swallowed like a not-found")
}

// TestAppFlowDebugService_Start_UnpublishedAgentDraft_NoLongerBlocked is the
// end-to-end regression test for the fix: before resolveDraftAgentIDs
// existed, Start on this exact draft (an agent node, never published, no
// _resolved_agent_ids stamp) failed with "validate: [unresolved_agent] ..."
// — the real bug hit live during Platform-as-Tenant Phase 6 verification
// (docs/PLATFORM_AS_TENANT_PLAN.md). With a real registry wired in, Start
// now succeeds without any publish having happened.
func TestAppFlowDebugService_Start_UnpublishedAgentDraft_NoLongerBlocked(t *testing.T) {
	d := &fakeAppFlowDebugDAL{
		app:   draftApp("chat"),
		draft: dal.AppDefinition{Definition: json.RawMessage(unpublishedAgentDraftDoc), Status: "draft"},
	}
	reg := &fakeRegistryResolver{
		resolveForPublishFn: func(_ context.Context, tenantID string, ref registry.DefinitionRef, _ string) (*registry.ComponentDefinition, error) {
			return &registry.ComponentDefinition{ID: "agent-uuid-1", Kind: registry.KindAgent}, nil
		},
	}
	lc := &fakeAppFlowDebugStarter{handle: &execution.ExecutionHandle{RunID: "run-1"}}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), reg)

	result, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hello", 7, nil, false)
	require.NoError(t, err, "an unpublished draft with an agent node must debug successfully once the registry can resolve it live")
	assert.Equal(t, "run-1", result.RunID)
	assert.True(t, lc.startCalled)
}

// Without a registry wired in (reg=nil, e.g. a deployment that never passes
// one), behavior must stay exactly as before this fix: unresolved_agent.
func TestAppFlowDebugService_Start_UnpublishedAgentDraft_NilRegistry_StillBlocked(t *testing.T) {
	d := &fakeAppFlowDebugDAL{
		app:   draftApp("chat"),
		draft: dal.AppDefinition{Definition: json.RawMessage(unpublishedAgentDraftDoc), Status: "draft"},
	}
	lc := &fakeAppFlowDebugStarter{handle: &execution.ExecutionHandle{RunID: "run-1"}}
	credStore := &fakeAppFlowDebugCredentialStore{}
	svc := NewAppFlowDebugService(d, lc, credStore, []byte("test-fernet-key-32-bytes-long!!"), nil)

	_, err := svc.Start(context.Background(), "tenant-1", "app-1", "chat", "hello", 7, nil, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unresolved_agent")
	assert.False(t, lc.startCalled, "must never admit or start a run when an agent node can't be resolved")
}

func TestResolveDraftAgentIDs_NonAgentComponents_Skipped(t *testing.T) {
	called := false
	reg := &fakeRegistryResolver{
		resolveForPublishFn: func(context.Context, string, registry.DefinitionRef, string) (*registry.ComponentDefinition, error) {
			called = true
			return nil, registry.ErrNotFound
		},
	}
	svc := &AppFlowDebugService{registry: reg, dal: &fakeAppFlowDebugDAL{}}

	agentByInstanceID := map[string]string{}
	err := svc.resolveDraftAgentIDs(context.Background(), "tenant-1", []byte(twoLLMNodesDraftDoc), agentByInstanceID)
	require.NoError(t, err)
	assert.False(t, called, "inline/llm components must never trigger a registry lookup — only kind=agent")
	assert.Empty(t, agentByInstanceID)
}
