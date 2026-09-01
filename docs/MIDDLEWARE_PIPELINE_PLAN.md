# A2A Middleware Pipeline — Implementation Plan
# the-M platform
# Created: 2026-09-01
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

## Architecture

```
Agent (any) returns FilePart / TextPart via A2A
    ↓
Go gateway (them-go-bridge)
    → intercepts parts at A2A response boundary
    → creates artifact row  (scan_status = pending)
    → enqueues middleware_job row  (processors = [...])
    → returns artifact reference to user immediately
    ↓
them-middleware-worker (N replicas, stateless)
    → SELECT FOR UPDATE SKIP LOCKED from them.middleware_jobs
    → runs enabled processors in order (AV → PII → inject → schema → audit)
    → updates them.artifacts.scan_status = clean | infected | flagged | error
    → publishes WS event: artifact_scan_result on run:<run_id> channel
    ↓
User
    → download endpoint: GET /artifacts/{id}/download
    → returns HTTP 202 + {"status":"scanning"} while pending
    → returns file bytes when clean
    → returns HTTP 451 + {"status":"infected","threat":"..."} when blocked
    ↓
Monitor View
    → receives artifact_scan_result WS event
    → shows scan badge: scanning… → clean ✓ / infected ✗
```

---

## Processors

Each processor implements one interface. Enabled/disabled per application in `security_config`.

| Processor | Input | Action | Config keys |
|---|---|---|---|
| `av_scan` | FilePart bytes | ClamAV scan via Unix socket sidecar | `enabled`, `max_file_bytes`, `block_on_infected` |
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
**Sidecar:** ClamAV daemon (`clamav/clamav` image) — one per worker pod, Unix socket

```
them-middleware-worker pod
├── middleware-worker binary   (Go)
└── clamd sidecar              (ClamAV daemon)
    └── /var/run/clamav/clamd.sock
```

Worker loop (per goroutine pool, configurable via `MIDDLEWARE_WORKER_CONCURRENCY`):

```go
for {
    job := claimNextJob(ctx, db)        // SELECT FOR UPDATE SKIP LOCKED
    if job == nil { sleep(pollInterval); continue }
    result := runPipeline(ctx, job)
    commitResult(ctx, db, job, result)  // update artifact + publish WS event
}
```

**Concurrency model:** Fixed goroutine pool (default 8 per replica). Each goroutine
claims one job independently. No shared state between goroutines except the DB pool
and the ClamAV socket client.

**Scaling:** Horizontal — add replicas. `SKIP LOCKED` ensures each job is claimed
by exactly one worker across all replicas. ClamAV sidecar is per-pod (not shared),
so AV scan capacity scales linearly with replicas.

**Failure handling:**
- Job has `attempt_count` and `max_attempts` (default 3)
- On processor error: increment attempt, release lock, retry after `retry_after`
- On max attempts exceeded: mark `failed`, publish error event, unblock download with error

---

## Database Schema

### New migration: `db/049_middleware_pipeline.sql`

