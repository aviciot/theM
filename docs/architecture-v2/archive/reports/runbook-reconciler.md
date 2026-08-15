# Operational Runbook — Run Reconciler (Phase 11b)

**Package:** `go/internal/reconciler/`
**Metrics prefix:** `them_reconciler_`
**Prometheus labels:** none (counters only)
**Status:** Production-validated — controlled write activation complete 2026-07-20

---

## What it does

The reconciler sweeps `them.runs` for rows stuck in `status='running'` and reconciles
them against Temporal's authoritative `DescribeWorkflowExecution` response. It:

1. Acquires a PostgreSQL advisory lock (`pg_try_advisory_lock(987654321)`) so only one pod
   sweeps at a time even in a multi-replica deployment.
2. Queries up to `BatchSize` rows that have been running for longer than `StaleAfter`.
3. For each row, calls Temporal to get the workflow's current status.
4. Maps the Temporal status to the DB status using ADR-002 and writes the update
   (unless in dry-run mode).
5. Releases the advisory lock at the end of each sweep.

It runs in the Go bridge process as a background goroutine started in `main.go`.

---

## Configuration

All configuration is via `reconciler.Config` set at startup in `main.go`.
Default values are safe for production.

| Field | Default | Description |
|---|---|---|
| `Interval` | 60s | Time between sweeps |
| `BatchSize` | 100 | Max rows per sweep |
| `StaleAfter` | 2m | Min age of eligible "running" row |
| `TemporalNamespace` | "default" | Temporal namespace to query |
| `DryRun` | **true** | When true, no DB writes are made |
| `Concurrency` | 5 | Concurrent DescribeWorkflow calls |

### `RECONCILER_DRY_RUN` env var

| Value | Behaviour |
|---|---|
| unset | `true` (safe default — no DB writes) |
| `"true"` | Dry-run mode: decisions logged and counted, no DB writes |
| `"false"` | Live mode: reconciler writes terminal status to DB |
| any other string | Falls back to `true` (safe) |

**Default is `true`.** The fallback for any invalid value is also `true`. A misconfigured
or missing env var never accidentally enables writes. To enable writes you must
explicitly set `RECONCILER_DRY_RUN=false`.

The value is read at binary startup via `config.Load()` → `cfg.ReconcilerDryRun`.

```yaml
# docker-compose.soak.yml / docker-compose.integration.yml
environment:
  RECONCILER_DRY_RUN: "false"
```

---

## Multi-pod coordination

The reconciler uses `pg_try_advisory_lock(987654321)`. Only one pod sweeps at a time.
If a pod holds the lock and crashes, PostgreSQL releases it automatically when the
connection closes — no manual cleanup required.

If a second pod starts a sweep while the first is active, the second pod logs
`"reconciler: advisory lock held by another pod — skipping sweep"` and returns immediately.
No error is recorded.

**If the lock appears stuck:** Connect to PostgreSQL and run:
```sql
SELECT pid, granted, pg_blocking_pids(pid)
FROM pg_locks
WHERE classid = 987654321 AND locktype = 'advisory';
```
If the holding `pid` is dead, PostgreSQL will have already released it. If the pid is
still alive, check Go bridge logs to understand why the sweep is running long.

---

## Prometheus metrics

| Metric | Type | Meaning |
|---|---|---|
| `them_reconciler_scanned_total` | Counter | Rows examined per sweep |
| `them_reconciler_updated_total` | Counter | Rows written to terminal status |
| `them_reconciler_dryrun_total` | Counter | Rows that would have been updated (dry-run) |
| `them_reconciler_notfound_total` | Counter | Rows where Temporal returned NotFound (no write) |
| `them_reconciler_unchanged_total` | Counter | Rows left running (Temporal says still running) |
| `them_reconciler_errors_total` | Counter | Errors (Temporal unavailable, DB write failure) |

### Alert thresholds (suggested)

- `them_reconciler_errors_total` rate > 0 for >5 minutes → investigate Temporal connectivity
- `them_reconciler_notfound_total` > 0 regularly → check Temporal history retention settings
- `them_reconciler_scanned_total` growing without `them_reconciler_updated_total` → Temporal may be unreachable or DryRun is still enabled

---

## Status mapping

See [ADR-002](adr-002-reconciler-status-mapping.md) for full rationale.

| Temporal status | DB status written | Notes |
|---|---|---|
| RUNNING | no update | Still active |
| COMPLETED | `completed` | Normal finish |
| FAILED | `failed` | Workflow error |
| CANCELED | `canceled` | Graceful cancel |
| TERMINATED | `stopped` | Operator kill (not a failure) |
| CONTINUED_AS_NEW | no update | New execution is active |
| TIMED_OUT | `failed` | No `timed_out` in schema |
| NOT_FOUND | no update | Safe policy — see below |

---

## Safe NotFound policy

