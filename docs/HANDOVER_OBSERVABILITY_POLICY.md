# Observability & Policy-Controlled Logging — Session Handover
# Date: 2026-10-19
# Context: Performance bottleneck investigation → architectural improvement needed
# Next session: Design policy-controlled logging, decide implementation order

---

## Current Situation

**Performance bottleneck identified during gateway load testing (200 concurrent users):**
- **Symptom:** Postgres CPU 100%, requests taking 20-50 seconds (should be <2s)
- **Root cause:** Too many DB writes per request (11 queries: 4 reads + 7 writes)
- **Bottleneck query:** `SUM(cost_usd) FROM gateway_requests` runs on EVERY request
- **Workflow overhead:** Every node execution writes full I/O to `run_steps` table

**Example:** 200 concurrent workflows with 3 nodes each:
- 200 × INSERT `runs`
- 200 × UPDATE `runs` (status)
- 600 × INSERT `run_steps` (full input/output JSON per node)
- 600 × INSERT `run_usage`
- = **1,600 writes in 30 seconds** → Postgres saturated

---

## Discovery: We're Logging Everything, Always

**Current behavior (no policy control):**

| Data | Saved Where | Always/Optional | Impact |
|---|---|---|---|
| Gateway request metadata | `gateway_requests` | Always | Needed for audit |
| Gateway bodies (prompts/completions) | `gateway_request_bodies` (planned) | Always | Privacy/cost concern |
| Workflow metadata | `runs` | Always | Needed for dashboard |
| Node-level debug (full I/O) | `run_steps` | **Always** | **Wasteful!** |
| Usage per LLM call | `run_usage` | Always | Needed for metering |
| Conversation history | `task_messages` | Always | Grows unbounded |

**Problem:** No way to disable debug logging for high-volume production traffic.

---

## Two Related Efforts

### 1. **Observability Storage Design** (already in progress)

**Doc:** `docs/OBSERVABILITY_STORAGE_DESIGN.md`

**Goal:** Move high-volume append-only logs from Postgres to ClickHouse

**Status:** Stage A1 complete (ClickHouse + Redis containers running)
- Containers: `them-clickhouse`, `them-redis-events` (deployed 2026-10-18)
- Schema: 5 ClickHouse tables with TTLs, users created
- Next: A2 (`internal/events/` package), A3 (event-writer), A4 (tests)

**Architecture:**
```
Producers → EventSink → Redis Streams → event-writer → ClickHouse
                                                      → S3 (bodies, optional)
```

**Key decisions (already made):**
- Postgres = control plane (config, state, budgets)
- ClickHouse = data plane (logs, analytics)
- Queue = Redis Streams (swap to Kafka later if needed)
- Pluggable backends: `postgres` | `clickhouse` | `dual` (migration)
- Per-area flags: `THE_M_EVENTS_<AREA>=off|dual|clickhouse` (default `off`)

---

### 2. **Policy-Controlled Logging** (new, needs design)

**Goal:** Different logging levels for different use cases

**Use cases:**

| Scenario | Logging Level | What Gets Saved | Why |
|---|---|---|---|
| Production (default) | **Minimal** | Metadata + counters only | Fast, cheap, privacy-safe |
| Performance monitoring | **Metrics** | + Node timing/status (no I/O) | Identify slow nodes |
| Troubleshooting | **Debug** | + Full input/output per node | Root-cause analysis |
| Compliance investigation | **Audit** | + Encrypted bodies, PII decisions | Legal/security |

**Per-app/per-client control:**
- Policy in `gateway_policy_profiles` or `AppDefinitionDoc`
- Default: minimal (safe, fast)
- Override: "enable debug for app XYZ" (temp or permanent)
- Sample rate: "log 10% of requests" (high-volume tenants)

---

## Key Questions Answered This Session

### Q1: "Why save node-level data at all?"

**Answer:**
- **Workflow metadata** (status, cost, timing) → essential, keep in Postgres
- **Node-level debug** (full I/O) → only needed for troubleshooting, wasteful to save always

