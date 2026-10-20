# Logging Level Testing Plan — the-M Gateway
# Date: 2026-10-20
# Status: Ready to Execute

---

## Overview

Test the conditional body logging enforcement implemented in commit `752a1581`.

**Goal:** Verify that DEBUG/AUDIT levels capture request/response bodies while MINIMAL/METRICS do not, and measure the performance overhead.

---

## Current State

### Logging Levels Implemented

| Level | Body Capture | DB Writes | Use Case |
|---|---|---|---|
| **minimal** | ❌ No | `gateway_requests` only | Production default — lean, fast |
| **metrics** | ❌ No | `gateway_requests` only | Same as minimal (future: +per-node timing) |
| **debug** | ✅ Yes | `gateway_requests` + `gateway_request_bodies` | Development, troubleshooting |
| **audit** | ✅ Yes | `gateway_requests` + `gateway_request_bodies` | Compliance, security investigations |

### Infrastructure Ready

✅ **Governance Profiles** (in `them.governance_policies`):
```sql
SELECT id, name, logging_level, tenant_id 
FROM them.governance_policies 
WHERE name LIKE 'perf-test-%';
```

Expected 4 rows:
- `perf-test-minimal` → `logging_level: 'minimal'`
- `perf-test-metrics` → `logging_level: 'metrics'`
- `perf-test-debug` → `logging_level: 'debug'`
- `perf-test-audit` → `logging_level: 'audit'`

✅ **Gateway Clients** (in `them.gateway_clients`):
```sql
SELECT id, label, token_hash, governance_policy_id::text 
FROM them.gateway_clients 
WHERE label LIKE 'perf-test-%';
```

Expected 4 rows, each linked to one of the profiles above.

✅ **Test Scenarios** (in `test-runner/data/scenarios/`):
- `40-logging-perf-minimal.json` — 100 users, 3 messages each
- `41-logging-perf-metrics.json` — 100 users, 3 messages each
- `42-logging-perf-debug.json` — 100 users, 3 messages each
- `43-logging-perf-audit.json` — 100 users, 3 messages each

✅ **Gateway Code** — Rebuilt and running (commit 752a1581)

---

## Testing Procedure

### Step 1: Pre-Test Verification

**1.1. Check gateway is healthy:**
```bash
docker logs them-go-bridge --tail 5
```
Should show no errors, service running.

**1.2. Verify governance profiles exist:**
```sql
SELECT name, logging_level 
FROM them.governance_policies 
WHERE name LIKE 'perf-test-%'
ORDER BY name;
```

Expected output:
```
perf-test-audit   | audit
perf-test-debug   | debug
perf-test-metrics | metrics
perf-test-minimal | minimal
```

**1.3. Verify gateway clients are linked:**
```sql
SELECT gc.label, gp.name as profile, gp.logging_level
FROM them.gateway_clients gc
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
ORDER BY gc.label;
```

**1.4. Clear old test data (optional):**
```sql
-- Delete previous test results to avoid confusion
DELETE FROM them.gateway_requests WHERE created_at < now() - interval '1 hour';
DELETE FROM them.gateway_request_bodies WHERE created_at < now() - interval '1 hour';
```

---

### Step 2: Run Test Scenarios

**UI:** http://localhost:3210/scenarios

**Run each scenario in sequence:**

1. **Scenario 40 — MINIMAL**
   - Click "Run" button
   - Wait for completion (~30-60 seconds for 300 requests)
   - Note the **average latency** from results page

2. **Scenario 41 — METRICS**
   - Click "Run" button
   - Wait for completion
   - Note the **average latency**

3. **Scenario 42 — DEBUG**
   - Click "Run" button
   - Wait for completion
   - Note the **average latency** (should be higher than MINIMAL)

4. **Scenario 43 — AUDIT**
   - Click "Run" button
   - Wait for completion
   - Note the **average latency** (should be similar to DEBUG)

**Alternative: Run via backend API:**
```bash
# Run scenario 40
curl -X POST http://localhost:8093/api/scenarios/40-logging-perf-minimal/run

# Check results
curl http://localhost:8093/api/results/<run-id>
```

---

### Step 3: Verify Body Capture Behavior

