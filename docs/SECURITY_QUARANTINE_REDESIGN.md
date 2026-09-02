# Security Gate Redesign — Quarantine-First Flow
# Status: IMPLEMENTED AND LIVE
# Designed: 2026-09-01 | Implemented: 2026-09-02

---

## Problem

Current flow writes file bytes to `run_artifacts.data` (Postgres) **before** scanning.
An infected file sits in the main DB alongside clean data until the worker gets to it.
There is no isolation between suspected and confirmed-clean files.

---

## Actual Flow (as implemented)

```
File arrives from A2A agent
     │
     ▼
FileGate.InterceptInline  (go/internal/middleware/gate.go)
     │
     ├─ Security disabled / gate error? ──► fail-open: original URL used, no artifact row
     │
     └─ Security enabled?
          │
          ▼
     Upload bytes to MinIO quarantine bucket  (them-quarantine, 1hr TTL)
     INSERT quarantine_artifacts row           (metadata only, storage_key = MinIO key)
     INSERT middleware_job row                 (status='pending', quarantine_id)
     INSERT run_artifacts row                  (scan_status='pending', data=NULL, storage_key=NULL)
     Return artifact_id to orchestrator
          │
          ▼
     Orchestrator emits file_scanning WS event to frontend (spinner in chat)
     Goroutine blocks on Redis pub/sub them:run:<runID> waiting for artifact_scan_result
          │
          ▼
     them-middleware-worker claims job (SELECT FOR UPDATE SKIP LOCKED)
          │
          ▼
     Loads bytes from MinIO quarantine  (LoadFileBytes reads from them-quarantine)
          │
          ▼
     ClamAV scans via TCP  them-clamd:3310  (INSTREAM protocol)
          │
          ├─ CLEAN ──────────► PUT bytes to them-artifacts MinIO bucket (storage_key set)
          │                    UPDATE run_artifacts SET scan_status='clean', storage_key=<key>
          │                    DELETE quarantine bytes from MinIO
          │                    PUBLISH artifact_scan_result on them:run:<runID>
          │                         → orchestrator goroutine emits file WS event (download link shown)
          │
          ├─ INFECTED ───────► DELETE quarantine bytes from MinIO
          │                    UPDATE run_artifacts SET scan_status='infected', data=NULL, storage_key=NULL
          │                    PUBLISH artifact_scan_result (scan_status='infected')
          │                         → orchestrator goroutine emits file_blocked WS event (red blocked bubble)
          │
          └─ ERROR (fail-open) ► UPDATE run_artifacts SET scan_status='error'
                                 PUBLISH artifact_scan_result (scan_status='error')
                                      → orchestrator goroutine emits file WS event (fail-open: user sees file)
```

**Key design differences from the original proposal:**
- **MinIO, not Postgres BYTEA** — file bytes go to MinIO `them-quarantine` bucket; `quarantine_artifacts.data` column is not used
- **ClamAV via TCP** (`them-clamd:3310`) not Unix socket — Unix sockets don't work cross-container in Docker
- **Artifact ID exists immediately** — `run_artifacts` row is inserted at gate time (pending state), before scan completes
- **WS events not dashboard-only** — `file_scanning` / `file_blocked` events go through the orchestrator → WS → chat bubble
- **5-minute timeout fallback** — if scan never completes, orchestrator goroutine emits `file` anyway so user is never stuck

---

## Schema Changes (applied via db/051_quarantine.sql)

### New table: `them.quarantine_artifacts`

```sql
CREATE TABLE them.quarantine_artifacts (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    application_id  UUID NOT NULL REFERENCES them.applications(id),
    run_id          UUID NOT NULL,
    session_id      UUID,
    tenant_id       UUID NOT NULL,
    filename        TEXT NOT NULL,
    content_type    TEXT NOT NULL DEFAULT 'application/octet-stream',
    size            BIGINT NOT NULL,
    storage_key     TEXT NOT NULL,   -- MinIO object key in them-quarantine bucket (NOT BYTEA)
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '1 hour'
);

-- Reaper index (for future reaper job)
CREATE INDEX idx_quarantine_expires ON them.quarantine_artifacts (expires_at);
```

**Note:** `data BYTEA NOT NULL` from the original design was replaced with `storage_key TEXT NOT NULL` — bytes live in MinIO, not Postgres.

### Changes to `them.run_artifacts`

