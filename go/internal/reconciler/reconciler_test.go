package reconciler_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	enumspb "go.temporal.io/api/enums/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	temporalclient "go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/aviciot/them/internal/domain"
	"github.com/aviciot/them/internal/reconciler"
)

// ── Fake DB ───────────────────────────────────────────────────────────────────

// fakeDB satisfies reconciler.DBQuerier with configurable responses.
type fakeDB struct {
	mu sync.Mutex

	// lockGranted controls pg_try_advisory_lock response.
	lockGranted bool

	// eligibleRunIDs are returned by the Query call as tenant-less rows.
	eligibleRunIDs []string

	// eligibleRows are returned by the Query call as-is (appended after eligibleRunIDs).
	eligibleRows []eligibleRow

	// queryErr is returned by Query if non-nil.
	queryErr error

	// updates accumulates (runID, status) pairs written via Exec.
	updates []statusUpdate

	// execErr is returned by Exec if non-nil.
	execErr error

	// lockErr is returned by AcquireLock if non-nil; released counts release() calls.
	lockErr  error
	released int

	// finishedRows are returned for the finished-runs query; finishedErr fails it;
	// finishedArgs records its arguments.
	finishedRows []finishedRow
	finishedErr  error
	finishedArgs []any

	// childCloses records children-only closes (runclose.ChildrenQuery) issued via Exec.
	childCloses []statusUpdate
}

type finishedRow struct {
	id     string
	status string
}

type statusUpdate struct {
	runID  string
	status string
	reason string
}

// eligibleRow mirrors one row of the reconciler's eligibility query.
type eligibleRow struct {
	id         string
	tenantID   string
	contextIDs []string
	pastGrace  bool
}

type fakeRows struct {
	rows []eligibleRow
	pos  int
	err  error
}

func (r *fakeRows) Next() bool { r.pos++; return r.pos <= len(r.rows) }
func (r *fakeRows) Scan(dest ...any) error {
	row := r.rows[r.pos-1]
	*dest[0].(*string) = row.id
	*dest[1].(*string) = row.tenantID
	*dest[2].(*[]string) = row.contextIDs
	*dest[3].(*bool) = row.pastGrace
	return nil
}
func (r *fakeRows) Err() error { return r.err }
func (r *fakeRows) Close()     {}

func (db *fakeDB) AcquireLock(_ context.Context, _ int64) (func(), bool, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.lockErr != nil {
		return nil, false, db.lockErr
	}
	if !db.lockGranted {
		return nil, false, nil
	}
	return func() {
		db.mu.Lock()
		db.released++
		db.mu.Unlock()
	}, true, nil
}

type fakeFinishedRows struct {
	rows []finishedRow
	pos  int
}

func (r *fakeFinishedRows) Next() bool { r.pos++; return r.pos <= len(r.rows) }
func (r *fakeFinishedRows) Scan(dest ...any) error {
	*dest[0].(*string) = r.rows[r.pos-1].id
	*dest[1].(*string) = r.rows[r.pos-1].status
	return nil
}
func (r *fakeFinishedRows) Err() error { return nil }
func (r *fakeFinishedRows) Close()     {}

func (db *fakeDB) Query(_ context.Context, sql string, args ...any) (reconciler.PgxRowsIterator, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if strings.Contains(sql, "finished-children") {
		db.finishedArgs = args
		if db.finishedErr != nil {
			return nil, db.finishedErr
		}
		return &fakeFinishedRows{rows: db.finishedRows}, nil
	}
	if db.queryErr != nil {
		return nil, db.queryErr
	}
	var rows []eligibleRow
	for _, id := range db.eligibleRunIDs {
		rows = append(rows, eligibleRow{id: id})
	}
	return &fakeRows{rows: append(rows, db.eligibleRows...)}, nil
}

