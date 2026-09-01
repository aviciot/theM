# A2A Middleware Pipeline — Implementation Plan
# the-M platform
# Created: 2026-09-01
# Updated: 2026-09-01 — file storage decision, Redis pub/sub progress events
# Status: DESIGN — not yet implemented

---

## Overview

A pluggable, per-application A2A middleware pipeline that intercepts agent output
(`FilePart`, `TextPart`, typed data parts) before it reaches the user. Each enabled
processor runs in order on a shared Postgres-backed job queue, served by a single
replicated Go worker container (`them-middleware-worker`).

The artifact scanner (AV scan via ClamAV) is the first processor and the reference
implementation. PII redaction, prompt injection detection, schema validation, and
audit capture follow the same interface.

---

## Feature Size Estimate

| Phase | What | Sessions |
|---|---|---|
| Phase 1 — Foundation | Schema + interfaces + config API + tests | 1 |
| Phase 2 — AV scan + worker binary + Docker | ClamAV client, worker loop, compose | 1–2 |
| Phase 3 — Gateway intercept + download gate + WS/Redis | Core integration | 1 |
| Phase 4 — UI | Canvas config panel, Monitor badges, Runtime audit tab | 1 |
| Phase 5 — Additional processors | PII, prompt inject, schema, audit capture | 1 |
| Phase 6 — Playground | Scan spinner on artifact cards | 0.5 |

**Total: ~6 focused sessions.** Phases 1–3 are the hard backend core. Phase 4 is the
visible payoff. Phases 5–6 are extensions that plug into the same interface.

---

## Architecture

```
Agent (any) returns FilePart / TextPart via A2A
    ↓
Go gateway (them-go-bridge)
    → intercepts parts at A2A response boundary
    → stores file bytes temporarily in them.artifacts.file_bytes (BYTEA)
    → creates artifact row  (scan_status = pending)
    → enqueues middleware_job row  (processors = [...])
    → returns artifact reference to user immediately
    ↓
them-middleware-worker (N replicas, stateless)
    → SELECT FOR UPDATE SKIP LOCKED from them.middleware_jobs
    → PUBLISH them:scan:<artifact_id> progress events as each processor runs
    → runs enabled processors in order (AV → PII → inject → schema → audit)
    → on CLEAN:    clears file_bytes from DB, updates scan_status = clean
    → on INFECTED: clears file_bytes from DB, updates scan_status = infected (tombstoned)
    → PUBLISH final result on them:run:<run_id> channel
    ↓
Dashboard WS (ws/dashboard)
    → already subscribed to run:<run_id> channels
    → forwards artifact_scan_result event to connected Monitor clients
    ↓
Monitor View (live, via existing WS)
    → receives per-processor progress events
    → artifact row updates in place: scanning… → av_scan ✓ → pii_redact… → clean ✓
    ↓
User download
    → GET /admin/artifacts/{id}/download
    → 202 scanning  — job in progress
    → 200 + bytes   — clean (bytes still in BYTEA until download, then cleared)
    → 451 blocked   — infected / flagged as block
```

---

## File Storage — Decision

**Problem:** the worker needs the file bytes to scan them. After scanning, the user
needs to download a clean file. Where do the bytes live between agent output and
user download?

**Answer: Postgres BYTEA column, temporary, with a 5MB cap.**

```
Agent sends file
    ↓
Gateway stores bytes in them.artifacts.file_bytes (BYTEA)
    ↓  (seconds to ~1 minute while worker scans)
Worker scans
    ↓
CLEAN    → bytes stay in BYTEA until user downloads
           → after download (or 1h TTL), bytes cleared — only metadata kept
INFECTED → bytes deleted immediately, row tombstoned
           → nobody downloads anything
```

**Why BYTEA and not a Docker volume:**
- Simple — no shared volume to mount across gateway + worker replicas
- No extra infra — bytes live in Postgres which is already required
- Fine for files up to ~5MB (covers JSON reports, short docs, small images)
- Files over `max_file_mb` (default 5MB) are rejected at the gateway before storage

