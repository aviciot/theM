# Runbook: Run Reconciler

**Component:** `internal/reconciler`
**Phase:** 11b
**Status:** Production-validated (controlled write activation complete 2026-07-20)

---

## Purpose

The run reconciler sweeps `them.runs` rows stuck in `status='running'` and reconciles
them against Temporal's authoritative `DescribeWorkflowExecution` response. It:

1. Acquires a PostgreSQL advisory lock (`pg_try_advisory_lock(987654321)`) so only one pod
   sweeps at a time even in a multi-replica deployment.
2. Queries up to `BatchSize` rows that have been running for longer than `StaleAfter`.
3. For each row, calls Temporal to get the workflow's current status.
4. Maps the Temporal status to the DB status using ADR-002 and writes the update
   (unless in dry-run mode).
5. Releases the advisory lock at the end of each sweep.

---

## Configuration

### `RECONCILER_DRY_RUN`

| Value | Behaviour |
|---|---|
| unset | `true` (safe default — no DB writes) |
| `"true"` | Dry-run mode: decisions logged and counted, no DB writes |
| `"false"` | Live mode: reconciler writes terminal status to DB |
| any other string | Falls back to `true` (safe) |

**Default is `true`.** The fallback for any invalid value is also `true`. This design
means a misconfigured or missing env var never accidentally enables writes.

The value is read at binary startup via `config.Load()` → `cfg.ReconcilerDryRun`.

**To enable writes:**
```yaml
# docker-compose.soak.yml / docker-compose.integration.yml
environment:
  RECONCILER_DRY_RUN: "false"
```

**To roll back to dry-run (leave as committed default):**
```yaml
environment:
  RECONCILER_DRY_RUN: "true"
```

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

---

## Safe NotFound policy

When Temporal returns gRPC `codes.NotFound` for a workflow, the reconciler **does not
write** to the DB. This protects:

- Python-native runs (started before the Go gateway was deployed)
- Runs whose Temporal history has expired (retention policy)
- Runs in a different Temporal namespace

---

## Advisory lock

The reconciler uses `pg_try_advisory_lock(987654321)`. This is a session-level lock
released automatically when the DB connection closes or when the sweep finishes.

If a second pod starts a sweep while the first is active, the second pod logs
`"reconciler: advisory lock held by another pod — skipping sweep"` and returns
immediately. No error is recorded.

---

## Controlled Write Activation checklist

This is the procedure we followed on 2026-07-20 to transition from dry-run to live mode.

### Pre-conditions

- [ ] Phase 11b soak complete (163+ dryrun decisions, 0 errors)
- [ ] `RECONCILER_DRY_RUN` env var wired in all compose files (defaults to `"true"`)
- [ ] Unit tests for the new config field pass (`go test ./internal/config/...`)
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

### Results (2026-07-20)

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

## ADR-002: Temporal status to DB status mapping

| Temporal status | DB status | Notes |
|---|---|---|
| RUNNING | (no update) | Still active |
| COMPLETED | `completed` | Normal finish |
| FAILED | `failed` | Workflow error |
| CANCELED | `canceled` | Graceful cancel |
| TERMINATED | `stopped` | Operator kill (not a failure) |
| TIMED_OUT | `failed` | No `timed_out` in schema |
| CONTINUED_AS_NEW | (no update) | New execution is active |
| NOT_FOUND | (no update) | Safe policy — see above |
