#!/usr/bin/env bash
# test_12_runs_api.sh — smoke test for /api/v1/runs
# No live run needed — just verifies auth, routes, and stats endpoint.
# Usage: bash scripts/tests/test_12_runs_api.sh

set -euo pipefail

CONTAINER="${BRIDGE_CONTAINER:-odin-bridge}"
PORT="${BRIDGE_PORT:-8001}"
BASE="http://localhost:$PORT/api/v1/runs"
PASS=0; FAIL=0

dcurl() { docker exec "$CONTAINER" curl -s "$@" 2>/dev/null; }

check() {
    local desc="$1" result="$2" expected="$3"
    if [ "$result" = "$expected" ]; then
        echo "  [PASS] $desc"; ((PASS++)) || true
    else
        echo "  [FAIL] $desc  (got: '$result', want: '$expected')"; ((FAIL++)) || true
    fi
}

echo "=== test_12_runs_api: Runs API ==="

# 1. No auth → 403 or 401
STATUS=$(dcurl -o /dev/null -w "%{http_code}" "$BASE")
check "GET /runs without auth returns 401/403" \
    "$([ "$STATUS" = "401" ] || [ "$STATUS" = "403" ] && echo auth_required || echo "$STATUS")" "auth_required"

# 2. Bad token → 401
STATUS=$(dcurl -o /dev/null -w "%{http_code}" -H "Authorization: Bearer bad-token" "$BASE")
check "GET /runs with bad JWT returns 401" "$STATUS" "401"

# 3. Stats endpoint exists (auth required)
STATUS=$(dcurl -o /dev/null -w "%{http_code}" "$BASE/stats")
check "GET /runs/stats without auth returns 401/403" \
    "$([ "$STATUS" = "401" ] || [ "$STATUS" = "403" ] && echo auth_required || echo "$STATUS")" "auth_required"

# 4. Non-existent run returns 401 (auth before 404)
STATUS=$(dcurl -o /dev/null -w "%{http_code}" "$BASE/00000000-0000-0000-0000-000000000000")
check "GET /runs/{id} without auth returns 401/403" \
    "$([ "$STATUS" = "401" ] || [ "$STATUS" = "403" ] && echo auth_required || echo "$STATUS")" "auth_required"

# 5. Bridge still healthy
STATUS=$(dcurl -o /dev/null -w "%{http_code}" "http://localhost:$PORT/health/live")
check "Bridge healthy after runs router mount" "$STATUS" "200"

echo ""
echo "Result: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
