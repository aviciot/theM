---
title: E2E clean-environment purge investigation
type: plan
status: draft
last_verified: 2026-11-01   # live read-only queries on the dev stack (SELECT/count, redis read cmds, CH select, temporal list). Nothing was changed.
---

# E2E clean-environment purge investigation

## Purpose
Find everything the-M writes when apps run, so run/log data can be purged safely before E2E testing, without touching config.
Method: live read-only queries, code reading (`INSERT INTO` sites), docs. Counts are exact `count(*)` (do not trust `pg_stat_user_tables`: it said `audit_logs`=64, real=490).

## Summary
- Postgres `them` DB is only **28 MB** in total. Run data is ~12 MB of it. The real disk weight is elsewhere: ClickHouse system logs (~700 MB) and Temporal DBs (~210 MB).
- The system has been **idle since 2026-10-25** (last `runs.started_at`). No live Redis run/gate/session state, no running Temporal workflows.
- **65 runs are stuck `running`** (payops_ai 20, default 45), not just 20. They have **no Temporal workflow** behind them (see section 3), so nothing can re-write them: they are plain orphans. The reconciler cannot repair them (backlog F-08).
- The superuser `them` has `rolbypassrls=t`, so RLS/FORCE does not block purge deletes when run as `them` via `docker exec ... psql -U them`. (`them_app` / `them_admin` would be blocked or scoped; do not use them.)
- No triggers exist in `them`/`auth_service`. Purge is therefore plain FK-order deletes.

## 1. Postgres tables

### 1a. Run / request / log DATA (safe to purge)
Exact rows and size on 2026-11-01. "Cascade" = what Postgres deletes automatically when this table's rows go.

| Table | Rows | Size | Written by | FK / cascade | RLS+FORCE | Notes |
|---|---|---|---|---|---|---|
| `them.run_steps` | 15,034 | 5.0 MB | `runrecorder/recorder.go:193`, `appflow/activities.go:1195` | `run_id`→runs CASCADE; `agent_id`→agents SETNULL | yes | 33 steps `running`, 13 of them under non-running runs |
| `them.runs` | 5,183 | 2.9 MB | `runrecorder.CreateRun` (recorder.go:104), status updates in appflow/orchestrator | children CASCADE: run_steps, run_usage. SETNULL: tasks.run_id, runs.parent_run_id. Parents (tenant, orchestrator, definition, user) are NOACTION/SETNULL, so deleting runs never touches them | yes | tenant split: payops_ai 4,900 (4,869 completed, 8 failed, 3 canceled, **20 running**), default 283 (171 completed, 66 failed, 1 canceled, **45 running**). bank/rnd: 0. Drives quotas (`CountActiveRuns`, `SumMonthlyTokens`) |
| `them.task_messages` | 2,016 | 2.2 MB | `history/pgx.go:160,229` (conversation history) | `task_id`→tasks CASCADE | yes | |
| `them.tasks` | 2,274 | 1.1 MB | `runrecorder.go:328,373`, `history/pgx.go:263` | `run_id`→runs **SETNULL** (not cascade!); children `tasks`, `task_messages`, `artifacts` CASCADE | yes | 173 `working`, 86 failed, 2,015 completed. Must be deleted explicitly: deleting runs leaves them behind |
| `them.run_artifacts` | 231 | 320 kB (246 kB bytea) | `runrecorder.go:270`, `middleware/job.go:239,263` | **no FK to runs** (only tenant). children CASCADE: middleware_jobs, middleware_audit | yes | **208 of 231 are already orphans** (run row gone). 35 rows have a MinIO `storage_key` |
| `them.middleware_jobs` | 53 | 96 kB | `middleware/job.go:84,99` | `application_id` CASCADE, `artifact_id`→run_artifacts CASCADE | yes | file-scan jobs |
| `them.middleware_audit` | 53 | 64 kB | `middleware/job.go:356` | →run_artifacts CASCADE | yes | |
| `them.quarantine_artifacts` | 0 | 48 kB | `middleware/gate.go:223` | app CASCADE | yes | files themselves are in MinIO `them-quarantine` |
| `them.run_usage` | 101 | 288 kB | `runrecorder.go:172` | `run_id`→runs CASCADE; key SETNULL | yes | per-run LLM tokens/cost (user daily-cost cap reads it) |
| `them.hil_approvals` | 21 | 80 kB | `appflow/activities.go:526` | **no FK at all**, **no RLS** | **no** | 5 `pending`, 11 approved, 5 rejected. 3 pending belong to stuck `running` runs |
| `them.artifacts` | 1 | 80 kB | agent artifacts | →tasks CASCADE | yes | |
| `them.gateway_requests` | 300 | 264 kB | `llmgateway/dal.go:163` | tenant CASCADE; `client_id` SETNULL; bodies CASCADE | yes | all from 2026-10-21 (perf test). Also mirrored to ClickHouse (dual mode) |
| `them.gateway_request_bodies` | 0 | 24 kB | `llmgateway/dal.go:201` | →gateway_requests CASCADE | yes | |
| `auth_service.user_sessions` | 1,189 | 792 kB | `internal/authserver` (login/refresh) | →users CASCADE | no | 1,180 are expired. **No retention**. Deleting live ones logs users out |
| `auth_service.blacklisted_tokens` | 166 | 104 kB | authserver logout/revoke | none | no | 74 expired. Active rows protect against revoked-token reuse |
| `them.audit_logs` | 490 | 280 kB | `admin/dal/audit_logs.go:67` | tenant/user SETNULL | yes | admin config-change audit trail (app.create 124, app.delete 101, ...). Log, not run data; purge only if you want a clean trail |
| `them.access_tokens` | 120 | 192 kB | `admin/dal/tokens.go:158` (test-runner and playground create temp tokens) | tenant NOACTION, user/orchestrator SETNULL/CASCADE | yes | **Mixed**: mostly `playground-*` labels, 109 expired. Real EP tokens can also live here. Purge only expired `playground-%` rows |

