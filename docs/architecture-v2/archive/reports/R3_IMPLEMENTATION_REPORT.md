# Phase R-3 Implementation Report — File Artifact Delivery

**Date:** 2026-07-29
**Phase:** R-3
**Commit:** ac12082
**Status:** COMPLETE
**Tests:** 422 passed, 0 failed (`go test ./...` and `go test -race ./...`)

---

## Summary

Phase R-3 implements file artifact delivery for the Go orchestrator per gate doc §1.11 and OD-6.
Agents can now produce file artifacts that are:
1. Stored in `them.run_artifacts` (BYTEA, 1MiB hard limit)
2. Emitted as metadata-only `"file"` events into the Redis Streams / in-process bus
3. Retrievable via `GET /api/v1/runs/{run_id}/artifacts/{artifact_id}` (bearer-token authenticated)

Artifact content is **never** placed inside the Redis event or any log line.

---

## Endpoint Classification

`GET /api/v1/runs/{run_id}/artifacts/{artifact_id}` is classified as **runtime data-plane**.

Reasoning:
- Artifacts are produced during WS/SSE/A2A sessions, not admin operations
- Access is authenticated via bearer token (same as WS/SSE), NOT via RequireSuperAdmin JWT
- Authorization: authenticated caller + artifact.run_id == URL run_id (cross-run denied by SQL)
- Future R-4 will add tenant_id column and cross-tenant enforcement; deferred per OD-6 scope

---

## New Files

| File | Purpose |
|---|---|
| `db/025_run_artifacts.sql` | Creates `them.run_artifacts` (UUID PK, BYTEA data, run_id + app_id indexes) |
| `go/internal/artifacts/handler.go` | Bearer-token-authenticated artifact download handler |
| `go/internal/artifacts/handler_test.go` | 9 handler tests: auth, cross-run denial, headers, body |

---

## Modified Files

| File | Changes |
|---|---|
| `go/internal/runrecorder/recorder.go` | Extended `DBQuerier` with `QueryRow`/`SingleRowScanner`; added `ArtifactInput`, `ArtifactMeta`, `ErrArtifactTooLarge`, `RecordArtifact`, `GetArtifact`, `sanitizeFilename` |
| `go/internal/runrecorder/pgx.go` | `PgxPoolQuerier.QueryRow` implementation |
| `go/internal/runrecorder/recorder_test.go` | 10 new artifact tests; `mockDB` updated for `QueryRow` |
| `go/internal/orchestrator/orchestrator.go` | `ArtifactRecorder` interface, `RunContext` struct, `WithArtifactRecorder`, `emitArtifactEvent`, artifact payload auto-detection in tool results |
| `go/internal/orchestrator/orchestrator_test.go` | 3 new artifact tests |
| `go/internal/temporal/activities.go` | `OrchestratorRunner` interface updated for variadic `RunContext` |
| `go/internal/temporal/worker_test.go` | `fakeOrchestratorRunner` updated |
| `go/internal/server/server.go` | `MountArtifacts` method added |
| `go/cmd/them/main.go` | Artifact handler wired at `/api/v1` with bearer token auth |
| `go/TEST_INDEX.md` | S1-09 +10 tests; S1-28 +3 tests; new S1-30 (9 tests) |
| Integration test fakes | `fakeDBQuerier` structs in ws, sse, a2a, integration tests updated |

---

## Architecture Decisions Made

### OD-6: Artifact storage backend — DB BYTEA with 1MiB limit

Per gate doc §10 OD-6:
- `them.run_artifacts.data BYTEA` — content stored in PostgreSQL
- 1MiB hard limit enforced at Go service layer (`ArtifactMaxBytes = 1 << 20`)
- `ErrArtifactTooLarge` sentinel → orchestrator emits error event, agent call records error text
- Object store (S3/MinIO) deferred to a future wave

### Artifact event schema (metadata only — no data)

```json
{
  "type": "file",
  "artifact_id": "<uuid>",
  "filename": "<sanitized>",
  "content_type": "<safe-mime-type>",
  "size": 12345,
  "run_id": "<run-uuid>",
  "application_id": "<app-uuid or empty>",
  "session_id": "<session-uuid or empty>",
  "download_url": "/api/v1/runs/<run_id>/artifacts/<artifact_id>"
}
```

### Artifact payload detection

Tool results are inspected for the structured artifact payload:
```json
{
  "artifact": {
    "filename": "report.pdf",
    "content_type": "application/pdf",
    "data_base64": "<base64-encoded bytes>"
  }
}
```

When this structure is found, the orchestrator decodes it, records it, and emits a metadata-only `"file"` event. The `data_base64` field is stripped from the event.

### Bearer token auth (not JWT) for artifact endpoint

The artifact download endpoint is NOT mounted under the admin JWT middleware. It uses bearer token validation (same as WS/SSE) so applications can download artifacts using their session token. The admin JWT path (`RequireSuperAdmin`) is for administrative operations only.

