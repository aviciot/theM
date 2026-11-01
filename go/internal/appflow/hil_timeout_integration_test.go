//go:build integration

package appflow

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MarkHILTimedOutActivity against the real DB: only the pending approval of the given node becomes
// timed_out (with the comment); decided approvals and other runs are untouched; repeat is a no-op.
func TestMarkHILTimedOutActivity_RealDB(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, integrationDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	runID, other := seedRun(t, pool), seedRun(t, pool)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM them.hil_approvals WHERE run_id IN ($1::uuid, $2::uuid)`, runID, other)
		pool.Exec(ctx, `DELETE FROM them.runs WHERE id IN ($1::uuid, $2::uuid)`, runID, other)
	})
	ins := func(run, node, status string) {
		if _, err := pool.Exec(ctx, `INSERT INTO them.hil_approvals (id, tenant_id, application_id, run_id, node_id, approver_role, fallback_action, status)
			VALUES (gen_random_uuid(), '`+defaultTenantID+`', gen_random_uuid(), $1::uuid, $2, 'admin', 'reject', $3)`, run, node, status); err != nil {
			t.Fatalf("seed approval: %v", err)
		}
	}
	ins(runID, "hil_a", "pending")
	ins(runID, "hil_b", "approved") // already decided
	ins(other, "hil_a", "pending")  // other run

	a := &AppFlowActivities{DB: pool}
	for i := 0; i < 2; i++ { // second call must be a no-op
		if err := a.MarkHILTimedOutActivity(ctx, HILTimedOutInput{RunID: runID, NodeID: "hil_a", Comment: "timeout-rejected"}); err != nil {
			t.Fatalf("activity: %v", err)
		}
	}

	get := func(run, node string) (status, comment string) {
		pool.QueryRow(ctx, `SELECT status, COALESCE(comment,'') FROM them.hil_approvals WHERE run_id = $1::uuid AND node_id = $2`, run, node).Scan(&status, &comment)
		return
	}
	if st, c := get(runID, "hil_a"); st != "timed_out" || c != "timeout-rejected" {
		t.Errorf("hil_a = %q/%q, want timed_out/timeout-rejected", st, c)
	}
	if st, _ := get(runID, "hil_b"); st != "approved" {
		t.Errorf("decided approval was touched: %q", st)
	}
	if st, _ := get(other, "hil_a"); st != "pending" {
		t.Errorf("another run's approval was touched: %q", st)
	}
}
