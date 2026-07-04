#!/usr/bin/env bash
# test_02_redis.sh — verify Odin Redis is reachable and read/write works
# Usage: bash scripts/tests/test_02_redis.sh

set -euo pipefail

CONTAINER="${REDIS_CONTAINER:-them-redis}"
REDIS_DB="${REDIS_DB:-0}"
PASS=0
FAIL=0

redis_cmd() {
    docker exec "$CONTAINER" redis-cli -n "$REDIS_DB" "$@" 2>/dev/null
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

echo "=== test_02_redis: Redis Connectivity ==="

# 1. Ping
result=$(redis_cmd PING || echo "ERR")
check "Redis PING" "$result" "PONG"

# 2. Read/write/delete a canary key
redis_cmd SET them:test:canary "ok" EX 10 > /dev/null
result=$(redis_cmd GET them:test:canary || echo "ERR")
check "Redis DB $REDIS_DB read/write" "$result" "ok"
redis_cmd DEL them:test:canary > /dev/null

echo ""
echo "Result: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