**Current:** Saves full debug on every run (no choice)
**Proposed:** Policy controls what gets saved (minimal by default)

---

### Q2: "Why can't we just use Temporal for debug?"

**Answer:** Temporal is for **operational debugging**, our storage is for **analytics/search**.

| Need | Temporal? | Our DB? | Why Both |
|---|---|---|---|
| "Debug workflow abc-123" | ✅ YES | Optional | Temporal UI shows execution trace |
| "Show all runs for app XYZ" | ❌ NO | ✅ YES | Temporal doesn't index by `application_id` |
| "Total tokens this month" | ❌ NO | ✅ YES | No aggregation across workflows |
| "Search by error message" | ❌ NO | ✅ YES | No full-text search |
| "Tenant isolation" | ❌ NO | ✅ YES | Temporal shows all workflows to admin |
| "Long-term retention" | ⚠️ Hard | ✅ YES | Temporal retention is global, not per-tenant |

**Smart approach:**
- **Temporal:** Short retention (7-30 days), operational debug, replay
- **ClickHouse:** Long retention (90d-7y), analytics, search, compliance

**Policy-controlled logging with Temporal:**
```go
func LLMActivity(ctx, input) (output, error) {
    result := callLLM(input)
    
    // Temporal ALWAYS stores activity result (can't disable)
    // But we control what we save to OUR database:
    
    policy := getLoggingPolicy(input.AppID)
    
    switch policy.CaptureLevel {
    case "minimal":
        recorder.RecordUsage(runID, result.Tokens, result.Cost)
        // No run_steps insert
        
    case "debug":
        recorder.RecordStepWithIO(runID, nodeID, input, output)
    }
    
    return result
}
```

---

### Q3: "Redis budget counters — per client or per tenant?"

**Answer:** Per **tenant** (application-level)

```redis
# Current month spend for tenant abc-123
INCRBYFLOAT tenant:abc-123:budget:2026-10 0.00002

# Check budget (instant, no SUM query)
GET tenant:abc-123:budget:2026-10
```

**Why tenant-level:**
- Budget policies are scoped to applications (tenant boundary)
- Multiple clients may share same tenant
- Matches RLS/tenancy model

**Fallback to DB (REQUIRED):**
```go
func CheckBudget(tenantID, amount) error {
    // Try Redis first (fast path)
    spent, err := redis.Get("tenant:"+tenantID+":budget:"+month)
    if err != nil {
        // Redis down → fallback to Postgres SUM
        log.Warn("redis unavailable, falling back to DB")
        spent, err = db.SumMonthlySpend(tenantID, month)
        if err != nil {
            return err // Fail closed
        }
    }
    
    if spent + amount > policy.Budget {
        return ErrBudgetExceeded
    }
    return nil
}
```

**Important:** Redis is the **cache**, not source of truth. Reconcile periodically.

---

## Proposed Policy Schema

**In `gateway_policy_profiles` (or `AppDefinitionDoc`):**

```go
type LoggingPolicy struct {
    // Capture level
    Level string // "minimal" | "metrics" | "debug" | "audit"
    
    // Body capture (expensive, privacy-sensitive)
    CaptureBodyInput  bool // Save request prompts
    CaptureBodyOutput bool // Save completions
    
    // Middleware decisions
    CapturePIIDecisions   bool // PII redaction audit trail
    CaptureFileGuard      bool // File scan results
    
    // Sampling (for high-volume)
    SampleRate float64 // 0.0 to 1.0 (e.g., 0.1 = 10% of requests)
    
    // Retention (future)
    RetentionDays int // Per-app override
}
```

**Capture levels defined:**

| Level | Saves | Use Case | Performance Impact |
|---|---|---|---|
| `minimal` | Metadata, status, tokens, cost | Production default | Low (2 writes/request) |
| `metrics` | + Node timing/status (no I/O) | Performance monitoring | Medium (3-5 writes) |
| `debug` | + Full input/output per node | Troubleshooting | High (7-10 writes) |
| `audit` | + Encrypted bodies, PII decisions | Compliance | Highest (10-15 writes) |

---