When `DescribeWorkflowExecution` returns NotFound (gRPC code 404), the reconciler:
1. Logs `WARN reconciler: workflow not found in Temporal — leaving DB unchanged`
2. Increments `them_reconciler_notfound_total`
3. Does NOT change the DB status

This protects: Python-native runs, runs whose Temporal history has expired, runs in a
different Temporal namespace.

### When to investigate NotFound

**Scenario A — History retention expired:**
Temporal's default history retention is 7 days. A run stuck in `running` for >7 days
whose Temporal history expired will trigger NotFound on every sweep.
To resolve, manually update the status via the admin API or SQL:
```bash
curl -X PUT /api/v1/runs/{run_id} -d '{"status": "failed"}'
```

**Scenario B — Python-native run:**
Python-native runs use Temporal Workflow ID `ctx-{context_id}`, not the run UUID.
`DescribeWorkflowExecution(runID)` returns NotFound for these — this is expected and safe.
The row is left unchanged. If the Python worker is stuck, investigate via the Temporal UI
using the `ctx-{context_id}` workflow ID (the `context_id` column from `them.runs`).

**Scenario C — Wrong namespace:**
If `TemporalNamespace` is misconfigured, all runs will return NotFound. Verify:
```bash
tctl --namespace <name> namespace describe
```

---

## Python-native runs

Python callers that bypass the Go bridge start Temporal workflows with ID `ctx-{context_id}`
and generate a separate `run_id` inside the workflow. These runs appear in `them.runs`
with a valid UUID but the Temporal workflow ID does not match the `run_id`.

The reconciler does not distinguish these rows at query time. When it calls
`DescribeWorkflowExecution(runID)` for such a row, Temporal returns NotFound and the row
is left unchanged (NotFound policy applies).

---

## Controlled Write Activation checklist

This is the procedure followed on 2026-07-20 to transition from dry-run to live mode.

### Pre-conditions

- [ ] Phase 11b soak complete (163+ dryrun decisions, 0 errors)
- [ ] `RECONCILER_DRY_RUN` env var wired in all compose files (defaults to `"true"`)
- [ ] Unit tests for the config field pass (`go test ./internal/config/...`)
- [ ] Full test suite passes (`go test ./...`)

### Activation steps

1. Set `RECONCILER_DRY_RUN: "false"` in `docker-compose.soak.yml` and
   `docker-compose.integration.yml`.
2. Rebuild and restart both Go bridges:
   ```bash
   docker compose ... build them-go-bridge them-go-bridge-2
   docker compose ... up -d --no-deps them-go-bridge them-go-bridge-2
   ```
3. Verify `dry_run: false` in bridge startup logs.
4. Wait 130s (2 sweep cycles).
5. Verify `them_reconciler_updated_total > 0` on the lock-holding bridge.
6. Verify `them_reconciler_errors_total = 0`.
7. Verify no rows updated to status outside `{completed, failed, canceled, stopped}`.
8. Wait 70s more (3rd sweep) — verify `updated_total` does not increase (idempotency).

### Rollback steps

1. Set `RECONCILER_DRY_RUN: "true"` in both compose files.
2. Restart both bridges (no rebuild needed — env var change only).
3. Verify `dry_run: true` in bridge startup logs.
4. Verify `them_reconciler_updated_total` does not increase after next sweep.
5. Verify `them_reconciler_dryrun_total` increases (confirming dry-run decisions are counted).

### Activation results (2026-07-20)

| Metric | Before | After |
|---|---|---|
| Eligible stale running rows | 37 | 7 |
| Rows written to `completed` | 0 (dry-run) | 30 |
| Invalid status writes | 0 | 0 |
| Reconciler errors | 0 | 0 |
| NotFound (no write) | 360 (cumulative) | 21 (this run) |
| Idempotency check | — | 0 new writes after 3rd sweep |
| Rollback | — | `updated_total` did not increase |

---

## Enabling reconciliation writes (simplified checklist)

1. Deploy with `DryRun = true` (default).
2. Let the reconciler run for at least 2 sweep intervals.
3. Check `them_reconciler_dryrun_total` in Prometheus — confirm it matches expected stuck runs.
4. Check `them_reconciler_notfound_total` — investigate any unexpected NotFound rows.
5. Set `RECONCILER_DRY_RUN=false` and redeploy.
6. Monitor `them_reconciler_updated_total` to confirm writes are occurring.

---

## Rollback

The reconciler does not hold state — it is a read-query + conditional-write loop.
To disable it:
- Set `RECONCILER_DRY_RUN=true` and redeploy (safe, immediate — no rebuild needed)
- Or remove the `go reconciler.Run(...)` call in `main.go` and redeploy

Rows already updated by the reconciler cannot be automatically rolled back. Use the
admin API or SQL to manually correct any incorrectly-updated rows.