### 1b. CONFIG (must NOT be purged)
`tenants` (4: default, payops_ai, bank, rnd), `tenant_quotas`, `tenant_runtime_config`, `tenant_system_agent_config`, `tenant_roles`, `tenant_role_grants`, `tenant_role_mappings`, `tenant_group_mappings`, `applications` (21), `application_definitions` (118), `app_orchestrators`, `orchestrators`, `entry_points` (41), `app_agent_bindings`, `agents`, `agent_definitions`, `agent_runtime_specs`, `component_definitions`, `mcp_servers`, `app_mcp_credentials`, `app_flow_llm_overrides`, `app_flow_http_params`, `app_debug_config`, `app_temporal_config`, `appflow_debug_presets` (12, debug presets are config), `middleware_defs`, `middleware_wirings`, `gateway_clients`, `gateway_profiles`, `gateway_profile_steps`, `governance_policies`, `llm_providers`, `llm_provider_keys` (encrypted keys), `config`, `schema_migrations`, `auth_service.users`, `roles`, `tenant_memberships`, `teams`, `team_members`, `user_overrides`, `auth_service.auth_audit` (0 rows).

### 1c. Quota counters
`max_concurrent_runs` is computed live in Postgres: `SELECT count(*) FROM them.runs WHERE tenant_id=$1 AND status IN ('admitted','running','input_required')` (`admin/dal/runs.go:164`). There is no persisted counter. Fixing/deleting the 65 running rows fixes the quota. The per-minute/monthly Redis counters (`rl:them:{tenant}:runs|api|runs:monthly:*`) currently **do not exist** (90 s TTL). `monthly_llm_tokens` sums `runs.total_tokens_*` for the current month, so deleting runs also resets token usage (payops_ai limit 50,000).

### 1d. Constraints to know
- `runs.status` CHECK allows only `running, completed, failed, canceled, cancelled, stopped`. There is no `admitted`/`input_required` in practice. Use `failed` or `canceled` to close stuck rows.
- `run_steps.status` allows `pending, running, completed, failed, timeout`. `tasks.state` allows `... working, failed, canceled ...`. `hil_approvals.status` allows `timed_out`.
- Sequences with run data: `run_usage_id_seq` (814), `task_messages_id_seq` (2091), `audit_logs_id_seq` (502), `user_sessions_id_seq` (2407). Other tables use UUIDs. Config sequences (`users`, `roles`, `llm_providers`, `llm_provider_keys`) must not be reset.

## 2. Redis