```sql
-- ── Extend them.artifacts ────────────────────────────────────────────────────

ALTER TABLE them.artifacts
  ADD COLUMN IF NOT EXISTS scan_status   TEXT NOT NULL DEFAULT 'disabled'
                           CHECK (scan_status IN ('disabled','pending','scanning','clean','infected','flagged','error','failed')),
  ADD COLUMN IF NOT EXISTS scan_result   JSONB,          -- final processor results
  ADD COLUMN IF NOT EXISTS scanned_at    TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS file_bytes    BYTEA,          -- ephemeral: cleared after scan passes
  ADD COLUMN IF NOT EXISTS file_size     BIGINT,
  ADD COLUMN IF NOT EXISTS file_name     TEXT,
  ADD COLUMN IF NOT EXISTS mime_type     TEXT;

CREATE INDEX IF NOT EXISTS artifacts_scan_status_idx
  ON them.artifacts (scan_status)
  WHERE scan_status IN ('pending','scanning','failed');

-- ── New: them.middleware_jobs ────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.middleware_jobs (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  artifact_id     UUID NOT NULL REFERENCES them.artifacts(id) ON DELETE CASCADE,
  application_id  UUID NOT NULL REFERENCES them.applications(id) ON DELETE CASCADE,
  run_id          UUID,                  -- for WS publish
  session_id      UUID,                  -- for WS publish
  processors      TEXT[] NOT NULL,       -- ordered list of enabled processors
  status          TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','claimed','done','failed')),
  attempt_count   INT NOT NULL DEFAULT 0,
  max_attempts    INT NOT NULL DEFAULT 3,
  claimed_at      TIMESTAMPTZ,           -- NULL = unclaimed
  retry_after     TIMESTAMPTZ,           -- NULL = immediately claimable
  result          JSONB,                 -- per-processor outcomes
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS middleware_jobs_claim_idx
  ON them.middleware_jobs (status, retry_after, created_at)
  WHERE status IN ('pending','failed') AND attempt_count < max_attempts;

-- ── Extend them.applications: security_config ────────────────────────────────

ALTER TABLE them.applications
  ADD COLUMN IF NOT EXISTS security_config JSONB NOT NULL DEFAULT '{}';

-- Default shape (stored in applications.security_config):
-- {
--   "enabled": false,
--   "processors": {
--     "av_scan":        {"enabled": true,  "max_file_mb": 50, "block_on_infected": true},
--     "pii_redact":     {"enabled": false, "llm_assist": false, "block_on_detect": false},
--     "prompt_inject":  {"enabled": false, "block_on_detect": false, "sensitivity": "medium"},
--     "schema_validate":{"enabled": false, "strict": false},
--     "audit_capture":  {"enabled": false}
--   }
-- }

-- ── Audit log table ───────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS them.middleware_audit (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  artifact_id     UUID NOT NULL REFERENCES them.artifacts(id) ON DELETE CASCADE,
  application_id  UUID NOT NULL,
  session_id      UUID,
  run_id          UUID,
  processor       TEXT NOT NULL,
  outcome         TEXT NOT NULL,  -- clean | infected | flagged | error | skipped
  detail          JSONB,          -- threat name, PII types found, etc.
  duration_ms     INT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS middleware_audit_app_idx
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
        ├── av/
        │   └── clamav.go        # ClamAV Unix socket client + scan logic
        ├── pii/
        │   └── redactor.go      # Regex patterns + optional LLM redaction
        ├── inject/
        │   └── detector.go      # Prompt injection pattern + LLM check
        ├── schema/
        │   └── validator.go     # JSON Schema validation via jsonschema lib
        └── audit/
            └── capture.go       # Write to them.middleware_audit
```

### Core interface

```go
// go/internal/middleware/processor.go

type Part struct {
    Kind     string  // "file" | "text" | "data"
    Bytes    []byte  // file content (nil for text/data)
    Text     string  // text content
    Data     []byte  // JSON bytes for data parts
    MimeType string
    FileName string
}

type Result struct {
    Outcome   string         // clean | infected | flagged | error | skipped
    Modified  *Part          // non-nil if processor modified the part (PII redaction)
    Block     bool           // true = stop pipeline, tombstone artifact
    Detail    map[string]any // threat name, PII types, etc.
    DurationMS int64
}

type Processor interface {
    Name() string
    Process(ctx context.Context, part Part, cfg json.RawMessage) (Result, error)
}
```

### Pipeline execution

```go
// go/internal/middleware/pipeline.go

func (p *Pipeline) Run(ctx context.Context, job Job) ([]Result, error) {
    part := loadPartFromArtifact(job.ArtifactID)
    var results []Result
    for _, proc := range p.processors {
        if !isEnabled(proc.Name(), job.AppConfig) { continue }
        r, err := proc.Process(ctx, part, procConfig(proc.Name(), job.AppConfig))
        results = append(results, r)
        if err != nil || r.Block { break }
        if r.Modified != nil { part = *r.Modified }
    }
    return results, nil
}
```

