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

- Go unit tests (`go test ./...` inside Dockerfile.go): **29 packages, 0 failed**
- Python sanity tests 01 02 03 04 15: **55 passed, 0 failed**
- `go test -race ./...`: **NOT RUN THIS SESSION** — run before next PR merge

---

## Architecture Decisions Made

1. **`context_id` and `application_id` not persisted to `them.runs`**: those columns do not
   exist in the DB. Only `tenant_id` is new. `ApplicationID` travels through domain+WorkflowInput
   for routing but is not written to DB.
2. **Nullable `*string` for tenant_id**: empty TenantID → nil → SQL NULL; prevents UUID CHECK
   violations for legacy runs.
3. **Activity boundary enforcement**: `RunOrchestratorActivity` fails non-retryably if
   TenantID, ApplicationID, or RunID is empty.

---

## Temporary Compatibility Code Still in Place

None introduced in R-4d.

---

## Known Bugs and Blockers

- `go test -race ./...` not run — must run before next PR merge
- `them-go-bridge` container was manually recreated (cosmetic: instance_id shows `go-bridge-2`
  on both; health endpoints confirm both running)
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

**R-4e: Tenant Propagation into A2A**

Propagate TenantID and ApplicationID from the request context into the A2A execution path.
Specifically:
- `a2a/server.go`: resolve tenant identity from the bearer token/JWT at the A2A handler level
- `agentregistry/registry.go`: pass tenant context through to A2A invocations
- Validate TenantID is non-empty before dispatching (consistent with R-4d boundary check)
- Focused tests for A2A tenant propagation

Before starting R-4e, read:
- `docs/A2A_REFERENCE.md`
- `go/internal/a2a/server.go`
- `go/internal/agentregistry/registry.go`
- This handover doc

---

## Commands for Next Session

```bash
# Verify state
git log --oneline -3
git status
docker ps --format "{{.Names}}\t{{.Status}}" | grep -E "go-bridge|go-worker"

# Read before touching A2A
cat docs/A2A_REFERENCE.md
cat go/internal/a2a/server.go
cat go/internal/agentregistry/registry.go

# Run sanity before any change
python3.12 scripts/tests/run_tests.py 01 02 03 04 15
```

**First prompt for next session:**
> Continue the THEM Python-to-Go migration at `/home/avi/them`. R-4d is complete (runtime
> tenant propagation for WS+SSE). Next task is R-4e: tenant propagation into the A2A execution
> path. Read `docs/architecture-v2/NEXT_SESSION_HANDOVER.md`, `go/CLAUDE.md`,
> `docs/A2A_REFERENCE.md`, `go/internal/a2a/server.go`, and
> `go/internal/agentregistry/registry.go` before writing any code. Confirm scope before implementing.
