# Security Gate Redesign — Quarantine-First Flow
# Status: PROPOSED — not yet implemented
# Date: 2026-09-01

---

## Problem

Current flow writes file bytes to `run_artifacts.data` (Postgres) **before** scanning.
An infected file sits in the main DB alongside clean data until the worker gets to it.
There is no isolation between suspected and confirmed-clean files.

---

## New Flow

```
File arrives
     │
     ▼
FileGate.Intercept / InterceptInline
     │
     ├─ Security disabled? ──► RecordArtifact (bytes → run_artifacts, scan_status='disabled')
     │                          deliver to chat immediately
     │
     └─ Security enabled?
          │
          ▼
     Write to quarantine_artifacts (bytes only, not visible to chat)
     Insert middleware_job (status='pending')
     Return quarantine_id to caller — NO artifact_id yet
          │
          ▼
     Middleware worker claims job
          │
          ▼
     ClamAV scans bytes from quarantine
          │
          ├─ CLEAN ──────────► promote: INSERT into run_artifacts (scan_status='clean')
          │                    DELETE quarantine bytes (keep metadata row)
          │                    publish artifact_ready event → chat delivers file bubble
          │
          ├─ INFECTED ───────► DELETE quarantine bytes
          │                    INSERT run_artifacts metadata only (data=NULL, scan_status='infected')
          │                    publish artifact_blocked event → chat shows "file blocked"
          │
          └─ ERROR (fail-open) ► promote with scan_status='error'
                                 deliver to chat (fail-open policy)
```

---

## Schema Changes

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
    data            BYTEA NOT NULL,   -- raw bytes, never served to users
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '1 hour'
);

-- Reaper index
CREATE INDEX idx_quarantine_expires ON them.quarantine_artifacts (expires_at);
```

### Changes to `them.run_artifacts`

- `data` column becomes **nullable** (`ALTER COLUMN data DROP NOT NULL`)
- `data = NULL` means infected (bytes scrubbed) or not yet promoted

### Changes to `them.middleware_jobs`

- Add `quarantine_id UUID REFERENCES them.quarantine_artifacts(id) ON DELETE SET NULL`
- Worker reads bytes from `quarantine_artifacts` instead of `run_artifacts`

---

## Code Changes

### `internal/middleware/gate.go`

**`storeArtifact`** split into two:
- `storeQuarantine(ctx, in, data) (quarantineID string, err error)` — writes to `quarantine_artifacts`
- `promoteToArtifact(ctx, quarantineID, artifactID string)` — called by worker after clean scan

`Intercept` / `InterceptInline` when security enabled:
1. Call `storeQuarantine`
2. Call `Enqueue(quarantineID, ...)`
3. Return `GateResult{QuarantineID: quarantineID, ScanStatus: "pending"}` — no `ArtifactID` yet

### `internal/middleware/job.go`

- `Claim` query also returns `quarantine_id`
- `LoadFileBytes` reads from `quarantine_artifacts` (not `run_artifacts`)
- `Complete` (clean path): INSERT into `run_artifacts`, DELETE `quarantine_artifacts.data` (zero the bytes), set job result
- `Complete` (infected path): INSERT into `run_artifacts` with `data=NULL`, DELETE from `quarantine_artifacts`

### `cmd/middleware-worker/main.go`

`processJob` after pipeline:
- Clean → promote → publish `artifact_ready` on `them:run:{runID}`
- Infected → block → publish `artifact_blocked` on `them:run:{runID}`
- Error → promote (fail-open)

### `internal/orchestrator/orchestrator.go`

`emitArtifactEvent` currently emits a file bubble immediately.
New behaviour when FileGate returns `ScanStatus="pending"`:
- Emit a `file_scanning` event (chat shows spinner)
- When `artifact_ready` arrives on the run channel → emit the real file bubble
- When `artifact_blocked` arrives → emit `file_blocked` bubble

### `internal/artifacts/handler.go`

Already correct: 451 for infected, 202 for pending.
Small addition: `data IS NULL` → 410 Gone (infected bytes scrubbed).

---

## What does NOT change

- `run_artifacts` public shape — callers still get an `artifact_id` (just after scan, not before)
- Download URL format — `/api/v1/runs/{run_id}/artifacts/{artifact_id}` unchanged
- ClamAV wiring — TCP to `them-clamd:3310`, already working
- Services stats page — reads from `run_artifacts`, unchanged

---

## Migration

1. Add `db/051_quarantine.sql` (new table + nullable data column)
2. Existing rows unaffected — `scan_status='disabled'` rows keep their data
3. No backfill needed

---

## Implementation order (2 sessions)

**Session A (this session or next):**
1. `db/051_quarantine.sql` — schema
2. `gate.go` — storeQuarantine replaces storeArtifact for enabled path
3. `job.go` — LoadFileBytes from quarantine, Complete promotes/blocks
4. `middleware-worker/main.go` — publish artifact_ready / artifact_blocked
5. Tests for all new DAL paths

**Session B:**
1. `orchestrator.go` — hold file bubble until scan result, emit file_scanning
2. Frontend — scanning spinner on file bubble, blocked state
3. Reaper job for expired quarantine rows (stuck pending > 1h)
