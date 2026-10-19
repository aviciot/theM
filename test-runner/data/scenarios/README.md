# Test Runner Scenarios — Organization Guide

## Clean Scenario List (Use These!)

### Functional Tests (01-09)
| ID | Name | Purpose | Users | Path |
|----|------|---------|-------|------|
| **01** | Gateway Basic (Mock) | Basic gateway routing + auth | 1 | Direct |
| **02** | Gateway Streaming | SSE streaming responses | 1 | Direct |
| **03** | PII Guardrail (Non-Stream) | PII detection/redaction | 1 | Direct |
| **04** | PII Guardrail (Stream) | PII with streaming | 1 | Direct |

### Performance Tests — Direct Path (10-19)
*No app assigned → bypasses Temporal → uses mock LLM directly*

| ID | Name | Users | Requests | Expected Latency | Status |
|----|------|-------|----------|-----------------|--------|
| **10** | 50 Users (Direct) | 50 | 150 | ~300-500ms | ✅ Baseline |
| **11** | 200 Users (Direct) | 200 | 600 | ~1.3s | ✅ Original failure case (now passes) |
| **12** | 400 Users (Direct) | 400 | 1200 | ~3-4s | ✅ Stress test |

### Performance Tests — WITH Temporal (20-29)
*App assigned → triggers AppFlow workflows → uses Temporal workers*

**Worker Capacity:** 4 workers × 25 concurrent activities = **100 slots**

| ID | Name | Users | Workflows | Expected Result |
|----|------|-------|-----------|----------------|
| **20** | 50 Users (Temporal) | 50 | 150 | ✅ Under capacity (5-6s latency) |
| **21** | 100 Users (Temporal) | 100 | 300 | ⚠️ EXCEEDS 100 slots! Expected queueing |

### Application Tests (30-39)
| ID | Name | Purpose | Users | Type |
|----|------|---------|-------|------|
| **30** | A2A Debator App | Tests A2A entry point + multi-agent workflow | 6 | A2A |

---

## NEW Fields Explained

### 1️⃣ `description` (Human-Friendly)
**What it is:** Plain English explanation of what the test covers.

**Example:**
```json
"description": "Performance test with 50 concurrent users, 3 messages each (150 total requests). Uses 'mock' profile with NO app assigned — requests bypass Temporal and go direct to mock LLM. Tests gateway concurrency handling without worker overhead. Expected latency: ~300-500ms."
```

**Shows:**
- What is being tested
- Which profile/policy is used
- Why this test matters
- Expected behavior

---

### 2️⃣ `test_result_ai_analysis` (LLM-Generated)
**What it is:** AI-generated analysis after test completes.

**Should include:**
- ✅ **Result:** Pass/Fail with % success rate
- 📊 **Performance:** Latency, throughput
- 🎯 **Grade:** A+ to F
- 🔍 **Findings:** Bottlenecks, errors, surprises
- ⚙️ **Config Notes:** Worker slots used, concurrency limits hit
- 💡 **Recommendations:** Next test, tuning suggestions

**Example:**
```json
"test_result_ai_analysis": "✅ Grade: A+ | Result: 50/50 users passed (100%) | Latency: 5.2s avg | Workers: 50/100 slots used (50% capacity) | Findings: Temporal overhead adds ~5s vs direct path (300ms). No failures, no queueing. System handled load comfortably. Recommendation: Run Test 21 (100 users) to test worker saturation at 100-slot limit."
```

---

## How to Use

### Running Tests
1. Go to: http://10.55.125.43:3210/scenarios
2. Find test by **number prefix** (e.g., "10 —", "20 —")
3. Click **Run**
4. View results in UI

### After Test Completes
**Update `test_result_ai_analysis`** with findings:
- Pass/fail rate
- Latency breakdown
- Worker utilization (for Temporal tests)
- Grade (A-F)
- What was learned
- What to test next

---

## Old Files (Can Be Deleted)
These are duplicates or superseded:
- `test-policy-mock.json` → replaced by `01-gateway-basic-mock.json`
- `perf-test-1b-25users.json` → unnecessary (10/11/12 cover range)
- `perf-test-1c-50users.json` → replaced by `10-perf-50-users-direct.json`
- `perf-test-1d-100users.json` → unnecessary
- `perf-test-1e-200users.json` → replaced by `11-perf-200-users-direct.json`
- `perf-test-2a-stress-test.json` → replaced by `12-perf-400-users-direct.json`
- `perf-test-2b-single-message.json` → not needed yet
- `perf-test-2c-long-conversation.json` → not needed yet
- `perf-test-3a-temporal-50users.json` → replaced by `20-perf-50-users-temporal.json`
- `perf-test-3b-temporal-100users.json` → replaced by `21-perf-100-users-temporal.json`
- `payops-debator-basic.json` → replaced by `30-a2a-debator-app.json`
- `gw-profile-*` → replaced by functional tests 01-04
- `7835db70-*.json` → orphaned file

---

## Naming Convention

**Format:** `<two-digit-prefix>-<descriptive-slug>.json`

**Prefix Ranges:**
- **01-09:** Functional tests (features, guardrails, basic flows)
- **10-19:** Performance tests (direct path, no Temporal)
- **20-29:** Performance tests (WITH Temporal workflows)
- **30-39:** Application-specific tests (A2A, specific apps)
- **40-49:** Reserved for stress/chaos tests
- **50-59:** Reserved for integration tests

---

## Test Results Tracking

**Before running test:** Read `description` to understand what it tests.

**After test completes:** Update `test_result_ai_analysis` with:
1. Grade (A+ to F)
2. Pass/fail stats
3. Performance metrics
4. Findings
5. Next recommended test

This creates a **living test history** right in the scenario files!

---

_Last updated: 2026-10-19_
_Total scenarios: 10 (clean set)_