---

## Gateway Integration: go/internal/a2a/

The gateway intercepts `FilePart` (and optionally `TextPart`) in A2A task responses
before forwarding to the user.

**Intercept point:** `go/internal/a2a/server.go` — in the artifact forwarding path
(after `TaskArtifactUpdateEvent` received from agent, before writing to `them.artifacts`
and before streaming to user session).

**Flow:**

```go
// pseudocode — actual implementation in go/internal/a2a/server.go

func handleArtifactUpdate(ctx context.Context, appID, runID, sessionID uuid.UUID,
                          artifact A2AArtifact) error {

    cfg := loadSecurityConfig(ctx, appID)    // cached, refreshed every 30s

    if !cfg.Enabled {
        // Fast path — write artifact directly, scan_status = disabled
        return recorder.SaveArtifact(ctx, artifact, "disabled")
    }

    // Determine which processors apply to this part type
    processors := enabledProcessors(cfg, artifact.Parts)
    if len(processors) == 0 {
        return recorder.SaveArtifact(ctx, artifact, "disabled")
    }

    // Save file bytes to artifact row, enqueue job — atomic transaction
    return db.InTx(ctx, func(tx pgx.Tx) error {
        artifactID := recorder.SaveArtifactTx(tx, artifact, "pending")
        enqueueJob(tx, artifactID, appID, runID, sessionID, processors)
        return nil
    })
    // Response to user carries artifact reference with scan_status=pending
    // Download endpoint blocks until scan_status transitions out of pending
}
```

**Security config cache:** `sync.Map` keyed by `appID`, refreshed every 30s via
background goroutine. Zero overhead on the hot path when scanning is disabled.

---

## Download Gate: go/internal/admin/

```
GET /admin/artifacts/{artifact_id}/download

scan_status = disabled  → 200 + file bytes (or inline part data)
scan_status = pending   → 202 + {"status":"scanning","artifact_id":"..."}
scan_status = scanning  → 202 + {"status":"scanning","artifact_id":"..."}
scan_status = clean     → 200 + file bytes
scan_status = infected  → 451 + {"status":"infected","threat":"Eicar-Test-Signature"}
scan_status = flagged   → 200 + redacted bytes (PII redacted in-place)
scan_status = error     → 200 + original bytes + X-Scan-Warning header
scan_status = failed    → 200 + original bytes + X-Scan-Warning header
```

`451 Unavailable For Legal Reasons` is the correct HTTP status for content blocked
by policy — semantically correct for AV-blocked content.

---

## WS Event: artifact_scan_result

Published on `run:<run_id>` channel (same channel as existing run events).
Received by Monitor View for live badge display.

```json
{
  "type": "artifact_scan_result",
  "artifact_id": "uuid",
  "artifact_name": "report.pdf",
  "scan_status": "clean",
  "processors": [
    {"name": "av_scan",  "outcome": "clean",    "duration_ms": 1240},
    {"name": "pii_redact","outcome": "skipped", "duration_ms": 0}
  ],
  "threat": null,
  "scanned_at": "2026-09-01T14:22:00Z"
}
```

On `infected`:
```json
{
  "type": "artifact_scan_result",
  "artifact_id": "uuid",
  "artifact_name": "invoice.pdf",
  "scan_status": "infected",
  "processors": [
    {"name": "av_scan", "outcome": "infected", "duration_ms": 890,
     "detail": {"threat": "Win.Trojan.Agent-1234"}}
  ],
  "threat": "Win.Trojan.Agent-1234",
  "scanned_at": "2026-09-01T14:22:01Z"
}
```

---

## docker-compose additions

