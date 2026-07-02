#!/usr/bin/env bash
# test_08_tokens_api.sh — CRUD smoke test for /api/v1/admin/tokens
# Usage: bash scripts/tests/test_08_tokens_api.sh

set -euo pipefail

CONTAINER="${BRIDGE_CONTAINER:-odin-bridge}"
PORT="${BRIDGE_PORT:-8001}"
BASE="http://localhost:$PORT/api/v1/admin/tokens"
PASS=0
FAIL=0

dcurl() { docker exec "$CONTAINER" curl -s "$@" 2>/dev/null; }

check() {
    local desc="$1" result="$2" expected="$3"
    if [ "$result" = "$expected" ]; then
        echo "  [PASS] $desc"; ((PASS++)) || true
    else
        echo "  [FAIL] $desc  (got: '$result', want: '$expected')"; ((FAIL++)) || true
    fi
}

py_field() {
    python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('$1','MISSING'))" 2>/dev/null || echo "MISSING"
}

echo "=== test_08_tokens_api: Access Tokens CRUD ==="

# 1. List
STATUS=$(dcurl -o /dev/null -w "%{http_code}" "$BASE")
check "GET /admin/tokens returns 200" "$STATUS" "200"

# 2. Create (user_id=1, no orchestrator scope)
BODY='{"label":"smoke-test-token","user_id":1}'
RESPONSE=$(dcurl -X POST "$BASE" -H "Content-Type: application/json" -d "$BODY")
TOKEN_ID=$(echo "$RESPONSE" | py_field id)
LABEL=$(echo "$RESPONSE" | py_field label)
TOKEN_VAL=$(echo "$RESPONSE" | py_field token)
ENABLED=$(echo "$RESPONSE" | py_field enabled)

check "POST creates token" "$LABEL" "smoke-test-token"
check "token plaintext returned" "$([ ${#TOKEN_VAL} -gt 20 ] && echo yes || echo no)" "yes"
check "token enabled=True" "$ENABLED" "True"

if [ "$TOKEN_ID" = "MISSING" ]; then
    echo "  [FAIL] no token ID — skipping"; ((FAIL++)) || true
    echo ""; echo "Result: $PASS passed, $FAIL failed"; exit 1
fi

# 3. GET by ID
STATUS=$(dcurl -o /dev/null -w "%{http_code}" "$BASE/$TOKEN_ID")
check "GET /admin/tokens/{id} returns 200" "$STATUS" "200"

# 4. PATCH — disable token
PATCH_RESP=$(dcurl -X PATCH "$BASE/$TOKEN_ID" -H "Content-Type: application/json" -d '{"enabled":false}')
UPDATED=$(echo "$PATCH_RESP" | py_field enabled)
check "PATCH disables token" "$UPDATED" "False"

# 5. PATCH — re-enable
PATCH_RESP=$(dcurl -X PATCH "$BASE/$TOKEN_ID" -H "Content-Type: application/json" -d '{"enabled":true}')
UPDATED=$(echo "$PATCH_RESP" | py_field enabled)
check "PATCH re-enables token" "$UPDATED" "True"

# 6. DELETE
STATUS=$(dcurl -o /dev/null -w "%{http_code}" -X DELETE "$BASE/$TOKEN_ID")
check "DELETE returns 204" "$STATUS" "204"

STATUS=$(dcurl -o /dev/null -w "%{http_code}" "$BASE/$TOKEN_ID")
check "GET deleted token returns 404" "$STATUS" "404"

echo ""
echo "Result: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
