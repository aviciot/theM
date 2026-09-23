//go:build integration

// Integration tests for the debug credential store. Requires a live Redis
// reachable at REDIS_ADDR (default localhost:6379). Run with:
//
//	go test -tags=integration ./internal/debugcred/...
package debugcred_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/rueidis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/debugcred"
)

func redisAddr() string {
	if v := os.Getenv("REDIS_ADDR"); v != "" {
		return v
	}
	return "localhost:6379"
}

func newStore(t *testing.T) (*debugcred.Store, rueidis.Client) {
	t.Helper()
	rc, err := rueidis.NewClient(rueidis.ClientOption{
		InitAddress:  []string{redisAddr()},
		DisableCache: true,
	})
	require.NoError(t, err)
	t.Cleanup(rc.Close)
	return debugcred.New(rc), rc
}

func TestIntegration_Store_SetGet_RoundTrips(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	tenantID, runID, nodeID := "tenant-a", fmt.Sprintf("run-%d", time.Now().UnixNano()), "llm_1"

	ov := debugcred.Override{UserID: 42, Provider: "anthropic", Model: "claude-haiku-4-5-20251001", APIKey: "sk-test-123"}
	require.NoError(t, store.Set(ctx, tenantID, runID, nodeID, ov))
	t.Cleanup(func() { _ = store.Delete(ctx, tenantID, runID, nodeID) })

	got, found, err := store.Get(ctx, tenantID, runID, nodeID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, ov, got)
}

// TestIntegration_Store_GetSurvivesRepeatedReads proves the value is NOT
// consumed on read — a Temporal activity retry re-runs the whole activity,
// including any credential lookup, so a second Get for the same key must
// still find it.
func TestIntegration_Store_GetSurvivesRepeatedReads(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	tenantID, runID, nodeID := "tenant-a", fmt.Sprintf("run-%d", time.Now().UnixNano()), "llm_1"

	require.NoError(t, store.Set(ctx, tenantID, runID, nodeID, debugcred.Override{Provider: "anthropic", APIKey: "sk-test"}))
	t.Cleanup(func() { _ = store.Delete(ctx, tenantID, runID, nodeID) })

	for i := 0; i < 3; i++ {
		_, found, err := store.Get(ctx, tenantID, runID, nodeID)
		require.NoError(t, err)
		require.True(t, found, "attempt %d: value should survive repeated reads (simulates a Temporal retry)", i+1)
	}
}

func TestIntegration_Store_Get_NotFound_ReturnsFalseNotError(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()

	_, found, err := store.Get(ctx, "tenant-a", "nonexistent-run", "llm_1")
	require.NoError(t, err)
	assert.False(t, found)
}

// TestIntegration_Store_TenantScoping_DifferentTenantsDoNotCollide proves the
// key shape's tenant segment actually isolates two tenants using the same
// run_id/node_id — this can only happen if run_ids somehow collided across
// tenants, but the isolation must hold regardless.
func TestIntegration_Store_TenantScoping_DifferentTenantsDoNotCollide(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	runID, nodeID := fmt.Sprintf("run-%d", time.Now().UnixNano()), "llm_1"

	require.NoError(t, store.Set(ctx, "tenant-a", runID, nodeID, debugcred.Override{Provider: "anthropic", APIKey: "tenant-a-key"}))
	t.Cleanup(func() { _ = store.Delete(ctx, "tenant-a", runID, nodeID) })

	_, found, err := store.Get(ctx, "tenant-b", runID, nodeID)
	require.NoError(t, err)
	assert.False(t, found, "tenant-b must not see tenant-a's override even with the same run_id/node_id")
}

func TestIntegration_Store_Delete_RemovesEntry(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	tenantID, runID, nodeID := "tenant-a", fmt.Sprintf("run-%d", time.Now().UnixNano()), "llm_1"

	require.NoError(t, store.Set(ctx, tenantID, runID, nodeID, debugcred.Override{Provider: "anthropic", APIKey: "sk-test"}))
	require.NoError(t, store.Delete(ctx, tenantID, runID, nodeID))

	_, found, err := store.Get(ctx, tenantID, runID, nodeID)
	require.NoError(t, err)
	assert.False(t, found)
}

// TestIntegration_Store_DeleteAllForRun_RemovesEveryNode closes issue #4 from
// the d941aca3 review: a fixed TTL was the ONLY cleanup mechanism. This is
// the primary cleanup path (proactive delete on run completion) — proves it
// removes every node's override for one run in a single call.
func TestIntegration_Store_DeleteAllForRun_RemovesEveryNode(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())

	require.NoError(t, store.Set(ctx, "tenant-a", runID, "llm_1", debugcred.Override{Provider: "anthropic", APIKey: "key-1"}))
	require.NoError(t, store.Set(ctx, "tenant-a", runID, "llm_2", debugcred.Override{Provider: "openai", APIKey: "key-2"}))
	require.NoError(t, store.Set(ctx, "tenant-a", runID, "llm_3", debugcred.Override{Provider: "openai", APIKey: "key-3"}))

	require.NoError(t, store.DeleteAllForRun(ctx, "tenant-a", runID))

	for _, nodeID := range []string{"llm_1", "llm_2", "llm_3"} {
		_, found, err := store.Get(ctx, "tenant-a", runID, nodeID)
		require.NoError(t, err)
		assert.False(t, found, "node %q should have been deleted by DeleteAllForRun", nodeID)
	}
}

// TestIntegration_Store_DeleteAllForRun_DoesNotTouchOtherRuns proves the
// pattern-scoped SCAN is scoped tightly enough to not delete a DIFFERENT
// run's (or tenant's) entries — a wildcard delete that was too broad would
// be worse than the bug it's fixing.
func TestIntegration_Store_DeleteAllForRun_DoesNotTouchOtherRuns(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	runA := fmt.Sprintf("run-a-%d", time.Now().UnixNano())
	runB := fmt.Sprintf("run-b-%d", time.Now().UnixNano())

	require.NoError(t, store.Set(ctx, "tenant-a", runA, "llm_1", debugcred.Override{Provider: "anthropic", APIKey: "key-a"}))
	require.NoError(t, store.Set(ctx, "tenant-a", runB, "llm_1", debugcred.Override{Provider: "anthropic", APIKey: "key-b"}))
	t.Cleanup(func() { _ = store.Delete(ctx, "tenant-a", runB, "llm_1") })

	require.NoError(t, store.DeleteAllForRun(ctx, "tenant-a", runA))

	_, foundB, err := store.Get(ctx, "tenant-a", runB, "llm_1")
	require.NoError(t, err)
	assert.True(t, foundB, "a different run's entry must survive cleanup of runA")
}

// TestIntegration_Store_DeleteAllForRun_NoEntries_NoError verifies cleanup
// of a run with zero stored overrides (e.g. a debug run with no LLM nodes at
// all) is a harmless no-op, not an error.
func TestIntegration_Store_DeleteAllForRun_NoEntries_NoError(t *testing.T) {
	store, _ := newStore(t)
	ctx := context.Background()
	runID := fmt.Sprintf("run-empty-%d", time.Now().UnixNano())

	assert.NoError(t, store.DeleteAllForRun(ctx, "tenant-a", runID))
}