**What is BYTEA:** a Postgres column type for raw binary data — just a blob of bytes
stored inside a database row. The gateway writes the bytes in, the worker reads them
out to scan, the gateway reads them out again to serve the download, then they are
deleted. It is purely temporary storage, not long-term file hosting.

**If large file support is needed later:** replace the BYTEA write/read with a
Docker volume or object storage (S3/MinIO). The `Processor` interface and job queue
do not change — only the storage backend behind `loadPartBytes()` changes.

**File lifecycle in full:**

```
created_at + scan pending    → file_bytes = <bytes>, scan_status = pending
worker claims job            → scan_status = scanning
worker: clean                → scan_status = clean, file_bytes kept for download
user downloads               → 200 + bytes streamed, file_bytes cleared after read
  OR: 1h TTL passes          → file_bytes cleared by cleanup goroutine
worker: infected             → scan_status = infected, file_bytes = NULL immediately
worker: error (max retries)  → scan_status = failed, file_bytes kept, X-Scan-Warning header on download
```

---

## Redis pub/sub — Live Progress Events

The worker publishes two levels of events as it processes each job.

### Channel 1: per-artifact progress (ephemeral, processor-level)

**Key:** `them:scan:<artifact_id>`
**TTL:** none needed — channel is live only while job runs, subscribers are transient
**Published by:** middleware worker, once per processor

```json
{"type":"scan_progress","artifact_id":"uuid","processor":"av_scan","status":"running"}
{"type":"scan_progress","artifact_id":"uuid","processor":"av_scan","status":"clean","duration_ms":1240}
{"type":"scan_progress","artifact_id":"uuid","processor":"pii_redact","status":"running"}
{"type":"scan_progress","artifact_id":"uuid","processor":"pii_redact","status":"skipped","duration_ms":0}
```

The dashboard WS subscribes to `them:scan:<artifact_id>` when it sees a
`scan_status = pending` artifact in a run feed. Unsubscribes on final result.

### Channel 2: final result on run channel (persistent in feed)

**Key:** `them:run:<run_id>` — existing channel, Monitor already listens here
**Published by:** middleware worker, once per job (on completion)

```json
{
  "type": "artifact_scan_result",
  "artifact_id": "uuid",
  "artifact_name": "report.pdf",
  "file_size_bytes": 204800,
  "scan_status": "clean",
  "processors": [
    {"name": "av_scan",   "outcome": "clean",   "duration_ms": 1240},
    {"name": "pii_redact","outcome": "skipped", "duration_ms": 0}
  ],
  "threat": null,
  "total_duration_ms": 1240,
  "scanned_at": "2026-09-01T14:22:00Z"
}
```

On infected:
```json
{
  "type": "artifact_scan_result",
  "artifact_id": "uuid",
  "artifact_name": "invoice.pdf",
  "file_size_bytes": 112640,
  "scan_status": "infected",
  "processors": [
    {"name": "av_scan", "outcome": "infected", "duration_ms": 890,
     "detail": {"threat": "Win.Trojan.Agent-1234"}}
  ],
  "threat": "Win.Trojan.Agent-1234",
  "total_duration_ms": 890,
  "scanned_at": "2026-09-01T14:22:01Z"
}
```

### How the dashboard WS handles scan channels

The existing `ws/dashboard` multiplexer (`go/internal/ws/`) already handles
`run:<run_id>` subscriptions. Extend it to also accept `scan:<artifact_id>`
subscriptions — same pattern, different channel prefix.

When Monitor sees an artifact event with `scan_status = pending`:
1. Client sends `{"type":"subscribe","channels":["scan:<artifact_id>"]}`
2. WS forwards `scan_progress` events to client as they arrive
3. On `artifact_scan_result` (final), client unsubscribes from `scan:<artifact_id>`

---

## Monitor View — Live Scan Display

