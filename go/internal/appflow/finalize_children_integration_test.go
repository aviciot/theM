//go:build integration

// Integration coverage for closeRunChildren / FinalizeRunActivity (F-122): a finished run
// must not leave run_steps, tasks or hil_approvals open. Needs a live Postgres (see
// trace_persist_integration_test.go for the DSN) with the seeded default tenant.
package appflow

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/domain"
)

const defaultTenantID = "00000000-0000-0000-0000-000000000001"

// seedOpenChildren gives a run one running step, one finished step, one working task,
// one finished task and one pending approval.
func seedOpenChildren(t *testing.T, pool *pgxpool.Pool, runID string) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		`INSERT INTO them.run_steps (run_id, iteration, node_id, node_kind, status) VALUES ($1::uuid, 0, 'n_open', 'llm', 'running')`,
		`INSERT INTO them.run_steps (run_id, iteration, node_id, node_kind, status) VALUES ($1::uuid, 0, 'n_done', 'llm', 'completed')`,
		`INSERT INTO them.tasks (id, run_id, state, kind, tenant_id) VALUES (gen_random_uuid(), $1::uuid, 'working', 'root', '` + defaultTenantID + `')`,
		`INSERT INTO them.tasks (id, run_id, state, kind, tenant_id) VALUES (gen_random_uuid(), $1::uuid, 'completed', 'delegated', '` + defaultTenantID + `')`,
		`INSERT INTO them.hil_approvals (id, tenant_id, application_id, run_id, node_id, approver_role, fallback_action, status)
		 VALUES (gen_random_uuid(), '` + defaultTenantID + `', gen_random_uuid(), $1::uuid, 'hil1', 'admin', 'reject', 'pending')`,
	}
	for _, q := range stmts {
		if _, err := pool.Exec(ctx, q, runID); err != nil {
			t.Fatalf("seed children: %v", err)
		}
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM them.hil_approvals WHERE run_id = $1::uuid`, runID)
		pool.Exec(ctx, `DELETE FROM them.tasks WHERE run_id = $1::uuid`, runID)
		pool.Exec(ctx, `DELETE FROM them.runs WHERE id = $1::uuid`, runID)
	})
}

func childStates(t *testing.T, pool *pgxpool.Pool, runID string) (steps, tasks, hil string) {
	t.Helper()
	ctx := context.Background()
	q := func(sql string) (s string) {
		if err := pool.QueryRow(ctx, sql, runID).Scan(&s); err != nil {
			t.Fatalf("query: %v", err)
		}
		return s
	}
	return q(`SELECT string_agg(node_id || '=' || status, ',' ORDER BY node_id) FROM them.run_steps WHERE run_id = $1::uuid`),
		q(`SELECT string_agg(state, ',' ORDER BY state) FROM them.tasks WHERE run_id = $1::uuid`),
		q(`SELECT string_agg(status, ',') FROM them.hil_approvals WHERE run_id = $1::uuid`)
}

func TestCloseRunChildren_FailedRunClosesOpenChildren(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), integrationDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close) // registered first so it runs after the seed cleanup
	runID := seedRun(t, pool)
	seedOpenChildren(t, pool, runID)
	other := seedRun(t, pool)
	seedOpenChildren(t, pool, other)

	a := &AppFlowActivities{DB: pool}
	a.closeRunChildren(context.Background(), runID, domain.RunFailed, "boom")
	a.closeRunChildren(context.Background(), runID, domain.RunFailed, "boom") // idempotent

	steps, tasks, hil := childStates(t, pool, runID)
	if steps != "n_done=completed,n_open=failed" {
		t.Errorf("steps = %q", steps)
	}
	if tasks != "completed,failed" {
		t.Errorf("tasks = %q", tasks)
	}
	if hil != "timed_out" {
		t.Errorf("hil = %q", hil)
	}
	var stepErr string
	if err := pool.QueryRow(context.Background(), `SELECT error FROM them.run_steps WHERE run_id = $1::uuid AND node_id = 'n_open'`, runID).Scan(&stepErr); err != nil || stepErr != "boom" {
		t.Errorf("step error = %q (%v), want boom", stepErr, err)
	}
	// another run's children are untouched
	steps, tasks, hil = childStates(t, pool, other)
	if steps != "n_done=completed,n_open=running" || tasks != "completed,working" || hil != "pending" {
		t.Errorf("other run touched: %q %q %q", steps, tasks, hil)
	}
}

func TestCloseRunChildren_CompletedRunCompletesOpenSteps(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), integrationDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close) // registered first so it runs after the seed cleanup
	runID := seedRun(t, pool)
	seedOpenChildren(t, pool, runID)

	(&AppFlowActivities{DB: pool}).closeRunChildren(context.Background(), runID, domain.RunCompleted, "")

	steps, tasks, _ := childStates(t, pool, runID)
	if steps != "n_done=completed,n_open=completed" || tasks != "completed,completed" {
		t.Errorf("steps=%q tasks=%q", steps, tasks)
	}
	var stepErr *string
	if err := pool.QueryRow(context.Background(), `SELECT error FROM them.run_steps WHERE run_id = $1::uuid AND node_id = 'n_open'`, runID).Scan(&stepErr); err != nil || stepErr != nil {
		t.Errorf("completed run must not add an error: %v %v", stepErr, err)
	}
}

type recordingStatusUpdater struct{ calls []string }

func (r *recordingStatusUpdater) UpdateRunStatus(_ context.Context, runID string, status domain.RunStatus, errMsg string) error {
	r.calls = append(r.calls, runID+"|"+string(status)+"|"+errMsg)
	return nil
}

// FinalizeRunActivity itself (not just the helper) must close the children after the status update.
func TestFinalizeRunActivity_ClosesChildren(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), integrationDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	runID := seedRun(t, pool)
	seedOpenChildren(t, pool, runID)

	upd := &recordingStatusUpdater{}
	a := &AppFlowActivities{DB: pool, StatusUpdater: upd}
	if err := a.FinalizeRunActivity(context.Background(), FinalizeRunActivityInput{RunID: runID, Status: "failed", ErrMsg: "node exploded"}); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	if len(upd.calls) != 1 || upd.calls[0] != runID+"|failed|node exploded" {
		t.Errorf("status updater calls = %v", upd.calls)
	}
	steps, tasks, hil := childStates(t, pool, runID)
	if steps != "n_done=completed,n_open=failed" || tasks != "completed,failed" || hil != "timed_out" {
		t.Errorf("children not closed: %q %q %q", steps, tasks, hil)
	}
}
