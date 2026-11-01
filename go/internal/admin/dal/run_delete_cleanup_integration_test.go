//go:build integration

package dal_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deleting a run removes its artifacts and approvals, queues the stored file for deletion, and keeps
// the conversation history (tasks.run_id is SET NULL by design). db/127, backlog F-125.

type runSeed struct {
	runID, taskID, key string
}

// seedRunWithChildren creates a run with: an artifact that points at a stored file, a pending approval,
// and a chat task with one message.
func seedRunWithChildren(t *testing.T, pool *pgxpool.Pool, tenantID, appID string, withFileKey bool) runSeed {
	t.Helper()
	ctx := context.Background()
	s := runSeed{runID: uuid.NewString(), taskID: uuid.NewString()}
	var key any
	if withFileKey {
		s.key = "inttest/" + uuid.NewString() + ".bin"
		key = s.key
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO them.runs (id, tenant_id, status, started_at) VALUES ($1::uuid, $2::uuid, 'completed', now())`, []any{s.runID, tenantID}},
		{`INSERT INTO them.run_artifacts (run_id, application_id, tenant_id, filename, content_type, size, storage_key)
		  VALUES ($1::uuid, $2::uuid, $3::uuid, 'f.bin', 'application/octet-stream', 1, $4)`, []any{s.runID, appID, tenantID, key}},
		{`INSERT INTO them.hil_approvals (tenant_id, application_id, run_id, node_id, approver_role, fallback_action, status)
		  VALUES ($1::uuid, $2::uuid, $3::uuid, 'hil1', 'admin', 'reject', 'pending')`, []any{tenantID, appID, s.runID}},
		{`INSERT INTO them.tasks (id, run_id, tenant_id, context_id, state, kind) VALUES ($1::uuid, $2::uuid, $3::uuid, gen_random_uuid(), 'completed', 'root')`, []any{s.taskID, s.runID, tenantID}},
		{`INSERT INTO them.task_messages (task_id, seq, role, parts) VALUES ($1::uuid, 1, 'user', '{}'::jsonb)`, []any{s.taskID}},
	} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, q.sql)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM them.task_messages WHERE task_id = $1::uuid`, s.taskID)
		pool.Exec(bg, `DELETE FROM them.tasks WHERE id = $1::uuid`, s.taskID)
		pool.Exec(bg, `DELETE FROM them.runs WHERE id = $1::uuid`, s.runID) // cascades artifacts/approvals
		if s.key != "" {
			pool.Exec(bg, `DELETE FROM them.storage_deletions WHERE storage_key = $1`, s.key)
		}
	})
	return s
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v\n%s", err, sql)
	}
	return n
}

func TestDAL_DeleteRun_CascadesArtifactsAndApprovals_QueuesFile_KeepsHistory(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-run-delete")
	s := seedRunWithChildren(t, pool, tenantID, appID, true)

	if err := d.DeleteRun(context.Background(), tenantID, s.runID); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}

	if n := count(t, pool, `SELECT count(*) FROM them.run_artifacts WHERE run_id = $1::uuid`, s.runID); n != 0 {
		t.Errorf("run_artifacts left behind: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM them.hil_approvals WHERE run_id = $1::uuid`, s.runID); n != 0 {
		t.Errorf("hil_approvals left behind: %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM them.storage_deletions WHERE storage_key = $1 AND bucket = 'artifacts' AND tenant_id = $2::uuid`, s.key, tenantID); n != 1 {
		t.Errorf("stored file must be queued exactly once for deletion, got %d", n)
	}
	// conversation history is kept on purpose
	if n := count(t, pool, `SELECT count(*) FROM them.tasks WHERE id = $1::uuid AND run_id IS NULL`, s.taskID); n != 1 {
		t.Errorf("chat task must survive with run_id NULL, got %d", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM them.task_messages WHERE task_id = $1::uuid`, s.taskID); n != 1 {
		t.Errorf("chat messages must survive, got %d", n)
	}
}

func TestDAL_BulkDeleteRuns_CascadesAndQueuesEachFile(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-run-bulk-delete")
	a := seedRunWithChildren(t, pool, tenantID, appID, true)
	b := seedRunWithChildren(t, pool, tenantID, appID, true)
	keep := seedRunWithChildren(t, pool, tenantID, appID, true)

	n, err := d.BulkDeleteRuns(context.Background(), tenantID, []string{a.runID, b.runID})
	if err != nil || n != 2 {
		t.Fatalf("BulkDeleteRuns n=%d err=%v", n, err)
	}
	if c := count(t, pool, `SELECT count(*) FROM them.run_artifacts WHERE run_id IN ($1::uuid, $2::uuid)`, a.runID, b.runID); c != 0 {
		t.Errorf("artifacts left behind: %d", c)
	}
	if c := count(t, pool, `SELECT count(*) FROM them.storage_deletions WHERE storage_key IN ($1, $2)`, a.key, b.key); c != 2 {
		t.Errorf("both files must be queued, got %d", c)
	}
	if c := count(t, pool, `SELECT count(*) FROM them.run_artifacts WHERE run_id = $1::uuid`, keep.runID); c != 1 {
		t.Errorf("the run that was not deleted lost its artifact: %d", c)
	}
	if c := count(t, pool, `SELECT count(*) FROM them.storage_deletions WHERE storage_key = $1`, keep.key); c != 0 {
		t.Errorf("the run that was not deleted must not queue its file: %d", c)
	}
}

func TestDAL_DeleteRun_ArtifactWithoutStoredFile_QueuesNothing(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-run-delete-nofile")
	s := seedRunWithChildren(t, pool, tenantID, appID, false) // bytes live in Postgres only

	if err := d.DeleteRun(context.Background(), tenantID, s.runID); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM them.storage_deletions WHERE tenant_id = $1::uuid`, tenantID); n != 0 {
		t.Errorf("no stored file, so nothing to queue; got %d", n)
	}
}

func TestDAL_ForceDeleteTenant_RemovesRunDataAndChatHistory_QueuesFiles(t *testing.T) {
	pool := integrationPool(t)
	d := newProviderDAL(t, pool)
	tenantID, appID := setupAppScopedConfigApp(t, pool, "inttest-force-delete-tenant")
	s := seedRunWithChildren(t, pool, tenantID, appID, true)

	if err := d.ForceDeleteTenant(context.Background(), tenantID); err != nil {
		t.Fatalf("ForceDeleteTenant: %v", err)
	}
	for name, n := range map[string]int{
		"runs":          count(t, pool, `SELECT count(*) FROM them.runs WHERE id = $1::uuid`, s.runID),
		"run_artifacts": count(t, pool, `SELECT count(*) FROM them.run_artifacts WHERE run_id = $1::uuid`, s.runID),
		"hil_approvals": count(t, pool, `SELECT count(*) FROM them.hil_approvals WHERE run_id = $1::uuid`, s.runID),
		"tasks":         count(t, pool, `SELECT count(*) FROM them.tasks WHERE id = $1::uuid`, s.taskID),
		"task_messages": count(t, pool, `SELECT count(*) FROM them.task_messages WHERE task_id = $1::uuid`, s.taskID),
	} {
		if n != 0 {
			t.Errorf("%s left behind after tenant delete: %d", name, n)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM them.storage_deletions WHERE storage_key = $1`, s.key); n != 1 {
		t.Errorf("stored file must be queued when the tenant is deleted, got %d", n)
	}
}