```yaml
# docker-compose.yml

  them-middleware-worker:
    build:
      context: .
      dockerfile: Dockerfile.middleware-worker
    image: them-middleware-worker:latest
    container_name: them-middleware-worker
    restart: unless-stopped
    environment:
      - DATABASE_URL=${DATABASE_URL}
      - REDIS_URL=${REDIS_URL}
      - MIDDLEWARE_WORKER_CONCURRENCY=8
      - MIDDLEWARE_POLL_INTERVAL_MS=500
      - CLAMAV_SOCKET=/var/run/clamav/clamd.sock
      - ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}   # for LLM-assisted processors
    volumes:
      - clamav-socket:/var/run/clamav
      - middleware-scratch:/tmp/middleware-scratch  # tmpfs for file bytes during scan
    depends_on:
      - them-postgres
      - them-redis
      - them-clamd
    networks:
      - them-network
    deploy:
      replicas: 2   # scale up as needed

  them-clamd:
    image: clamav/clamav:stable
    container_name: them-clamd
    restart: unless-stopped
    volumes:
      - clamav-socket:/var/run/clamav
      - clamav-db:/var/lib/clamav
    networks:
      - them-network
    # ClamAV downloads virus definitions on first start (~300MB)
    # Allow 2-5 minutes on first boot before worker connects

volumes:
  clamav-socket:
  clamav-db:
  middleware-scratch:
    driver_opts:
      type: tmpfs
      device: tmpfs
```

**Scaling:** `docker compose --project-name them_gateway ... scale them-middleware-worker=N`

Note: ClamAV is a single shared daemon (one container) accessed by all worker replicas
via the shared socket volume. This is simpler than one-per-replica and sufficient for
most loads — the ClamAV daemon itself is multi-threaded. If ClamAV becomes a bottleneck,
move to one daemon per worker pod using a sidecar pattern.

---

## Security Config — API

### GET /admin/applications/{app_id}/security-config
Returns current security config for the application.

### PUT /admin/applications/{app_id}/security-config
Replaces security config. Validates processor config shapes.

Response shape:
```json
{
  "enabled": true,
  "processors": {
    "av_scan": {
      "enabled": true,
      "max_file_mb": 50,
      "block_on_infected": true
    },
    "pii_redact": {
      "enabled": false,
      "llm_assist": false,
      "block_on_detect": false
    },
    "prompt_inject": {
      "enabled": false,
      "block_on_detect": false,
      "sensitivity": "medium"
    },
    "schema_validate": {
      "enabled": false,
      "strict": false
    },
    "audit_capture": {
      "enabled": false
    }
  }
}
```

---

## UI — Three Surfaces

### 1. Canvas Builder — design-time default

Location: right-side config panel → "Security" section (same as monitoring thresholds)

```
┌─ Security ──────────────────────────────────┐
│  ☑ Enable artifact middleware               │
│                                             │
│  Processors:                                │
│  ☑ AV Scan          Max size: [50] MB       │
│    Policy: ● Block infected  ○ Warn only    │
│  ☐ PII Redaction                            │
│  ☐ Prompt Injection Detection               │
│  ☐ Schema Validation                        │
│  ☐ Audit Capture                            │
└─────────────────────────────────────────────┘
```

Saved to `applications.security_config` via PUT /admin/applications/{id}/security-config.

### 2. Runtime View — operational override

New "Security" tab in RuntimeView (read + write):

- Same toggles as Canvas Builder (live override — takes effect immediately)
- Audit log table below the config:
  | Time | Session | File | Processors | Result | Threat | Duration |
  | --- | --- | --- | --- | --- | --- | --- |
  | 14:22 | abc123 | report.pdf | av_scan, pii | clean | — | 1.2s |
  | 14:18 | def456 | invoice.pdf | av_scan | infected | Win.Trojan | 0.9s |

Filterable by: date range, result (clean/infected/flagged), processor.

### 3. Monitor View — observation only (no config)