func (db *fakeDB) Exec(_ context.Context, sql string, args ...any) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.execErr != nil {
		return 0, db.execErr
	}
	if len(args) >= 3 {
		runID, _ := args[0].(string)
		st, _ := args[1].(string)
		reason, _ := args[2].(string)
		u := statusUpdate{runID: runID, status: st, reason: reason}
		if strings.Contains(sql, "UPDATE them.runs") {
			db.updates = append(db.updates, u) // run status change (closeRunQuery)
		} else {
			db.childCloses = append(db.childCloses, u) // children only (runclose.ChildrenQuery)
		}
	}
	return 1, nil
}

func (db *fakeDB) recordedChildCloses() []statusUpdate {
	db.mu.Lock()
	defer db.mu.Unlock()
	return append([]statusUpdate(nil), db.childCloses...)
}

func (db *fakeDB) recordedUpdates() []statusUpdate {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := make([]statusUpdate, len(db.updates))
	copy(out, db.updates)
	return out
}

// ── Fake Temporal client ──────────────────────────────────────────────────────

// fakeTemporalClient satisfies temporalclient.Client for the subset of methods
// used by the reconciler. All other methods panic — they should not be called.
type fakeTemporalClient struct {
	temporalclient.Client // embed for unimplemented methods

	mu        sync.Mutex
	responses map[string]describeResponse
}

type describeResponse struct {
	status enumspb.WorkflowExecutionStatus
	err    error
}

func newFakeTemporalClient() *fakeTemporalClient {
	return &fakeTemporalClient{responses: make(map[string]describeResponse)}
}

func (c *fakeTemporalClient) set(runID string, s enumspb.WorkflowExecutionStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses[runID] = describeResponse{status: s}
}

func (c *fakeTemporalClient) setErr(runID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.responses[runID] = describeResponse{err: err}
}

func (c *fakeTemporalClient) DescribeWorkflowExecution(
	ctx context.Context, workflowID, runID string,
) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.responses[workflowID]
	if !ok {
		return nil, notFoundErr()
	}
	if r.err != nil {
		return nil, r.err
	}
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Status: r.status,
		},
	}, nil
}

// notFoundErr returns a gRPC NotFound error, which is the form the Temporal
// SDK surfaces when a workflow execution does not exist.
func notFoundErr() error {
	return status.Error(codes.NotFound, "workflow not found")
}

// unavailableErr simulates a Temporal service outage.
func unavailableErr() error {
	return status.Error(codes.Unavailable, "temporal server unavailable")
}

// ── Helper ────────────────────────────────────────────────────────────────────

// fastConfig returns a Config appropriate for unit tests: minimal timeouts,
// no stale delay, concurrency=1 for deterministic ordering, DryRun=false.
func fastConfig() reconciler.Config {
	return reconciler.Config{
		Interval:   10 * time.Second, // not used in single-sweep tests
		BatchSize:  100,
		StaleAfter: 0, // include all rows regardless of age
		Concurrency: 1,
		DryRun:     false,
	}
}

// runOneSweep creates a Reconciler and calls sweepOnce — a test-only exported
// helper that runs exactly one sweep without the ticker loop.
func runOneSweep(t *testing.T, db *fakeDB, tc *fakeTemporalClient, cfg reconciler.Config) {
	t.Helper()
	r := reconciler.New(cfg, db, tc, nil)
	r.SweepOnce(context.Background())
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestReconciler_FreshRunSkipped verifies that a run started very recently
// (within StaleAfter) is excluded by the query — not presented to the reconciler.
// Since StaleAfter is enforced in SQL (not in Go), this test verifies the fake
// DB correctly returns no rows when the stale window excludes them.
func TestReconciler_FreshRunSkipped(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: nil} // DB returns no eligible rows
	tc := newFakeTemporalClient()

	cfg := fastConfig()
	runOneSweep(t, db, tc, cfg)

	assert.Empty(t, db.recordedUpdates(), "fresh run must not be updated")
}

// TestReconciler_TemporalRunningLeaveUnchanged verifies that a RUNNING Temporal
// workflow produces no DB update.
func TestReconciler_TemporalRunningLeaveUnchanged(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-001"}}
	tc := newFakeTemporalClient()
	tc.set("run-001", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING)

	runOneSweep(t, db, tc, fastConfig())

	assert.Empty(t, db.recordedUpdates())
}

