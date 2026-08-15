# R-4d Implementation Report: Runtime Tenant Propagation

**Date:** 2026-08-01
**Branch:** main
**Status:** Complete (R-4d fixup applied same session)

---

## Objective

Propagate tenant identity (TenantID, ApplicationID) from the server-resolved
`epconfig.EPConfig` into the WS and SSE Temporal execution paths — so every run
recorded in `them.runs` and every `WorkflowInput` dispatched to the Go Worker
carries correct tenant identity, never client-supplied data.

---

## What Changed

### 1. `go/internal/domain/domain.go`
- `Run.ApplicationID`: changed from `int64` to `string` (UUID string, matches PostgreSQL UUID type)
- `Run.TenantID string`: new field — set from `resolvedCfg.TenantID`, never from request data

### 2. `go/internal/temporal/workflow.go`
- `WorkflowInput.ApplicationID`: changed from `int64` to `string` (UUID string — JSON round-trip safe)
- `WorkflowInput.TenantID string`: new field — propagated from EPConfig into the Temporal payload

### 3. `go/internal/runrecorder/recorder.go`
- `CreateRun` SQL updated: inserts only columns that exist on `them.runs`
  - Drops `context_id` and `application_id` (those columns do not exist on `them.runs`)
  - Adds `tenant_id` — passed as a **plain string** (NOT NULL column; `ErrMissingTenantID` returned before DB call if empty)
  - Signature: `id, tenant_id, entry_point_slug, status, started_at, events_transport` (6 args)
  - `UpdateRunStatus` fixed: uses `error` column (not `error_message`); removes `updated_at` (column does not exist)
- `TenantID` is sourced exclusively from `domain.Run.TenantID`, which is set from `resolvedCfg`
- `ErrMissingTenantID` sentinel exported: callers can `errors.Is()` against it

### 4. `go/internal/ws/handler.go`
- Step 9 (Create run record): `run.TenantID` and `run.ApplicationID` populated from `resolvedCfg`
- Step 11 (WorkflowInput): `input.TenantID` and `input.ApplicationID` populated from `resolvedCfg`
- Client headers (e.g. `X-Tenant-ID`) have no effect — `resolvedCfg` is server-authoritative

### 5. `go/internal/sse/handler.go`
- Steps 10 and 11 (SSE numbering): same tenant propagation as WS handler

### 6. `go/internal/temporal/activities.go`
- Added execution boundary validation in `RunOrchestratorActivity`:
  - Empty `TenantID` → non-retryable `ApplicationError("InvalidInput")`
  - Empty `ApplicationID` → non-retryable `ApplicationError("InvalidInput")`
  - Empty `RunID` → non-retryable `ApplicationError("InvalidInput")`
  - These fail fast with a clear message rather than producing an untenanted run

---

## Schema Note

`them.runs` has `tenant_id UUID NOT NULL` (added by R-4a migration `db/026_tenant_foundation.sql`).
It does **not** have `context_id` or `application_id` columns — those are Go domain concepts
tracked in `domain.Run` for in-memory routing but are not persisted to this table.
`ApplicationID` in `domain.Run` flows into `WorkflowInput` for activity-level routing but is
not written to the runs table.

---

## Tests Added

### `go/internal/runrecorder/recorder_test.go`
| Test | Coverage |
|---|---|
| `TestCreateRun_callsCorrectSQL` | Updated: verifies 6-arg INSERT, tenant_id at arg[1] |
| `TestCreateRun_eventsTransportByMode` | Updated: events_transport now at arg[5] |
| `TestCreateRun_explicitTransportOverridesMode` | Updated: events_transport at arg[5] |
| `TestCreateRun_writesTenantID` | New (fixup): TenantID written as plain string arg[1]; NOT NULL, no *string |
| `TestCreateRun_emptyTenantIDReturnsError` | New (fixup): empty TenantID → `ErrMissingTenantID`, no DB call |
| `TestCreateRun_twoTenantsProduceDistinctRows` | New: two tenant UUIDs → distinct plain-string arg[1] values |

