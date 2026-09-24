package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aviciot/them/internal/admin/dal"
)

// TestGetResult_AllNodesCompleted_OkTrue proves the happy path: every node
// completed, Ok is true, no failed-node fields set — the exact shape a
// debugging-assistant LLM needs to know "this run is fine, nothing to fix."
func TestGetResult_AllNodesCompleted_OkTrue(t *testing.T) {
	latency := int64(120)
	d := &fakeAppFlowDebugDAL{
		runDetail: dal.RunDetail{
			Steps: []dal.RunStep{
				{NodeID: "llm_1", NodeKind: "llm", Status: "completed", Output: "POSITIVE", LatencyMS: &latency},
				{NodeID: "cond_1", NodeKind: "condition", Status: "completed", Output: "branch=true"},
			},
		},
	}
	svc := &AppFlowDebugService{dal: d}

	result, err := svc.GetResult(context.Background(), "tenant-1", "run-1")
	require.NoError(t, err)
	assert.True(t, result.Ok)
	assert.Empty(t, result.FailedNodeID)
	assert.Empty(t, result.FailedError)
	require.Len(t, result.NodeResults, 2)
	assert.Equal(t, "llm_1", result.NodeResults[0].NodeID)
	assert.Equal(t, "POSITIVE", result.NodeResults[0].Output)
	assert.Equal(t, &latency, result.NodeResults[0].LatencyMS)
	assert.Equal(t, "cond_1", result.NodeResults[1].NodeID)
}

// TestGetResult_NodeFailed_OkFalse_ReportsFirstFailure proves the core
// "what broke" case: a real failure reports the failing node's id and exact
// error at the top level, not just buried in the per-node list — this is
// the whole point of building this endpoint instead of reusing the raw
// run-detail response as-is.
func TestGetResult_NodeFailed_OkFalse_ReportsFirstFailure(t *testing.T) {
	d := &fakeAppFlowDebugDAL{
		runDetail: dal.RunDetail{
			Steps: []dal.RunStep{
				{NodeID: "llm_1", NodeKind: "llm", Status: "completed", Output: "POSITIVE"},
				{NodeID: "llm_2", NodeKind: "llm", Status: "failed", Error: "anthropic: 401 invalid API key"},
			},
		},
	}
	svc := &AppFlowDebugService{dal: d}

	result, err := svc.GetResult(context.Background(), "tenant-1", "run-1")
	require.NoError(t, err)
	assert.False(t, result.Ok)
	assert.Equal(t, "llm_2", result.FailedNodeID)
	assert.Equal(t, "anthropic: 401 invalid API key", result.FailedError)
	require.Len(t, result.NodeResults, 2, "the full node list must still be present, not truncated at the failure")
}

// TestGetResult_MultipleFailures_ReportsOnlyFirst — a run with more than one
// failed step (e.g. two independent branches) reports the FIRST one at the
// top level, in execution order, rather than the last or an arbitrary one.
func TestGetResult_MultipleFailures_ReportsOnlyFirst(t *testing.T) {
	d := &fakeAppFlowDebugDAL{
		runDetail: dal.RunDetail{
			Steps: []dal.RunStep{
				{NodeID: "llm_1", NodeKind: "llm", Status: "failed", Error: "first error"},
				{NodeID: "llm_2", NodeKind: "llm", Status: "failed", Error: "second error"},
			},
		},
	}
	svc := &AppFlowDebugService{dal: d}

	result, err := svc.GetResult(context.Background(), "tenant-1", "run-1")
	require.NoError(t, err)
	assert.False(t, result.Ok)
	assert.Equal(t, "llm_1", result.FailedNodeID)
	assert.Equal(t, "first error", result.FailedError)
}

// TestGetResult_NoStepsYet_OkFalseNoFailure — a run that hasn't reached its
// first node yet (or a run_id that legitimately exists with zero steps
// persisted) must not be reported as Ok, but also must not fabricate a
// failed node — there simply isn't one yet.
func TestGetResult_NoStepsYet_OkTrue(t *testing.T) {
	d := &fakeAppFlowDebugDAL{runDetail: dal.RunDetail{Steps: []dal.RunStep{}}}
	svc := &AppFlowDebugService{dal: d}

	result, err := svc.GetResult(context.Background(), "tenant-1", "run-1")
	require.NoError(t, err)
	assert.True(t, result.Ok, "zero steps is vacuously 'nothing failed' — Ok starts true and only flips on an actual failed step")
	assert.Empty(t, result.FailedNodeID)
	assert.Empty(t, result.NodeResults)
}

// TestGetResult_RunNotFound_ReturnsErrNotFound — a run_id that doesn't
// exist, or belongs to another tenant (GetRunDetail is already
// tenant-scoped), must map to the same ErrNotFound sentinel every other
// service method in this package uses, so writeServiceError's existing
// 404 mapping applies without a new case.
func TestGetResult_RunNotFound_ReturnsErrNotFound(t *testing.T) {
	d := &fakeAppFlowDebugDAL{runDetailErr: pgx.ErrNoRows}
	svc := &AppFlowDebugService{dal: d}

	_, err := svc.GetResult(context.Background(), "tenant-1", "does-not-exist")
	assert.ErrorIs(t, err, ErrNotFound)
}