The artifact event row in the run feed updates in place as progress events arrive.
No polling — purely push via existing WS connection.

**Progression:**

```
[artifact]  report.pdf    204 KB    ⟳ scanning…
[artifact]  report.pdf    204 KB    av_scan ✓ (1.2s)   pii_redact ⟳
[artifact]  report.pdf    204 KB    clean ✓  1.2s    ↓ Download
```

```
[artifact]  invoice.pdf   110 KB    ⟳ scanning…
[artifact]  invoice.pdf   110 KB    infected ✗   Win.Trojan.Agent-1234   🚫 Blocked
```

```
[artifact]  data.json     12 KB     scan disabled   ↓ Download
```

The `artifact_scan` event row type is new in `MonitorView.tsx`. It subscribes to
`scan:<artifact_id>` on mount (if scan_status is pending/scanning) and maintains
local processor state as progress events arrive.

---

## Processors

Each processor implements one interface. Enabled/disabled per application in `security_config`.

| Processor | Input | Action | Config keys |
|---|---|---|---|
| `av_scan` | FilePart bytes | ClamAV scan via Unix socket | `enabled`, `max_file_mb`, `block_on_infected` |
| `pii_redact` | TextPart text | Regex + optional LLM — mask SSN/CC/email/phone | `enabled`, `llm_assist`, `block_on_detect` |
| `prompt_inject` | TextPart text | Pattern + LLM — detect hidden instructions | `enabled`, `block_on_detect`, `sensitivity` |
| `schema_validate` | DataPart JSON | Validate against declared output schema | `enabled`, `strict` |
| `audit_capture` | Any part | Copy to immutable audit log | `enabled`, `min_classification` |

Processors run sequentially on the same job. If a blocking processor fires, remaining
processors are skipped and the artifact is tombstoned.

---

## New Container: them-middleware-worker

**Language:** Go
**Source:** `go/cmd/middleware-worker/` + `go/internal/middleware/`
**Sidecar:** ClamAV daemon (`clamav/clamav` image) — one shared container, all workers
connect via shared Docker volume socket `/var/run/clamav/clamd.sock`

Worker loop (per goroutine, pool of `MIDDLEWARE_WORKER_CONCURRENCY`, default 8):

```go
for {
    job := claimNextJob(ctx, db)         // SELECT FOR UPDATE SKIP LOCKED
    if job == nil { sleep(pollInterval); continue }
    publishProgress(redis, job, "claimed")
    result := runPipeline(ctx, job)      // publishes per-processor progress
    commitResult(ctx, db, redis, job, result) // update artifact + publish final
}
```

**Scaling:** horizontal — add replicas. `SKIP LOCKED` ensures each job is claimed
by exactly one worker across all replicas. ClamAV daemon is shared (one container),
accessed via socket volume mounted into all worker replicas.

**Failure handling:**
- Job has `attempt_count` and `max_attempts` (default 3)
- On processor error: increment attempt, release lock, retry after backoff
- On max attempts: mark `failed`, publish error event, download returns bytes + warning header

---

## Database Schema

### New migration: `db/049_middleware_pipeline.sql`

