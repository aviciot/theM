//go:build integration

package runrecorder

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aviciot/them/internal/domain"
)

func integrationDSN() string {
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://them:them_secret@localhost:15432/them?sslmode=disable"
}

// F-133 against the real DB: stopped is terminal (ended_at set) and a late writer cannot overwrite a
// finished run; a normal running -> completed transition still works.
func TestUpdateRunStatus_RealDB_TerminalGuard(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, integrationDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	rec := New(NewPgxPoolQuerier(pool))

	seed := func() string {
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `INSERT INTO them.runs (id, tenant_id, status, started_at) VALUES ($1::uuid, '00000000-0000-0000-0000-000000000001'::uuid, 'running', now())`, id); err != nil {
			t.Fatalf("seed: %v", err)
		}
		t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM them.runs WHERE id = $1::uuid`, id) })
		return id
	}
	state := func(id string) (status string, ended bool, errMsg string) {
		if err := pool.QueryRow(ctx, `SELECT status, ended_at IS NOT NULL, COALESCE(error,'') FROM them.runs WHERE id = $1::uuid`, id).Scan(&status, &ended, &errMsg); err != nil {
			t.Fatalf("read: %v", err)
		}
		return
	}

	stopped := seed()
	if err := rec.UpdateRunStatus(ctx, stopped, domain.RunStopped, "stopped by operator"); err != nil {
		t.Fatal(err)
	}
	if st, ended, e := state(stopped); st != "stopped" || !ended || e != "stopped by operator" {
		t.Errorf("stopped run: status=%q ended=%v error=%q", st, ended, e)
	}
	if err := rec.UpdateRunStatus(ctx, stopped, domain.RunFailed, "startup failed (late)"); err != nil {
		t.Fatal(err)
	}
	if st, _, e := state(stopped); st != "stopped" || e != "stopped by operator" {
		t.Errorf("late writer overwrote a finished run: status=%q error=%q", st, e)
	}

	normal := seed()
	if err := rec.UpdateRunStatus(ctx, normal, domain.RunCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if st, ended, _ := state(normal); st != "completed" || !ended {
		t.Errorf("normal completion: status=%q ended=%v", st, ended)
	}
}