// TestReconciler_CompletedUpdatesStatus verifies COMPLETED → "completed".
func TestReconciler_CompletedUpdatesStatus(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-002"}}
	tc := newFakeTemporalClient()
	tc.set("run-002", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED)

	runOneSweep(t, db, tc, fastConfig())

	updates := db.recordedUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, "run-002", updates[0].runID)
	assert.Equal(t, string(domain.RunCompleted), updates[0].status)
}

// TestReconciler_FailedUpdatesStatus verifies FAILED → "failed".
func TestReconciler_FailedUpdatesStatus(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-003"}}
	tc := newFakeTemporalClient()
	tc.set("run-003", enumspb.WORKFLOW_EXECUTION_STATUS_FAILED)

	runOneSweep(t, db, tc, fastConfig())

	updates := db.recordedUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, string(domain.RunFailed), updates[0].status)
}

// TestReconciler_CanceledUpdatesStatus verifies CANCELED → "canceled" (single-L).
func TestReconciler_CanceledUpdatesStatus(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-004"}}
	tc := newFakeTemporalClient()
	tc.set("run-004", enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED)

	runOneSweep(t, db, tc, fastConfig())

	updates := db.recordedUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, "canceled", updates[0].status, "must use single-L canonical spelling")
}

// TestReconciler_TerminatedMapsToStopped verifies TERMINATED → "stopped" (ADR-002).
func TestReconciler_TerminatedMapsToStopped(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-005"}}
	tc := newFakeTemporalClient()
	tc.set("run-005", enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED)

	runOneSweep(t, db, tc, fastConfig())

	updates := db.recordedUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, string(domain.RunStopped), updates[0].status)
}

// TestReconciler_TimedOutMapsToFailed verifies TIMED_OUT → "failed" (ADR-002).
func TestReconciler_TimedOutMapsToFailed(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-006"}}
	tc := newFakeTemporalClient()
	tc.set("run-006", enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT)

	runOneSweep(t, db, tc, fastConfig())

	updates := db.recordedUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, string(domain.RunFailed), updates[0].status)
}

// TestReconciler_NotFoundNoDestructiveUpdate verifies that NotFound from
// Temporal produces no DB write for a run that is not past the grace period.
func TestReconciler_NotFoundNoDestructiveUpdate(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-007"}}
	tc := newFakeTemporalClient()
	// run-007 has no entry in fakeTemporalClient → returns notFoundErr()

	runOneSweep(t, db, tc, fastConfig())

	assert.Empty(t, db.recordedUpdates(), "NotFound must not write to DB")
}

// TestReconciler_TemporalUnavailableNoDBUpdate verifies that a transient
// Temporal error produces no DB write.
func TestReconciler_TemporalUnavailableNoDBUpdate(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-008"}}
	tc := newFakeTemporalClient()
	tc.setErr("run-008", unavailableErr())

	runOneSweep(t, db, tc, fastConfig())

	assert.Empty(t, db.recordedUpdates(), "Temporal error must not write to DB")
}

// TestReconciler_AdvisoryLockPreventsDoubleSweep verifies that a second
// Reconciler instance skips the sweep when the advisory lock is not granted.
func TestReconciler_AdvisoryLockPreventsDoubleSweep(t *testing.T) {
	// Lock NOT granted → sweep should be skipped; no Query call, no updates.
	db := &fakeDB{lockGranted: false, eligibleRunIDs: []string{"run-009"}}
	tc := newFakeTemporalClient()
	tc.set("run-009", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED)

	runOneSweep(t, db, tc, fastConfig())

	assert.Empty(t, db.recordedUpdates(), "locked-out reconciler must not update DB")
}

