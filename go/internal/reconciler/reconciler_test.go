package reconciler_test

import (
	"context"
	"errors"
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

	// eligibleRunIDs are returned by the Query call.
	eligibleRunIDs []string

	// queryErr is returned by Query if non-nil.
	queryErr error

	// updates accumulates (runID, status) pairs written via Exec.
	updates []statusUpdate

	// execErr is returned by Exec if non-nil.
	execErr error
}

type statusUpdate struct {
	runID  string
	status string
}

type fakeScanner struct{ val any }

func (s *fakeScanner) Scan(dest ...any) error {
	if len(dest) == 0 {
		return nil
	}
	switch d := dest[0].(type) {
	case *bool:
		if b, ok := s.val.(bool); ok {
			*d = b
		}
	case *string:
		if str, ok := s.val.(string); ok {
			*d = str
		}
	}
	return nil
}

type fakeRows struct {
	ids  []string
	pos  int
	err  error
}

func (r *fakeRows) Next() bool  { r.pos++; return r.pos <= len(r.ids) }
func (r *fakeRows) Scan(dest ...any) error {
	if len(dest) > 0 {
		if d, ok := dest[0].(*string); ok {
			*d = r.ids[r.pos-1]
		}
	}
	return nil
}
func (r *fakeRows) Err() error { return r.err }
func (r *fakeRows) Close()     {}

func (db *fakeDB) QueryRow(_ context.Context, sql string, args ...any) reconciler.PgxScanner {
	db.mu.Lock()
	defer db.mu.Unlock()
	// advisory lock/unlock calls return lockGranted
	return &fakeScanner{val: db.lockGranted}
}

func (db *fakeDB) Query(_ context.Context, sql string, args ...any) (reconciler.PgxRowsIterator, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.queryErr != nil {
		return nil, db.queryErr
	}
	return &fakeRows{ids: db.eligibleRunIDs}, nil
}

func (db *fakeDB) Exec(_ context.Context, sql string, args ...any) (int64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.execErr != nil {
		return 0, db.execErr
	}
	if len(args) >= 2 {
		runID, _ := args[0].(string)
		st, _ := args[1].(string)
		db.updates = append(db.updates, statusUpdate{runID: runID, status: st})
	}
	return 1, nil
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
// Temporal produces no DB write (the row is left as "running").
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
