// Package reconciler periodically inspects them.runs rows that are stuck in
// "running" status and reconciles them against the authoritative state in
// Temporal.
//
// # Identity contract
//
// A run's Temporal Workflow ID is not its run ID. Canvas (AppFlow) runs use
// "appflow:{tenant}:{runID}"; orchestration (WS/SSE) runs share one workflow per
// session, "{tenant}:ctx-{contextID}", where contextID comes from the run's tasks.
// For each eligible row the reconciler tries those IDs (see stuckRun.workflowIDs),
// plus the bare run ID, and maps the first workflow it finds back to the DB status.
//
// # Status mapping
//
//	Temporal status          →  DB status written
//	RUNNING                  →  no update (run is progressing normally)
//	COMPLETED                →  "completed"
//	FAILED                   →  "failed"
//	CANCELED                 →  "canceled"  (canonical single-L per DB CHECK)
//	TERMINATED               →  "stopped"   (ADR-002: operator-initiated stop)
//	CONTINUED_AS_NEW         →  no update   (new execution is active)
//	TIMED_OUT                →  "failed"    (ADR-002: no "timed_out" in DB schema)
//
// # NotFound policy
//
// Temporal always finds a running workflow, and keeps closed ones only for the
// namespace retention (24 h by default). So NotFound for every candidate ID means the
// run has no live workflow. When the run is also older than Config.NotFoundGrace
// (default 2 h, longer than the 1 h platform run timeout) and has a tenant (needed to
// build the IDs), it is marked "failed" (orphaned). Younger runs, and runs without a
// tenant, are left unchanged. A run's tasks, steps and HIL approvals are closed with it.
//
// # Multi-pod coordination
//
// Each sweep acquires a PostgreSQL advisory lock (pg_try_advisory_lock) keyed on
// a constant application-level integer. Only one pod runs the sweep at a time. If
// the acquiring pod crashes the lock is automatically released by PostgreSQL on
// connection close — no separate expiry needed.
//
// # Safe rollout
//
// DryRun defaults to true in Config. In dry-run mode the reconciler logs intended
// changes and increments metrics but performs no DB writes.
package reconciler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	temporalclient "go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/aviciot/them/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// advisoryLockKey is a stable int64 for the PostgreSQL advisory lock.
// Must not collide with other pg_advisory_lock users in the same database.
const advisoryLockKey = int64(987654321)

// Config controls reconciler behaviour.
type Config struct {
	// Interval between sweeps. Default: 60s.
	Interval time.Duration

	// BatchSize is the maximum number of rows processed per sweep. Default: 100.
	BatchSize int

	// StaleAfter is the minimum age a "running" row must have before being
	// eligible. Prevents racing with runs that just started. Default: 2 minutes.
	StaleAfter time.Duration

	// TemporalNamespace is the Temporal namespace to query. Default: "default".
	TemporalNamespace string

	// DryRun controls whether DB writes are performed. Default: true (no writes).
	// Set to false explicitly to enable reconciliation writes.
	DryRun bool

	// Concurrency is the number of concurrent DescribeWorkflowExecution calls
	// per sweep. Default: 5.
	Concurrency int

	// NotFoundGrace is how old a "running" row must be before NotFound from Temporal
	// is treated as "orphaned" and the run is failed. Default: 2 hours.
	NotFoundGrace time.Duration
}

func (c *Config) applyDefaults() {
	if c.Interval == 0 {
		c.Interval = 60 * time.Second
	}
	if c.BatchSize == 0 {
		c.BatchSize = 100
	}
	if c.StaleAfter == 0 {
		c.StaleAfter = 2 * time.Minute
	}
	if c.TemporalNamespace == "" {
		c.TemporalNamespace = "default"
	}
	if c.Concurrency == 0 {
		c.Concurrency = 5
	}
	if c.NotFoundGrace == 0 {
		c.NotFoundGrace = 2 * time.Hour
	}
}

// ── Prometheus metrics ────────────────────────────────────────────────────────

