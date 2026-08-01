# R-4d Complete — Handover to R-4e

**Date:** 2026-08-01
**Branch:** main
**Phase completed:** R-4d — Runtime Tenant Propagation (WS + SSE Temporal paths)

---

## Current Objective

R-4d is complete. The next session should implement **R-4e: Tenant Propagation into A2A**.

A2A invocations (`/a2a`, `internal/a2a/server.go`, `internal/agentregistry/registry.go`)
do not yet carry tenant identity. R-4e is the remaining gap in the tenant propagation chain.

---

## Branch and HEAD

Branch: `main`
HEAD after this session's commit: see `git log --oneline -1` (R-4d commit in progress).

---

## Commits Created This Session

1. Architecture review doc: `0038c05 docs(arch): R-4d execution architecture review and corrected scope`
2. R-4d implementation: feat(tenant): R-4d — runtime tenant propagation for WS + SSE Temporal paths (pending)

---

## Work Completed

### R-4a (complete)
DB foundation: `them.tenants` table, `tenant_id UUID NOT NULL` on 7 tables, bootstrap
tenant `00000000-0000-0000-0000-000000000001`, `them.run_artifacts` with tenant_id.
Migration: `db/026_tenant_foundation.sql`. No application code changed.

### R-4b (complete)
Authenticated tenant identity: `tenantctx` package, TenantID in `Claims`/`TokenInfo`/`TokenRow`,
`BearerTenantMiddleware`, `HS256TenantMiddleware`, `RuntimeIdentity` struct.

### R-4c1 (complete)
Tenant-scoped DAL and service layers for admin APIs.

### R-4c2 (complete)
`BearerTenantMiddleware` wired to all tenant-scoped admin routes; bootstrap shim removed.

### R-4d (complete — this session)
- `domain.Run.TenantID string` and `domain.Run.ApplicationID string` (was int64) added
- `temporal.WorkflowInput.TenantID string` and `.ApplicationID string` (was int64) added
- `recorder.CreateRun` writes `tenant_id` to `them.runs` (6-arg INSERT matching actual schema)
  - Schema note: `them.runs` has NO `context_id` or `application_id` columns — those are domain-only
- WS and SSE handlers propagate `resolvedCfg.TenantID`/`.AppID` into `domain.Run` and `WorkflowInput`
- `RunOrchestratorActivity` validates TenantID, ApplicationID, RunID non-empty (non-retryable error)
- 13 new tests; Docker build all 29 packages pass; Python sanity 55 passed
- See `docs/architecture-v2/R4D_IMPLEMENTATION_REPORT.md` for full details

---

## Deployed / Live State

- Both Go bridges: healthy (`{"status":"ok"}`)
- Both Go workers: polling `them-orchestration-go`
- Python bridge, auth service, frontend: unchanged, healthy
- DB: `them.runs.tenant_id UUID NOT NULL` (R-4a migration applied)

---

## Tests Executed

- `go test ./...` (Dockerfile.go build): **29 packages, 0 failed**
- `go test -race ./...` (builder container with CGO_ENABLED=1 + gcc): **29 packages, 0 data races**
- Python sanity tests 01 02 03 04 15: **55 passed, 0 failed**

---

## Architecture Decisions Made

1. **`context_id` and `application_id` not persisted to `them.runs`**: those columns do not
   exist in the DB. Only `tenant_id` is new. `ApplicationID` travels through domain+WorkflowInput
   for routing but is not written to DB. Linkage is via `entry_point_slug → entry_points.application_id`.
2. **`tenant_id` is NOT NULL — plain string, not `*string`**: The initial R-4d implementation
   used a nullable `*string` which would produce a NOT NULL violation at the DB. Fixed: `CreateRun`
   now validates TenantID is non-empty and passes it as a plain `string`. Empty → `ErrMissingTenantID`.
3. **`UpdateRunStatus` SQL corrected**: column is `error` not `error_message`; `updated_at` does not exist.
4. **Activity boundary enforcement**: `RunOrchestratorActivity` fails non-retryably if
   TenantID, ApplicationID, or RunID is empty.
5. **Run-to-application linkage**: `runs.entry_point_slug` → `entry_points.slug` → `entry_points.application_id`.
   Indexed, unambiguous. Future migration can add `application_id` to `runs` if direct filtering needed.

---

## Temporary Compatibility Code Still in Place

None introduced in R-4d.

---

## Known Bugs and Blockers

- `them-go-bridge` container was manually recreated (cosmetic: instance_id shows `go-bridge-2`
  on both; health endpoints confirm both running — no functional impact)
- A2A path (`/a2a`) does not yet carry tenant identity — R-4e

---

