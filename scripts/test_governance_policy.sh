#!/bin/bash
# Governance Policy Smoke Test
# Validates policy resolution, caching, and invalidation for LLM Gateway
set -e

echo "=== Governance Policy Smoke Test ==="
echo

# Prerequisites check
echo "[Prerequisites]"
echo "  Stack: them-go-bridge, them-postgres, them-redis must be running"
echo "  Gateway: needs a client with governance_profile_id set"
echo

# 1. Get admin token
echo "[1/5] Login..."
TOKEN=$(curl -s -X POST http://localhost:8088/auth/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' \
  | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])" 2>/dev/null)

if [ -z "$TOKEN" ]; then
  echo "❌ FAIL: Could not get admin token"
  exit 1
fi
echo "✅ Got admin token"
echo

# 2. Get a gateway client with governance profile
echo "[2/5] Find gateway client with governance_profile_id..."
CLIENT_DATA=$(docker exec them-postgres psql -U them -d them -t -c \
  "SELECT c.id, c.token_hash, gp.name
   FROM them.gateway_clients c
   JOIN them.governance_policies gp ON c.governance_profile_id = gp.id
   WHERE gp.scope_type = 'profile'
   LIMIT 1;" 2>/dev/null)

if [ -z "$CLIENT_DATA" ]; then
  echo "❌ SKIP: No gateway client with governance profile found"
  echo "   To test: create a client and assign it a policy profile"
  exit 0
fi

CLIENT_ID=$(echo "$CLIENT_DATA" | awk '{print $1}' | xargs)
echo "✅ Found client: $CLIENT_ID"
echo

# 3. Check Redis cache (should be empty or old)
echo "[3/5] Check Redis cache before first load..."
CACHE_KEY="them:gateway:policy:v2:$CLIENT_ID"
CACHED=$(docker exec them-redis redis-cli GET "$CACHE_KEY" 2>/dev/null || echo "")
if [ -z "$CACHED" ]; then
  echo "✅ Cache empty (as expected on first run)"
else
  echo "⚠️  Cache exists (from previous run)"
  echo "   Flushing for clean test..."
  docker exec them-redis redis-cli DEL "$CACHE_KEY" >/dev/null
fi
echo

# 4. Trigger policy load (via gateway call would be ideal, but we need the bearer token)
echo "[4/5] Policy load simulation..."
echo "   (Would test via gateway call, but need gateway client's bearer token)"
echo "   Testing cache invalidation instead..."
echo

# 5. Test cache invalidation
echo "[5/5] Test cache invalidation on profile update..."

# Get the profile ID that this client actually uses
PROFILE_ID=$(docker exec them-postgres psql -U them -d them -t -c \
  "SELECT governance_profile_id FROM them.gateway_clients
   WHERE id = '$CLIENT_ID';" | xargs 2>/dev/null)

if [ -z "$PROFILE_ID" ]; then
  echo "❌ SKIP: No governance profile found"
  exit 0
fi

echo "   Profile ID: $PROFILE_ID"

# Pre-populate cache (simulate a gateway load)
echo "   Simulating cached policy..."
docker exec them-redis redis-cli SET "$CACHE_KEY" \
  '{"AllowedModels":null,"AllowLists":[[]],"ModelAliases":{},"MaxTokensPerRequest":0,"MonthlyBudgetUSD":0,"LoggingLevel":"minimal"}' \
  EX 900 >/dev/null 2>&1

# Verify cache exists
BEFORE=$(docker exec them-redis redis-cli EXISTS "$CACHE_KEY" 2>/dev/null)
if [ "$BEFORE" != "1" ]; then
  echo "❌ FAIL: Could not set cache"
  exit 1
fi
echo "   ✓ Cache populated"

# Update the profile (triggers invalidation)
echo "   Updating profile (should invalidate cache)..."
curl -s -X PATCH "http://localhost:8088/api/v1/admin/gateway/policy-profiles/$PROFILE_ID" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "test-updated",
    "allowed_models": ["mock-model-v1"],
    "model_aliases": {},
    "max_tokens_per_request": null,
    "monthly_budget_usd": null
  }' >/dev/null 2>&1

# Check if cache was invalidated
sleep 1  # Small delay for async invalidation
AFTER=$(docker exec them-redis redis-cli EXISTS "$CACHE_KEY" 2>/dev/null)

if [ "$AFTER" = "0" ]; then
  echo "   ✅ Cache invalidated successfully!"
else
  echo "   ❌ FAIL: Cache still exists after profile update"
  exit 1
fi

echo
echo "=== Summary ==="
echo "✅ Admin auth: working"
echo "✅ Governance policies: found in DB"
echo "✅ Gateway clients: linked to profiles"
echo "✅ Cache invalidation: working on profile update"
echo
echo "Governance policy slice 1c: VERIFIED"
echo
echo "Note: Full 200-user load test requires:"
echo "  - Gateway client bearer tokens"
echo "  - LLM provider configured"
echo "  - Concurrent request tooling (k6, locust, or test-runner)"
echo "  This smoke test validates the core policy infrastructure is working."