// TestReconciler_IdempotentUpdate verifies that running the reconciler twice
// for the same completed run results in the same update (not a double-write).
// The WHERE status='running' guard makes the second update a no-op at the DB
// level; the fake increments either way — we verify the reconciler sends the
// same payload both times (external idempotency at DB level confirmed by SQL).
func TestReconciler_IdempotentUpdate(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-010"}}
	tc := newFakeTemporalClient()
	tc.set("run-010", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED)

	cfg := fastConfig()
	runOneSweep(t, db, tc, cfg)
	runOneSweep(t, db, tc, cfg)

	updates := db.recordedUpdates()
	// Both sweeps send the same update payload; DB idempotency comes from SQL.
	for _, u := range updates {
		assert.Equal(t, string(domain.RunCompleted), u.status)
	}
}

// TestReconciler_DryRunNoWrites verifies that DryRun=true produces no DB writes
// even when a run should be updated.
func TestReconciler_DryRunNoWrites(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-011"}}
	tc := newFakeTemporalClient()
	tc.set("run-011", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED)

	cfg := fastConfig()
	cfg.DryRun = true
	runOneSweep(t, db, tc, cfg)

	assert.Empty(t, db.recordedUpdates(), "dry-run must not write to DB")
}

// TestReconciler_ContinuedAsNewNoUpdate verifies CONTINUED_AS_NEW → no update
// (the workflow is still logically active in a new execution).
func TestReconciler_ContinuedAsNewNoUpdate(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-012"}}
	tc := newFakeTemporalClient()
	tc.set("run-012", enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW)

	runOneSweep(t, db, tc, fastConfig())

	assert.Empty(t, db.recordedUpdates())
}

// ── Workflow ID lookup and orphan handling ────────────────────────────────────

const tenantA = "tenant-a"

// TestReconciler_AppflowWorkflowIDFound verifies the canvas workflow ID
// "appflow:{tenant}:{runID}" is looked up (the bare run ID does not exist).
func TestReconciler_AppflowWorkflowIDFound(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRows: []eligibleRow{{id: "run-a1", tenantID: tenantA}}}
	tc := newFakeTemporalClient()
	tc.set("appflow:tenant-a:run-a1", enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT)

	runOneSweep(t, db, tc, fastConfig())

	updates := db.recordedUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, "run-a1", updates[0].runID)
	assert.Equal(t, string(domain.RunFailed), updates[0].status)
	assert.Contains(t, updates[0].reason, "TimedOut", "failure reason records the Temporal status")
}

// TestReconciler_ContextWorkflowIDFound verifies the orchestration workflow ID
// "{tenant}:ctx-{contextID}" is tried when the canvas ID does not exist.
func TestReconciler_ContextWorkflowIDFound(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRows: []eligibleRow{{id: "run-c1", tenantID: tenantA, contextIDs: []string{"ctx-1"}}}}
	tc := newFakeTemporalClient()
	tc.set("tenant-a:ctx-ctx-1", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED)

	runOneSweep(t, db, tc, fastConfig())

	updates := db.recordedUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, string(domain.RunCompleted), updates[0].status)
	assert.Empty(t, updates[0].reason, "completed runs carry no failure reason")
}

// TestReconciler_RunningWorkflowIDLeavesUnchanged verifies a live canvas workflow is never touched,
// even for a run past the grace period.
func TestReconciler_RunningWorkflowIDLeavesUnchanged(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRows: []eligibleRow{{id: "run-r1", tenantID: tenantA, pastGrace: true}}}
	tc := newFakeTemporalClient()
	tc.set("appflow:tenant-a:run-r1", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING)

	runOneSweep(t, db, tc, fastConfig())

	assert.Empty(t, db.recordedUpdates())
}

// TestReconciler_NotFoundPastGraceFailsOrphan verifies an old run with no workflow under any ID is failed.
func TestReconciler_NotFoundPastGraceFailsOrphan(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRows: []eligibleRow{{id: "run-o1", tenantID: tenantA, contextIDs: []string{"c9"}, pastGrace: true}}}
	tc := newFakeTemporalClient()

	runOneSweep(t, db, tc, fastConfig())

	updates := db.recordedUpdates()
	require.Len(t, updates, 1)
	assert.Equal(t, "run-o1", updates[0].runID)
	assert.Equal(t, string(domain.RunFailed), updates[0].status)
	assert.Contains(t, updates[0].reason, "orphaned")
}

