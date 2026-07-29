# Phase R-3 Complete — Handover to R-4

**Date:** 2026-07-29
**Branch:** main
**HEAD:** ac12082 feat(r3): Phase R-3 file artifact delivery — DB schema, recorder, handler, orchestrator wiring
**Prepared by:** Phase R-3 session

---

## Current Objective

Phase R-3 (File Artifact Delivery) is **complete**. The next task is **Phase R-4: Tenant Foundation** per gate doc §9 and TENANT_FOUNDATION_DECISIONS.md.

---

## What Was Completed This Session (R-3)

1. `db/025_run_artifacts.sql` — new `them.run_artifacts` table (UUID PK, BYTEA data, 1MiB hard limit enforced at Go layer)
2. `go/internal/runrecorder/recorder.go` — `RecordArtifact` (1MiB guard, filename sanitization), `GetArtifact` (cross-run denied by SQL), `ErrArtifactTooLarge` sentinel
3. `go/internal/runrecorder/pgx.go` — `QueryRow` added to `PgxPoolQuerier`
4. `go/internal/orchestrator/orchestrator.go` — `ArtifactRecorder` interface, `RunContext` struct, `emitArtifactEvent` (metadata-only), artifact payload auto-detection in tool results
5. `go/internal/artifacts/handler.go` — bearer-token-authenticated download endpoint; RFC 5987 Content-Disposition; safe content-type allow-list; no filesystem access
6. `go/cmd/them/main.go` — artifact handler wired at `/api/v1/runs/{run_id}/artifacts/{artifact_id}` with tokenCache authenticator
7. `go/TEST_INDEX.md` — S1-09 +10 tests, S1-28 +3 tests, new S1-30 section (9 tests)
8. `docs/architecture-v2/R3_IMPLEMENTATION_REPORT.md` — full R-3 implementation report
9. `docs/architecture-v2/implementation-status.md` — updated with R-3 state
10. `docs/architecture-v2/lessons-learned.md` — R-3 lessons added

Full details: `docs/architecture-v2/R3_IMPLEMENTATION_REPORT.md`

---

## Stack State

| Container | Status |
|---|---|
| them-go-bridge (×2) | Healthy |
| them-go-worker (×2) | Healthy, polling `them-orchestration-go` |
| them-worker (Python) | Healthy, polling `them-orchestration` |
| them-temporal-* | Healthy |
| them-postgres | Healthy |
| them-redis | Healthy |

**Migration not yet applied to running stack:** `db/025_run_artifacts.sql` must be applied before the new binary is deployed.

---

## Test State

```
go test ./...        →   422 passed, 0 failed (27 packages)
go test -race ./...  →   422 passed, 0 data races
```

Python suite last clean run: 390 passed (Phase R-2 baseline; no Python changes in R-3).

---

## Hard Constraints — Carry Forward

- **Temporal is the single durable owner of every run.** No inline execution path exists in Go or Python.
- **Never log token values, API keys, or secrets** at any log level.
- **Never log artifact binary content** — not even at debug level.
- **All Go changes require `go test ./...` before commit.** Zero regressions allowed.
- **Workflow ID scheme `ctx-{contextID}`** must be preserved — changing it orphans in-flight runs.
- **`PythonOrchestrationInput` in `go/internal/temporal/python_input.go`** is dead code — retained until the Python worker is decommissioned. Do not delete it yet.
- **Python worker** remains running on `them-orchestration`. It is not decommissioned. The two workers run in parallel on separate queues.
- **`RUN_EVENTS_MODE`**: Worker must be `streams`; Bridge must be `dual`. Do not change without understanding the Bridge read path.
- **Tenant-aware design**: all new DB queries and Redis keys must include `application_id` or entry point slug.
- DB name and schema: `them` only. Never `odin`.
- **`them.run_artifacts` vs `them.artifacts`**: The `them.run_artifacts` table (R-3) is for Go run file artifacts (BYTEA). The `them.artifacts` table (pre-existing) is for A2A task part artifacts (JSONB). These are separate concerns — do not merge them.
- **Cross-tenant artifact isolation**: `them.run_artifacts` does not yet have a `tenant_id` column. Full cross-tenant enforcement deferred to R-4. The SQL WHERE clause (`id=$1 AND run_id=$2`) provides cross-run isolation.

