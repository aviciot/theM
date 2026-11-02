---
title: Production rollout checklist (November 2026 release)
type: plan
status: draft
last_verified: 2026-11-02   # written from the repo and the dev stack; the production host itself was NOT inspected
---

# Production rollout checklist — November 2026 release

Everything below was built and verified on the **dev stack**. Production (Hetzner) has not been touched or inspected. Run the pre-flight first: several steps depend on facts only the server can tell you.
Never paste real secrets, `.env` contents or full DSNs into tickets or chat (`deploy.sh config` prints secrets: do not use it for this).

## 1. What is in the release, and what applies to production

| Area | What changed | In production? |
|---|---|---|
| Run lifecycle | runs always finish (finalize closes steps/tasks/approvals, admin cancel cancels the workflow, cancel ends flows waiting for input, orchestrator failures are reported, HIL timeout marks the approval `timed_out`); reconciler repaired; `stopped` is terminal | yes |
| Data hygiene | deleting a run now deletes its artifacts and approvals (db/127); tenant delete also removes chat history | yes |
| Retention | auth-session/blacklist purge (ON by default), platform retention job (dry-run by default) | yes |
| Role requirement | per-entry-point required tenant role for `external_jwt` EPs; canvas checkbox + Runtime dropdown | yes (inert until a role is set) |
| AppFlow agent node | now sends `X-Them-Agent-Id` (F-137): calls to canvas-built agents stop failing with 401 | yes — **behaviour change**, see §9 |
| Run id as trace key | `gateway_requests.run_id` (db/129), run id on tool-call events | partly: the PG column yes; ClickHouse parts only if the events pipeline runs |
| Redis hygiene | gate wake-up tokens capped + 15 s expiry; run streams expire 24 h after their last event | yes |
| Startup schema check | services log an ERROR when the newest required migration is not recorded | yes |
| Worker logs | one INFO line per activity | yes (check `LOG_LEVEL`, §9) |
| ClickHouse system-log TTL, event-writer fixes, object janitor | | **only if you run the `events` / `security` profiles by hand.** `deploy.sh` starts neither, and the production overlay does not define them |
| HIL default 300 s | new canvas HIL nodes only | yes (frontend) |

Production stack = `docker-compose.yml` + `docker-compose.hetzner.yml` + `--profile temporal`: traefik, postgres, redis, frontend, auth-go, mcp-service, `them-go-bridge` (+`-2`), `them-go-worker` (+`-2`), `them-dag-worker` (+`-2`, `-debug`), temporal. `them-agent-runtime`, event pipeline and middleware worker are **not** started by `deploy.sh`: confirm how (and whether) they run on the server.

## 2. Decisions to make before starting
- [ ] **Audit-log retention** (`THE_M_RETENTION_AUDIT_DAYS`): compliance owner decides; default stays off.
- [ ] **Chat / run / gateway retention periods** (`..._CHAT_DAYS`, `_RUNS_`, `_GATEWAY_`): leave off at rollout; decide later (§7).
- [ ] **Auth retention** is ON by default and has no dry-run: it deletes expired login-session rows older than 7 days and expired blacklist rows at startup and hourly (nothing reads `user_sessions`). Keep, or set `THE_M_AUTH_RETENTION_ENABLED=false` on `them-auth-go`.
- [ ] **Window:** low traffic. Bridge recreation drops live WebSocket/SSE sessions (clients reconnect); workers can be recreated any time (Temporal retries in-flight activities).
- [ ] **Backup location** and who can restore.
- [ ] Who verifies after each phase, and who may call a rollback.

## 3. Phase 0 — pre-flight (read-only, no changes)
Set once per shell (adjust paths):
```bash
ROOT=/home/avi/them
COMPOSE="docker compose --project-name them_gateway --project-directory $ROOT -f $ROOT/docker-compose.yml -f $ROOT/docker-compose.hetzner.yml --profile temporal"
cd $ROOT && git fetch && git status -sb      # clean tree on the release commit; note it:
git rev-parse HEAD                          # RELEASE_SHA
```
- [ ] **Record the running state** (for rollback): the commit currently deployed (`PROD_SHA`), and the images: `docker ps --format '{{.Names}} {{.Image}}'`. List what changes: `git log --oneline PROD_SHA..HEAD`.
- [ ] **Tag the current images** so rollback needs no rebuild (names from `docker inspect <container> --format '{{.Config.Image}}'`):
  `docker tag <image>:latest <image>:pre-2026-11` for each of bridge, bridge-2, go-worker(-2), dag-worker(-2,-debug), auth-go, mcp-service, frontend.