// TestReconciler_NotFoundWithinGraceUnchanged verifies a young run with no workflow yet is left alone
// (the workflow may simply not have started).
func TestReconciler_NotFoundWithinGraceUnchanged(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRows: []eligibleRow{{id: "run-o2", tenantID: tenantA, pastGrace: false}}}

	runOneSweep(t, db, newFakeTemporalClient(), fastConfig())

	assert.Empty(t, db.recordedUpdates())
}

// TestReconciler_NotFoundWithoutTenantUnchanged verifies a run with no tenant is never orphaned:
// its workflow IDs cannot be built, so absence proves nothing.
func TestReconciler_NotFoundWithoutTenantUnchanged(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRows: []eligibleRow{{id: "run-o3", tenantID: "", pastGrace: true}}}

	runOneSweep(t, db, newFakeTemporalClient(), fastConfig())

	assert.Empty(t, db.recordedUpdates())
}

// TestReconciler_TemporalErrorOnLaterCandidateNoOrphan verifies a transient error is not read as
// NotFound: the run must not be failed when any lookup errored.
func TestReconciler_TemporalErrorOnLaterCandidateNoOrphan(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRows: []eligibleRow{{id: "run-o4", tenantID: tenantA, contextIDs: []string{"c1"}, pastGrace: true}}}
	tc := newFakeTemporalClient()
	tc.setErr("tenant-a:ctx-c1", unavailableErr())

	runOneSweep(t, db, tc, fastConfig())

	assert.Empty(t, db.recordedUpdates())
}

// TestReconciler_DryRunOrphanNoWrites verifies dry-run logs but never fails an orphan.
func TestReconciler_DryRunOrphanNoWrites(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRows: []eligibleRow{{id: "run-o5", tenantID: tenantA, pastGrace: true}}}
	cfg := fastConfig()
	cfg.DryRun = true

	runOneSweep(t, db, newFakeTemporalClient(), cfg)

	assert.Empty(t, db.recordedUpdates())
}

// ── Advisory lock handling ────────────────────────────────────────────────────

// TestReconciler_LockReleasedAfterSweep verifies the lock is released on the same handle after a sweep.
func TestReconciler_LockReleasedAfterSweep(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-l1"}}
	tc := newFakeTemporalClient()
	tc.set("run-l1", enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING)

	runOneSweep(t, db, tc, fastConfig())

	assert.Equal(t, 1, db.released, "lock must be released exactly once")
}

// TestReconciler_LockReleasedWhenQueryFails verifies an early return still releases the lock.
func TestReconciler_LockReleasedWhenQueryFails(t *testing.T) {
	db := &fakeDB{lockGranted: true, queryErr: errors.New("boom")}

	runOneSweep(t, db, newFakeTemporalClient(), fastConfig())

	assert.Equal(t, 1, db.released)
}

// TestReconciler_LockErrorSkipsSweep verifies a lock error runs nothing and releases nothing.
func TestReconciler_LockErrorSkipsSweep(t *testing.T) {
	db := &fakeDB{lockGranted: true, lockErr: errors.New("no connection"), eligibleRunIDs: []string{"run-l2"}, finishedRows: []finishedRow{{"run-f9", "failed"}}}
	tc := newFakeTemporalClient()
	tc.set("run-l2", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED)

	runOneSweep(t, db, tc, fastConfig())

	assert.Empty(t, db.recordedUpdates())
	assert.Empty(t, db.recordedChildCloses())
	assert.Equal(t, 0, db.released)
}

// ── Finished runs with open children ──────────────────────────────────────────