---

## Known Issues and Blockers

| Issue | Severity | Notes |
|---|---|---|
| `db/025_run_artifacts.sql` not applied to live stack | Non-fatal | Artifact endpoints return DB errors until migration is applied. Apply before deploying new binary. |
| Cross-tenant artifact access not enforced | Known gap | Deferred to R-4 (tenant foundation). Documented in R3_IMPLEMENTATION_REPORT.md. |
| `go/them` and `go/worker` binaries in repo root | Cosmetic | Local build artifacts, covered by `.gitignore`. Not committed. |
| `application_id` not on `them.runs` table | Known gap | Artifact's `application_id` is caller-provided via `RunContext`. Not verified against run row. R-4 fixes this. |

---

## Next Task: Phase R-4 — Tenant Foundation

**Scope:** Add `tenant_id` columns, backfill, and propagate through all layers.

This is a **dedicated, isolated wave** as mandated by D-14 in `TENANT_FOUNDATION_DECISIONS.md`. It must not be mixed with any other migration wave.

**Read before starting R-4:**
- `docs/architecture-v2/TENANT_FOUNDATION_DECISIONS.md` (all decisions O-01..O-08)
- `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §2 (tenant boundary)
- `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §9 Phase R-4 scope

**R-4 scope:**
1. Schema migration: `tenant_id UUID NOT NULL` on `applications`, `agents`, `orchestrators`, `access_tokens`, `runs`, `audit_logs`, `run_artifacts`
2. Backfill all existing rows with `default-tenant` UUID
3. Auth middleware: resolve `TenantID` from JWT claim or bearer token lookup
4. DAL: all admin queries gain `tenantID` parameter and `WHERE tenant_id = $n`
5. Session: `SessionInfo.TenantID` populated (T-1 debt)
6. Run recorder: `CreateRun` gains `tenant_id` parameter
7. Cross-tenant 403 enforcement

**Implementation may begin:** after gate doc open decisions O-01..O-08 are resolved.

**Files most relevant to R-4:**

| File | Why |
|---|---|
| `docs/architecture-v2/TENANT_FOUNDATION_DECISIONS.md` | All O-xx decisions and constraints |
| `docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md` §2 | Tenant boundary design |
| `go/internal/transport/transport.go` | `SessionInfo` struct (add TenantID) |
| `go/internal/auth/token_cache.go` | `TokenInfo` (add TenantID from DB) |
| `go/internal/session/session.go` | `SessionInfo` serialization (add tenant_id field) |
| `go/internal/runrecorder/recorder.go` | `CreateRun` (add tenant_id param) |
| `go/internal/admin/dal/` | All query files (add tenant_id WHERE clauses) |
| `db/001_schema.sql` | Schema source of truth |

**Do not touch** the Python worker, Python adapters, or auth service in R-4.

---

## Starting the Next Session

```bash
# Confirm HEAD and stack health first
git log --oneline -5
cd theM_gateway && docker compose -f docker-compose.yml -f docker-compose.linux.yml -f docker-compose.integration.yml -f docker-compose.traefik.yml --profile temporal ps

# Apply R-3 migration if not done
docker cp db/025_run_artifacts.sql them-postgres:/tmp/025_run_artifacts.sql
docker exec them-postgres psql -U them -d them -f /tmp/025_run_artifacts.sql

# Run Go tests to confirm clean baseline (inside container since no local Go install)
docker run --rm -v /opt/docker/them/go:/workspace -w /workspace golang:1.24-alpine go test ./...
```

**First prompt for the next session:**

> Phase R-3 is complete (HEAD ac12082, 422 Go tests passing, artifact delivery implemented). Start Phase R-4: Tenant Foundation. Read docs/architecture-v2/TENANT_FOUNDATION_DECISIONS.md and docs/architecture-v2/CRITICAL_RUNTIME_ARCHITECTURE_GATE.md §2 and §9 before writing any code. Confirm all open decisions O-01..O-08 are resolved, then plan the migration. Use Opus for planning.

---

## Commits This Session

- `ac12082` feat(r3): Phase R-3 file artifact delivery — DB schema, recorder, handler, orchestrator wiring

Push status: **pushed to origin/main** (confirmed).
