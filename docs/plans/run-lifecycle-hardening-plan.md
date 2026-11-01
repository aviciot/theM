---
title: Run lifecycle hardening - fix and test plan
type: plan
status: draft
last_verified: 2026-11-01
---

# Run lifecycle hardening - fix and test plan

Source: `docs/plans/e2e-clean-env-purge-investigation.md` and backlog F-08, F-122..F-133 (`docs/plans/code-findings-backlog.md`).
Goal: runs always reach a terminal state, deleting data leaves no orphans, data and logs have bounds, E2E results can be trusted.

## Done (2026-11-01)
- Reconciler repaired (F-08): real workflow IDs, orphan handling, atomic close of run + steps/tasks/approvals. Live-verified.
- `FinalizeRunActivity` closes open steps/tasks/approvals; failed finalize is logged (F-122). Integration-tested; **not yet deployed** (see P0).

## Test layers (every item uses the ones that apply)
| Layer | Where | Run | Needs |
|---|---|---|---|
| L1 unit | `go/internal/**/_test.go` (fakes) | `cd go && go test ./...` | nothing |
| L2 integration | files with `//go:build integration` | `go test -tags=integration <pkg>` with `TEST_POSTGRES_DSN` | a TEST database; tests must clean their own rows (register `pool.Close` with `t.Cleanup` BEFORE seeding) |
| L3 live E2E | `scripts/tests/test_NN_*.py` (+ test-runner scenarios) | python, against the dev stack | stack up with `temporal` profile |
| L4 invariant check | `scripts/tests/check_run_invariants.sql` (new) | `docker exec -i them-postgres psql ... < file` | none; run after every E2E session |

Invariants (L4, must all return 0 rows): runs `running` older than 3 h with no live workflow; `run_steps` `running`/`pending` under a terminal run; `tasks` `working`/`submitted` under a terminal run; `hil_approvals` `pending` under a terminal run; `run_artifacts` / `tasks` with no run; `tasks.context_id` without a root task.
Every item also gets `go/TEST_INDEX.md`, the owning architecture doc, and the backlog entry updated in the same commit.

## P0 - unblock deploys (blocker)
`them-dag-worker*` images run `go test ./...` during build; `cmd/event-writer/recovery_test.go` (3 tests) needs Redis on `localhost:6379` and has no `integration` tag, so the build fails and workers cannot be rebuilt. Resolved 2026-11-01: tagged `integration` (repo convention), run with `-tags=integration` when event-writer/events code changes. Done when `up -d --build` for all five dag workers succeeds.
Then deploy the finalize change: `up -d --build them-dag-worker -2 -3 -4 them-dag-worker-debug`, check `docker logs` shows polling.
Deployed 2026-11-01. Live check: a run answered to completion finishes `completed` with all steps `completed` (no regression). The close-children wiring is covered by `TestFinalizeRunActivity_ClosesChildren` (integration); a live run that ends through finalize with open steps could not be produced because cancel does not stop a waiting flow (F-134).

## P1 - runs always finish (F-122, F-124, F-133, F-123, known gap 4)
| # | Fix | Tests | Deploy |
|---|---|---|---|
| 1.1 | Reconciler also closes open children of runs that are already terminal; dedicated connection for the advisory lock (`pool.Acquire`) so sweeps are not skipped | L1 fakes for the new sweep query; L2 seed a `failed` run with a `working` task, sweep once, assert closed | bridge |
| 1.2 | HIL timer firing writes `hil_approvals.timed_out` (needs `workflow.GetVersion` for in-flight workflows); decide a default timeout when `timeout_seconds=0` (product decision: use `appFlowHILTimeout` 24 h?) | Temporal test env (`workflow_temporal_test.go`) for timer vs signal; L2 for the DAL write; replay test for GetVersion | all dag workers |
| 1.3 | `runs.completed_at` written with `ended_at`; `stopped` treated as terminal and guarded; recorder update guarded by `status='running'` | L1 on recorder and dag-worker updater; L2 on the SQL | bridge + dag workers |
| 1.4 | (F-134 fixed 2026-11-01: cancel now ends flows waiting for input) Admin cancel also cancels the workflow (Temporal `CancelWorkflow` by ID `appflow:{tenant}:{run}` or `{tenant}:ctx-{ctx}`), releases gate/session, closes children | L1 with fake Temporal client; L3: cancel via admin API, workflow `Canceled`, tokens stop, run `canceled` | bridge |
| 1.5 | Orchestration path (WS/SSE): errors before `orchestrator.Run` update the run row and publish an `error` event; `OrchestrationWorkflow` finalizes | L1 on `RunOrchestratorActivity` failure paths; L3 with a bad EP config | go-worker(-2), bridge |
Done when: L4 invariants hold after a mixed E2E session (complete, fail, cancel, HIL timeout, workflow timeout, worker killed mid-run).