### `go/internal/temporal/worker_test.go`
| Test | Coverage |
|---|---|
| `TestWorkflowInput_TenantIDAndApplicationIDPresent` | TenantID + ApplicationID fields survive JSON round-trip |
| `TestWorkflowInput_ApplicationID_IsString` | ApplicationID serialises as JSON string (UUID), not number |
| `TestRunOrchestratorActivity_RejectsEmptyTenantID` | Empty TenantID → non-retryable error |
| `TestRunOrchestratorActivity_RejectsEmptyApplicationID` | Empty ApplicationID → non-retryable error |
| `TestRunOrchestratorActivity_RejectsEmptyRunID` | Empty RunID → non-retryable error |
| `TestRunOrchestratorActivity_PropagatesTenantToRunner` | All fields set → activity succeeds |

### `go/internal/ws/handler_test.go`
| Test | Coverage |
|---|---|
| `TestWS_RunStoresTenantID` | WS run INSERT carries TenantID from EPConfig; WorkflowInput.TenantID and ApplicationID set from EPConfig |
| `TestWS_ClientTenantHeaderIgnored` | X-Tenant-ID header cannot override server-resolved TenantID |

### `go/internal/sse/handler_test.go`
| Test | Coverage |
|---|---|
| `TestSSE_RunStoresTenantID` | SSE run INSERT carries TenantID from EPConfig; WorkflowInput.TenantID set from EPConfig |
| `TestSSE_ClientTenantHeaderIgnored` | X-Tenant-ID header cannot override server-resolved TenantID |

---

## Security Properties Verified

1. **TenantID is never client-sourced.** Both WS and SSE handlers read tenant identity
   exclusively from `resolvedCfg`, which is loaded from the DB at request time.
2. **Activity boundary enforcement.** `RunOrchestratorActivity` rejects any input where
   TenantID, ApplicationID, or RunID is empty — non-retryable, so misconfigurations
   surface immediately rather than silently producing untenanted runs.
3. **Recorder enforces NOT NULL pre-flight.** `CreateRun` returns `ErrMissingTenantID`
   before any DB call if `TenantID` is empty — consistent with `them.runs.tenant_id UUID NOT NULL`.
   The previous nullable `*string` fallback was a bug (nil → NOT NULL violation at DB).
4. **No NULL tenant_id rows created by Go paths.** WS/SSE handlers populate TenantID from
   EPConfig before calling `CreateRun`; the recorder refuses to proceed without it.

---

## Test Results

- `go test ./...` (Dockerfile.go build): **29 packages, 0 failed**
- `go test -race ./...` (inside builder container): **29 packages, 0 data races**
- Python sanity tests 01 02 03 04 15: **55 passed, 0 failed**
- Go bridges: both healthy (`{"status":"ok"}`)
- Go workers: both polling `them-orchestration-go`

---

## Run-to-Application Linkage

`them.runs` does not store `application_id` directly. The linkage is recoverable via a JOIN:

```sql
SELECT r.*, ep.application_id
FROM them.runs r
JOIN them.entry_points ep ON ep.slug = r.entry_point_slug
WHERE r.tenant_id = $1;
```

`them.entry_points.slug` is UNIQUE, so the join is unambiguous. The query is indexed via
`idx_runs_entry_point_slug` and `idx_entry_points_slug`.

**Assessment:** Sufficient for audit and operational queries. The `entry_point_slug` is always
set by the WS/SSE handler and is stable for the run's lifetime.

**Future schema gap (documented, not fixed here):** If direct `application_id` filtering on `runs`
without the join becomes a performance requirement, a migration can add `application_id UUID`
to `them.runs`. That is not required for R-4d and would need a separate migration + backfill.

---

## Fixup Applied (same session as R-4d)

The initial R-4d commit (`8c64d51`) had a bug: `CreateRun` passed `tenant_id` as `*string`
(nullable), but `them.runs.tenant_id` is `UUID NOT NULL`. A nil `*string` would produce a
`NOT NULL constraint violation` at the DB level on any codepath where `TenantID` was empty.

Fixup changes (this commit):
- Removed `*string` nullable pattern for `tenant_id`
- Added `ErrMissingTenantID` sentinel — returned before DB call if `TenantID` is empty
- Fixed `UpdateRunStatus`: `error_message` → `error`, removed `updated_at` (neither column exists)
- Updated all tests: `*string` assertions → plain string; `TestCreateRun_nullTenantWhenEmpty`
  renamed to `TestCreateRun_emptyTenantIDReturnsError` with inverted expectation
- Race detector: `go test -race ./...` — **0 data races**

---

## Out of Scope

- **A2A path**: tenant propagation into A2A is R-4e.
- **No new DB migration**: R-4a already added `tenant_id` to `them.runs`. R-4d only writes to the existing column.