### Main `them-redis` (22 keys, 5 MB used, `allkeys-lru`, AOF + RDB, AOF 35 MB)
| Key(s) | Type / TTL | Class | Action |
|---|---|---|---|
| `them:events` | stream, **no TTL**, 3,170 entries | **Stale legacy stream.** Last delivered id is 2026-10-20. Nobody reads it: the event-writer reads `them-redis-events`. Contains gateway events for tenant payops_ai | purge (DEL) |
| `them:ep:gate:queue:ep-user_-32566a` (1), `...ep-token-1d63a4` (3), `...ep-exter-e5ddf4` (2) | list, **no TTL** | **Leaked gate queue entries** (6 total). Old-format key (EP slug only, no app id; current code uses `them:ep:gate:queue:{appID}:{slug}`) | purge (DEL). Left in place they can wake/confuse new admissions |
| `them:metrics:{tenant}:2026-10-25`, `...:app:{app}:2026-10-25` (x3), `...:users:2026-10-25` | hash/string, TTL ~25 days | run metrics (dashboard counters). Derived data | optional DEL; they self-expire |
| `them:{tenant}:mcp:manifest|health:*` (8) | string, 30-300 s | cache | keep (rebuilds) |
| `them:mcp:leader`, `them:pod:go-bridge-1`, `them:dash:services:health`, `them:dash:app_status_cache`, `them:oidc:last_login:*` | short TTL | coordination / cache | keep |
| **Absent right now** | | `them:dash:run:*:stream`, `them:wait:*`, `them:hitl:*`, `them:agent:a2atask:*`, `them:debug:*`, `them:ep:*:sessions`, `them:app:*:sessions`, `them:sess:*`, shadow keys, `rl:*`, `them:session:token:*` | nothing to clean now. Include patterns in the script for safety if tests run before purge |

Run streams (`them:dash:run:{id}:stream`) have no TTL by design (MAXLEN ~5000 only), yet none exist: they were lost (RDB/AOF restart plus `allkeys-lru`). Postgres run rows exist with no stream, so replay for old runs is already impossible.

Do **not** `FLUSHALL`: it is unnecessary, and it also removes the MCP leader lock and pod heartbeat (they recover, but it is needless risk). Use targeted `DEL`.

### Events `them-redis-events` (3 keys, 1.8 MB, `noeviction`, AOF 15 MB)
| Key | State |
|---|---|
| `them:events` | stream, 12 entries, group `event-writers` with 1 consumer, 0 pending, lag 0 (1,845 entries ever added). Keep the group |
| `them:events:dlq` | stream, 0 entries |
| `them:events:gateway` | stream, 0 entries, group `event-writers` with 0 consumers. Left by perf scripts (`scripts/perf_focused_test.py`); no code in `go/` uses this name. Harmless |

## 3. ClickHouse and Temporal

### ClickHouse (`them-clickhouse`, volume dir `data/them-clickhouse` = **856 MB**)
Event tables `them_events.*` are tiny: `events_gateway_request` 300 rows / 18 kB, `events_user_activity` 53, `events_tool_call` 1, `events_audit` 0, `events_bodies` 0. TTLs: 90 d / 180 d / 7 y / 30 d (`db/clickhouse/001_events.sql`).
The size is in **system log tables with no TTL**: `text_log` 312 MB (8.2 M rows), `asynchronous_metric_log` 242 MB (389 M rows), `metric_log` 131 MB, `trace_log` 24 MB, `processors_profile_log` 3.7 MB, `query_log` 2.3 MB, `part_log` 1.3 MB.

### Temporal (same Postgres server, separate DBs)
- `temporal` DB 118 MB (`history_node` 100 MB), `temporal_visibility` 95 MB (`executions_visibility`, 87 MB; 30 rows, so mostly bloat).
- Namespace `default` retention is **24 h**; `temporal-system` 7 d.
- Live check: `temporal workflow count` and `count --query "ExecutionStatus='Running'"` return **0** in `default`. The only executions in the DB are the system workflows `temporal-sys-history-scanner` and `temporal-sys-tq-scanner` (namespace `temporal-system`).
- Conclusion: the 65 stuck runs have **no workflow**, so no worker will re-write their rows. There is nothing to terminate. If a new test leaves workflows running, terminate them before touching the DB (section 5, step 1).
- Run workflow ID formats: `appflow:{tenant}:{runID}` (queues `appflow-dag`, `appflow-dag-debug`) and `{tenant}:ctx-{contextID}` (`them-orchestration-go`).