var (
	metricScanned = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "them", Subsystem: "reconciler", Name: "scanned_total",
		Help: "Stuck-running rows examined per sweep.",
	})
	metricUnchanged = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "them", Subsystem: "reconciler", Name: "unchanged_total",
		Help: "Rows left unchanged because Temporal workflow is still running.",
	})
	metricUpdated = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "them", Subsystem: "reconciler", Name: "updated_total",
		Help: "Rows updated to a terminal status by the reconciler.",
	})
	metricNotFound = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "them", Subsystem: "reconciler", Name: "notfound_total",
		Help: "Rows where Temporal returned NotFound for every candidate workflow ID.",
	})
	metricOrphaned = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "them", Subsystem: "reconciler", Name: "orphaned_total",
		Help: "Old running rows failed because no Temporal workflow exists for them.",
	})
	metricErrors = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "them", Subsystem: "reconciler", Name: "errors_total",
		Help: "Reconciler errors (Temporal unavailable, DB write failure, etc.).",
	})
	metricDryRun = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "them", Subsystem: "reconciler", Name: "dryrun_total",
		Help: "Rows that would have been updated in dry-run mode.",
	})
)

// DBQuerier is the database interface used by the reconciler.
// The production implementation wraps pgxpool.Pool; tests inject a fake.
type DBQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) PgxScanner
	Query(ctx context.Context, sql string, args ...any) (PgxRowsIterator, error)
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
}

// PgxScanner is a minimal interface satisfied by pgx.Row. Exported for tests.
type PgxScanner interface {
	Scan(dest ...any) error
}

// PgxRowsIterator is a minimal interface satisfied by pgx.Rows. Exported for tests.
type PgxRowsIterator interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close()
}

// PgxQuerier wraps *pgxpool.Pool to satisfy DBQuerier.
type PgxQuerier struct{ pool *pgxpool.Pool }

// NewPgxQuerier wraps a pgxpool.Pool for use as a DBQuerier.
func NewPgxQuerier(p *pgxpool.Pool) *PgxQuerier { return &PgxQuerier{pool: p} }

func (q *PgxQuerier) QueryRow(ctx context.Context, sql string, args ...any) PgxScanner {
	return q.pool.QueryRow(ctx, sql, args...)
}

func (q *PgxQuerier) Query(ctx context.Context, sql string, args ...any) (PgxRowsIterator, error) {
	return q.pool.Query(ctx, sql, args...)
}

func (q *PgxQuerier) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := q.pool.Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// Reconciler sweeps for stuck runs and reconciles them against Temporal.
type Reconciler struct {
	cfg    Config
	db     DBQuerier
	tc     temporalclient.Client
	logger *slog.Logger
}

// New creates a Reconciler. DryRun defaults to true unless the caller sets
// cfg.DryRun = false explicitly.
func New(cfg Config, db DBQuerier, tc temporalclient.Client, logger *slog.Logger) *Reconciler {
	if logger == nil {
		logger = slog.Default()
	}
	cfg.applyDefaults()
	return &Reconciler{cfg: cfg, db: db, tc: tc, logger: logger}
}

// Run starts the reconciliation loop and blocks until ctx is cancelled.
// It is the top-level entry point for wiring in main.go.
func Run(ctx context.Context, cfg Config, db DBQuerier, tc temporalclient.Client, logger *slog.Logger) {
	New(cfg, db, tc, logger).loop(ctx)
}

// SweepOnce executes a single reconciliation sweep and returns. It is exported
// for use in unit tests that need deterministic, single-pass control.
func (r *Reconciler) SweepOnce(ctx context.Context) { r.sweep(ctx) }

func (r *Reconciler) loop(ctx context.Context) {
	r.logger.Info("reconciler: starting",
		"interval", r.cfg.Interval,
		"batch_size", r.cfg.BatchSize,
		"stale_after", r.cfg.StaleAfter,
		"dry_run", r.cfg.DryRun,
		"concurrency", r.cfg.Concurrency,
	)

	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()

	// Run once immediately, then on each tick.
	r.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("reconciler: stopping")
			return
		case <-ticker.C:
			r.sweep(ctx)
		}
	}
}

// $1 stale seconds, $2 limit, $3 NotFoundGrace seconds.
const eligibleQuery = `
	SELECT r.id::text,
	       COALESCE(r.tenant_id::text, ''),
	       COALESCE((SELECT array_agg(DISTINCT t.context_id::text)
	                 FROM them.tasks t
	                 WHERE t.run_id = r.id AND t.context_id IS NOT NULL), '{}'::text[]),
	       r.started_at < now() - ($3 * interval '1 second')
	FROM them.runs r
	WHERE r.status = 'running'
	  AND r.started_at < now() - ($1 * interval '1 second')
	ORDER BY r.started_at
	LIMIT $2`

