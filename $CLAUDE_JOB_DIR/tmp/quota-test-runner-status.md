# Gateway Quota E2E Tests — Implementation Status

## ✅ Completed

### Test Runner JWT Support (Commit d7417ede)

Added `X-User-JWT` header support to the test runner for testing Gateway per-user quota enforcement:

**Changes:**
1. **scenario.Scenario struct** — Added `user_jwt` field (optional, Gateway-only)
2. **RunUserGateway function** — Accept userJWT parameter, set X-User-JWT header when present  
3. **Scenario runner** — Pass sc.UserJWT through to Gateway requests
4. **JWT generation script** — `test-runner/scripts/generate-test-jwt.go` for creating valid HS256 tokens

### Test Scenarios (Commit fdfaca40)

Created 3 E2E test scenarios following test-runner format:

**48 — Gateway User JWT Tracking** (Phase 3)
- Tests that `X-User-JWT` header is parsed and `user_id` extracted
- 3 simple requests with JWT
- Verifies user_id recorded in `gateway_requests` table
- Expected: 100% pass rate (all 200 OK responses)

**49 — Gateway Per-User Rate Limit** (Phase 4)  
- Tests rate limiting with 100 requests/minute limit
- 120 requests from viewer user (role_id=4)
- Expected: ~100 succeed (200), ~20 fail (429 rate limited)
- Uses Redis INCR pattern: `rl:user:{userID}:minute:{timestamp}`

**50 — Gateway Per-User Daily Cost Limit** (Phase 4)
- Tests daily $5.00 cost limit for viewer role
- 10 expensive requests using claude-sonnet-4-6  
- ⚠️ **Uses real LLM** — costs $5-10 to run
- Expected: ~5-10 succeed before hitting 402 (payment required)
- Verifies SUM(cost_usd) query on gateway_requests table

## ⚠️ Setup Required Before Running Tests

The test scenarios need a valid JWT token to work. See instructions:

**📄 `/opt/docker/them/test-runner/data/scenarios/GATEWAY_QUOTA_TESTS.md`**

### Quick Setup (2 steps):

1. **Get a JWT token:**
   ```bash
   curl -s -X POST http://localhost:8088/payops_ai/auth/login \
     -H "Content-Type: application/json" \
     -d '{"username":"dev@rnd.com","password":"admin123"}' | jq -r '.access_token'
   ```

2. **Add JWT to scenario files** (48, 49, 50):
   ```json
   {
     "gateway_token": "test-gateway-token-12345",
     "user_jwt": "eyJhbGc...paste_token_here",
     ...
   }
   ```

## 🚀 How to Run

### Prerequisites
- the-M stack running: `docker compose up -d`
- Test runner running: port 3210
- User ID 80 exists with viewer role (role_id=4) ✓ (in seed data)
- Gateway token `test-gateway-token-12345` exists ✓

### Run Tests

1. Navigate to http://localhost:3210/scenarios
2. Find test 48, 49, or 50
3. Click Run button
4. View results in real-time
5. Check `test_result_ai_analysis` field for findings

### Verification Queries

```sql
-- Check user_id tracking (Test 48)
SELECT id, user_id, model, cost_usd 
FROM them.gateway_requests 
WHERE user_id = 80 
ORDER BY created_at DESC 
LIMIT 10;

-- Check rate limit keys (Test 49)  
-- In Redis: docker exec them-redis redis-cli KEYS "rl:user:80:*"

-- Check daily spend (Test 50)
SELECT SUM(cost_usd) as daily_spend 
FROM them.gateway_requests 
WHERE user_id = 80 
AND created_at >= CURRENT_DATE AT TIME ZONE 'UTC';
```

## 📊 Backend Unit Test Coverage

All quota enforcement logic has unit test coverage:

### Canvas (Execution Lifecycle)
- `go/internal/execution/lifecycle_quota_test.go` — 8 tests
  - Rate limit pass/fail
  - Cost limit pass/fail
  - Fail-open behavior
  - Unlimited role (0 = unlimited)

### Gateway (LLM Gateway Handler)  
- `go/internal/llmgateway/handler_quota_test.go` — 5 tests
  - Rate limit enforced/not exceeded
  - Cost limit enforced/not exceeded  
  - Unlimited role

**Total:** 13 unit tests covering quota enforcement

## 🎯 What's Next

1. **Add JWT to scenarios** — follow GATEWAY_QUOTA_TESTS.md
2. **Rebuild test-runner** — ensure it picks up Scenario struct changes
3. **Run test 48 first** — simplest test, verifies JWT parsing works
4. **Run test 49** — moderate cost, verifies rate limiting
5. **Run test 50 LAST** — expensive ($5-10), verifies cost limiting

## Files Changed

### Test Runner Code
- `test-runner/backend/scenario/scenario.go` — UserJWT field
- `test-runner/backend/scenario/run.go` — Pass JWT to Gateway runner
- `test-runner/backend/them/gateway_runner.go` — Set X-User-JWT header
- `test-runner/scripts/generate-test-jwt.go` — JWT generation helper

### Test Scenarios  
- `test-runner/data/scenarios/48-gateway-user-jwt.json`
- `test-runner/data/scenarios/49-gateway-rate-limit-user.json`
- `test-runner/data/scenarios/50-gateway-cost-limit-user.json`

### Documentation
- `test-runner/data/scenarios/GATEWAY_QUOTA_TESTS.md` — Setup guide