New `artifact_scan` event row type in the run feed column.

```
[scan]  report.pdf          scanning…    ⟳
[scan]  report.pdf          clean ✓      1.2s
[scan]  invoice.pdf         infected ✗   Win.Trojan.Agent-1234
```

Driven by the `artifact_scan_result` WS event — no polling.
No config controls in Monitor — read-only observation.

### 4. Playground (low priority)

When a playground run produces an artifact and scanning is enabled on the application:
- Show a "scanning…" spinner on the artifact card
- Flip to "clean — download" or "infected — blocked" when WS event arrives
- No extra config needed — the application's security_config already applies

---

## Performance Targets

| Metric | Target | Notes |
|---|---|---|
| Gateway overhead (scan disabled) | < 0.5ms | Config cache hit, no DB write |
| Gateway overhead (scan enabled) | < 5ms | One TX: artifact row + job row |
| AV scan (1MB file) | < 3s | ClamAV in-memory DB |
| AV scan (50MB file) | < 30s | Within A2A task timeout |
| PII redaction (1K tokens) | < 100ms | Regex only (no LLM) |
| Worker poll latency | < 500ms | `MIDDLEWARE_POLL_INTERVAL_MS` |
| WS notification to user | < 100ms after job done | Existing pub/sub path |
| Throughput per worker replica | ~120 small files/min | 8 goroutines × ~1s avg |

---

## Implementation Order

Each phase is independently deployable and testable.

### Phase 1 — Foundation (no user-visible change)
1. `db/049_middleware_pipeline.sql` — schema additions
2. `go/internal/middleware/processor.go` — interface + registry
3. `go/internal/middleware/job.go` — claim/release/commit DAL
4. `go/internal/middleware/pipeline.go` — chain runner
5. `go/internal/middleware/config.go` — SecurityConfig type + validation
6. `go/internal/admin/` — GET/PUT security-config API endpoints
7. Tests: `go test ./internal/middleware/...`

### Phase 2 — AV Scan processor
1. `go/internal/middleware/av/clamav.go` — ClamAV Unix socket client
2. Register `av_scan` processor in pipeline registry
3. `go/cmd/middleware-worker/main.go` — worker binary, pool, graceful shutdown
4. `docker-compose.yml` — add `them-clamd` + `them-middleware-worker`
5. `Dockerfile.middleware-worker`
6. Tests: mock ClamAV socket, test clean/infected/oversized paths

### Phase 3 — Gateway intercept
1. `go/internal/a2a/server.go` — intercept FilePart, create job atomically
2. Download gate: `GET /admin/artifacts/{id}/download`
3. WS publish: `artifact_scan_result` event on `run:<run_id>`
4. Tests: end-to-end with mock processor (clean path + infected path)

### Phase 4 — UI
1. Canvas Builder: Security config panel
2. Monitor View: `artifact_scan` event row type
3. Runtime View: Security tab + audit log table
4. Frontend API calls: GET/PUT security-config

### Phase 5 — Additional processors (after Phase 3 is stable)
1. `pii_redact` — regex patterns (SSN, CC, email, phone, IBAN)
2. `prompt_inject` — pattern library + optional LLM call
3. `schema_validate` — jsonschema validation
4. `audit_capture` — write to `them.middleware_audit`

### Phase 6 — Playground integration (low priority)
1. Scanning spinner on artifact cards in playground
2. WS event handler for `artifact_scan_result` in playground UI

---

## Open Questions (decide before Phase 2)

1. **File storage for pending artifacts** — file bytes written to `them.artifacts.file_bytes`
   (BYTEA in Postgres) or to a mounted volume? BYTEA is simpler but large files inflate
   the DB. Volume is faster but adds infra. Recommended: BYTEA up to 10MB, volume for larger.
   Decide based on expected artifact sizes.

