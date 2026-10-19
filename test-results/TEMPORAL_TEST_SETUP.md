# Testing WITH Temporal Workflows

## Summary

Previous tests (1a-2a) did NOT use Temporal workflows because the test client had no app assigned.
This document explains how to run tests WITH Temporal to measure real worker capacity.

---

## Why Previous Tests Skipped Temporal

**Gateway Execution Path:**
```go
// In go/internal/llmgateway/handler.go line 168-184
if h.lc != nil {
    ca, caErr := h.dal.LoadClientApp(r.Context(), gwClient.TokenHash)
    if caErr == nil && ca != nil {
        // HAS APP → Use AppFlow/Temporal
        h.dispatchAppFlowSync(...)
    }
}
// NO APP → Direct handleSync (fast path)
h.handleSync(...)
```

**Test Client Configuration:**
- Client ID: `00000000-0000-0000-0000-000000000099`
- **app_id: NULL** ← No app assigned!
- Result: Skipped Temporal, used direct mock provider path

**Performance:**
- 400 users: 100% success, 3.8s latency
- No Temporal overhead
- No worker slot usage

---

## New Test Client WITH Temporal

Created second client linked to an app:

```sql
Client ID: 00000000-0000-0000-0000-000000000098
Label: perf-test-temporal-client
Token: test-temporal-workflow-token-99
App: payops-response-sanitizer (1b096328-d661-4d31-808b-35936281b0bb)
Profile: mock
Status: valid
```

**This client WILL use Temporal** because it has `app_id` assigned.

---

## Test Scenarios Created

### Temporal Test Scenarios:
1. **perf-test-3a-temporal-50users.json** - 50 users (150 workflows)
2. **perf-test-3b-temporal-100users.json** - 100 users (300 workflows)

Both use token: `test-temporal-workflow-token-99`

---

## How to Run Temporal Tests

### Option 1: Via UI (Recommended)

1. Go to: http://10.55.125.43:3210/scenarios
2. Find scenario: **"Perf Test 3a - Temporal Workflow (50 Users)"**
3. Click **"Run"**
4. Watch for workflows in logs:
   ```bash
   docker logs them-go-bridge -f | grep "appflow"
   ```

### Option 2: Via API

```bash
curl -X POST http://localhost:8093/run \
  -H "Content-Type: application/json" \
  -d '{
    "scenario_id": "perf-test-3a-temporal-50users",
    "n_users": 50
  }'
```

---

## Expected Results

### Test 3a: 50 Users (150 Workflows)

**With 100 worker slots:**
- ✅ Should pass 100%
- Latency: 5-10s per request (Temporal overhead)
- Worker utilization: ~50-60%

### Test 3b: 100 Users (300 Workflows)

**Exceeds 100 worker slots:**
- ⚠️ Expected: Some queueing
- ⚠️ Possible: Some 502 errors (timeout)
- Latency: 10-20s per request
- Worker utilization: 100% (saturated)

---

## Verification

### Check if Temporal is Being Used:

```bash
# Should see appflow workflow starts:
docker logs them-go-bridge --since 1m | grep "execution: starting appflow"

# Should see appflow sync completions:
docker logs them-go-bridge --since 1m | grep "appflow sync"

# Check worker activity:
docker logs them-dag-worker --since 1m | grep "Activity"
```

### If NO appflow logs appear:
- Client still doesn't have app assigned
- OR test runner is caching old scenario
- OR using wrong token

---

## Token Security Issue

**PROBLEM:** Raw tokens stored in plaintext in scenario JSON files!

```json
{
  "gateway_token": "test-gateway-token-12345"  ← Visible in UI!
}
```

**For Production:**
1. Store tokens in secure vault (HashiCorp Vault, AWS Secrets Manager)
2. Mask tokens in UI: `test-***-***-12345`
3. Use environment variables
4. Never commit real tokens to git

**For Testing:** Current setup is OK (non-production tokens only).

---

## Comparison: Direct vs Temporal

| Metric | Direct Path (No App) | Temporal Path (With App) |
|--------|---------------------|-------------------------|
| Client | ...99 | ...98 |
| App Assigned | ❌ No | ✅ Yes (payops-response-sanitizer) |
| Uses Temporal | ❌ No | ✅ Yes |
| Latency | 300-500ms | 5-10s |
| Worker Slots | 0 used | 1 per concurrent request |
| 100 users | 100% pass | TBD (test needed) |
| 400 users | 100% pass | Would fail (exceeds slots) |

---

## Next Steps

1. **Run Test 3a** (50 users with Temporal) via UI
2. **Verify appflow logs** appear in bridge logs
3. **Run Test 3b** (100 users) to find worker saturation
4. **Compare results** to direct path (Tests 1a-2a)

---

## Script Limitation

`run-perf-test.sh` always uses scenario `"test-policy-mock"` regardless of test ID parameter.

**Workaround:** Run Temporal tests manually via UI.

**Fix needed:** Update script to accept `scenario_id` parameter:
```bash
./scripts/run-perf-test.sh 3a 50 perf-test-3a-temporal-50users
```

---

_Created: 2026-10-19 09:15 UTC_