- `data` column is now **nullable** — infected rows have `data=NULL, storage_key=NULL`
- New `storage_key TEXT` column — set to MinIO key in `them-artifacts` bucket for clean files
- Artifact byte resolution in the download handler:
  1. `storage_key != ""` → fetch from MinIO `them-artifacts` bucket
  2. `storage_key == "" AND data IS NULL` → 410 Gone (infected/scrubbed)
  3. `data != NULL` → serve legacy Postgres BYTEA (pre-quarantine rows)

### Changes to `them.middleware_jobs`

- Added `quarantine_id UUID REFERENCES them.quarantine_artifacts(id) ON DELETE SET NULL`
- Worker reads bytes via MinIO using the `quarantine_artifacts.storage_key`

---

## Code Changes (as implemented)

### `go/internal/middleware/gate.go`

`InterceptInline` when security enabled:
1. `storage.PutQuarantine(ctx, key, data)` — upload to MinIO `them-quarantine`
2. INSERT `quarantine_artifacts` row (metadata + `storage_key`)
3. INSERT `run_artifacts` row (`scan_status='pending'`, `data=NULL`, `storage_key=NULL`)
4. INSERT `middleware_jobs` row (`quarantine_id` set)
5. Return `GateResult{ArtifactID: artifactID, Gated: true}`

Fail-open: any MinIO or DB error → returns `GateResult{Gated: false}`, original URL used.

### `go/internal/middleware/job.go`

- `EnqueueWithQuarantine` — inserts job with `quarantine_id`
- `LoadFileBytes` — fetches bytes from MinIO using `quarantine_artifacts.storage_key`
- `Complete` (clean): `storage.PutArtifact(ctx, key, data)` → UPDATE `run_artifacts SET scan_status='clean', storage_key=<key>`; DELETE quarantine from MinIO
- `Complete` (infected): UPDATE `run_artifacts SET scan_status='infected'`; DELETE quarantine from MinIO; `data` stays NULL

### `go/cmd/middleware-worker/main.go`

After pipeline completes, publishes `artifact_scan_result` on `them:run:<runID>` with `scan_status`, `threat`, `artifact_id`, `artifact_name`, `file_size_bytes`.

### `go/internal/orchestrator/orchestrator.go`

`emitArtifactEvent` when FileGate returns `Gated: true`:
1. Synchronously emits `file_scanning` WS event (chat shows spinner)
2. Spawns background goroutine (`context.Background()`, 5-minute timeout)
3. Goroutine calls `scanSubscriber.WaitForScanResult(ctx, runID, artifactID, 5min)`
4. On `clean`/`error`/`disabled` or timeout → emits `file` WS event (download link shown)
5. On `infected` → emits `file_blocked` WS event (red blocked bubble)

### `go/internal/orchestrator/scan_subscriber.go`

`RedisScanSubscriber.WaitForScanResult` — subscribes to `them:run:<runID>` Redis pub/sub, filters `artifact_scan_result` by `artifact_id`, cancels `Receive` loop on first match.

### `go/internal/artifacts/handler.go`

Three-path byte resolution:
1. `storage_key != ""` → `storage.GetArtifact(ctx, key)` → 200 + bytes
2. `storage_key == "" && data == nil` → 410 Gone (infected/scrubbed)
3. `data != nil` → serve legacy Postgres BYTEA

### Frontend (Sessions D — `frontend/src/app/admin/playground/`)

- `playgroundTypes.ts`: `FileMsg` gains `artifact_id?`, `scanning?`, `blocked?`, `threat?`
- `useChatConnection.ts`: handles `file_scanning` (spinner bubble), `file_blocked` (red blocked), `file` (replaces scanning bubble in-place or appends)
- `ChatColumn.tsx`: renders three visual states in the file card

---

## What does NOT change

- Download URL format — `/api/v1/runs/{run_id}/artifacts/{artifact_id}` unchanged
- ClamAV wiring — TCP to `them-clamd:3310`
- Services stats page — reads from `run_artifacts`, unchanged
- `scan_status='disabled'` rows — still served from Postgres BYTEA (legacy path)

---

## Migration (applied)

`db/051_quarantine.sql`:
1. New `them.quarantine_artifacts` table (`storage_key TEXT` instead of `data BYTEA`)
2. `run_artifacts.data` made nullable
3. `run_artifacts.storage_key TEXT` column added
4. `middleware_jobs.quarantine_id` column added

Existing rows unaffected — `scan_status='disabled'` rows keep their BYTEA data.

---

## What's NOT done yet

- **Reaper job** — expired quarantine objects (rows where `expires_at < now()` and bytes still in MinIO) are not cleaned up yet. MinIO TTL policy is the only protection for now.
- Additional processors: `pii_redact`, `prompt_inject`, `schema_validate`, `audit_capture`