```sql
-- ── Extend them.artifacts ────────────────────────────────────────────────────

ALTER TABLE them.artifacts
  ADD COLUMN IF NOT EXISTS scan_status TEXT NOT NULL DEFAULT 'disabled'
    CHECK (scan_status IN
      ('disabled','pending','scanning','clean','infected','flagged','error','failed')),
  ADD COLUMN IF NOT EXISTS scan_result  JSONB,       -- final per-processor results
  ADD COLUMN IF NOT EXISTS scanned_at   TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS file_bytes   BYTEA,       -- temporary: cleared after scan + download
  ADD COLUMN IF NOT EXISTS file_size    BIGINT,
  ADD COLUMN IF NOT EXISTS file_name    TEXT,
  ADD COLUMN IF NOT EXISTS mime_type    TEXT;

-- Partial index — only rows that need worker attention
CREATE INDEX IF NOT EXISTS artifacts_scan_pending_idx
  ON them.artifacts (scan_status, created_at)
  WHERE scan_status IN ('pending','scanning');

-- ── New: them.middleware_jobs ────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.middleware_jobs (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  artifact_id     UUID NOT NULL REFERENCES them.artifacts(id) ON DELETE CASCADE,
  application_id  UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
  run_id          UUID,              -- for WS publish on them:run:<run_id>
  session_id      UUID,              -- context
  processors      TEXT[] NOT NULL,   -- ordered: e.g. {av_scan,pii_redact}
  status          TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending','claimed','done','failed')),
  attempt_count   INT NOT NULL DEFAULT 0,
  max_attempts    INT NOT NULL DEFAULT 3,
  claimed_at      TIMESTAMPTZ,       -- NULL = unclaimed
  retry_after     TIMESTAMPTZ,       -- NULL = claimable immediately
  result          JSONB,             -- per-processor outcomes after completion
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for the claim query: only unclaimed/retryable pending rows
CREATE INDEX IF NOT EXISTS middleware_jobs_claim_idx
  ON them.middleware_jobs (created_at)
  WHERE status = 'pending' AND attempt_count < max_attempts;

-- ── Extend them.applications: security_config ────────────────────────────────

ALTER TABLE them.applications
  ADD COLUMN IF NOT EXISTS security_config JSONB NOT NULL DEFAULT '{}';

-- Default shape written by the API when first saved:
-- {
--   "enabled": false,
--   "processors": {
--     "av_scan":         {"enabled": true,  "max_file_mb": 5,  "block_on_infected": true},
--     "pii_redact":      {"enabled": false, "llm_assist": false, "block_on_detect": false},
--     "prompt_inject":   {"enabled": false, "block_on_detect": false, "sensitivity": "medium"},
--     "schema_validate": {"enabled": false, "strict": false},
--     "audit_capture":   {"enabled": false}
--   }
-- }

-- ── Audit log ────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.middleware_audit (
  id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  artifact_id    UUID NOT NULL REFERENCES them.artifacts(id) ON DELETE CASCADE,
  application_id UUID NOT NULL,
  session_id     UUID,
  run_id         UUID,
  processor      TEXT NOT NULL,
  outcome        TEXT NOT NULL,  -- clean|infected|flagged|error|skipped
  detail         JSONB,          -- threat name, PII types, etc.
  duration_ms    INT,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS middleware_audit_app_time_idx
  ON them.middleware_audit (application_id, created_at DESC);
CREATE INDEX IF NOT EXISTS middleware_audit_artifact_idx
  ON them.middleware_audit (artifact_id);
```

---

## Go Package Structure

```
go/
├── cmd/
│   └── middleware-worker/
│       └── main.go              # env load, pool start, graceful shutdown
└── internal/
    └── middleware/
        ├── pipeline.go          # Pipeline type, Run() — chains processors
        ├── processor.go         # Processor interface + registry
        ├── job.go               # Job claim/release/commit (DAL)
        ├── config.go            # SecurityConfig type, defaults, validation
        ├── progress.go          # Redis publish helpers (scan_progress, final result)
        ├── av/
        │   └── clamav.go        # ClamAV Unix socket client + scan logic
        ├── pii/
        │   └── redactor.go      # Regex patterns + optional LLM redaction
        ├── inject/
        │   └── detector.go      # Prompt injection pattern + LLM check
        ├── schema/
        │   └── validator.go     # JSON Schema validation
        └── audit/
            └── capture.go       # Write to them.middleware_audit
```

### Core interface