// sweep runs one reconciliation pass under an advisory lock.
func (r *Reconciler) sweep(ctx context.Context) {
	// Try to acquire advisory lock. Non-blocking — skip if another pod holds it.
	var locked bool
	if err := r.db.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockKey).Scan(&locked); err != nil {
		r.logger.Warn("reconciler: advisory lock query failed", "error", err)
		metricErrors.Inc()
		return
	}
	if !locked {
		r.logger.Debug("reconciler: advisory lock held by another pod — skipping sweep")
		return
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var released bool
		_ = r.db.QueryRow(releaseCtx, "SELECT pg_advisory_unlock($1)", advisoryLockKey).Scan(&released)
	}()

	staleSeconds := int(r.cfg.StaleAfter.Seconds())
	rows, err := r.db.Query(ctx, eligibleQuery, staleSeconds, r.cfg.BatchSize, int(r.cfg.NotFoundGrace.Seconds()))
	if err != nil {
		r.logger.Error("reconciler: failed to query eligible rows", "error", err)
		metricErrors.Inc()
		return
	}
	defer rows.Close()

	var runs []stuckRun
	for rows.Next() {
		var run stuckRun
		if err := rows.Scan(&run.ID, &run.TenantID, &run.ContextIDs, &run.PastGrace); err != nil {
			r.logger.Warn("reconciler: scan error", "error", err)
			continue
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		r.logger.Error("reconciler: rows iteration error", "error", err)
		metricErrors.Inc()
		return
	}

	if len(runs) == 0 {
		r.logger.Debug("reconciler: no eligible rows in this sweep")
		return
	}

	r.logger.Info("reconciler: sweep started", "eligible_count", len(runs), "dry_run", r.cfg.DryRun)
	r.reconcileBatch(ctx, runs)
}

// reconcileBatch processes runs with bounded concurrency.
func (r *Reconciler) reconcileBatch(ctx context.Context, runs []stuckRun) {
	sem := make(chan struct{}, r.cfg.Concurrency)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, run := range runs {
			sem <- struct{}{}
			go func(run stuckRun) {
				defer func() { <-sem }()
				r.reconcileOne(ctx, run)
			}(run)
		}
		// Drain semaphore — wait for all goroutines.
		for i := 0; i < cap(sem); i++ {
			sem <- struct{}{}
		}
	}()
	<-done
}

// describeRun returns the status of the first candidate workflow that exists.
// found=false with err=nil means every candidate ID was NotFound.
func (r *Reconciler) describeRun(ctx context.Context, run stuckRun) (st enumspb.WorkflowExecutionStatus, found bool, err error) {
	for _, wfID := range run.workflowIDs() {
		resp, derr := r.tc.DescribeWorkflowExecution(ctx, wfID, "")
		if derr == nil {
			return resp.WorkflowExecutionInfo.GetStatus(), true, nil
		}
		if !isNotFound(derr) {
			return 0, false, derr
		}
	}
	return 0, false, nil
}

// reconcileOne reconciles a single run against Temporal.
func (r *Reconciler) reconcileOne(ctx context.Context, run stuckRun) {
	metricScanned.Inc()
	runID := run.ID

	tStatus, found, err := r.describeRun(ctx, run)
	if err != nil {
		// Temporal unavailable or transient error — skip, do not update DB.
		r.logger.Warn("reconciler: DescribeWorkflowExecution failed — skipping",
			"run_id", runID, "error", err)
		metricErrors.Inc()
		return
	}
	if !found {
		r.handleNotFound(ctx, run)
		return
	}

	newStatus, shouldUpdate := mapTemporalStatus(tStatus)
	if !shouldUpdate {
		r.logger.Debug("reconciler: workflow still active — no update",
			"run_id", runID, "temporal_status", tStatus)
		metricUnchanged.Inc()
		return
	}

	reason := ""
	if newStatus != domain.RunCompleted {
		reason = temporalReason(tStatus.String())
	}
	if r.cfg.DryRun {
		r.logger.Info("reconciler: [dry-run] would update run",
			"run_id", runID, "new_status", string(newStatus), "temporal_status", tStatus.String())
		metricDryRun.Inc()
		return
	}
	r.apply(ctx, runID, newStatus, reason, tStatus.String())
}