## P2 - orphans on delete (F-125)
One migration `db/NNN_*.sql`: delete existing orphans; `run_artifacts.run_id` FK `ON DELETE CASCADE` (`NOT VALID` then `VALIDATE`) + index; `tasks.run_id` to `CASCADE`; `hil_approvals.run_id` FK CASCADE (and RLS later). Code: delete MinIO `storage_key` objects before deleting runs/tenant; add `tasks`/`hil_approvals` to `ForceDeleteTenant`.
Tests: L2 delete a run with artifacts/tasks/approvals, assert nothing left; L2 `ForceDeleteTenant` on a throwaway tenant; L3 delete via admin API then L4. Apply migration by hand to the dev DB (`docker cp` + `psql -f`), regenerate `docs/reference/schema-generated.md`. Take a `pg_dump` first.

## P3 - retention and hygiene (F-126, F-131)
Order: expired `auth_service.user_sessions`, expired `access_tokens` (fix Admin Playground to delete its token on close, F-131), then `runs`/`run_steps`/`tasks`/`task_messages` older than N days (config, default off), `gateway_requests`, `audit_logs`, `blacklisted_tokens`. A small scheduled job (in-process, advisory-locked like the reconciler) with per-table retention env vars, dry-run first.
Tests: L1 on the retention SQL builders; L2 seed old and new rows, assert only old removed; never touches rows of running runs.

## P4 - events, ClickHouse, observability (F-127..F-130, F-129, F-132)
- F-130: `clickhouse/config.d/system-logs.xml` (TTL 7 d, quieter `text_log`) mounted in `docker-compose.events.yml` + one-off `ALTER TABLE system.<t> MODIFY TTL`. Test: query `system.tables` TTL after restart; size stays flat over a day.
- F-128: DLQ keeps the payload; pending message no longer blocks trimming; MAXLEN on producers. Tests: L1/L2 in `cmd/event-writer` (needs the P0 decision).
- F-127: decide which events must exist (audit, bodies, run events), add `ApplicationID`/`UserID`/`RunID`, emit after the PG insert. Test: L3 gateway call then assert the ClickHouse row has non-zero IDs.
- F-129: delete the stale `them:events` stream and old gate queue keys; give run streams a TTL. Test: L3 run then `TTL` on its stream key.
- F-132: per-activity start/finish/error logs with `run_id` (INFO). Test: L1 captures `slog`; L3 `docker logs` shows a run's lifecycle.

## Order and cadence
P0 -> P1.1 -> P1.4 -> P1.3 -> P1.2 -> P1.5 -> P2 -> P3 -> P4. One item per commit; after each: full `go test ./...` (zero failures expected), L3 for that item, L4, then update `docs/CURRENT.md`.
Rebuild rules: appflow/temporal changes -> all five dag workers; `cmd/worker` -> `them-go-worker` and `-2`; bridge-only changes -> `them-go-bridge`. `restart` does not pick up a new image; use `up -d --build`.

## Production notes
`docker-compose.hetzner.yml` keeps `RECONCILER_DRY_RUN=true`. Before turning writes on there: run one dry-run sweep against production data, read the `[dry-run] would ...` lines, take a `pg_dump`.