## 4. Other storage and log sources
| Source | Size | Notes |
|---|---|---|
| MinIO volume `them-minio-data` (buckets `them-artifacts`, `them-quarantine`) | 556 kB | Referenced by 35 `run_artifacts.storage_key`. Purge together with `run_artifacts` |
| Container logs (docker json-file; rotation configured in compose) | Host log files are root-only; sizes measured via `docker logs`: traefik 65 MB, temporal-frontend 81 MB, postgres 39 MB, mcp-service 16 MB, redis 11 MB, dag-worker-debug 4.7 MB, go-worker(-2) 2.3 MB each, bridge 0.5 MB, rest ~0 | Caps: workers 10x50 MB, bridge/auth/mcp/postgres/traefik 5x20 MB, others 3x10 MB. Global daemon default 3x50 MB. `them-control-plane` has no log limits. Truncating needs root (`truncate -s 0 <LogPath>`), recreating the container also resets them. Not needed for a clean DB |
| Volume `them-logs` / `./logs` | 4 MB / 4 kB | `them-logs` holds 4 MB; not inspected in detail |
| Test-runner `test-runner/data/` | 12 MB: `history/` 100 files, `scenarios/` 24, `config.json` | `history/*.json` = past run results. **55 of them are tracked in git** (and `config.json`, `scenarios/`), so deleting them shows up in `git status`. Keep `config.json` + `scenarios/` |
| ClickHouse / events Redis volumes | `data/them-redis-events` 15 MB | see above |
| Docker images/build cache | 45 GB images (41 GB reclaimable), 10 GB build cache | not run data; out of scope |

## 5. Proposed purge procedure (full)
Run all SQL as superuser `them` (bypasses RLS) via `docker exec`. Nothing below touches config tables.

**0. Back up first** (28 MB, seconds):
```bash
docker exec them-postgres pg_dump -U them -d them -Fc -f /tmp/them_prepurge.dump
docker cp them-postgres:/tmp/them_prepurge.dump /opt/docker/them/tmp/   # tmp/ is untracked
docker exec them-redis redis-cli --rdb /tmp/main.rdb   # optional
```

**1. Quiesce.** The system is idle, but make sure no run is in flight and no workflow is open:
```bash
T="docker exec temporal-admin-tools temporal --address temporal-frontend:7233"
$T workflow count --query "ExecutionStatus='Running'"          # expect 0
# if not 0:
$T workflow terminate --query "ExecutionStatus='Running'" --reason "e2e purge" --yes
$T workflow delete    --query "ExecutionStatus!='Running'" --yes   # optional; default ns keeps only 24h anyway
```
Only the `default` namespace is queried, so `temporal-sys-*` workflows are not touched. For an extra guarantee, stop the writers for the duration: `docker stop them-go-bridge them-go-worker them-go-worker-2 them-dag-worker them-dag-worker-2 them-dag-worker-3 them-dag-worker-4 them-dag-worker-debug` and start them again in step 6 (`docker start`, no rebuild needed).

**2. Postgres run data (one transaction, FK-safe order):**
```sql
BEGIN;
DELETE FROM them.hil_approvals;                 -- no FK, no RLS
DELETE FROM them.quarantine_artifacts;          -- (middleware_jobs.quarantine_id SETNULL)
DELETE FROM them.run_artifacts;                 -- cascades middleware_jobs, middleware_audit
DELETE FROM them.tasks;                         -- cascades task_messages, artifacts, child tasks
DELETE FROM them.runs;                          -- cascades run_steps, run_usage (parent_run_id self-ref is SETNULL)
DELETE FROM them.gateway_requests;              -- cascades gateway_request_bodies
-- expected: runs 0, run_steps 0, run_usage 0, tasks 0, task_messages 0, run_artifacts 0, hil 0
COMMIT;
```
`TRUNCATE them.runs, them.tasks, them.run_artifacts, them.hil_approvals, them.gateway_requests CASCADE` also works (cascade set verified from the FK list: run_steps, run_usage, task_messages, artifacts, middleware_jobs, middleware_audit, gateway_request_bodies, nothing config). DELETE is preferred (transactional, row counts visible, no accidental extra cascade).

Optional, in the same or a second transaction:
```sql
-- log-style tables (skip any you want to keep)
DELETE FROM them.audit_logs;                                           -- admin audit trail, 490 rows
DELETE FROM them.access_tokens WHERE expires_at < now() AND label LIKE 'playground-%';
DELETE FROM auth_service.user_sessions WHERE expires_at < now();       -- 1,180 expired; keeps live logins
DELETE FROM auth_service.blacklisted_tokens WHERE expires_at < now();
-- reset sequences of the purged tables only (cosmetic)
ALTER SEQUENCE them.run_usage_id_seq RESTART;  ALTER SEQUENCE them.task_messages_id_seq RESTART;
```
Then `VACUUM (ANALYZE)` on the touched tables (optional; DB is 28 MB).