```go
// go/internal/middleware/processor.go

type Part struct {
    Kind     string  // "file" | "text" | "data"
    Bytes    []byte  // file content (nil for text/data)
    Text     string  // text content (nil for file/data)
    Data     []byte  // JSON bytes for data parts
    MimeType string
    FileName string
}

type Result struct {
    Outcome    string         // clean | infected | flagged | error | skipped
    Modified   *Part          // non-nil if processor modified the part (e.g. PII redaction)
    Block      bool           // true = stop pipeline, tombstone artifact
    Detail     map[string]any // threat name, PII types found, etc.
    DurationMS int64
}

type Processor interface {
    Name() string
    Process(ctx context.Context, part Part, cfg json.RawMessage) (Result, error)
}
```

### Progress publisher

```go
// go/internal/middleware/progress.go

// PublishProgress publishes a per-processor progress event on them:scan:<artifactID>
func PublishProgress(ctx context.Context, rc rueidis.Client,
                     artifactID uuid.UUID, processor, status string,
                     detail map[string]any, durationMS int64) error {
    payload, _ := json.Marshal(map[string]any{
        "type":        "scan_progress",
        "artifact_id": artifactID,
        "processor":   processor,
        "status":      status,
        "duration_ms": durationMS,
        "detail":      detail,
    })
    return rc.Do(ctx, rc.B().Publish().
        Channel("them:scan:" + artifactID.String()).
        Message(string(payload)).Build()).Error()
}

// PublishFinalResult publishes the completed scan result on them:run:<runID>
// (existing channel — Monitor already subscribed)
func PublishFinalResult(ctx context.Context, rc rueidis.Client,
                        runID, artifactID uuid.UUID, result FinalResult) error {
    payload, _ := json.Marshal(map[string]any{
        "type":              "artifact_scan_result",
        "artifact_id":       artifactID,
        "artifact_name":     result.FileName,
        "file_size_bytes":   result.FileSizeBytes,
        "scan_status":       result.ScanStatus,
        "processors":        result.Processors,
        "threat":            result.Threat,
        "total_duration_ms": result.TotalDurationMS,
        "scanned_at":        result.ScannedAt,
    })
    return rc.Do(ctx, rc.B().Publish().
        Channel("them:run:" + runID.String()).
        Message(string(payload)).Build()).Error()
}
```

---

## Redis Keys

| Key | Published by | Consumed by | Purpose |
|---|---|---|---|
| `them:scan:<artifact_id>` | middleware-worker | ws/dashboard → Monitor | Per-processor progress during scan |
| `them:run:<run_id>` | middleware-worker | ws/dashboard → Monitor (existing) | Final scan result event |
| `them:security_config:invalidated:<app_id>` | admin API (on PUT) | gateway config cache | Invalidate 30s security config cache |

Add `them:scan:` prefix to `docs/REDIS.md` when implementing.

---

## Gateway Integration

**Intercept point:** `go/internal/a2a/server.go` — in the artifact forwarding path,
after `TaskArtifactUpdateEvent` received from agent, before streaming to user session.

```go
func handleArtifactUpdate(ctx context.Context, appID, runID, sessionID uuid.UUID,
                          artifact A2AArtifact) error {

    cfg := secCfgCache.Get(appID)   // 30s cached, invalidated via Redis sub

    if !cfg.Enabled || len(enabledProcessors(cfg, artifact.Parts)) == 0 {
        // Fast path — zero overhead, scan_status = disabled
        return recorder.SaveArtifact(ctx, artifact, "disabled")
    }

    // Reject oversized files before storage
    if fileSize(artifact) > cfg.MaxFileMB*1024*1024 {
        return errArtifactTooLarge
    }

    // Atomic: save artifact with bytes + enqueue job in one transaction
    return db.InTx(ctx, func(tx pgx.Tx) error {
        artifactID := recorder.SaveArtifactWithBytesTx(tx, artifact, "pending")
        enqueueJob(tx, artifactID, appID, runID, sessionID,
                   enabledProcessors(cfg, artifact.Parts))
        return nil
    })
    // Artifact reference returned to user immediately — download gate handles the rest
}
```