## Files Most Relevant to R-4e

- `go/internal/a2a/server.go` — A2A JSON-RPC handler entry point
- `go/internal/agentregistry/registry.go` — A2A invocation + Redis cache
- `go/internal/temporal/activities.go` — already validates TenantID; A2A bypasses this
- `go/internal/domain/domain.go` — `Run.TenantID` and `.ApplicationID` defined
- `docs/A2A_REFERENCE.md` — A2A SDK v1.1.0 ground truth
- `docs/architecture-v2/R4D_IMPLEMENTATION_REPORT.md` — what R-4d did/didn't do

---

## Hard Constraints That Must Remain in Force

1. **TenantID is NEVER accepted from request headers, query params, or body** — only from
   server-resolved EPConfig or auth token cache lookup.
2. **Never use DB name `odin` or schema `odin`** — everything is `them`.
3. **Never query `auth_service.*` tables directly** — use `internal/auth/` from Go.
4. **500 responses must use static strings** — never `err.Error()` from service/DAL layers.
5. **Secrets never appear in log output** — use `cfg.SafeString()`.
6. **Every code change MUST have a test** — zero new failures before commit.

---

## Exact Next Single Focused Task

**R-4e: A2A Execution Path Alignment (auth + gate + session + Temporal)**

The current A2A handler (`go/internal/a2a/server.go`) bypasses authentication, EP config
resolution, tenant identity, admission gate, session registration, and Temporal. It calls
the orchestrator directly in-process. R-4e replaces the direct `orch.Run` call with the
same execution pipeline used by WS and SSE.

See `docs/architecture-v2/R4E_A2A_ARCHITECTURE_REVIEW.md` for the full design.
Read that document before writing any code.

**Corrected scope (not just "pass TenantID through"):**

1. `go/internal/a2a/server.go`:
   - Add deps: `auth.Cache`, `epconfig.Loader`, `gate.Gate`, `session.Store`, `temporal.Client`
   - Remove dep: `*orchestrator.Orchestrator` (no longer used for inbound)
   - `handleMessageSend`: auth → EP config (from `app_slug`) → CheckAccess → gate.Check →
     session.Register → gate.Confirm → recorder.CreateRun → bus.Subscribe → ExecuteWorkflow →
     block → session.End + gate.Release → rpcResult
   - TenantID and AppID come from EPConfig only — never from request payload or headers

2. **Verify `OrchestratorName` on `EPConfig` before starting.** Check `go/internal/ws/handler.go`
   to see how `OrchestratorName` is set in `WorkflowInput`. If it is not on `EPConfig`,
   add it to `EPConfigRow`/`EPConfig` and update `epconfig/pgx.go` first.

3. `go/cmd/them/main.go`: update A2A Server wiring.

4. Fix wire format bug: current `{"kind": "text", "text": "..."}` → spec-correct `{"text": "..."}`.

5. `agentregistry/registry.go`: **NO changes needed.** It is outbound A2A only.

6. Write all tests from `R4E_A2A_ARCHITECTURE_REVIEW.md §15` in `go/internal/a2a/server_test.go`.

7. Update `go/TEST_INDEX.md` in the same commit.

Before starting R-4e, read:
- `docs/architecture-v2/R4E_A2A_ARCHITECTURE_REVIEW.md` (architecture review — authoritative)
- `docs/A2A_REFERENCE.md`
- `go/internal/a2a/server.go`
- `go/internal/ws/handler.go` (reference for OrchestratorName resolution)
- This handover doc

---

## Commands for Next Session

```bash
# Verify state
git log --oneline -3
git status
docker ps --format "{{.Names}}\t{{.Status}}" | grep -E "go-bridge|go-worker"

# Read before touching A2A
cat docs/architecture-v2/R4E_A2A_ARCHITECTURE_REVIEW.md
cat docs/A2A_REFERENCE.md
cat go/internal/a2a/server.go
cat go/internal/ws/handler.go   # reference: how OrchestratorName is set in WorkflowInput

# Run sanity before any change
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
```

**First prompt for next session:**
> Continue the THEM Python-to-Go migration at `/home/avi/them`. R-4d is complete (runtime
> tenant propagation for WS+SSE). Next task is R-4e: align the A2A execution path with
> WS/SSE (auth, EP config, gate, session, Temporal dispatch). Architecture review is
> complete — read `docs/architecture-v2/R4E_A2A_ARCHITECTURE_REVIEW.md` and
> `docs/architecture-v2/NEXT_SESSION_HANDOVER.md` before writing any code. Do NOT start
> with `agentregistry` — that package requires no changes for R-4e. Verify scope for
> OrchestratorName before implementing.