**3. Redis (main), targeted:**
```bash
R="docker exec them-redis redis-cli"
$R DEL them:events them:ep:gate:queue:ep-user_-32566a them:ep:gate:queue:ep-token-1d63a4 them:ep:gate:queue:ep-exter-e5ddf4
# run/session/gate state (all currently absent; harmless if tests ran since). Review the SCAN output first.
for p in 'them:dash:run:*' 'them:wait:*' 'them:hitl:*' 'them:agent:a2atask:*' 'them:agent:task:*' 'them:debug:*' 'them:ep:*' 'them:app:*:sessions' 'them:app:*:shadow:*' 'them:sess:*' 'rl:*' 'them:metrics:*'; do
  $R --scan --pattern "$p" | xargs -r -n 100 $R UNLINK
done
```
Careful: `them:app:*` would match `them:app:{app}:orch:*` caches, so use the narrower `sessions`/`shadow` patterns shown. `them:ep:*` has no config keys (only gate sets, shadows, queues); pub/sub channels are not keys. Do not delete `them:mcp:leader`, `them:pod*`, `them:session:token:*` (cache, 5 min), `them:oidc:*`.

**4. Events pipeline (only if you want a clean ClickHouse):**
```bash
CH="docker exec them-clickhouse clickhouse-client -q"
for t in events_gateway_request events_user_activity events_tool_call events_audit events_bodies; do $CH "TRUNCATE TABLE them_events.$t"; done
# reclaim ~700 MB of system logs (no TTL on any of them):
for t in text_log asynchronous_metric_log metric_log trace_log processors_profile_log query_log part_log; do $CH "TRUNCATE TABLE system.$t"; done
```
Events Redis: leave `them:events` and its consumer group alone (12 entries drain by themselves). If you must empty it, wait for lag 0 and pending 0 (`XINFO GROUPS them:events`) and use `XTRIM them:events MAXLEN 0`; never `DEL` the stream (that drops the group and the writer's `XREADGROUP` then errors until restart). Optional: `docker exec them-redis-events redis-cli DEL them:events:gateway` (empty, unused).
Order matters: do Postgres first, ClickHouse after, otherwise a running gateway would re-emit nothing anyway (idle), but keep it in this order if traffic is possible.

**5. MinIO artifacts** (matching the deleted `run_artifacts`; 556 kB). With `mc` from a throwaway container on `them-network` using the MinIO root creds from `.env` (never print them): `mc rm --recursive --force local/them-artifacts local/them-quarantine`. Do not remove the buckets themselves.

**6. Test-runner history** (optional): `rm test-runner/data/history/*.json`. Commit the deletion or `git checkout` it back later; do not remove `config.json` / `scenarios/`.

**7. Restarts.** None required if the writers were not stopped: no process caches run rows. If you stopped them in step 1, `docker start` them (same containers/images, so no rebuild; CLAUDE.md "recreate" rule only applies to code changes). `them-go-bridge` re-registers its pod heartbeat on start. Verify:
```bash
docker exec them-postgres psql -U them -d them -At -c "select count(*) from them.runs"
docker exec them-redis redis-cli --scan --pattern 'them:ep:*' | wc -l       # 0
docker logs them-go-worker --tail 5; docker logs them-dag-worker --tail 5    # polling
```
Then do a quick smoke run per tenant before E2E.

### What to preserve (checklist)
All tables in 1b, MinIO buckets, `them:mcp:leader`/pod/caches in Redis, the event-writer consumer group, `test-runner/data/config.json` + `scenarios/`, live `user_sessions` (or you will need to log in again), Temporal `temporal-system` workflows.

### Risks
- **FK order.** `tasks` and `run_artifacts` are not cleaned by `DELETE FROM runs` (SETNULL / no FK). Skipping them leaves orphan conversation history and files (208 orphan `run_artifacts` exist already because of this). Config parents are never affected.
- **RLS.** Only the superuser `them` is safe. Running the same SQL as `them_app` would return 0 rows deleted silently (FORCE RLS needs the `app.tenant_id` GUC; backlog/memory note).
- **`hil_approvals` has no RLS, no FK.** It is the one run table that nothing cascades to; do not forget it.
- **Counters that must move together.** The only quota counter that matters (`max_concurrent_runs`) is the DB, so Redis gate keys and DB runs are independent. But if tests start while old gate sets/shadows exist, `luaAdmit` caps may be wrong, so purge the `them:ep:*` / `them:app:*` keys with the DB. Monthly token usage is derived from `runs`, so it resets with them.
- **Do not delete runs while a workflow is live**: an activity would try to update/insert children (`run_steps`) and hit FK errors. Section 1 (count = 0) prevents this.
- **Cost of losing history**: dashboards, `run_usage` daily cost caps, and old HIL links all go to zero. That is the intent here.

## 6. Minimal option: fix only the stale `running` runs
Frees the payops_ai quota (20 rows) and the default tenant (45 rows); loses nothing.
```sql
BEGIN;
UPDATE them.run_steps SET status='failed', error=COALESCE(error,'orphaned: no workflow'), completed_at=now(), ended_at=now()
  WHERE status='running' AND run_id IN (SELECT id FROM them.runs WHERE status='running');
UPDATE them.hil_approvals SET status='timed_out', decided_at=now(), updated_at=now()
  WHERE status='pending' AND run_id IN (SELECT id FROM them.runs WHERE status='running');
UPDATE them.runs SET status='failed', error='orphaned: workflow no longer exists (e2e cleanup)', completed_at=now(), ended_at=now()
  WHERE status='running';      -- 65 rows; add AND tenant_id='ad7d567d-b222-40be-acb6-7929cf6ae168' for payops_ai only (20)
COMMIT;
```
Optional: `UPDATE them.tasks SET state='failed' WHERE state='working';` (173 rows; all of them are stale, none belongs to a running run). The 13 `running` steps under already-finished runs are covered only by the first statement if you drop the `run_id IN (...)` filter. No Redis or Temporal action needed (verified: no gate keys, no workflows). `Release()` will not run for these runs, so the DB row is the only state.

## 7. Surprises (not fixed)
1. **65, not 20, stuck `running` runs**: default tenant has 45 (since 2026-09-16, newest 10-15). 173 `tasks` stuck `working`, 33 `run_steps` `running`, 5 `hil_approvals` `pending` (3 on stuck runs). Nothing ever closes them (reconciler F-08 never matches workflow IDs and is dry-run).
2. **No Postgres retention anywhere** for runs, run_steps, tasks, task_messages, gateway_requests, audit_logs, user_sessions, blacklisted_tokens, access_tokens. `housekeeping-service` is an `omni2` leftover (0 of 13 jobs enabled, reads `omni2.omni2_config`).
3. **`run_artifacts` has no FK to `runs`**: 208 of 231 are orphans, and their `bytea`/MinIO objects never get cleaned.
4. **`tasks.run_id` is SETNULL**: deleting a run (admin `DELETE /runs`, `dal/runs.go:330,350`) orphans its tasks and conversation history. `DeleteTenantData` (`dal/tenants.go:544`) also does not delete tasks/hil/gateway rows explicitly.
5. **Stale main-Redis `them:events` stream** (3,170 entries, no TTL, no reader) and **3 leaked old-format gate queue lists** with no TTL.
6. **Run streams have no TTL** and live in an `allkeys-lru` Redis, so they vanish silently (none exist now).
7. **ClickHouse system logs have no TTL**: ~700 MB of 856 MB (`text_log`, `asynchronous_metric_log`, `metric_log`). Events tables themselves are tiny.
8. **Temporal visibility DB** is 95 MB for 30 rows (bloat; `history_node` 100 MB for 408 rows).
9. `pg_stat_user_tables.n_live_tup` is stale (reports `audit_logs` 64 vs 490); use `count(*)`.
10. **Test pollution in `access_tokens`**: 120 rows, ~100 expired `playground-*` tokens never cleaned (test-runner invariant "delete at end" not met). `user_sessions`: 1,180 of 1,189 expired, never pruned.
11. **`middleware_*` / `hil_approvals` RLS gaps**: `hil_approvals`, `middleware_defs`, `tenant_quotas`, `app_flow_llm_overrides`, `app_debug_config`, `app_temporal_config`, `gateway_profile_steps`, `config` have no RLS; `tenant_role_*` have RLS but not FORCE.
12. `them-control-plane` container has no log rotation limit configured; host container log files are root-only (sizes above come from `docker logs`).
13. `docker logs` for the four main `them-dag-worker*` containers is ~0 MB although they have been up 13 days (very little output, or logging elsewhere); not investigated.
14. Test-runner `history/*.json` (55 files) and `config.json` (admin password, F-111) are git-tracked, so a purge produces a dirty working tree.
