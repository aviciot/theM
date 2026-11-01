---
title: Data retention
type: architecture
status: current
owns:
  - go/internal/retention/
last_verified: 2026-11-01   # policies and filters tested against the real tables; live dry-run and delete runs on the dev stack
---

# Data retention

## Purpose
Nothing in the platform used to expire operational data: runs, chat history, gateway history, audit logs, temporary tokens, auth sessions and run streams grew forever. This subsystem deletes old data on a schedule, in small batches, with the dangerous deletions off until an operator configures a period. What each table receives while the platform is used: `docs/guides/data-written-by-usage.md`.

## How it works
Two independent jobs, because the repo rule is that only the auth service touches `auth_service.*`:

**1. Platform job** (`go/internal/retention/`, started in `cmd/them/main.go` step 13d, admin pool so it spans tenants). Every `THE_M_RETENTION_INTERVAL_MINUTES` (60) it takes a cross-pod advisory lock on one dedicated connection (key `987654322`; the unlock runs on the same connection, a failed unlock closes it) and applies each enabled `Policy` in batches of 1000 (max 50 batches per policy per run):

| Policy (env `THE_M_RETENTION_<NAME>_DAYS`) | Default | Deletes | Also removed / queued |
|---|---|---|---|
| `ACCESS_TOKENS` | 7 days | expired tokens whose label starts `playground-` or `tr-` (Admin Playground, test-runner). Real EP tokens are never touched | — |
| `RUNS` | off (0) | runs in a terminal status older than N days (by `ended_at`) | steps, usage, artifacts, approvals cascade; stored files are queued for the janitor (`db/127`) |
| `CHAT` | off | finished chat tasks (`completed/failed/canceled/rejected`) not updated for N days | their messages and task artifacts cascade. This trims the oldest turns of conversations |
| `GATEWAY` | off | `gateway_requests` older than N days | bodies cascade (needs `db/128` grant) |
| `AUDIT` | off | `audit_logs` older than N days (compliance data: keep unless policy says otherwise) | — |

`THE_M_RETENTION_DRY_RUN` (default **true**) only counts and logs `[dry-run] would delete rows policy=… rows=N`. Metrics: `them_retention_deleted_total{policy}`, `them_retention_errors_total`.

**2. Auth job** (`go/internal/authserver/retention.go`, started in `cmd/auth-server/main.go`). Hourly (and at startup): deletes `auth_service.user_sessions` rows expired more than `THE_M_AUTH_RETENTION_SESSIONS_DAYS` (7; 0 keeps them) ago and `blacklisted_tokens` rows as soon as they expire. Nothing reads `user_sessions` (access tokens are stateless JWTs); a blacklist row only matters until the token it revokes expires. `THE_M_AUTH_RETENTION_ENABLED=false` switches it off. Deletes are idempotent, so several replicas are harmless.

## Key invariants
- A policy with 0 days never runs; only expired temporary tokens and expired auth rows are on by default.
- Filters are narrow on purpose and pinned by `TestPolicyFilters`: runs only in terminal statuses (never `running`/`admitted`), chat only finished tasks, tokens only temporary labels.
- Deleting a run never deletes chat history (`tasks.run_id` is SET NULL); that is the `CHAT` policy's job.
- Batched deletes (`IN (SELECT key … LIMIT n)`) keep locks short; a backlog is worked off over several runs.
- The platform job uses the admin pool (BYPASSRLS); it must never run with the tenant pool.

## Failure modes
| Failure | Effect |
|---|---|
| Another pod holds the lock | the run is skipped, retried next interval |
| A policy's delete fails | logged, counted in `errors_total`, other policies still run |
| Dry-run left on | nothing is deleted (by design); set `THE_M_RETENTION_DRY_RUN=false` |
| `them_admin` lacks DELETE on a table | that policy fails every run; `db/128` grants it on `gateway_requests` |
| Temporal visibility / ClickHouse growth | not covered here (backlog F-130; ClickHouse event tables have their own TTLs) |

## Known gaps
No retention yet for: Redis run streams (no TTL, LRU instance), ClickHouse system logs (F-130), Temporal visibility bloat, `them.storage_deletions` rows that exhausted their 20 retries, expired `them.access_tokens` that are not `playground-`/`tr-` (`p4-tok-`, `wstest-` from scripts), `middleware_*` jobs. The Admin Playground still does not delete its token when it closes (F-131); retention cleans it up after 7 days.

## Where to change things
| To do | Edit |
|---|---|
| Add or change a policy | `DefaultPolicies` in `go/internal/retention/retention.go` (+ a test in `retention_integration_test.go`); add the env name to `ConfigFromEnv` |
| Enable writes in dev / prod | `THE_M_RETENTION_DRY_RUN=false` in `docker-compose.dev.yml` (dev); set the env vars in the production overlay deliberately, after a dry-run |
| Auth sessions / blacklist | `go/internal/authserver/retention.go` |
Tests: `go test ./internal/retention/ ./internal/authserver/ -run "Retention|PurgeExpired"`; integration (`-tags=integration`, TEST database): `TestRetention_RealDB_AllPolicies`, `TestPurgeExpired_RealDB`. Index: `go/TEST_INDEX.md`.

## Related
`docs/architecture/run-lifecycle.md` (reconciler, run deletion), `docs/guides/data-written-by-usage.md`, `docs/plans/run-lifecycle-hardening-plan.md` (P3), `docs/architecture/auth-and-roles.md`.
