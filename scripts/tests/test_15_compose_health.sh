#!/usr/bin/env bash
# test_15_compose_health.sh — Phase 7: verify all containers healthy
# Usage: bash scripts/tests/test_15_compose_health.sh

set -euo pipefail
PASS=0; FAIL=0

check() {
    local desc="$1" result="$2" expected="$3"
    if [ "$result" = "$expected" ]; then
        echo "  [PASS] $desc"; ((PASS++)) || true
    else
        echo "  [FAIL] $desc  (got: '$result', want: '$expected')"; ((FAIL++)) || true
    fi
}
checkne() {
    local desc="$1" result="$2"
    if [ -n "$result" ]; then
        echo "  [PASS] $desc"; ((PASS++)) || true
    else
        echo "  [FAIL] $desc  (got empty)"; ((FAIL++)) || true
    fi
}

echo "=== test_15_compose_health: Container Health Check ==="

# ─── Container existence ──────────────────────────────────────────────────────
for cname in odin-postgres odin-redis odin-auth-service odin-bridge; do
    STATUS=$(docker inspect --format='{{.State.Status}}' "$cname" 2>/dev/null || echo "missing")
    check "Container $cname running" "$STATUS" "running"
done

# ─── Docker healthcheck state ─────────────────────────────────────────────────
for cname in odin-postgres odin-redis odin-auth-service odin-bridge; do
    HEALTH=$(docker inspect --format='{{if .State.Health}}{{.State.Health.Status}}{{else}}no-healthcheck{{end}}' "$cname" 2>/dev/null || echo "missing")
    check "$cname healthcheck healthy/no-healthcheck" \
        "$([ "$HEALTH" = "healthy" ] || [ "$HEALTH" = "no-healthcheck" ] && echo ok || echo "$HEALTH")" "ok"
done

# ─── HTTP endpoints ───────────────────────────────────────────────────────────
echo ""
echo "── HTTP health endpoints"

AUTH_STATUS=$(docker exec odin-auth-service curl -s -o /dev/null -w "%{http_code}" http://localhost:8701/health/live 2>/dev/null || echo "err")
check "Auth service /health/live = 200" "$AUTH_STATUS" "200"

BRIDGE_LIVE=$(docker exec odin-bridge curl -s -o /dev/null -w "%{http_code}" http://localhost:8001/health/live 2>/dev/null || echo "err")
check "Bridge /health/live = 200" "$BRIDGE_LIVE" "200"

BRIDGE_READY=$(docker exec odin-bridge curl -s -o /dev/null -w "%{http_code}" http://localhost:8001/health/ready 2>/dev/null || echo "err")
check "Bridge /health/ready = 200" "$BRIDGE_READY" "200"

BRIDGE_HEALTH=$(docker exec odin-bridge curl -s http://localhost:8001/health 2>/dev/null || echo "{}")
BRIDGE_STATUS=$(echo "$BRIDGE_HEALTH" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('status',''))" 2>/dev/null || echo "")
check "Bridge /health returns status=ok" "$BRIDGE_STATUS" "ok"

# ─── Network connectivity ─────────────────────────────────────────────────────
echo ""
echo "── Network connectivity"

PG_PING=$(docker exec odin-bridge python3 -c "
import socket, sys
try:
    s = socket.create_connection(('odin-postgres', 5432), timeout=3)
    s.close(); print('ok')
except Exception as e:
    print('fail')
" 2>/dev/null || echo "fail")
check "Bridge can reach odin-postgres (TCP)" "$PG_PING" "ok"

REDIS_PING=$(docker exec odin-bridge python3 -c "
import socket, sys
try:
    s = socket.create_connection(('odin-redis', 6379), timeout=3)
    s.close(); print('ok')
except Exception as e:
    print('fail')
" 2>/dev/null || echo "fail")
check "Bridge can reach odin-redis (TCP)" "$REDIS_PING" "ok"

AUTH_PING=$(docker exec odin-bridge curl -s -o /dev/null -w "%{http_code}" http://odin-auth-service:8701/health/live 2>/dev/null || echo "err")
check "Bridge can reach odin-auth-service" "$AUTH_PING" "200"

echo ""
echo "Result: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
