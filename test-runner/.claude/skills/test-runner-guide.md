---
name: test-runner-guide
description: Complete guide for working with the test-runner - creating tests, managing tokens, running scenarios, analyzing results
tags: [test-runner, testing, scenarios, gateway, performance]
---

# Test Runner Skill — Complete Guide

This skill teaches you how to work with the test-runner system for testing the-M gateway and applications.

## Table of Contents
1. [Understanding the Test Runner](#understanding)
2. [Creating New Test Scenarios](#creating-scenarios)
3. [Managing Gateway Tokens](#managing-tokens)
4. [Running Tests](#running-tests)
5. [Analyzing and Updating Results](#analyzing-results)
6. [Common Test Patterns](#common-patterns)

---

## Understanding the Test Runner {#understanding}

### Architecture
- **Frontend**: Next.js UI at http://localhost:3210 (port 3210 → 3000 internal)
- **Backend**: Go service at http://localhost:8093 (port 8093 → 8090 internal)
- **Scenarios**: JSON files in `test-runner/data/scenarios/`
- **Results**: Stored in database + JSON files in `test-results/`

### Test Types
1. **Gateway Tests** (01-19): Test OpenAI-compatible gateway endpoint
   - Direct path: NO app assigned → bypasses Temporal
   - Temporal path: App assigned → triggers AppFlow workflows
2. **Performance Tests** (10-19): Load testing with many concurrent users
3. **Application Tests** (30+): Test specific apps (A2A, WebSocket, SSE)

### File Structure
```
test-runner/
├── backend/              # Go backend service
│   ├── scenario/        # Scenario loading/management
│   └── handler/         # Test execution handlers
├── frontend/            # Next.js UI
│   ├── app/scenarios/   # Scenario list & edit pages
│   └── app/run/         # Test run results page
├── data/
│   └── scenarios/       # JSON scenario files (the source of truth)
└── .claude/skills/      # This skill file
```

---

## Creating New Test Scenarios {#creating-scenarios}

### Method 1: Via UI (Recommended)

1. **Navigate to UI**: http://localhost:3210/scenarios
2. **Click "New Scenario"**
3. **Choose Type**:
   - **Gateway Profile**: Tests OpenAI-compatible gateway
   - **Agentic App**: Tests app with WebSocket/SSE/A2A entry points

4. **Fill Required Fields**:

   **For Gateway Tests:**
   ```
   Name: 05 — Gateway Rate Limit Test
   Description: Tests rate limiting with 'strict' profile...
   
   🔧 Gateway Config:
   • Token: gw-ratelimit-token-xyz
   • NO app assigned → Direct path
   • Governance Profile: 'strict'
     - Rate limit: 10 req/min
     - Logging: metrics
   
   Scenario Type: Gateway Profile
   Tenant: payops_ai
   Gateway Token: <token-here>
   Model: fast (or claude-sonnet-4-6)
   Streaming: No
   Users: 5
   Messages:
     - "Test message 1"
     - "Test message 2"
   ```

   **For App Tests:**
   ```
   Name: 31 — Vision Agent Test
   Description: Tests vision agent with image input...
   
   Scenario Type: Agentic App
   Tenant: payops_ai
   App: vision-agent
   Entry Point: a2a (or websocket/sse)
   Auth Mode: token
   Users: 2
   Messages:
     - "Analyze this image"
   ```

5. **Save** or **Save & Run**

### Method 2: Create JSON File Directly

Create file in `test-runner/data/scenarios/`:

```json
{
  "id": "05-gateway-ratelimit",
  "name": "05 — Gateway Rate Limit Test",
  "description": "Tests rate limiting...\n\n🔧 Gateway Config:\n• Profile: 'strict'\n• Rate limit: 10 req/min",
  "test_result_ai_analysis": "",
  "tenant_slug": "payops_ai",
  "ep_type": "gateway",
  "gateway_token": "gw-ratelimit-token-xyz",
  "stream": false,
  "model": "fast",
  "n_users": 5,
  "messages": ["Test 1", "Test 2"],
  "app_id": "",
  "app_slug": "",
  "ep_slug": "gateway",
  "auth_mode": "token",
  "created_at": "2026-10-19T00:00:00Z",
  "updated_at": "2026-10-19T00:00:00Z"
}
```

Then restart backend:
```bash
docker restart them-test-runner-backend
```

### Naming Convention

Use numbered prefixes:
- `01-09`: Functional tests (basic features, guardrails)
- `10-19`: Performance tests - direct path
- `20-29`: Performance tests - WITH Temporal
- `30-39`: Application tests (A2A, specific apps)
- `40-49`: Stress/chaos tests
- `50-59`: Integration tests

---

## Managing Gateway Tokens {#managing-tokens}

### Understanding Tokens

Gateway clients use **token authentication**:
1. Test runner sends **raw token** (hex string)
2. Gateway hashes it with SHA256
3. Looks up client by **token_hash** in `them.gateway_clients`

### Creating a New Gateway Token

**Step 1: Generate Raw Token**
```bash
# Generate random 64-char hex token
openssl rand -hex 32
# Example output: a1b2c3d4e5f6...
```

**Step 2: Calculate SHA256 Hash**
```bash
echo -n "a1b2c3d4e5f6..." | sha256sum
# Example output: 799846fa... (use this as token_hash)
```

**Step 3: Insert into Database**
```sql
INSERT INTO them.gateway_clients (
  id,
  tenant_id,
  label,
  token_hash,
  governance_profile_id,
  config_status,
  created_at
) VALUES (
  gen_random_uuid(),
  (SELECT id FROM them.applications WHERE slug = 'payops_ai'),
  'test-ratelimit-client',
  '799846fa...',  -- SHA256 hash from step 2
  (SELECT id FROM them.governance_policies WHERE name = 'strict'),
  'valid',
  NOW()
);
```

**Step 4: Use Raw Token in Scenario**
```json
{
  "gateway_token": "a1b2c3d4e5f6..."  // Raw token, NOT hash!
}
```

### Test Tokens Already Created

| Token (Raw) | Purpose | Profile | App |
|-------------|---------|---------|-----|
| `test-gateway-token-12345` | Basic tests | mock | None (direct) |
| `test-temporal-workflow-token-99` | Temporal tests | mock | payops-response-sanitizer |
| `gw-pii-test-token-67890` | PII tests (⚠️ NOT created) | — | — |

### Checking Token Status

```bash
# Check if token exists
docker exec them-postgres psql -U them -d them -c "
SELECT 
  id,
  label,
  token_hash,
  config_status,
  governance_profile_id
FROM them.gateway_clients
WHERE token_hash = '<hash-here>';
"
```

---

## Running Tests {#running-tests}

### Via UI (Easiest)

1. Go to http://localhost:3210/scenarios
2. Find your scenario
3. Click **▶ Run**
4. View live results at http://localhost:3210/run/{run_id}

### Via Script

```bash
cd /opt/docker/them
./scripts/run-perf-test.sh <test-id> <users>

# Examples:
./scripts/run-perf-test.sh 1a 10   # Run test 1a with 10 users
./scripts/run-perf-test.sh 3a 50   # Run test 3a with 50 users
```

### Checking Container Health

```bash
# Check test runner containers
docker ps | grep test-runner

# Backend logs
docker logs them-test-runner-backend --tail 50

# Frontend logs
docker logs them-test-runner-frontend --tail 50
```

### Common Issues

**Issue: API Error / Empty Scenarios**
```bash
# Check backend network
docker exec them-test-runner-frontend wget -qO- http://tr-backend:8090/scenarios

# If fails, reconnect network
docker network connect --alias tr-backend test-runner_default them-test-runner-backend
docker restart them-test-runner-frontend
```

**Issue: Database Error**
```bash
# Check DB password
docker exec them-postgres env | grep POSTGRES_PASSWORD

# If backend has wrong password, recreate:
docker stop them-test-runner-backend && docker rm them-test-runner-backend

docker run -d \
  --name them-test-runner-backend \
  --network them-network \
  -p 8093:8090 \
  -v /opt/docker/them/test-runner/data:/data \
  -e THEM_URL=http://them-traefik:8088 \
  -e DB_URL='postgres://them:<correct-password>@them-postgres:5432/them?sslmode=disable' \
  -e ADMIN_USER=admin \
  -e ADMIN_PASS=admin123 \
  test-runner-tr-backend

docker network connect --alias tr-backend test-runner_default them-test-runner-backend
```

---

## Analyzing and Updating Results {#analyzing-results}

### After Running a Test

1. **View Results in UI**: The run page shows pass/fail, latency, errors
2. **Edit Scenario**: Click Edit on the scenario
3. **Update "Test Result Analysis" Field**

### Analysis Format

Use this template:

```
✅ Grade: A+ (or A, B, C, F)

📊 Result: 50/50 users passed (100%)
⏱️ Latency: 320ms average
🔧 Config: Mock provider, 'mock' governance profile
👷 Workers: 50/100 slots used (for Temporal tests)

🔍 Findings:
• What worked well
• What failed or was slow
• Any errors or warnings
• Resource utilization (DB, Redis, workers)

💡 Recommendation: What to test next or how to improve
```

### Example Analysis (Direct Path Test)

```
✅ Grade: A+

📊 Result: 50/50 users passed (100%)
⏱️ Latency: 320ms average
🔧 Path: Direct (no Temporal)
👷 Workers: 0 used (direct mock provider)

🔍 Findings:
• Linear scaling: ~6ms added latency per concurrent user
• No failures, no timeouts
• Database connections stable (68-70)
• Redis ops minimal (18-24 ops/sec)
• Gateway handled 50 concurrent users comfortably

💡 Recommendation: Test scales well. Run Test 11 (200 users) to test higher load.
```

### Example Analysis (Temporal Path Test)

```
✅ Grade: A

📊 Result: 50/50 users passed (100%)
⏱️ Latency: 5.2s average (vs 320ms direct path)
🔧 Path: WITH Temporal — triggers AppFlow workflows
👷 Workers: 50/100 slots used (50% capacity)
⚙️ Config: 4 workers × 25 concurrent activities = 100 total slots

🔍 Findings:
• Temporal workflows executed successfully (150 workflows total)
• Workflow overhead: ~5s added latency vs direct path
• No queueing, no failures
• Workers handled load comfortably at 50% capacity
• Logs confirmed workflow execution

💡 Recommendation: Test 21 (100 users) will EXCEED 100 worker slots. Expected: queueing or failures.
```

### Checking Logs for Details

```bash
# Gateway logs (request/response)
docker logs them-go-bridge --tail 100 | grep "gateway\|appflow"

# Temporal worker logs
docker logs them-dag-worker --tail 50

# Database queries
docker logs them-postgres --tail 20
```

---

## Common Test Patterns {#common-patterns}

### Pattern 1: Testing Direct Path (No Temporal)

**Purpose**: Baseline performance, no workflow overhead

**Setup**:
- Gateway token WITHOUT app assigned
- Any governance profile
- Set `app_id: ""` in scenario

**Expected**:
- Fast latency (200-500ms)
- No worker usage
- Good for testing gateway itself

### Pattern 2: Testing Temporal Path (WITH Workflows)

**Purpose**: Test AppFlow orchestration, worker capacity

**Setup**:
- Create gateway client WITH app_id:
  ```sql
  UPDATE them.gateway_clients
  SET app_id = '<app-uuid>'
  WHERE token_hash = '<token-hash>';
  ```
- Scenario will trigger workflows

**Expected**:
- Slower latency (5-10s due to workflow overhead)
- Worker slots consumed
- Tests real production path

### Pattern 3: Testing Governance Policies

**Purpose**: Verify rate limits, PII guardrails, logging

**Setup**:
- Create governance profile with settings:
  ```sql
  INSERT INTO them.governance_policies (...)
  VALUES (
    ...,
    '{"rate_limits": {"requests_per_minute": 10}, "pii": {"enabled": true}, "logging": {"level": "audit"}}'
  );
  ```
- Assign to gateway client
- Run test that triggers policy

**Expected**:
- Rate limit: 429 errors after limit
- PII: Requests blocked or redacted
- Logging: Captured in DB/ClickHouse

### Pattern 4: Performance Testing

**Purpose**: Find saturation point, capacity limits

**Setup**:
- Start small (10 users) and increase
- Use same scenario, vary `n_users`
- Monitor latency progression

**Pattern**:
- 10 users: Baseline
- 50 users: Light load
- 100 users: Medium load
- 200 users: Heavy load
- 400 users: Stress test

**Watch for**:
- Linear vs exponential latency growth
- Worker queue saturation (Temporal)
- Database connection exhaustion
- Redis bottlenecks

---

## Quick Reference Commands

```bash
# View scenarios
ls /opt/docker/them/test-runner/data/scenarios/

# Restart backend (reload scenarios)
docker restart them-test-runner-backend

# Restart frontend (reload UI changes)
docker restart them-test-runner-frontend

# Check backend is serving scenarios
docker exec them-test-runner-backend wget -qO- http://localhost:8090/scenarios | python3 -c "import sys,json; print(len(json.load(sys.stdin)), 'scenarios')"

# Run database query
docker exec them-postgres psql -U them -d them -c "SELECT * FROM them.gateway_clients;"

# Check test results
ls /opt/docker/them/test-results/

# View specific test run
docker exec them-postgres psql -U them -d them -c "SELECT * FROM test_runs WHERE id = '<run-id>';"
```

---

## Example: Creating a Complete New Test

**Scenario**: Test rate limiting with 15 requests (should hit 10/min limit)

**Step 1: Create Governance Profile**
```sql
INSERT INTO them.governance_policies (id, name, tenant_id, doc)
VALUES (
  gen_random_uuid(),
  'ratelimit-strict',
  (SELECT id FROM them.applications WHERE slug = 'payops_ai'),
  '{"rate_limits": {"requests_per_minute": 10}, "logging": {"level": "metrics"}}'::jsonb
);
```

**Step 2: Create Gateway Client**
```bash
# Generate token
TOKEN=$(openssl rand -hex 32)
HASH=$(echo -n "$TOKEN" | sha256sum | awk '{print $1}')

# Insert client
docker exec them-postgres psql -U them -d them -c "
INSERT INTO them.gateway_clients (id, tenant_id, label, token_hash, governance_profile_id, config_status)
VALUES (
  gen_random_uuid(),
  (SELECT id FROM them.applications WHERE slug = 'payops_ai'),
  'ratelimit-test-client',
  '$HASH',
  (SELECT id FROM them.governance_policies WHERE name = 'ratelimit-strict'),
  'valid'
);
"

echo "Raw token (use in scenario): $TOKEN"
```

**Step 3: Create Scenario**
```json
{
  "id": "05-gateway-ratelimit",
  "name": "05 — Gateway Rate Limit (10/min)",
  "description": "Tests rate limiting...\n\n🔧 Config:\n• Profile: ratelimit-strict\n• Limit: 10 req/min\n• Logging: metrics",
  "test_result_ai_analysis": "",
  "tenant_slug": "payops_ai",
  "ep_type": "gateway",
  "gateway_token": "<TOKEN-from-step-2>",
  "stream": false,
  "model": "fast",
  "n_users": 1,
  "messages": ["1","2","3","4","5","6","7","8","9","10","11","12","13","14","15"],
  "app_id": "",
  "app_slug": "",
  "ep_slug": "gateway",
  "auth_mode": "token",
  "created_at": "2026-10-19T00:00:00Z",
  "updated_at": "2026-10-19T00:00:00Z"
}
```

**Step 4: Load and Run**
```bash
# Restart backend to load scenario
docker restart them-test-runner-backend

# Run via UI or script
# Expected: First 10 succeed, next 5 get 429 errors
```

**Step 5: Update Analysis**
After running, edit scenario and add:
```
✅ Grade: A

📊 Result: 10/15 requests passed (66.7%)
⏱️ Latency: 250ms average (successful requests)
🔧 Config: ratelimit-strict profile, 10 req/min limit

🔍 Findings:
• First 10 requests succeeded
• Requests 11-15 failed with 429 (rate limit exceeded)
• Rate limiting working correctly
• Logging captured metrics

💡 Recommendation: Rate limiting verified. Test burst patterns or multi-user scenarios.
```

---

## When to Invoke This Skill

Use `/test-runner-guide` or reference this skill when:
- Creating new test scenarios
- Setting up gateway tokens
- Running performance tests
- Analyzing test results
- Debugging test runner issues
- Understanding governance policies in tests
- Planning test coverage

---

## See Also

- `docs/reference/schema.md` — Database schema for gateway_clients, governance_policies
- `docs/reference/redis.md` — Redis keys for rate limiting
- `test-runner/data/scenarios/README.md` — Scenario organization guide
- `test-results/EXTENDED_TEST_RESULTS.md` — Performance test findings
