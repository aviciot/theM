# Gateway Per-User Quota Tests — Setup Guide

Tests 48, 49, and 50 verify Phase 3 and Phase 4 quota enforcement for the Gateway.

## Prerequisites

1. **User exists in database** with `viewer` role (role_id = 4)
   - User ID 80 (`dev@rnd.com`) is available in seed data
   
2. **Valid JWT token** for that user

3. **Gateway token** configured (`test-gateway-token-12345`)

## Obtaining a JWT Token

### Option 1: Login via Auth API (Easiest)

```bash
# Login as the viewer user
curl -s -X POST http://localhost:8088/payops_ai/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"dev@rnd.com","password":"admin123"}' | jq -r '.access_token'
```

Copy the returned JWT and add it to the scenario JSON files.

### Option 2: Generate using the script

```bash
cd test-runner/scripts
go run generate-test-jwt.go "<THE_M_JWT_SECRET_value_from_.env>" "80"
```

## Updating Scenario Files

Edit each scenario JSON file (48, 49, 50) and add the `user_jwt` field:

```json
{
  "id": "48-gateway-user-jwt",
  ...
  "gateway_token": "test-gateway-token-12345",
  "user_jwt": "eyJhbGc...paste_jwt_here",
  ...
}
```

## Verifying the Setup

1. **Check user exists:**
   ```bash
   docker exec them-postgres psql -U them -d them \
     -c "SELECT id, username, role_id FROM auth_service.users WHERE id = 80"
   ```

2. **Check gateway token exists:**
   ```bash
   docker exec them-postgres psql -U them -d them \
     -c "SELECT id, tenant_slug FROM them.gateway_clients WHERE token = 'test-gateway-token-12345'"
   ```

3. **Run test 48** (simplest - just tracks user_id):
   - Navigate to http://localhost:3210/scenarios
   - Find "48 — Gateway User JWT Tracking"
   - Click Run
   - Verify: All 3 requests succeed, user_id=80 recorded in gateway_requests table

4. **Run test 49** (rate limiting):
   - Sends 120 requests with 100 rpm limit
   - Expected: ~100 succeed (200), ~20 fail (429)

5. **Run test 50** (cost limiting - ⚠️ EXPENSIVE):
   - Uses real LLM (claude-sonnet-4-6)
   - Costs $5-10 to run
   - Expected: ~5-10 requests succeed before hitting $5 daily limit (402)

## Troubleshooting

### JWT validation fails
- Check that THE_M_JWT_SECRET in .env matches the secret used to generate the JWT
- Verify JWT hasn't expired (check `exp` claim)
- Use Option 1 (login API) to get a fresh token

### user_id not recorded in gateway_requests
- Check logs: `docker logs them-go-bridge --tail 50 | grep "X-User-JWT"`
- Verify OptionalJWTMiddleware is wired in handler.go
- Check JWT claims contain `user_id` or `sub` field

### Rate limit not enforced
- Check Redis: `docker exec them-redis redis-cli KEYS "rl:user:*"`
- Verify RoleConfig for viewer role has RateLimit > 0
- Check logs for quota enforcement messages

### Cost limit not enforced
- Verify requests are actually costing money (check `gateway_requests.cost_usd`)
- Check daily spend query: 
  ```sql
  SELECT SUM(cost_usd) FROM them.gateway_requests 
  WHERE user_id = 80 
  AND created_at >= CURRENT_DATE AT TIME ZONE 'UTC';
  ```