**Security config cache:** `sync.Map` keyed by `appID`, refreshed every 30s.
Invalidated immediately when gateway receives on `them:security_config:invalidated:<app_id>`.

---

## Download Gate

```
GET /admin/artifacts/{artifact_id}/download

scan_status = disabled   → 200 + file bytes (or part data inline)
scan_status = pending    → 202 + {"status":"scanning","artifact_id":"..."}
scan_status = scanning   → 202 + {"status":"scanning","artifact_id":"..."}
scan_status = clean      → 200 + file bytes, then clear file_bytes column
scan_status = infected   → 451 + {"status":"infected","threat":"Win.Trojan..."}
scan_status = flagged    → 200 + redacted bytes (PII stripped in-place by worker)
scan_status = error      → 200 + original bytes + X-Scan-Warning: scan-error header
scan_status = failed     → 200 + original bytes + X-Scan-Warning: scan-failed header
```

`HTTP 451 Unavailable For Legal Reasons` — correct status for policy-blocked content.

After serving a clean file: worker has already cleared `file_bytes` OR the download
handler clears it after streaming (whichever happens first — idempotent NULL set).

---

## docker-compose additions

```yaml
  them-middleware-worker:
    build:
      context: .
      dockerfile: Dockerfile.middleware-worker
    restart: unless-stopped
    environment:
      - DATABASE_URL=${DATABASE_URL}
      - REDIS_URL=${REDIS_URL}
      - MIDDLEWARE_WORKER_CONCURRENCY=8
      - MIDDLEWARE_POLL_INTERVAL_MS=500
      - CLAMAV_SOCKET=/var/run/clamav/clamd.sock
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
    volumes:
      - clamav-socket:/var/run/clamav
    depends_on:
      - them-postgres
      - them-redis
      - them-clamd
    networks:
      - them-network
    deploy:
      replicas: 2

  them-clamd:
    image: clamav/clamav:stable
    restart: unless-stopped
    volumes:
      - clamav-socket:/var/run/clamav
      - clamav-db:/var/lib/clamav    # virus definition database (~300MB, downloaded on first start)
    networks:
      - them-network
    healthcheck:
      test: ["CMD", "clamdcheck.sh"]
      interval: 60s
      timeout: 10s
      retries: 3

volumes:
  clamav-socket:   # shared Unix socket between clamd and all worker replicas
  clamav-db:       # ClamAV virus definitions — persisted across restarts
```

**First boot note:** ClamAV downloads ~300MB of virus definitions on first start.
Allow 2–5 minutes before the worker can connect. Subsequent starts use the cached
`clamav-db` volume — fast.

**Scaling:** `docker compose --project-name them_gateway ... scale them-middleware-worker=N`
All replicas share the same `clamav-socket` volume — one ClamAV daemon serves all.

---

## Security Config API

### GET /admin/applications/{app_id}/security-config
Returns current config (or defaults if never set).

### PUT /admin/applications/{app_id}/security-config
Validates and saves. Publishes `them:security_config:invalidated:<app_id>` to Redis
so all gateway instances invalidate their cache immediately.

**Response shape:**
```json
{
  "enabled": true,
  "processors": {
    "av_scan":         {"enabled": true,  "max_file_mb": 5,  "block_on_infected": true},
    "pii_redact":      {"enabled": false, "llm_assist": false, "block_on_detect": false},
    "prompt_inject":   {"enabled": false, "block_on_detect": false, "sensitivity": "medium"},
    "schema_validate": {"enabled": false, "strict": false},
    "audit_capture":   {"enabled": false}
  }
}
```

---

## UI — Three Surfaces

### 1. Canvas Builder — design-time default

Right-side config panel → "Security" section (same panel as monitoring thresholds):

```
┌─ Security ───────────────────────────────────────────┐
│  ☑ Enable artifact middleware                        │
│                                                      │
│  ☑ AV Scan         Max file size: [5] MB             │
│    Policy:  ● Block infected   ○ Warn only           │
│  ☐ PII Redaction                                     │
│  ☐ Prompt Injection Detection                        │
│  ☐ Schema Validation                                 │
│  ☐ Audit Capture                                     │
└──────────────────────────────────────────────────────┘
```

