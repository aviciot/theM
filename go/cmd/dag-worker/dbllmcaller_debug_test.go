package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/appflow"
	"github.com/aviciot/them/internal/debugcred"
)

// fakeDebugStore is a hand-rolled debugCredentialGetter — no live Redis
// needed. Mirrors debugcred.Store's Get semantics: found=false is NOT the
// same as "no error," and both must be distinguishable from a real error.
type fakeDebugStore struct {
	overrides map[string]debugcred.Override // key: tenantID+"|"+runID+"|"+nodeID
	err       error
}

func (f *fakeDebugStore) Get(_ context.Context, tenantID, runID, nodeID string) (debugcred.Override, bool, error) {
	if f.err != nil {
		return debugcred.Override{}, false, f.err
	}
	ov, ok := f.overrides[tenantID+"|"+runID+"|"+nodeID]
	return ov, ok, nil
}

func newFakeDebugStore(tenantID, runID, nodeID string, ov debugcred.Override) *fakeDebugStore {
	return &fakeDebugStore{overrides: map[string]debugcred.Override{tenantID + "|" + runID + "|" + nodeID: ov}}
}

// TestDBLLMCaller_Complete_Debug_UsesOverride verifies a debug call with a
// found override uses THAT override's provider/model/key — never the normal
// llmresolve chain (proven here by never wiring a resolver into the caller
// at all; if the code fell through, it would nil-panic, not silently succeed).
func TestDBLLMCaller_Complete_Debug_UsesOverride(t *testing.T) {
	store := newFakeDebugStore("tenant-1", "run-1", "llm_1", debugcred.Override{
		Provider: "mock", Model: "mock-model", APIKey: "unused-by-mock",
	})
	caller := &dbLLMCaller{factory: &multiLLMFactory{}, debugStore: store}

	resp, err := caller.Complete(context.Background(), appflow.InlineLLMRequest{
		SystemPrompt: "sys", UserPrompt: "hi",
		TenantID: "tenant-1", RunID: "run-1", NodeID: "llm_1",
		Debug: true,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp, "mock provider always returns a canned response")
}

// TestDBLLMCaller_Complete_Debug_MissingOverride_FailsClearly verifies a
// debug call whose override is absent (expired/evicted) fails loudly rather
// than falling back to normal key resolution — the plan's explicit
// requirement: "if a requested credential becomes unavailable, fail clearly
// rather than silently switching to another key." No resolver is wired into
// the caller here either — if the code fell through to c.resolveKey, it
// would nil-panic on c.resolver, not silently succeed with some other key.
func TestDBLLMCaller_Complete_Debug_MissingOverride_FailsClearly(t *testing.T) {
	store := &fakeDebugStore{overrides: map[string]debugcred.Override{}} // nothing stored
	caller := &dbLLMCaller{factory: &multiLLMFactory{}, debugStore: store}

	_, err := caller.Complete(context.Background(), appflow.InlineLLMRequest{
		SystemPrompt: "sys", UserPrompt: "hi",
		TenantID: "tenant-1", RunID: "run-1", NodeID: "llm_1",
		Debug: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unavailable")
}

// TestDBLLMCaller_Complete_Debug_StoreErrorFailsClearly verifies a Redis
// error during the override lookup also fails loudly, not silently.
func TestDBLLMCaller_Complete_Debug_StoreErrorFailsClearly(t *testing.T) {
	store := &fakeDebugStore{err: errors.New("redis unavailable")}
	caller := &dbLLMCaller{factory: &multiLLMFactory{}, debugStore: store}

	_, err := caller.Complete(context.Background(), appflow.InlineLLMRequest{
		SystemPrompt: "sys", UserPrompt: "hi",
		TenantID: "tenant-1", RunID: "run-1", NodeID: "llm_1",
		Debug: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lookup failed")
}

// TestDBLLMCaller_Complete_Debug_NilStore_FailsClearly verifies a debug call
// with no debugStore configured at all fails the same way as "not found" —
// it must never be interpreted as "skip the check, use normal resolution,"
// since that would nil-panic on c.resolver anyway, but the point is it must
// fail via the explicit "unavailable" path, not by accident.
func TestDBLLMCaller_Complete_Debug_NilStore_FailsClearly(t *testing.T) {
	caller := &dbLLMCaller{factory: &multiLLMFactory{}, debugStore: nil}

	_, err := caller.Complete(context.Background(), appflow.InlineLLMRequest{
		SystemPrompt: "sys", UserPrompt: "hi",
		TenantID: "tenant-1", RunID: "run-1", NodeID: "llm_1",
		Debug: true,
	})
	require.Error(t, err)
}

// TestDBLLMCaller_Complete_Debug_DifferentNode_DoesNotLeakOverride verifies
// two nodes in the same run each get their own override — llm_2 must not
// see llm_1's stored credential.
func TestDBLLMCaller_Complete_Debug_DifferentNode_DoesNotLeakOverride(t *testing.T) {
	store := newFakeDebugStore("tenant-1", "run-1", "llm_1", debugcred.Override{
		Provider: "mock", APIKey: "node-1-key",
	})
	caller := &dbLLMCaller{factory: &multiLLMFactory{}, debugStore: store}

	_, err := caller.Complete(context.Background(), appflow.InlineLLMRequest{
		SystemPrompt: "sys", UserPrompt: "hi",
		TenantID: "tenant-1", RunID: "run-1", NodeID: "llm_2", // different node
		Debug: true,
	})
	require.Error(t, err, "llm_2 has no override of its own — must fail, not borrow llm_1's")
}
