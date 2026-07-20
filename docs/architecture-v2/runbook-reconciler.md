# Operational Runbook — Run Reconciler (Phase 11b)

**Package:** `go/internal/reconciler/`  
**Metrics prefix:** `them_reconciler_`  
**Prometheus labels:** none (counters only)

---

## What it does

The reconciler periodically queries `them.runs` for rows stuck in `status='running'`
for longer than `StaleAfter` (default: 2 minutes). For each such row it calls
`DescribeWorkflowExecution` on the Temporal server and reconciles the DB status to
match Temporal's authoritative state.

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

**Important:** `DryRun = true` is the default. Set it to `false` explicitly to
enable writes. Start with dry-run enabled in production to validate the metric
output before enabling writes.

---

## Multi-pod coordination

The reconciler uses `pg_try_advisory_lock(987654321)` to ensure only one Go pod
runs the sweep at a time. If a pod holds the lock and crashes, PostgreSQL releases
it automatically when the connection closes — no manual cleanup required.

**If the lock appears stuck:** Connect to PostgreSQL and run:
```sql
SELECT pid, granted, pg_blocking_pids(pid)
FROM pg_locks
WHERE classid = 987654321 AND locktype = 'advisory';
```
If the holding `pid` is dead, PostgreSQL will have already released it. If the
pid is still alive, check Go bridge logs to understand why the sweep is running
long.

---

## Metrics

| Metric | Type | Meaning |
|---|---|---|
| `them_reconciler_scanned_total` | counter | Eligible rows examined |
| `them_reconciler_unchanged_total` | counter | Rows left unchanged (Temporal RUNNING) |
| `them_reconciler_updated_total` | counter | Rows updated to terminal status |
| `them_reconciler_notfound_total` | counter | Rows where Temporal returned NotFound |
| `them_reconciler_errors_total` | counter | Errors (Temporal unavailable, DB write fail) |
| `them_reconciler_dryrun_total` | counter | Rows that would have been updated (dry-run only) |

### Alert thresholds (suggested)

- `them_reconciler_errors_total` rate > 0 for >5 minutes → investigate Temporal connectivity
- `them_reconciler_notfound_total` > 0 regularly → check Temporal history retention settings
- `them_reconciler_scanned_total` growing without `them_reconciler_updated_total` → Temporal may be unreachable or DryRun is still enabled

---

## Status mapping

See [ADR-002](adr-002-reconciler-status-mapping.md) for full rationale.

| Temporal status | DB status written |
|---|---|
| RUNNING | no update |
| COMPLETED | `completed` |
| FAILED | `failed` |
| CANCELED | `canceled` |
| TERMINATED | `stopped` |
| CONTINUED_AS_NEW | no update |
| TIMED_OUT | `failed` |
| NOT_FOUND | no update (warn + metric) |

---

## NotFound policy

When `DescribeWorkflowExecution` returns NotFound (gRPC code 404), the reconciler:
1. Logs `WARN reconciler: workflow not found in Temporal — leaving DB unchanged`
2. Increments `them_reconciler_notfound_total`
3. Does NOT change the DB status

### When to investigate NotFound

**Scenario A — History retention expired:**  
Temporal's default history retention is 7 days. A run that has been "running" for
>7 days and whose Temporal history expired will trigger NotFound on every sweep.
To resolve: manually update the status to `failed` or `stopped` via the admin API:
```bash
curl -X PUT /api/v1/runs/{run_id} -d '{"status": "failed"}'
```
(Or directly in SQL if there is no admin endpoint for status override.)

**Scenario B — Python-native run:**  
Python-native runs use Temporal Workflow ID `ctx-{context_id}`, not the run UUID.
DescribeWorkflowExecution(runID) returns NotFound for these runs — this is expected
and safe. The row is left unchanged. If the Python worker is stuck, investigate
via the Temporal UI using the `ctx-{context_id}` workflow ID.

**Scenario C — Wrong namespace:**  
If `TemporalNamespace` is misconfigured, all runs will return NotFound. Verify:
```bash
# Check current namespace in Temporal
tctl --namespace <name> namespace describe
```

---

## Python-native runs

Python callers that bypass the Go bridge start Temporal workflows with ID
`ctx-{context_id}` and generate a separate `run_id` inside the workflow. These
runs are present in `them.runs` with a valid UUID but the Temporal workflow ID
does not match.

The reconciler does not distinguish these rows at query time. When it calls
`DescribeWorkflowExecution(runID)` for such a row, Temporal returns NotFound and
the row is left unchanged (NotFound policy applies).

To reconcile Python-native stuck runs, use the Temporal UI or `tctl` with the
workflow ID `ctx-{context_id}` (the `context_id` column from `them.runs`).

---

## Enabling reconciliation writes

1. Deploy with `DryRun = true` (default).
2. Let the reconciler run for at least 2 sweep intervals.
3. Check `them_reconciler_dryrun_total` in Prometheus — confirm it matches
   the expected number of stuck runs.
4. Check `them_reconciler_notfound_total` — investigate any unexpected NotFound rows.
5. Set `DryRun = false` in the Config and redeploy.
6. Monitor `them_reconciler_updated_total` to confirm writes are occurring.

---

## Rollback

The reconciler does not hold state — it is a read-query + conditional-write loop.
To disable it, either:
- Set `DryRun = true` and redeploy (safe, immediate)
- Remove the `go reconciler.Run(...)` call in `main.go` and redeploy

Rows already updated by the reconciler cannot be automatically rolled back. Use
the admin API to manually correct any incorrectly-updated rows.