- [ ] **Backup:** `docker exec them-postgres pg_dump -U them -d them -Fc > <backup-dir>/them-pre-rollout-$(date +%F).dump`; check it is non-empty (`pg_restore -l <file> | head`). Keep a safe copy of `.env` and `secrets.local` (do not print them).
- [ ] **DATABASE SETTINGS ON THE RUNNING CONTAINERS (biggest risk, backlog F-103).** Every Go binary now requires `THEM_DB_URL_APP` and `THEM_DB_URL_ADMIN`. In the repo these were missing from the production overlay for `them-go-bridge-2`, the go-workers and the dag workers (fixed in `docker-compose.hetzner.yml` on 2026-11-02, but already-running containers keep their old environment). A container recreated without them **crash-loops** (this happened to `them-middleware-worker` in dev). Check, printing counts only:
  ```bash
  for c in them-go-bridge them-go-bridge-2 them-go-worker them-go-worker-2 them-dag-worker them-dag-worker-2 them-dag-worker-debug; do
    echo "$c: $(docker inspect $c --format '{{range .Config.Env}}{{println .}}{{end}}' | cut -d= -f1 | grep -c '^THEM_DB_URL')  (2 = has both)"; done
  ```
  `0` means the old image never needed them: fine for now, but the new code does. Then confirm the **new** compose renders them and `.env` has them (keys only):
  ```bash
  $COMPOSE config --format json | python3 -c "import json,sys; [print(n, sorted(k for k in (s.get('environment') or {}) if k.startswith('THEM_DB_URL'))) for n,s in sorted(json.load(sys.stdin)['services'].items()) if n.startswith(('them-go','them-dag'))]"
  grep -c '^THEM_DB_URL_' $ROOT/.env     # expect 2; if 0 run ./generate-env.sh (same secrets.local => same values)
  ```
  All seven services must list both keys. **Stop if not.**
- [ ] **Which migrations are pending:** `python3 scripts/check_migrations.py` (needs a running `them-postgres`; read-only). Every `MISSING` line is real. Expect some of 124–129.
- [ ] **Size what the new jobs will touch** (read-only SQL via `docker exec them-postgres psql -U them -d them -At -c "..."`):
  - stuck runs the reconciler may close: `select tenant_id, count(*) from them.runs where status='running' and started_at < now() - interval '2 hours' group by 1;`
  - temporary tokens retention would delete: `select count(*) from them.access_tokens where expires_at < now() - interval '7 days' and label ~ '^(playground-|tr-)';`
  - auth rows the auth purge deletes at startup: `select count(*) from auth_service.user_sessions where expires_at < now() - interval '7 days';` and `select count(*) from auth_service.blacklisted_tokens where expires_at < now();`
  - orphans migration 127 deletes (**irreversible without the dump**): `select count(*) from them.run_artifacts a where not exists (select 1 from them.runs r where r.id=a.run_id);` and the same for `them.hil_approvals`. Also `... and a.storage_key is not null`: stored files of those rows are **not** deleted (no janitor in production); note the count.
  - Write the numbers down: they are the baseline for §5–§7.
- [ ] **Temporal:** note running workflows (UI `/temporal/` or `temporal workflow count --query "ExecutionStatus='Running'"`), so you can tell afterwards what was in flight.
- [ ] **Baseline health:** `docker ps` all healthy; `/health/ready`; free disk; current `LOG_LEVEL` of the workers (the overlay sets `DEBUG`).
- [ ] Release owner and tester agree: **go / no-go for Phase 1.**

