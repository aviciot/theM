#!/usr/bin/env bash
# test_05_agents_api.sh — CRUD smoke test for /api/v1/admin/agents
# Runs curl inside them-bridge container (no host ports required).
# Usage: bash scripts/tests/test_05_agents_api.sh

set -euo pipefail

CONTAINER="${BRIDGE_CONTAINER:-them-bridge}"
PORT="${BRIDGE_PORT:-8001}"
BASE="http://localhost:$PORT/api/v1/admin/agents"
PASS=0
FAIL=0

dcurl() {
    docker exec "$CONTAINER" curl -s "$@" 2>/dev/null
}

check() {
    local desc="$1" result="$2" expected="$3"
    if [ "$result" = "$expected" ]; then
        echo "  [PASS] $desc"
        ((PASS++)) || true
    else
        echo "  [FAIL] $desc  (got: '$result', want: '$expected')"
        ((FAIL++)) || true
    fi
}

py_field() {
    python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "MISSING"
}

echo "=== test_05_agents_api: Agents CRUD ==="

# 1. List agents
STATUS=$(dcurl -o /dev/null -w "%{http_code}" "$BASE")
check "GET /admin/agents returns 200" "$STATUS" "200"

# 2. Create agent
BODY='{"slug":"test_smoke_agent","display_name":"Smoke Test Agent","description":"Temp agent created by test_05","transport":"a2a_async","endpoint_url":"http://localhost:9999/","auth_token":"test-token-abc123","timeout_seconds":60,"max_concurrency":2,"tags":["test","smoke"]}'

RESPONSE=$(dcurl -X POST "$BASE" -H "Content-Type: application/json" -d "$BODY")
AGENT_ID=$(echo "$RESPONSE" | py_field id)
SLUG=$(echo "$RESPONSE" | py_field slug)
TOKEN_SET=$(echo "$RESPONSE" | py_field auth_token_set)

check "POST creates agent" "$SLUG" "test_smoke_agent"
check "auth_token_set=True (stored encrypted)" "$TOKEN_SET" "True"

if [ "$AGENT_ID" = "MISSING" ]; then
    echo "  [FAIL] Could not get agent ID — skipping remaining tests"
    ((FAIL++)) || true
    echo ""; echo "Result: $PASS passed, $FAIL failed"; exit 1
fi

# 3. GET by ID
STATUS=$(dcurl -o /dev/null -w "%{http_code}" "$BASE/$AGENT_ID")
check "GET /admin/agents/{id} returns 200" "$STATUS" "200"

# 4. PATCH
PATCH_RESP=$(dcurl -X PATCH "$BASE/$AGENT_ID" -H "Content-Type: application/json" -d '{"display_name":"Smoke Test Agent (updated)"}')
UPDATED_NAME=$(echo "$PATCH_RESP" | py_field display_name)
check "PATCH updates display_name" "$UPDATED_NAME" "Smoke Test Agent (updated)"

# 5. Conflict
STATUS=$(dcurl -o /dev/null -w "%{http_code}" -X POST "$BASE" -H "Content-Type: application/json" -d "$BODY")
check "POST duplicate slug returns 409" "$STATUS" "409"

# 6. Invalid transport
STATUS=$(dcurl -o /dev/null -w "%{http_code}" -X POST "$BASE" -H "Content-Type: application/json" \
    -d '{"slug":"x","display_name":"x","description":"x","transport":"invalid","endpoint_url":"ws://x"}')
check "POST invalid transport returns 422" "$STATUS" "422"

# 7. DELETE
STATUS=$(dcurl -o /dev/null -w "%{http_code}" -X DELETE "$BASE/$AGENT_ID")
check "DELETE returns 204" "$STATUS" "204"

# 8. Verify deleted
STATUS=$(dcurl -o /dev/null -w "%{http_code}" "$BASE/$AGENT_ID")
check "GET deleted agent returns 404" "$STATUS" "404"

echo ""
echo "Result: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
