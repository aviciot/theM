//go:build integration

package dal_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/aviciot/them/internal/admin/dal"
)

const cancelTestTenant = "00000000-0000-0000-0000-000000000001" // seeded default tenant

// CancelRun must flip a running run to canceled AND close the steps/tasks/approvals it left open.
func TestDAL_CancelRun_ClosesOpenChildren(t *testing.T) {
	pool := integrationPool(t)
	ctx := context.Background()
	runID := uuid.NewString()

	if _, err := pool.Exec(ctx, `INSERT INTO them.runs (id, tenant_id, status, started_at) VALUES ($1::uuid, $2::uuid, 'running', now())`, runID, cancelTestTenant); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM them.hil_approvals WHERE run_id = $1::uuid`, runID)
		pool.Exec(ctx, `DELETE FROM them.tasks WHERE run_id = $1::uuid`, runID)
		pool.Exec(ctx, `DELETE FROM them.runs WHERE id = $1::uuid`, runID)
	})
	for _, q := range []string{
		`INSERT INTO them.run_steps (run_id, iteration, node_id, node_kind, status) VALUES ($1::uuid, 0, 'n_open', 'llm', 'running')`,
		`INSERT INTO them.run_steps (run_id, iteration, node_id, node_kind, status) VALUES ($1::uuid, 0, 'n_done', 'llm', 'completed')`,
		`INSERT INTO them.tasks (id, run_id, state, kind, tenant_id) VALUES (gen_random_uuid(), $1::uuid, 'working', 'root', '` + cancelTestTenant + `')`,
		`INSERT INTO them.hil_approvals (id, tenant_id, application_id, run_id, node_id, approver_role, fallback_action, status)
		 VALUES (gen_random_uuid(), '` + cancelTestTenant + `', gen_random_uuid(), $1::uuid, 'hil1', 'admin', 'reject', 'pending')`,
	} {
		if _, err := pool.Exec(ctx, q, runID); err != nil {
			t.Fatalf("seed children: %v", err)
		}
	}

	d := dal.NewDB(newPgxQuerier(pool))
	run, err := d.CancelRun(ctx, cancelTestTenant, runID)
	if err != nil {
		t.Fatalf("CancelRun: %v", err)
	}
	if run.Status != "canceled" {
		t.Errorf("returned status = %q, want canceled", run.Status)
	}

	var steps, tasks, hil string
	pool.QueryRow(ctx, `SELECT string_agg(node_id || '=' || status, ',' ORDER BY node_id) FROM them.run_steps WHERE run_id = $1::uuid`, runID).Scan(&steps)
	pool.QueryRow(ctx, `SELECT string_agg(state, ',') FROM them.tasks WHERE run_id = $1::uuid`, runID).Scan(&tasks)
	pool.QueryRow(ctx, `SELECT string_agg(status, ',') FROM them.hil_approvals WHERE run_id = $1::uuid`, runID).Scan(&hil)
	if steps != "n_done=completed,n_open=failed" || tasks != "canceled" || hil != "timed_out" {
		t.Errorf("children not closed: steps=%q tasks=%q hil=%q", steps, tasks, hil)
	}

	// Cancelling again: the run is no longer running.
	if _, err := d.CancelRun(ctx, cancelTestTenant, runID); !dal.IsNoRows(err) {
		t.Errorf("second CancelRun err = %v, want no-rows", err)
	}
}