2. **ClamAV topology** — shared daemon (one `them-clamd` container, all workers use the
   shared socket volume) vs sidecar (one ClamAV per worker pod). Shared is simpler to
   operate. Sidecar scales better at high replica counts (>5). Start shared, migrate
   if ClamAV becomes the bottleneck.

3. **LLM-assisted processors** — `pii_redact` and `prompt_inject` can optionally call
   an LLM for higher accuracy. Uses `ANTHROPIC_API_KEY` already in env. Optional — regex-
   only mode is the default. LLM mode adds ~500ms latency per TextPart.

4. **Retroactive scan of existing artifacts** — artifacts already in `them.artifacts`
   have `scan_status = disabled`. If an application enables scanning, should existing
   artifacts be re-scanned? Recommendation: no — only new artifacts are scanned. Existing
   ones keep `disabled` status and download unchanged.

---

## Files to Create (summary)

| File | Purpose |
|---|---|
| `db/049_middleware_pipeline.sql` | Schema additions |
| `go/cmd/middleware-worker/main.go` | Worker binary |
| `go/internal/middleware/processor.go` | Processor interface + registry |
| `go/internal/middleware/pipeline.go` | Chain runner |
| `go/internal/middleware/job.go` | Job DAL (claim/release/commit) |
| `go/internal/middleware/config.go` | SecurityConfig type + validation |
| `go/internal/middleware/av/clamav.go` | ClamAV client |
| `go/internal/middleware/pii/redactor.go` | PII redaction |
| `go/internal/middleware/inject/detector.go` | Prompt injection |
| `go/internal/middleware/schema/validator.go` | Schema validation |
| `go/internal/middleware/audit/capture.go` | Audit capture |
| `go/internal/admin/security_config.go` | HTTP handler GET/PUT security-config |
| `go/internal/admin/dal/security_config.go` | DAL for security_config |
| `go/internal/admin/service/security_config.go` | Service layer |
| `Dockerfile.middleware-worker` | Worker container image |
| `frontend/src/app/admin/applications/components/SecurityConfigPanel.tsx` | Canvas Builder panel |
| `frontend/src/app/admin/applications/components/SecurityAuditTab.tsx` | Runtime View tab |

## Files to Modify

| File | Change |
|---|---|
| `go/internal/a2a/server.go` | Add FilePart intercept + job enqueue |
| `go/internal/admin/runs.go` | Add artifact download gate endpoint |
| `docker-compose.yml` | Add them-clamd + them-middleware-worker services + volumes |
| `docker-compose.dev.yml` | Dev overrides for worker |
| `go/cmd/them/main.go` | Register security-config routes |
| `frontend/src/app/admin/applications/components/MonitorView.tsx` | Add artifact_scan event row |
| `frontend/src/app/admin/applications/components/CanvasBuilderView.tsx` | Add Security panel |
| `frontend/src/app/admin/applications/components/RuntimeView.tsx` | Add Security tab |
| `frontend/src/lib/api.ts` | Add securityConfig GET/PUT + artifact download |
| `docs/SCHEMA.md` | Document new columns + middleware_jobs + middleware_audit |
| `docs/CURRENT.md` | Record next task |
| `go/TEST_INDEX.md` | Add middleware test rows |

---

## Key Constraints

- Never store decrypted file bytes in Redis — Postgres BYTEA or volume only
- ClamAV socket path must match between clamd container and worker: `/var/run/clamav/clamd.sock`
- `SKIP LOCKED` is essential — without it all workers race on the same row
- security_config cache in gateway must be invalidated on PUT — publish invalidation
  event on Redis channel `them:security_config:invalidated:<app_id>`
- `scan_status = disabled` must be the default — zero performance impact when feature is off
- File bytes in `them.artifacts.file_bytes` must be cleared (set NULL) after scan passes,
  keeping only the scan result metadata. Store bytes only as long as needed for scanning.
- 500 responses must use static strings — never expose ClamAV or processor error details to users