## 4. Phase 1 — database migrations (before any code)
Apply **only the ones `check_migrations.py` reported as missing, in numeric order**. Always take the §3 dump first. Template:
```bash
docker cp db/NNN_name.sql them-postgres:/tmp/m.sql
docker exec them-postgres psql -U them -d them -v ON_ERROR_STOP=1 -f /tmp/m.sql
```
| File | Effect | Risk / notes |
|---|---|---|
| `124_entry_point_required_role.sql` | `entry_points.required_role` + CHECK | plain `ADD COLUMN` (fails if re-run) |
| `125_gateway_user_id.sql` | `gateway_requests.user_id` | without it **every gateway usage write fails** (logged as a warning only); plain `ADD COLUMN` |
| `126_entry_point_required_role_tenant_roles.sql` | drops the CHECK so tenant role names can be saved | idempotent; without it the Runtime role dropdown returns 404 |
| `127_run_artifact_cleanup.sql` | outbox table + trigger, **deletes orphan `run_artifacts`/`hil_approvals`**, cascading FKs (added `NOT VALID`, then validated) | the only data-deleting migration: dump first; FK validation does not block writes; apply once |
| `128_retention_grants.sql` | `them_admin` may delete `gateway_requests`; records 125–128 | harmless |
| `129_gateway_requests_run_id.sql` | `gateway_requests.run_id` + partial index | additive, fast |
- [ ] After each: no error; then `python3 scripts/check_migrations.py` reports **0 unexplained**; `select version from them.schema_migrations where version >= '127' order by 1;` shows 127–129.
- [ ] Old code keeps working with all of these applied (additive, plus the FKs): this phase can be done a day early.
- **Rollback:** restore the dump (data loss since the dump) — or leave the schema: 124–126, 128, 129 are additive; with 127 old code only gains FK errors for artifacts of runs that do not exist.

## 5. Phase 2 — build images (nothing recreated yet)
```bash
git checkout <RELEASE_SHA>
$ROOT/scripts/deploy.sh build          # builds all images (Dockerfiles run go test; a failing test stops the build)
```
- [ ] Build succeeds for every service. `docker images | grep them_gateway` shows fresh images.
- [ ] **Go / no-go.** Stop here if any build fails.

## 6. Phase 3 — recreate services one at a time (rolling)
`deploy.sh up` uses `--no-recreate` and `docker restart` keeps the old image: **never** use them to roll out code. One service at a time, verify, then the next:
```bash
$COMPOSE up -d --no-deps --build <service>
docker ps --filter name=^<service>$ --format '{{.Names}} {{.Status}}'
docker logs <service> --since 2m 2>&1 | grep -E "schema is (current|BEHIND)|FATAL|panic|ERROR" | cut -c1-240
```
| # | Service | Then verify |
|---|---|---|
| 1 | `them-go-worker-2`, `them-go-worker` | "database schema is current"; pollers registered (Temporal UI → task queue `them-orchestration-go`) |
| 2 | `them-dag-worker-2`, `them-dag-worker`, `them-dag-worker-debug` — **back to back, within minutes** | schema current; `AppFlowWorkflow` pollers on `appflow-dag` / `appflow-dag-debug`. Old and new dag workers share a queue: a new activity (e.g. `AppFlowMarkHILTimedOutActivity`) scheduled by a new worker can land on an old one and be retried; keep this window short |
| 3 | `them-go-bridge-2`, then `them-go-bridge` | healthy; `/health/ready`; schema current. WebSocket/SSE sessions on the recreated bridge drop and reconnect; the other replica serves meanwhile |
| 4 | `them-auth-go` | healthy; cookie login → `/me` → refresh → logout all 200; logs show `auth retention started` and (first run) `purged expired rows` matching the §3 counts |
| 5 | `them-mcp-service` | healthy |
| 6 | `them-frontend` | UI loads; open a canvas |
| 7 | `them-agent-runtime` replicas, **only if they run on the server** | healthy |
- [ ] Any `FATAL`, restart loop or `schema is BEHIND`: **stop, roll that service back (§10), investigate.** Do not continue to the next service.
- [ ] After step 4 of the table (bridge up): optionally set `THE_M_REQUIRE_MIGRATIONS=true` later (§7), not now.

## 7. Phase 4 — verify (read-only) and observe 24–48 h
**Do not run `scripts/tests/*` against production**: they create and delete apps, runs, tokens and rows.
- [ ] Operator smoke in the UI: log in; run an existing canvas app end to end; open Debug mode on a throwaway flow; cancel a run from the runs page and confirm it ends `canceled` and the workflow stops in Temporal.
- [ ] Logs: `docker logs them-dag-worker --since 10m | grep "activity done"` shows activity lines with `workflow_id`; no unexplained `activity failed` bursts.
- [ ] Reconciler still **dry-run** (overlay has `RECONCILER_DRY_RUN: "true"`): `docker logs them-go-bridge | grep -i "reconciler"` — `[dry-run] would update run` / `would fail orphaned run`. Record the run ids.
- [ ] Retention still **dry-run** (default): `grep "retention: \[dry-run\]"` gives `ACCESS_TOKENS ... rows=N`; N should match the §3 sizing.
- [ ] Redis: gate queue lists have a TTL (`redis-cli ttl them:ep:gate:queue:<app>:<slug>` ≤ 15) and `them:dash:run:*:stream` keys have a TTL ≤ 86400.
- [ ] Watch error rates, restarts, WebSocket reconnect counts and disk for 24–48 h. Anything new → §10.