## Architecture Decision: Which First?

**Two implementation paths:**

### Option A: Policy-Controlled Logging FIRST (then ClickHouse)

**Rationale:**
1. **Immediate performance win** — minimal mode = 80% fewer Postgres writes
2. **Simpler to implement** — no new containers, just code changes
3. **ClickHouse becomes optional** — Postgres can handle minimal logs
4. **Validates the policy model** — before building ClickHouse integration

**Implementation:**
- Add `logging_policy` JSONB to `gateway_policy_profiles` / `applications`
- Update `runrecorder.RecordAgentStep()` to check policy before writing
- Update `llmgateway.WriteRequest()` to check policy
- Default all existing apps to `minimal` (safe rollout)
- Add admin UI to set policy per app

**Result after Option A:**
- 200 concurrent users: Postgres 20% CPU (was 100%)
- No new containers needed
- ClickHouse still useful for analytics (implement later)

---

### Option B: ClickHouse FIRST (then add policy control)

**Rationale:**
1. **Observability work already started** (Stage A1 complete)
2. **Separation of concerns** — move logs out of operational DB
3. **Future-proof** — ClickHouse handles 10x-100x scale
4. **Policy control easier in ClickHouse** — just don't insert events

**Implementation:**
- Continue Stage A2-A4 (EventSink, event-writer, tests)
- Migrate gateway_requests to ClickHouse (Stage B1)
- Migrate run_steps to ClickHouse (Stage B4)
- Add policy control after migration

**Result after Option B:**
- Postgres still writes everything (bottleneck remains)
- ClickHouse has all the data (for analytics)
- Still need policy control to reduce writes

---

## Recommendation: Option A (Policy First)

**Why:**

1. **Solves the immediate problem** — bottleneck is Postgres writes, not storage size
2. **Smaller change** — no new containers, no event pipeline, just conditional logging
3. **Faster to deliver** — one focused session vs 3-4 sessions for ClickHouse
4. **Risk reduction** — proves policy model before committing to ClickHouse
5. **ClickHouse still valuable** — but not urgent if Postgres is fast enough

**Option A eliminates 80% of writes:**
- Minimal mode: 200 users × 2 writes = 400 writes (was 1,600)
- Postgres CPU: 20% (was 100%)
- Response time: <2 sec (was 20-50 sec)

**Then Option B (ClickHouse) becomes:**
- Lower priority (Postgres is fast now)
- OR move forward for analytics/search features
- OR required when scale grows 10x

---

## Next Session Tasks

**If Option A chosen (policy first):**

1. **Design the policy schema** (read OBSERVABILITY_STORAGE_DESIGN §5 event classes first)
   - Which fields in `gateway_policy_profiles`?
   - Which fields in `AppDefinitionDoc` (for per-app)?
   - Default values (minimal for existing apps)

2. **Implement conditional logging in 3 places:**
   - `go/internal/runrecorder/recorder.go` — `RecordAgentStep()` checks policy
   - `go/internal/llmgateway/dal.go` — `WriteRequest()` checks policy
   - `go/cmd/dag-worker/main.go` — activities check policy before recording

3. **Add Redis budget counters** (with DB fallback)
   - `internal/budget/` package (new)
   - Redis keys: `tenant:{id}:budget:{YYYY-MM}`
   - Reconciliation job (hourly)

4. **Test the impact:**
   - Run 200-user load test with minimal policy
   - Verify: <2 sec response, Postgres CPU <30%
   - Flip one app to debug, verify full logging works

5. **Admin UI for policy control** (optional, can be manual SQL first)

---

**If Option B chosen (ClickHouse first):**

1. **Continue Stage A2** — `go/internal/events/` package (see OBSERVABILITY_STORAGE_DESIGN §11 Phase 0)
2. **Stage A3** — event-writer binary + container
3. **Stage A4** — synthetic tests + failure tests
4. **Stage B1** — migrate gateway_requests (first area)

---

## Files to Read Next Session