**After running all 4 scenarios**, verify the database state:

**3.1. Count gateway_requests rows (all levels should have entries):**
```sql
SELECT 
  gc.label,
  gp.logging_level,
  COUNT(*) as request_count
FROM them.gateway_requests gr
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
  AND gr.created_at > now() - interval '1 hour'
GROUP BY gc.label, gp.logging_level
ORDER BY gc.label;
```

**Expected:**
```
perf-test-audit-client   | audit   | ~300
perf-test-debug-client   | debug   | ~300
perf-test-metrics-client | metrics | ~300
perf-test-minimal-client | minimal | ~300
```

**3.2. Count gateway_request_bodies rows (only DEBUG/AUDIT should have entries):**
```sql
SELECT 
  gc.label,
  gp.logging_level,
  COUNT(*) as body_count
FROM them.gateway_request_bodies grb
JOIN them.gateway_requests gr ON grb.request_id = gr.id
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
  AND grb.created_at > now() - interval '1 hour'
GROUP BY gc.label, gp.logging_level
ORDER BY gc.label;
```

**Expected:**
```
perf-test-audit-client | audit | ~300  ← Bodies captured ✅
perf-test-debug-client | debug | ~300  ← Bodies captured ✅
(no rows for metrics/minimal)         ← No bodies ✅
```

**3.3. Inspect a sample body (DEBUG level):**
```sql
SELECT 
  gr.id,
  gr.model_requested,
  gr.tokens_in,
  gr.tokens_out,
  LENGTH(grb.request_text) as req_len,
  LENGTH(grb.response_text) as resp_len,
  LEFT(grb.request_text, 100) as req_sample
FROM them.gateway_requests gr
JOIN them.gateway_request_bodies grb ON grb.request_id = gr.id
JOIN them.gateway_clients gc ON gr.client_id = gc.id
WHERE gc.label = 'perf-test-debug-client'
  AND gr.created_at > now() - interval '1 hour'
LIMIT 1;
```

**Expected:** Should show JSON text in `request_text` and `response_text` columns.

**3.4. Verify MINIMAL has NO bodies:**
```sql
SELECT COUNT(*) as should_be_zero
FROM them.gateway_request_bodies grb
JOIN them.gateway_requests gr ON grb.request_id = gr.id
JOIN them.gateway_clients gc ON gr.client_id = gc.id
WHERE gc.label IN ('perf-test-minimal-client', 'perf-test-metrics-client')
  AND grb.created_at > now() - interval '1 hour';
```

**Expected:** `should_be_zero = 0`

---

### Step 4: Measure Performance Overhead

**4.1. Calculate average latency per logging level:**
```sql
SELECT 
  gp.logging_level,
  COUNT(*) as requests,
  ROUND(AVG(gr.latency_ms)) as avg_latency_ms,
  ROUND(MIN(gr.latency_ms)) as min_latency_ms,
  ROUND(MAX(gr.latency_ms)) as max_latency_ms,
  ROUND(PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY gr.latency_ms)) as p50_ms,
  ROUND(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY gr.latency_ms)) as p95_ms
FROM them.gateway_requests gr
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
  AND gr.created_at > now() - interval '1 hour'
  AND gr.status = 'ok'
GROUP BY gp.logging_level
ORDER BY avg_latency_ms;
```

**Expected output example:**
```
minimal | 300 |  62 |  45 | 120 |  60 |  85  ← Fastest (baseline)
metrics | 300 |  64 |  47 | 115 |  62 |  88  ← Similar to minimal
debug   | 300 |  95 |  55 | 180 |  92 | 140  ← ~50% slower (body capture overhead)
audit   | 300 |  98 |  58 | 185 |  95 | 145  ← Similar to debug
```

