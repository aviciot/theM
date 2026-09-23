package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

// fakeOpenAIServer stands in for a provider's real API endpoint, recording
// which server actually received the request and replying with a minimal
// valid SSE stream so llm.OpenAIProvider.Stream completes successfully.
func fakeOpenAIServer(t *testing.T, hitCount *int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hitCount, 1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\ndata: [DONE]\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDBLLMCaller_Complete_Debug_UsesOverrideBaseURL verifies issue #1 from
// the d941aca3 review: BaseURL was stored in the debug override but never
// passed into provider creation, so every debug node silently called
// whatever endpoint multiLLMFactory's static per-provider map happened to
// have (or the provider's hardcoded default) instead of the one configured
// for that node's override. Two nodes, two different fake endpoints, two
// independent debug overrides — each request must land on ITS OWN server.
func TestDBLLMCaller_Complete_Debug_UsesOverrideBaseURL(t *testing.T) {
	var hitsA, hitsB int32
	srvA := fakeOpenAIServer(t, &hitsA)
	srvB := fakeOpenAIServer(t, &hitsB)

	store := &fakeDebugStore{overrides: map[string]debugcred.Override{
		"tenant-1|run-1|llm_a": {Provider: "openai", Model: "gpt-4o-mini", APIKey: "key-a", BaseURL: srvA.URL},
		"tenant-1|run-1|llm_b": {Provider: "openai", Model: "gpt-4o-mini", APIKey: "key-b", BaseURL: srvB.URL},
	}}
	caller := &dbLLMCaller{factory: &multiLLMFactory{}, debugStore: store}

	_, err := caller.Complete(context.Background(), appflow.InlineLLMRequest{
		SystemPrompt: "sys", UserPrompt: "hi",
		TenantID: "tenant-1", RunID: "run-1", NodeID: "llm_a", Debug: true,
	})
	require.NoError(t, err)
	_, err = caller.Complete(context.Background(), appflow.InlineLLMRequest{
		SystemPrompt: "sys", UserPrompt: "hi",
		TenantID: "tenant-1", RunID: "run-1", NodeID: "llm_b", Debug: true,
	})
	require.NoError(t, err)

	assert.Equal(t, int32(1), atomic.LoadInt32(&hitsA), "llm_a's request must reach srvA, its own configured endpoint")
	assert.Equal(t, int32(1), atomic.LoadInt32(&hitsB), "llm_b's request must reach srvB, its own configured endpoint")
}