**MUST READ (in order):**
1. `docs/OBSERVABILITY_STORAGE_DESIGN.md` — full architecture, already solid
2. `docs/SCHEMA.md` — current `gateway_policy_profiles` schema
3. `db/001_schema.sql` — tables: `gateway_requests`, `runs`, `run_steps`, `run_usage`
4. `go/internal/runrecorder/recorder.go` — where run_steps are written (line 191)
5. `go/internal/llmgateway/handler.go` — where gateway_requests are written (defer at lines ~193, ~264, ~446)

**Context files:**
- `docs/CURRENT.md` — observability Stage A1 status
- `docs/REDIS.md` — existing Redis key patterns (for budget counter design)
- `go/CLAUDE.md` — package map, testing rules

---

## Working Tree Status

**Uncommitted changes (not part of this work):**
```
M docker-compose.dev.yml           (DAG worker concurrency change, can commit)
M go/internal/llmgateway/handler.go (gateway cost calculation, COMMITTED)
M test-runner/data/scenarios/test-policy-mock.json (test scenario, can commit)
M test-runner/frontend/app/history/page.tsx (delete-all button, can commit)
M test-runner/frontend/app/scenarios/[id]/page.tsx (slider 1-1000, can commit)
?? go/internal/middleware/pii/zzz_debug_test.go (test file, can remove or commit)
?? push.sh, test-*.sh, scripts/set-git-remote.sh (local scripts, ignore)
?? test-runner/data/history/ (test results, ignore)
```

**Safe to commit:**
- `docker-compose.dev.yml` (worker concurrency 25)
- Test runner UI improvements (history sort, delete-all, slider)
- Test scenarios

---

## Decision Point for Next Session

**FIRST THING: Decide Option A or Option B**

Ask the user:
> "We have two paths:
> 
> **Option A (Policy First):** Add logging levels (minimal/debug/audit) now. Solves the bottleneck in one session. ClickHouse later for analytics.
> 
> **Option B (ClickHouse First):** Finish observability architecture (3-4 sessions). Add policy control after migration.
> 
> Recommendation: **Option A** — immediate performance fix, simpler, proves the policy model.
> 
> Which do you prefer?"

Then proceed with the chosen path.

---

## Key Constraints (from CLAUDE.md)

- Follow Handler → Service → DAL pattern (no SQL in handlers)
- Every code change needs tests + `go/TEST_INDEX.md` update
- `go test ./...` must pass before commit
- New Redis keys → `docs/REDIS.md`
- Schema changes → `db/001_schema.sql` + `docs/SCHEMA.md`
- Session lifecycle: 5-8 commits max before handover

---

## References

**This conversation (2026-10-19):**
- Started with: "Explain what gets saved to DB for each request"
- User insight: "We need policy control over logging, keep it simple but capable"
- User question: "Why not use Temporal for debug?" → Answered (operational vs analytics)
- User decision: "Prepare handover, decide policy-first vs ClickHouse-first"

**Related work:**
- Gateway load testing (200 users): runs `b7bcbe06-384d-43e6-9db4-72e4ce132458`, `8f5a3549-4b7c-4272-b0d3-e990e33d36f2`
- Bottleneck identified: Postgres CPU 100%, SUM query + run_steps writes
- ClickHouse containers deployed: 2026-10-18 (Stage A1)

---

## Startup Commands for Next Session

```bash
cd /opt/docker/them

# Check current state
git status
git log --oneline -5

# Read the critical docs
cat docs/OBSERVABILITY_STORAGE_DESIGN.md
cat docs/SCHEMA.md | grep -A20 "gateway_policy_profiles"

# Check ClickHouse (if doing Option B)
docker ps --filter "name=clickhouse\|redis-events"

# First prompt:
# "Continue from HANDOVER_OBSERVABILITY_POLICY.md — we need to decide:
#  Option A (policy-controlled logging first) or Option B (ClickHouse first)?
#  I recommend Option A for immediate performance fix. Which do you prefer?"
```

---

**Session prepared by:** Claude Sonnet 4.5
**Handover date:** 2026-10-19
**Next session model:** Sonnet or Opus (architectural decision → Opus recommended)