// TestReconciler_ClosesChildrenOfFinishedRuns verifies leftover children are closed with the run's own
// status; a failure-type status carries a reason, completed does not.
func TestReconciler_ClosesChildrenOfFinishedRuns(t *testing.T) {
	db := &fakeDB{lockGranted: true, finishedRows: []finishedRow{{"run-f1", "failed"}, {"run-f2", "completed"}, {"run-f3", "canceled"}}}

	runOneSweep(t, db, newFakeTemporalClient(), fastConfig())

	closes := db.recordedChildCloses()
	require.Len(t, closes, 3)
	assert.Equal(t, statusUpdate{runID: "run-f1", status: "failed", reason: "reconciler: run finished with open steps/tasks/approvals"}, closes[0])
	assert.Equal(t, statusUpdate{runID: "run-f2", status: "completed", reason: ""}, closes[1])
	assert.Equal(t, "canceled", closes[2].status)
	assert.Empty(t, db.recordedUpdates(), "the run rows themselves are never rewritten")
}

// TestReconciler_FinishedRunsDryRunNoWrites verifies dry-run only logs.
func TestReconciler_FinishedRunsDryRunNoWrites(t *testing.T) {
	db := &fakeDB{lockGranted: true, finishedRows: []finishedRow{{"run-f4", "failed"}}}
	cfg := fastConfig()
	cfg.DryRun = true

	runOneSweep(t, db, newFakeTemporalClient(), cfg)

	assert.Empty(t, db.recordedChildCloses())
}

// TestReconciler_FinishedRunsQueryErrorDoesNotBreakSweep verifies a failing finished-runs query is
// isolated: the stuck-run pass still applied its update.
func TestReconciler_FinishedRunsQueryErrorDoesNotBreakSweep(t *testing.T) {
	db := &fakeDB{lockGranted: true, eligibleRunIDs: []string{"run-f5"}, finishedErr: errors.New("boom")}
	tc := newFakeTemporalClient()
	tc.set("run-f5", enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED)

	runOneSweep(t, db, tc, fastConfig())

	require.Len(t, db.recordedUpdates(), 1)
	assert.Equal(t, 1, db.released)
}

// TestReconciler_FinishedRunsUsesChildrenGraceAndBatch verifies the grace (seconds) and limit parameters.
func TestReconciler_FinishedRunsUsesChildrenGraceAndBatch(t *testing.T) {
	db := &fakeDB{lockGranted: true}
	cfg := fastConfig()
	cfg.ChildrenGrace = 90 * time.Second
	cfg.BatchSize = 7

	runOneSweep(t, db, newFakeTemporalClient(), cfg)

	require.Len(t, db.finishedArgs, 2)
	assert.Equal(t, 90, db.finishedArgs[0])
	assert.Equal(t, 7, db.finishedArgs[1])
}

// ── MapTemporalStatus direct tests ────────────────────────────────────────────

// TestMapTemporalStatus covers all enum values directly to guard against
// future changes in the status mapping without needing a full reconciler run.
func TestMapTemporalStatus(t *testing.T) {
	cases := []struct {
		input    enumspb.WorkflowExecutionStatus
		wantSt   domain.RunStatus
		wantUp   bool
	}{
		{enumspb.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED, "", false},
		{enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, "", false},
		{enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED, domain.RunCompleted, true},
		{enumspb.WORKFLOW_EXECUTION_STATUS_FAILED, domain.RunFailed, true},
		{enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED, domain.RunCanceled, true},
		{enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED, domain.RunStopped, true},
		{enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW, "", false},
		{enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT, domain.RunFailed, true},
	}
	for _, tc := range cases {
		st, up := reconciler.MapTemporalStatus(tc.input)
		assert.Equal(t, tc.wantSt, st, "status mismatch for %v", tc.input)
		assert.Equal(t, tc.wantUp, up, "shouldUpdate mismatch for %v", tc.input)
	}
}

// TestIsNotFound verifies that gRPC NotFound errors are detected correctly and
// other errors are not misclassified.
func TestIsNotFound(t *testing.T) {
	assert.True(t, reconciler.IsNotFound(notFoundErr()), "gRPC NotFound should be detected")
	assert.False(t, reconciler.IsNotFound(unavailableErr()), "Unavailable must not be treated as NotFound")
	assert.False(t, reconciler.IsNotFound(errors.New("some other error")), "generic error must not be treated as NotFound")
	assert.False(t, reconciler.IsNotFound(nil), "nil must not be treated as NotFound")
}