## 8. Phase 5 — turn on writes, one at a time (never in the same change as a code deploy)
Each step: take a fresh dump, change one env var in `docker-compose.hetzner.yml`, recreate **both** bridges (`-2` first), verify, observe, then decide on the next.
1. **Reconciler** (`RECONCILER_DRY_RUN: "false"` on `them-go-bridge` and `-2`). Go if: at least two full sweeps in dry-run; every `would fail orphaned run` id is older than 2 h and **not Running in Temporal** (spot-check at least 5, all if fewer than 20); the count matches the §3 stuck-run sizing. It clears 100 runs per minute and closes their steps, tasks and approvals. Watch `reconciler: updated run status` lines and `errors_total`.
2. **Retention of temporary tokens** (`THE_M_RETENTION_DRY_RUN: "false"`): the only policy on by default. Go if the dry-run count matches the sizing. Expect one `retention: deleted rows` line.
3. **Optional policies**, each a separate decision with its owner and a dump first: `THE_M_RETENTION_RUNS_DAYS`, `_CHAT_DAYS` (trims conversation memory: tell users), `_GATEWAY_DAYS`, `_AUDIT_DAYS` (compliance sign-off). Start with dry-run to read the counts.
4. **`THE_M_REQUIRE_MIGRATIONS=true`** on the bridge, auth and workers, once every service reports "schema is current": a database behind the code then stops the service instead of only logging.

## 9. Behaviour changes users and owners should be told about
- **AppFlow agent nodes that call canvas-built agents now work** (they returned 401 before, F-137). Flows that were silently failing will start running LLM and tool calls: expect new traffic and cost; ask app owners before the rollout.
- **Admin "cancel run" now cancels the workflow** and closes its children; cancel ends flows waiting for user input.
- **Stuck runs get closed** by the reconciler once writes are on (they were counting against `max_concurrent_runs`).
- **Deleting a run deletes its artifacts and approvals; deleting a tenant deletes its chat history.** Chat history survives deleting individual runs.
- **Run streams expire 24 h after the last event** (reconnect/replay of old runs no longer possible after that).
- **Worker log volume rises** (one INFO line per activity). The overlay runs the workers at `LOG_LEVEL: DEBUG`, which also logs per-node trace events: consider `INFO` in production and check the log rotation caps.
- **New HIL canvas nodes default to a 300 s timeout** (existing nodes unchanged).
- **A `required_role` on an `external_jwt` entry point is enforced** as soon as one is set (none are set by this release).

## 10. Rollback
| What | How |
|---|---|
| One service | `docker tag <image>:pre-2026-11 <image>:latest && $COMPOSE up -d --no-deps --no-build <service>` |
| Whole release | the line above for each service, reverse order of §6, then `git checkout PROD_SHA` |
| A dry-run flip (reconciler / retention) | set the env var back to `"true"`, recreate both bridges. Rows already changed are not restored (reconciler: runs marked failed/stopped; retention: deleted rows → only from the dump) |
| Migrations | leave them (additive; 127 is safe for old code). Restore the dump only for a data problem, accepting loss since the dump |
| Frontend | tag restore as above |

## 11. Watch list (first week)
Orphan-closing volume on the first reconciler sweeps; new `activity failed` patterns from agent nodes that now reach their agents; `max_concurrent_runs` 429s dropping; gateway usage rows appearing (if migration 125 was missing); Redis memory; log disk; any `schema is BEHIND`.

## 12. Not part of this rollout
Events pipeline in production (ClickHouse + event-writer + system-log TTL file `clickhouse/system-logs.xml`), the object janitor / middleware worker, TraceID for the chat path, audit/body event producers, and the stale header of `docker-compose.hetzner.yml`. If the events or security profiles do run on the server, follow `docs/architecture/events-pipeline.md` (ClickHouse restart renames old system tables to `*_0`; drop them).

## 13. Sign-off
| Phase | Done by | Date | Notes / numbers |
|---|---|---|---|
| 0 pre-flight (DSN check, backup, sizing) | | | |
| 1 migrations | | | |
| 2 build | | | |
| 3 rolling recreate | | | |
| 4 verify + observe | | | |
| 5a reconciler on | | | |
| 5b token retention on | | | |
| 5c optional retention | | | |
| 5d require-migrations | | | |
