#!/usr/bin/env bash
# test_01_db.sh — verify odin DB is up and schema tables exist
# Usage: bash scripts/tests/test_01_db.sh
# Safe to run against a live container at any time.

set -euo pipefail

CONTAINER="${POSTGRES_CONTAINER:-them-postgres}"
DB="${POSTGRES_DB:-them}"
USER="${POSTGRES_USER:-them}"

PASS=0
FAIL=0

run_sql() {
    docker exec "$CONTAINER" psql -U "$USER" -d "$DB" -tAc "$1" 2>/dev/null
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

echo "=== test_01_db: Database & Schema ==="

# 1. Basic connectivity
result=$(run_sql "SELECT 1" || echo "ERR")
check "DB connectivity" "$result" "1"

# 2. them schema exists
result=$(run_sql "SELECT count(*) FROM information_schema.schemata WHERE schema_name='them'" || echo "ERR")
check "them schema exists" "$result" "1"

# 3. Required tables
for tbl in llm_providers config agents orchestrators access_tokens runs run_steps run_usage audit_logs; do
    result=$(run_sql "SELECT count(*) FROM information_schema.tables WHERE table_schema='them' AND table_name='$tbl'" || echo "ERR")
    check "table them.$tbl exists" "$result" "1"
done

echo ""
echo "Result: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] && exit 0 || exit 1
