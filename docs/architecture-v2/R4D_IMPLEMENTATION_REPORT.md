# R-4d Implementation Report: Runtime Tenant Propagation

**Date:** 2026-08-01
**Branch:** main
**Status:** Complete

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
  - Adds `tenant_id` — passed as `*string` (nil → SQL NULL for legacy runs without tenant context)
  - Signature: `id, tenant_id, entry_point_slug, status, started_at, events_transport` (6 args)
- `TenantID` is sourced exclusively from `domain.Run.TenantID`, which is set from `resolvedCfg`

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
| `TestCreateRun_writesTenantID` | New: non-empty TenantID → non-nil *string at arg[1] |
| `TestCreateRun_nullTenantWhenEmpty` | New: empty TenantID → nil *string (SQL NULL) |
| `TestCreateRun_twoTenantsProduceDistinctRows` | New: two tenant UUIDs → distinct arg[1] values |

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
3. **SQL NULL safety.** Empty TenantID → `nil *string` → SQL NULL, preventing UUID
   CHECK constraint violations on the `tenant_id` column.

---

## Test Results

- Docker build: **all 29 packages pass, 0 failed** (`go test ./...` inside Dockerfile.go)
- Python sanity tests 01 02 03 04 15: **55 passed, 0 failed**
- Go bridges: both healthy (`{"status":"ok"}`)
- Go workers: both polling `them-orchestration-go`

---

## Limitations / Out of Scope

- **A2A path**: A2A execution (`/a2a`) does not use `WorkflowInput` — tenant propagation
  into A2A is a separate task (R-4e).
- **`context_id` / `application_id` not in DB**: `domain.Run.ContextID` and
  `domain.Run.ApplicationID` are in-memory fields only. A future migration could add
  `application_id` to `them.runs` for query-time filtering; that is not R-4d scope.
- **No new DB migration**: R-4a already added `tenant_id` to `them.runs`. R-4d only
  writes to the existing column.
