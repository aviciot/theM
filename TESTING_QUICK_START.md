# Logging Level Testing — Quick Start Guide
# the-M Gateway Body Capture Enforcement

---

## What We Have

**4 Logging Levels** implemented and ready to test:

| Level | Body Capture | Performance | Use Case |
|---|---|---|---|
| **minimal** | ❌ No | Fastest (baseline) | Production default |
| **metrics** | ❌ No | ~Same as minimal | Same as minimal |
| **debug** | ✅ Yes | +30-60% slower | Development/troubleshooting |
| **audit** | ✅ Yes | +30-60% slower | Compliance/security |

**Infrastructure Ready:**
- ✅ 4 governance profiles configured (perf-test-minimal/metrics/debug/audit)
- ✅ 4 gateway clients linked to profiles
- ✅ 4 test scenarios ready (40-43)
- ✅ Gateway code deployed (commit 752a1581)

---

## Option 1: Fully Automated (Recommended)

**Run all 4 scenarios, verify, and generate analysis:**

```bash
# 1. Run tests (takes ~5-10 minutes for all 4)
./scripts/test-logging-levels.sh all

# 2. Verify body capture behavior
docker exec -it them-postgres psql -U them -d them -f scripts/verify-logging-levels.sql

# 3. Generate analysis for each scenario
./scripts/analyze-logging-results.sh
```

**What to expect:**
- Tests will run automatically for all 4 logging levels
- Each test sends 300 requests (100 users × 3 messages)
- Results are shown in terminal and saved to database
- Verification script shows:
  - ✅ Body count (should be ~300 for DEBUG/AUDIT, 0 for MINIMAL/METRICS)
  - ✅ Latency comparison (DEBUG/AUDIT should be 30-60% slower)
  - ✅ Sample body content
- Analysis script generates formatted analysis text for each scenario

---

## Option 2: Manual Testing via UI

**Step-by-step:**

1. **Open test runner UI:**
   ```
   http://localhost:3210/scenarios
   ```

2. **Run each scenario:**
   - Click "Run" on scenario 40 (MINIMAL)
   - Wait for completion (~60 seconds)
   - Note the average latency
   - Repeat for scenarios 41, 42, 43

3. **View results:**
   ```
   http://localhost:3210/results
   ```

4. **Verify in database:**
   ```bash
   docker exec -it them-postgres psql -U them -d them -f scripts/verify-logging-levels.sql
   ```

---

## Option 3: Run Individual Tests

**Test just one logging level:**

```bash
# MINIMAL (baseline performance)
./scripts/test-logging-levels.sh minimal

# DEBUG (should show body capture + higher latency)
./scripts/test-logging-levels.sh debug

# AUDIT (similar to debug)
./scripts/test-logging-levels.sh audit
```

---

## Verification Queries (Manual)

**Check if bodies are captured:**

```sql
-- Should return ~300 for debug/audit, 0 for minimal/metrics
SELECT 
  gp.logging_level,
  COUNT(*) as body_count
FROM them.gateway_request_bodies grb
JOIN them.gateway_requests gr ON grb.request_id = gr.id
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
  AND grb.created_at > now() - interval '1 hour'
GROUP BY gp.logging_level;
```

**Compare performance:**

```sql
-- Should show DEBUG/AUDIT ~30-60% slower than MINIMAL
SELECT 
  gp.logging_level,
  ROUND(AVG(gr.latency_ms)) as avg_latency_ms
FROM them.gateway_requests gr
JOIN them.gateway_clients gc ON gr.client_id = gc.id
JOIN them.governance_policies gp ON gc.governance_policy_id = gp.id
WHERE gc.label LIKE 'perf-test-%'
  AND gr.created_at > now() - interval '1 hour'
  AND gr.status = 'ok'
GROUP BY gp.logging_level
ORDER BY avg_latency_ms;
```

---

## Success Criteria ✅

**Functional:**
- [ ] MINIMAL: Zero bodies in `gateway_request_bodies`
- [ ] METRICS: Zero bodies in `gateway_request_bodies`
- [ ] DEBUG: ~300 bodies with valid JSON
- [ ] AUDIT: ~300 bodies with valid JSON

**Performance:**
- [ ] MINIMAL: Lowest latency (baseline)
- [ ] METRICS: Similar to MINIMAL (±5%)
- [ ] DEBUG: +30-60% latency vs MINIMAL
- [ ] AUDIT: Similar to DEBUG (±5%)

**No Regressions:**
- [ ] All scenarios complete successfully
- [ ] Token counts correct
- [ ] Gateway logs show correct logging_level

---

## Troubleshooting

**Test runner not responding:**
```bash
# Check if backend is running
curl http://localhost:8093/health

# If not, start it:
cd test-runner/backend && go run main.go
```

**No test data in database:**
```bash
# Check gateway logs
docker logs them-go-bridge --tail 50 | grep logging_level

# Verify profiles exist
docker exec -it them-postgres psql -U them -d them -c \
  "SELECT name, logging_level FROM them.governance_policies WHERE name LIKE 'perf-test-%';"
```

**Tests timing out:**
- Check if gateway is running: `docker ps | grep them-go-bridge`
- Check gateway health: `curl http://localhost:8088/health/ready`
- Reduce test load: Edit scenario JSON files, change `n_users` from 100 to 10

---

## Next Steps After Testing

1. **Review Generated Analysis**
   ```bash
   # View analysis for each scenario
   cat /tmp/analysis-40.txt  # MINIMAL
   cat /tmp/analysis-41.txt  # METRICS
   cat /tmp/analysis-42.txt  # DEBUG
   cat /tmp/analysis-43.txt  # AUDIT
   ```

2. **Update Scenario Files** (Choose one method):

   **Method A: Via UI** (Easiest)
   1. Go to http://localhost:3210/scenarios
   2. Click "Edit" on scenario 40
   3. Paste `/tmp/analysis-40.txt` content into "Test Result Analysis" field
   4. Click Save
   5. Repeat for scenarios 41, 42, 43

   **Method B: Via JSON Files**
   ```bash
   cd /opt/docker/them/test-runner/data/scenarios/
   
   # Edit each scenario JSON file
   # Copy content from /tmp/analysis-XX.txt into "test_result_ai_analysis" field
   
   # Example:
   vim 40-logging-perf-minimal.json
   # Update: "test_result_ai_analysis": "<paste analysis here>"
   ```

3. **Commit Updated Scenarios**
   ```bash
   git add test-runner/data/scenarios/40*.json
   git add test-runner/data/scenarios/41*.json
   git add test-runner/data/scenarios/42*.json
   git add test-runner/data/scenarios/43*.json
   git commit -m "test: update logging level scenario analysis with actual results"
   ```

4. **Production Rollout**
   - Keep all apps on MINIMAL by default
   - Use DEBUG only for specific troubleshooting
   - Use AUDIT for compliance-regulated apps

3. **Future Enhancements**
   - Extend AUDIT: Add PII/FileGuard decision logs
   - Extend METRICS: Add per-node AppFlow timing
   - Optimize MINIMAL: Reduce DB columns (15 → 4 fields)

---

## Full Documentation

**Detailed testing plan:**
```
docs/LOGGING_LEVEL_TESTING_PLAN.md
```

**Implementation details:**
```
git show 752a1581  # Conditional logging enforcement
git show 9074c8cf  # Updated docs/CURRENT.md
git show b164caa3  # Testing plan and scripts
```

**Current state:**
```
docs/CURRENT.md
```