**4.2. Calculate overhead percentage:**
```sql
WITH baseline AS (
  SELECT AVG(latency_ms) as baseline_ms
  FROM them.gateway_requests gr
  JOIN them.gateway_clients gc ON gr.client_id = gc.id
  WHERE gc.label = 'perf-test-minimal-client'
    AND gr.created_at > now() - interval '1 hour'
    AND gr.status = 'ok'
)
SELECT 
  gp.logging_level,
  ROUND(AVG(gr.latency_ms)) as avg_ms,
  baseline.baseline_ms as minimal_baseline_ms,
  ROUND(((AVG(gr.latency_ms) - baseline.baseline_ms) / baseline.baseline_ms * 100)::numeric, 1) as overhead_pct
FROM them.gateway_requests gr
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
CROSS JOIN baseline
WHERE gc.label LIKE 'perf-test-%'
  AND gr.created_at > now() - interval '1 hour'
  AND gr.status = 'ok'
GROUP BY gp.logging_level, baseline.baseline_ms
ORDER BY overhead_pct;
```

**Expected:**
```
minimal |  62 |  62 |   0.0%  ← Baseline
metrics |  64 |  62 |   3.2%  ← Negligible difference
debug   |  95 |  62 |  53.2%  ← ~50% overhead due to body capture
audit   |  98 |  62 |  58.1%  ← Similar to debug
```

---

### Step 5: Update Test Scenario Documentation

Update the `test_result_ai_analysis` field in each scenario file with actual results:

**40-logging-perf-minimal.json:**
```json
{
  "test_result_ai_analysis": "✅ PASSED — Baseline performance: avg 62ms, p95 85ms. No bodies written to gateway_request_bodies (verified). Fastest logging level as expected."
}
```

**42-logging-perf-debug.json:**
```json
{
  "test_result_ai_analysis": "✅ PASSED — DEBUG overhead measured: avg 95ms (+53% vs minimal). Bodies captured successfully (300/300 rows in gateway_request_bodies). Trade-off acceptable for debugging scenarios."
}
```

**Via UI:** Edit scenario → paste analysis → Save

**Via file:** Edit JSON directly, commit changes.

---

## Success Criteria

### ✅ Functional Correctness
- [ ] MINIMAL: **Zero** rows in `gateway_request_bodies`
- [ ] METRICS: **Zero** rows in `gateway_request_bodies`
- [ ] DEBUG: **~300** rows in `gateway_request_bodies` with valid JSON
- [ ] AUDIT: **~300** rows in `gateway_request_bodies` with valid JSON

### ✅ Performance
- [ ] MINIMAL: Lowest latency (baseline)
- [ ] METRICS: Similar to MINIMAL (±5%)
- [ ] DEBUG: Measurably higher latency (expected: +30-60%)
- [ ] AUDIT: Similar to DEBUG (±5%)

### ✅ No Regressions
- [ ] All 4 scenarios complete successfully (no HTTP errors)
- [ ] Token counts and cost calculations still work
- [ ] Gateway logs show `logging_level` field correctly

---

## Rollback Plan

If body capture causes issues (crashes, extreme latency, DB errors):

**Immediate:**
```sql
-- Disable all DEBUG/AUDIT profiles temporarily
UPDATE them.governance_policies 
SET logging_level = 'minimal' 
WHERE logging_level IN ('debug', 'audit');
```

**Code revert:**
```bash
git revert 752a1581  # Revert conditional logging enforcement
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml build them-go-bridge
docker compose --project-name them_gateway -f docker-compose.yml -f docker-compose.dev.yml restart them-go-bridge
```

---

## Future Enhancements

### AUDIT Level (Full Implementation)
- Middleware integration: PII detection decisions
- File Guard audit trail
- Governance policy enforcement logs

### METRICS Level
- Per-node timing in AppFlow workflows
- Activity-level timing via Temporal activities

### MINIMAL Optimization
- Reduce `gateway_requests` columns (currently 15 fields)
- Only write: status, tokens, cost, latency (4 fields)

---

## Appendix: Quick Reference

### Tokens
```
perf-test-minimal-token-cf99ed849d8af033c1d5ca3c193ca059
perf-test-metrics-token-9a3f8b6e2d1c5a4f7e8b9c0d1a2b3c4d
perf-test-debug-token-1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d
perf-test-audit-token-7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b
```

### Test Runner UI
- **Scenarios:** http://localhost:3210/scenarios
- **Results:** http://localhost:3210/results

### Database Access
```bash
docker exec -it them-postgres psql -U them -d them
```

### Gateway Logs
```bash
docker logs them-go-bridge --tail 50 -f | grep logging_level
```