// handleNotFound fails an old run that has no workflow under any candidate ID.
func (r *Reconciler) handleNotFound(ctx context.Context, run stuckRun) {
	metricNotFound.Inc()
	if !run.PastGrace || run.TenantID == "" {
		r.logger.Warn("reconciler: workflow not found — leaving run unchanged",
			"run_id", run.ID, "past_grace", run.PastGrace, "has_tenant", run.TenantID != "")
		return
	}
	if r.cfg.DryRun {
		r.logger.Info("reconciler: [dry-run] would fail orphaned run", "run_id", run.ID)
		metricDryRun.Inc()
		return
	}
	if r.apply(ctx, run.ID, domain.RunFailed, orphanReason, "NotFound") {
		metricOrphaned.Inc()
	}
}

// apply writes the terminal status (and closes children). Returns true if the row changed.
func (r *Reconciler) apply(ctx context.Context, runID string, status domain.RunStatus, reason, source string) bool {
	n, err := r.writeStatus(ctx, runID, status, reason)
	if err != nil {
		r.logger.Error("reconciler: failed to update run status",
			"run_id", runID, "new_status", string(status), "error", err)
		metricErrors.Inc()
		return false
	}
	if n == 0 {
		r.logger.Debug("reconciler: run already finalized — no change", "run_id", runID)
		return false
	}
	r.logger.Info("reconciler: updated run status",
		"run_id", runID, "new_status", string(status), "source", source)
	metricUpdated.Inc()
	return true
}

// writeStatus closes the run and its children in one statement (see closeRunQuery).
// The returned count is 0 when the run was no longer 'running'.
func (r *Reconciler) writeStatus(ctx context.Context, runID string, status domain.RunStatus, reason string) (int64, error) {
	n, err := r.db.Exec(ctx, closeRunQuery, runID, string(status), reason)
	if err != nil {
		return 0, fmt.Errorf("reconciler: update run %s to %s: %w", runID, status, err)
	}
	return n, nil
}

// MapTemporalStatus converts a Temporal workflow execution status to a DB
// RunStatus. Returns ("", false) when the workflow is still active or the
// status does not have a direct DB mapping. Exported for tests.
//
// ADR-002 documents the TERMINATED→"stopped" and TIMED_OUT→"failed" choices.
func MapTemporalStatus(s enumspb.WorkflowExecutionStatus) (domain.RunStatus, bool) {
	return mapTemporalStatus(s)
}

func mapTemporalStatus(s enumspb.WorkflowExecutionStatus) (domain.RunStatus, bool) {
	switch s {
	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return "", false
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return domain.RunCompleted, true
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:
		return domain.RunFailed, true
	case enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return domain.RunCanceled, true
	case enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		// Operator-initiated stop. "stopped" is in the DB CHECK constraint and is
		// semantically distinct from a failure. See ADR-002.
		return domain.RunStopped, true
	case enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		// A new execution is active — treat as still running.
		return "", false
	case enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		// No "timed_out" status in the DB schema. Mapped to "failed". See ADR-002.
		return domain.RunFailed, true
	default:
		return "", false
	}
}

// IsNotFound returns true when err indicates the workflow does not exist in
// Temporal. Exported for tests.
func IsNotFound(err error) bool { return isNotFound(err) }

// isNotFound returns true when err indicates the workflow does not exist in
// Temporal (history retention expiry, wrong ID, etc.).
//
// The Temporal Go SDK surfaces this as a gRPC status error with code
// codes.NotFound. We check via google.golang.org/grpc/status which is already
// a transitive dependency of the Temporal SDK.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	// Primary check: gRPC codes.NotFound (the authoritative signal from
	// Temporal's service layer).
	if st, ok := grpcstatus.FromError(err); ok && st.Code() == codes.NotFound {
		return true
	}
	// Fallback: plain string match for SDK-wrapped errors that lose gRPC status.
	msg := err.Error()
	return strings.Contains(msg, "workflow not found")
}