---

## Security Properties

| Property | Implementation |
|---|---|
| No content in Redis event | `emitArtifactEvent` marshals only metadata fields |
| No content in logs | Data field excluded from all log lines (SECURITY comments) |
| Path traversal prevention | `sanitizeFilename` (write time) + `safeResponseFilename` (response time) |
| Cross-run denial | `WHERE id=$1::uuid AND run_id=$2::uuid` — SQL enforced |
| Cross-tenant denial | Caller must authenticate; tenant isolation deferred to R-4 |
| No filesystem access | Artifact data from DB only (no `os.Open`, `filepath.Join`) |
| Safe Content-Disposition | RFC 5987 encoding for non-ASCII; ASCII filenames quoted |
| Safe Content-Type | Allow-list via `safeContentType`; falls back to `application/octet-stream` |
| Client disconnect cancels DB fetch | `r.Context()` used for `GetArtifact` call |
| Oversized artifacts | `ErrArtifactTooLarge` → orchestrator error event, no panic |
| Cache-Control | `private, no-store` on all artifact responses |

---

## Test Coverage

### S1-09 additions (runrecorder)
- `TestRecordArtifact_Success`
- `TestRecordArtifact_ExactlyOneMB`
- `TestRecordArtifact_OverLimit`
- `TestRecordArtifact_SanitizesFilename`
- `TestGetArtifact_WrongRun`
- `TestGetArtifact_WrongArtifact`
- `TestSanitizeFilename_PathTraversal`
- `TestSanitizeFilename_Safe`
- `TestSanitizeFilename_HiddenFile`
- `TestSanitizeFilename_Empty`

### S1-28 additions (orchestrator)
- `TestOrchestrator_ArtifactEmitted`
- `TestOrchestrator_ArtifactEventContainsNoPayload`
- `TestOrchestrator_ArtifactTooLarge_ErrorEvent`

### New S1-30 (artifacts handler)
- `TestArtifactDownload_Success`
- `TestArtifactDownload_Unauthorized`
- `TestArtifactDownload_WrongRun`
- `TestArtifactDownload_WrongArtifact`
- `TestArtifactDownload_CrossRun`
- `TestArtifactDownload_SafeContentDisposition`
- `TestArtifactDownload_CorrectHeaders`
- `TestArtifactDownload_BodyContainsData`
- `TestArtifactDownload_UnsafeContentTypeDefaultsToOctetStream`

---

## Redis Event Invariant — Confirmed

`TestOrchestrator_ArtifactEventContainsNoPayload` verifies:
- Event payload does NOT contain `"data"`
- Event payload does NOT contain `"data_base64"`
- Event payload does NOT contain `"storage_path"`
- Event payload DOES contain `artifact_id`, `filename`, `content_type`, `size`, `download_url`

---

## Cross-Tenant Isolation — Current State

Cross-tenant isolation for artifact downloads is **partial** in R-3:
- Cross-run access is denied by SQL (artifact.run_id must match URL run_id)
- Cross-tenant access is not yet enforced because `them.runs` does not have a `tenant_id` column
- Full cross-tenant enforcement deferred to Phase R-4 (tenant foundation)
- Risk: a caller with a valid bearer token from Tenant A can theoretically download artifacts from Tenant B's runs if they know the run_id and artifact_id

This is the same risk that exists for the existing `/api/v1/runs/{run_id}` endpoint. It is a known gap documented in the gate doc under Tier 0 isolation requirements.

---

## Remaining Risks

1. **Cross-tenant artifact access** — deferred to R-4 tenant foundation (same gap as runs endpoint)
2. **`application_id` not on `them.runs`** — artifact's `application_id` is passed from `RunContext` (caller-provided); not verified against the run row. R-4 will add `application_id` to `them.runs`.
3. **Binary content in tool result** — if an agent sends a very large response body, the base64 decode happens in-process before the 1MiB check. Memory spike possible if malformed input. Future: add streaming decode with size gate.
4. **No artifact listing endpoint** — clients know the artifact_id from the "file" event; no `/api/v1/runs/{run_id}/artifacts` list endpoint exists. Deferred.
5. **DB BYTEA limit** — PostgreSQL BYTEA can store larger values but Go enforces 1MiB. Object store migration needed for larger artifacts.

---

## Deployment Notes

Apply migration before deploying the new binary:
```bash
docker cp db/025_run_artifacts.sql them-postgres:/tmp/025_run_artifacts.sql
docker exec them-postgres psql -U them -d them -f /tmp/025_run_artifacts.sql
```

The migration uses `CREATE TABLE IF NOT EXISTS` — safe to apply multiple times.

---

## Confirmation: R-4 and R-5 Not Started

Phase R-4 (Tenant Foundation) and Phase R-5 (Observability) were **not started** in this session. All changes are scoped to R-3 artifact delivery only.