Calls PUT /admin/applications/{id}/security-config on save.

### 2. Runtime View — operational override + audit log

New "Security" tab:
- Top half: same toggles as Canvas Builder — live override, takes effect immediately
- Bottom half: audit log table

| Time | Session | File | Size | Processors | Result | Threat | Duration |
|---|---|---|---|---|---|---|---|
| 14:22 | abc…123 | report.pdf | 204 KB | av_scan, pii | clean | — | 1.2s |
| 14:18 | def…456 | invoice.pdf | 110 KB | av_scan | infected | Win.Trojan | 0.9s |

Filterable by date range, result, processor.
Paginated — calls GET /admin/applications/{id}/middleware-audit.

### 3. Monitor View — live observation (no config)

New `artifact_scan` event row type in the run feed column.
Driven purely by WS events — no polling.

**In-place progression:**
```
[artifact]  report.pdf    204 KB    ⟳ scanning…
[artifact]  report.pdf    204 KB    av_scan ✓ 1.2s   pii_redact ⟳
[artifact]  report.pdf    204 KB    ✓ clean  1.2s    ↓ Download
```

```
[artifact]  invoice.pdf   110 KB    ✗ infected   Win.Trojan.Agent-1234   🚫 Blocked
```

```
[artifact]  data.json     12 KB     scan disabled   ↓ Download
```

On mount, if artifact has `scan_status = pending/scanning`:
→ subscribe to `scan:<artifact_id>` on the dashboard WS
→ update row as `scan_progress` events arrive
→ on `artifact_scan_result` (final): unsubscribe, render final state

### 4. Playground (low priority, Phase 6)

Artifact card in playground chat shows scan spinner while pending.
Same WS event flow — no extra backend work needed.

---

## Performance Targets

| Metric | Target |
|---|---|
| Gateway overhead (scan disabled) | < 0.5ms — config cache hit, no DB write |
| Gateway overhead (scan enabled) | < 5ms — one atomic TX: artifact row + job row |
| AV scan (1MB file) | < 3s |
| AV scan (5MB file, max) | < 15s |
| PII redaction (1K tokens, regex only) | < 100ms |
| Worker poll latency (job to claimed) | < 500ms |
| WS progress event to Monitor client | < 100ms after Redis publish |
| Throughput per worker replica (8 goroutines) | ~120 small files/min |

---

## Implementation Order

### Phase 1 — Foundation
1. `db/049_middleware_pipeline.sql`
2. `go/internal/middleware/processor.go` — interface + registry
3. `go/internal/middleware/job.go` — claim/release/commit DAL
4. `go/internal/middleware/pipeline.go` — chain runner
5. `go/internal/middleware/config.go` — SecurityConfig type + validation
6. `go/internal/middleware/progress.go` — Redis publish helpers
7. `go/internal/admin/security_config.go` + DAL + service — GET/PUT API
8. Tests: `go test ./internal/middleware/...`

### Phase 2 — AV scan processor + worker binary
1. `go/internal/middleware/av/clamav.go` — ClamAV Unix socket client
2. Register `av_scan` in pipeline registry
3. `go/cmd/middleware-worker/main.go` — goroutine pool, graceful shutdown
4. `Dockerfile.middleware-worker`
5. `docker-compose.yml` — add `them-clamd` + `them-middleware-worker` + volumes
6. Tests: mock ClamAV socket, clean/infected/oversized/timeout paths

### Phase 3 — Gateway intercept + download gate + WS
1. `go/internal/a2a/server.go` — intercept FilePart, create job atomically
2. `go/internal/admin/runs.go` — download gate endpoint
3. `go/internal/ws/` — handle `scan:<artifact_id>` subscription channel
4. `go/cmd/them/main.go` — register new routes
5. Tests: end-to-end with mock processor

