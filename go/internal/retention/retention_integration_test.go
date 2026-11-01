//go:build integration

package retention

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const itTenant = "00000000-0000-0000-0000-000000000001" // seeded default tenant

func itDSN() string {
	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://them:them_secret@localhost:15432/them?sslmode=disable"
}

// The built-in policies against the real tables. Every policy is narrowed to rows carrying this test's
// unique tag, so the test can never touch other data, while the policy's own filter (age, status, label
// pattern) still decides which tagged rows go.
func TestRetention_RealDB_AllPolicies(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, itDSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	tag := "retention-it-" + uuid.NewString()[:8]
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("count: %v\n%s", err, sql)
		}
		return n
	}
	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM them.access_tokens WHERE label LIKE $1`, "%"+tag+"%")
		pool.Exec(bg, `DELETE FROM them.task_messages WHERE task_id IN (SELECT id FROM them.tasks WHERE external_user_id = $1)`, tag)
		pool.Exec(bg, `DELETE FROM them.tasks WHERE external_user_id = $1`, tag)
		pool.Exec(bg, `DELETE FROM them.runs WHERE entry_point_slug = $1`, tag)
		pool.Exec(bg, `DELETE FROM them.storage_deletions WHERE storage_key LIKE $1`, tag+"%")
		pool.Exec(bg, `DELETE FROM them.gateway_requests WHERE provider = $1`, tag)
		pool.Exec(bg, `DELETE FROM them.audit_logs WHERE entity_type = $1`, tag)
	})

	// ── access_tokens: only expired playground-/tr- tokens past the period go ─────
	tok := func(name, label, expires string) {
		exec(`INSERT INTO them.access_tokens (token_hash, tenant_id, label, expires_at) VALUES ($1, $2::uuid, $3, `+expires+`)`, tag+name, itTenant, label)
	}
	tok("a", "playground-"+tag+"-old", `now() - interval '10 days'`) // gone
	tok("b", "playground-"+tag+"-new", `now() - interval '1 hour'`)  // kept: expired too recently
	tok("c", "real-ep-"+tag, `now() - interval '10 days'`)           // kept: not a temporary token
	tok("d", "playground-"+tag+"-live", `now() + interval '1 day'`)  // kept: still valid
	tok("e", "tr-"+tag+"-old", `now() - interval '10 days'`)         // gone

	// ── runs: finished and old go (with artifacts, approvals, stored-file queue); running/recent stay ──
	run := func(status, ended string) string {
		id := uuid.NewString()
		exec(`INSERT INTO them.runs (id, tenant_id, status, started_at, ended_at, entry_point_slug) VALUES ($1::uuid, $2::uuid, $3, `+ended+`, `+ended+`, $4)`, id, itTenant, status, tag)
		return id
	}
	oldDone, oldRunning, newDone := run("completed", `now() - interval '10 days'`), run("running", `now() - interval '10 days'`), run("completed", `now() - interval '1 hour'`)
	exec(`INSERT INTO them.run_artifacts (run_id, tenant_id, filename, content_type, size, storage_key) VALUES ($1::uuid, $2::uuid, 'f', 'x/y', 1, $3)`, oldDone, itTenant, tag+"/old.bin")

	// ── tasks (chat history): old finished go (messages cascade); open or recent stay ──
	task := func(state, updated string) string {
		id := uuid.NewString()
		exec(`INSERT INTO them.tasks (id, tenant_id, context_id, state, kind, external_user_id, updated_at) VALUES ($1::uuid, $2::uuid, gen_random_uuid(), $3, 'root', $4, `+updated+`)`, id, itTenant, state, tag)
		exec(`INSERT INTO them.task_messages (task_id, seq, role, parts) VALUES ($1::uuid, 1, 'user', '{}'::jsonb)`, id)
		return id
	}
	oldTask, openTask, newTask := task("completed", `now() - interval '10 days'`), task("working", `now() - interval '10 days'`), task("completed", `now() - interval '1 hour'`)

	// ── gateway_requests (+ bodies) and audit_logs ──
	gw := func(created string) string {
		id := uuid.NewString()
		exec(`INSERT INTO them.gateway_requests (id, tenant_id, provider, status, tokens_in, tokens_out, cost_usd, streamed, created_at) VALUES ($1::uuid, $2::uuid, $3, 'ok', 0, 0, 0, false, `+created+`)`, id, itTenant, tag)
		exec(`INSERT INTO them.gateway_request_bodies (request_id, tenant_id) VALUES ($1::uuid, $2::uuid)`, id, itTenant)
		return id
	}
	oldGw, newGw := gw(`now() - interval '10 days'`), gw(`now() - interval '1 hour'`)
	exec(`INSERT INTO them.audit_logs (action, entity_type, created_at) VALUES ('retention.test', $1, now() - interval '10 days')`, tag)
	exec(`INSERT INTO them.audit_logs (action, entity_type, created_at) VALUES ('retention.test', $1, now() - interval '1 hour')`, tag)

	// Policies at 1 day, narrowed to this test's rows.
	narrow := map[string]string{
		"ACCESS_TOKENS": ` AND label LIKE '%` + tag + `%'`,
		"RUNS":          ` AND entry_point_slug = '` + tag + `'`,
		"CHAT":          ` AND external_user_id = '` + tag + `'`,
		"GATEWAY":       ` AND provider = '` + tag + `'`,
		"AUDIT":         ` AND entity_type = '` + tag + `'`,
	}
	days := map[string]int{"ACCESS_TOKENS": 1, "RUNS": 1, "CHAT": 1, "GATEWAY": 1, "AUDIT": 1}
	policies := DefaultPolicies(days)
	for i := range policies {
		policies[i].Where += narrow[policies[i].Name]
	}
	db := NewPgxDB(pool)

	// Dry-run: counts the eligible rows and deletes nothing.
	out := RunOnce(ctx, db, Config{Policies: policies, Batch: 100, DryRun: true}, quiet)
	want := map[string]int64{"ACCESS_TOKENS": 2, "RUNS": 1, "CHAT": 1, "GATEWAY": 1, "AUDIT": 1}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("dry-run %s would delete %d, want %d", k, out[k], v)
		}
	}
	if n := count(`SELECT count(*) FROM them.access_tokens WHERE label LIKE $1`, "%"+tag+"%"); n != 5 {
		t.Fatalf("dry-run deleted tokens: %d left, want 5", n)
	}

	// Real run.
	out = RunOnce(ctx, db, Config{Policies: policies, Batch: 100, DryRun: false}, quiet)
	for k, v := range want {
		if out[k] != v {
			t.Errorf("deleted %s = %d, want %d", k, out[k], v)
		}
	}

	if n := count(`SELECT count(*) FROM them.access_tokens WHERE token_hash IN ($1,$2)`, tag+"a", tag+"e"); n != 0 {
		t.Errorf("expired temporary tokens must be gone, %d left", n)
	}
	if n := count(`SELECT count(*) FROM them.access_tokens WHERE token_hash IN ($1,$2,$3)`, tag+"b", tag+"c", tag+"d"); n != 3 {
		t.Errorf("recent, real and valid tokens must stay, %d of 3 left", n)
	}

	if n := count(`SELECT count(*) FROM them.runs WHERE id = $1::uuid`, oldDone); n != 0 {
		t.Error("old finished run must be gone")
	}
	if n := count(`SELECT count(*) FROM them.run_artifacts WHERE run_id = $1::uuid`, oldDone); n != 0 {
		t.Error("its artifacts must cascade")
	}
	if n := count(`SELECT count(*) FROM them.storage_deletions WHERE storage_key = $1`, tag+"/old.bin"); n != 1 {
		t.Errorf("its stored file must be queued for the janitor, got %d", n)
	}
	if n := count(`SELECT count(*) FROM them.runs WHERE id IN ($1::uuid, $2::uuid)`, oldRunning, newDone); n != 2 {
		t.Errorf("a running run and a recent run must stay, %d of 2 left", n)
	}

	if n := count(`SELECT count(*) FROM them.tasks WHERE id = $1::uuid`, oldTask); n != 0 {
		t.Error("old finished chat task must be gone")
	}
	if n := count(`SELECT count(*) FROM them.task_messages WHERE task_id = $1::uuid`, oldTask); n != 0 {
		t.Error("its messages must cascade")
	}
	if n := count(`SELECT count(*) FROM them.tasks WHERE id IN ($1::uuid, $2::uuid)`, openTask, newTask); n != 2 {
		t.Errorf("an open task and a recent task must stay, %d of 2 left", n)
	}

	if n := count(`SELECT count(*) FROM them.gateway_requests WHERE id = $1::uuid`, oldGw); n != 0 {
		t.Error("old gateway request must be gone")
	}
	if n := count(`SELECT count(*) FROM them.gateway_request_bodies WHERE request_id = $1::uuid`, oldGw); n != 0 {
		t.Error("its bodies must cascade")
	}
	if n := count(`SELECT count(*) FROM them.gateway_requests WHERE id = $1::uuid`, newGw); n != 1 {
		t.Error("recent gateway request must stay")
	}
	if n := count(`SELECT count(*) FROM them.audit_logs WHERE entity_type = $1`, tag); n != 1 {
		t.Errorf("only the recent audit row must stay, %d left", n)
	}
}
