//go:build integration

// Integration coverage for persistTrace (docs/APP_CANVAS_DEBUG_PLAN.md Phase 3
// — durable trace storage). Requires a live Postgres with db/103_run_steps_
// appflow_trace.sql applied — the (run_id, node_id) partial unique index and
// node_id/node_kind columns exercised here don't exist without it.
package appflow

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// integrationDSN returns the test Postgres DSN from env or the standard
// host-exposed port for them-postgres (see docs/LOCAL_TEST_ENVIRONMENT_RUNBOOK.md).
func integrationDSN() string {
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://them:them_secret@localhost:15432/them?sslmode=disable"
}

// seedRun inserts a minimal them.runs row (FK target for run_steps) using the
// seeded default tenant, and returns its UUID string.
func seedRun(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	runID := uuid.NewString()
	const q = `
		INSERT INTO them.runs (id, tenant_id, status, started_at)
		VALUES ($1::uuid, '00000000-0000-0000-0000-000000000001'::uuid, 'running', now())`
	if _, err := pool.Exec(context.Background(), q, runID); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	return runID
}

func TestPersistTrace_NodeStartThenNodeDone_UpsertsOneRow(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), integrationDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	runID := seedRun(t, pool)
	defer pool.Exec(context.Background(), `DELETE FROM them.runs WHERE id = $1::uuid`, runID)

	a := &AppFlowActivities{DB: pool}
	a.persistTrace(context.Background(), runID, "cond1", "condition", "node_start", "")
	a.persistTrace(context.Background(), runID, "cond1", "condition", "node_done", "branch=true")

	var count int
	var status, output, nodeKind string
	var latencyMS *int64
	row := pool.QueryRow(context.Background(),
		`SELECT count(*) OVER (), status, COALESCE(output,''), node_kind, latency_ms
		 FROM them.run_steps WHERE run_id = $1::uuid AND node_id = 'cond1'`, runID)
	if err := row.Scan(&count, &status, &output, &nodeKind, &latencyMS); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 row for (run_id, node_id), got %d", count)
	}
	if status != "completed" {
		t.Fatalf("expected status=completed, got %q", status)
	}
	if output != "branch=true" {
		t.Fatalf("expected output=branch=true, got %q", output)
	}
	if nodeKind != "condition" {
		t.Fatalf("expected node_kind=condition, got %q", nodeKind)
	}
	if latencyMS == nil {
		t.Fatal("expected latency_ms to be set")
	}
}

func TestPersistTrace_NodeError_SetsFailedStatusAndError(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), integrationDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	runID := seedRun(t, pool)
	defer pool.Exec(context.Background(), `DELETE FROM them.runs WHERE id = $1::uuid`, runID)

	a := &AppFlowActivities{DB: pool}
	a.persistTrace(context.Background(), runID, "router1", "router", "node_start", "")
	a.persistTrace(context.Background(), runID, "router1", "router", "node_error", "no output_labels configured")

	var status, errText string
	row := pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(error,'') FROM them.run_steps WHERE run_id = $1::uuid AND node_id = 'router1'`, runID)
	if err := row.Scan(&status, &errText); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if status != "failed" {
		t.Fatalf("expected status=failed, got %q", status)
	}
	if errText != "no output_labels configured" {
		t.Fatalf("expected error text preserved, got %q", errText)
	}
}

func TestPersistTrace_NilDB_NoOp(t *testing.T) {
	a := &AppFlowActivities{}
	// Must not panic with a nil DB — same guard every other activity uses.
	a.persistTrace(context.Background(), uuid.NewString(), "n1", "condition", "node_start", "")
}

func TestPersistTrace_EmptyNodeID_NoOp(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), integrationDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	runID := seedRun(t, pool)
	defer pool.Exec(context.Background(), `DELETE FROM them.runs WHERE id = $1::uuid`, runID)

	a := &AppFlowActivities{DB: pool}
	// Empty node_id must not insert a row — node_id IS NOT NULL is the partial
	// index predicate that keeps orchestrator-mode rows (node_id always NULL)
	// out of this constraint; an empty string is a distinct, wrong case that
	// would otherwise silently create untraceable rows.
	a.persistTrace(context.Background(), runID, "", "condition", "node_start", "")

	var count int
	row := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM them.run_steps WHERE run_id = $1::uuid`, runID)
	if err := row.Scan(&count); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no rows for empty node_id, got %d", count)
	}
}