### Phase 4 — UI
1. `SecurityConfigPanel.tsx` — Canvas Builder security section
2. `MonitorView.tsx` — `artifact_scan` event row type + WS subscription
3. `SecurityAuditTab.tsx` — Runtime View security tab + audit table
4. `frontend/src/lib/api.ts` — add securityConfig GET/PUT + audit fetch

### Phase 5 — Additional processors
1. `pii/redactor.go` — regex (SSN, CC, email, phone, IBAN)
2. `inject/detector.go` — pattern library + optional LLM
3. `schema/validator.go` — JSON Schema validation
4. `audit/capture.go` — write to `them.middleware_audit`

### Phase 6 — Playground (low priority)
1. Scan spinner on artifact cards in playground UI
2. Subscribe to `scan:<artifact_id>` WS channel from playground

---

## Files to Create

| File | Purpose |
|---|---|
| `db/049_middleware_pipeline.sql` | Schema additions |
| `go/cmd/middleware-worker/main.go` | Worker binary |
| `go/internal/middleware/processor.go` | Processor interface + registry |
| `go/internal/middleware/pipeline.go` | Chain runner |
| `go/internal/middleware/job.go` | Job DAL |
| `go/internal/middleware/config.go` | SecurityConfig type |
| `go/internal/middleware/progress.go` | Redis publish helpers |
| `go/internal/middleware/av/clamav.go` | ClamAV client |
| `go/internal/middleware/pii/redactor.go` | PII redaction |
| `go/internal/middleware/inject/detector.go` | Prompt injection |
| `go/internal/middleware/schema/validator.go` | Schema validation |
| `go/internal/middleware/audit/capture.go` | Audit capture |
| `go/internal/admin/security_config.go` | HTTP handler |
| `go/internal/admin/dal/security_config.go` | DAL |
| `go/internal/admin/service/security_config.go` | Service layer |
| `Dockerfile.middleware-worker` | Worker image |
| `frontend/.../SecurityConfigPanel.tsx` | Canvas Builder panel |
| `frontend/.../SecurityAuditTab.tsx` | Runtime View tab |

## Files to Modify

| File | Change |
|---|---|
| `go/internal/a2a/server.go` | FilePart intercept + job enqueue |
| `go/internal/admin/runs.go` | Artifact download gate |
| `go/internal/ws/handler.go` | Handle `scan:<artifact_id>` subscription |
| `go/cmd/them/main.go` | Register security-config + audit routes |
| `docker-compose.yml` | Add clamd + middleware-worker + volumes |
| `frontend/.../MonitorView.tsx` | artifact_scan event row |
| `frontend/.../CanvasBuilderView.tsx` | Security config panel |
| `frontend/.../RuntimeView.tsx` | Security tab |
| `frontend/src/lib/api.ts` | Security config + audit API calls |
| `docs/SCHEMA.md` | Document new columns + tables |
| `docs/REDIS.md` | Document them:scan: + them:security_config:invalidated: keys |
| `docs/CURRENT.md` | Record next task |
| `go/TEST_INDEX.md` | Add middleware test rows |

---

## Key Constraints

- `scan_status = disabled` is the default — zero overhead when feature is off
- File bytes (`file_bytes` BYTEA) are temporary — cleared after clean download or on infected verdict
- Files over `max_file_mb` are rejected at the gateway before any DB write
- `SKIP LOCKED` on the job claim query is non-negotiable — without it replicas race
- Security config cache in gateway must be invalidated on PUT via Redis pub/sub
- ClamAV socket path must match across clamd container and worker: `/var/run/clamav/clamd.sock`
- 500 responses must use static strings — never expose ClamAV error details to users
- File bytes in BYTEA are cleared after download (or 1h TTL) — not long-term storage
- `them:scan:<artifact_id>` is an ephemeral channel — no persistence needed, pub/sub only
