#!/bin/bash
# Gateway Performance Test Runner
# Usage: ./scripts/perf-test.sh <test_id> <n_users>
# Example: ./scripts/perf-test.sh 1a 10

set -e

TEST_ID="${1:-1a}"
N_USERS="${2:-10}"
SCENARIO_FILE="/opt/docker/them/test-runner/data/scenarios/test-policy-mock.json"
RESULTS_DIR="/opt/docker/them/test-results"

mkdir -p "$RESULTS_DIR"

echo "=== Gateway Performance Test ==="
echo "Test ID: $TEST_ID"
echo "Users: $N_USERS"
echo "Timestamp: $(date -Iseconds)"
echo

# Update scenario file
echo "[1/5] Updating scenario: n_users=$N_USERS"
sed -i "s/\"n_users\": [0-9]\\+/\"n_users\": $N_USERS/" "$SCENARIO_FILE"

# Check DB connections before test
echo "[2/5] Checking DB connection baseline..."
DB_BEFORE=$(docker exec them-postgres psql -U them -d them -t -c "SELECT count(*) FROM pg_stat_activity;" | xargs)
echo "   Active DB connections: $DB_BEFORE"

# Check Redis ops before test
echo "[3/5] Checking Redis baseline..."
docker exec them-redis redis-cli INFO stats | grep "instantaneous_ops_per_sec" || true

echo "[4/5] Starting test..."
echo "   Go to http://10.55.125.43:3210/scenarios"
echo "   Click 'Test-Policy-Mock'"
echo "   Click 'Run'"
echo
echo "   Waiting for test to complete..."
echo "   (This will take ~$(( N_USERS * 3 / 10 )) seconds for $N_USERS users)"
echo

read -p "Press ENTER when test is complete and you have the run_id: " RUN_ID

if [ -z "$RUN_ID" ]; then
  echo "❌ No run_id provided. Exiting."
  exit 1
fi

echo "[5/5] Collecting results..."

# Get results from test runner logs
RESULT_JSON=$(docker logs them-test-runner-backend 2>&1 | grep "\"run_id\":\"$RUN_ID\"" | grep "run: complete" | tail -1)

if [ -z "$RESULT_JSON" ]; then
  echo "❌ Could not find results for run_id: $RUN_ID"
  exit 1
fi

# Parse results
PASSED=$(echo "$RESULT_JSON" | grep -oP '"passed":\K[0-9]+' || echo "0")
FAILED=$(echo "$RESULT_JSON" | grep -oP '"failed":\K[0-9]+' || echo "0")

# Check DB connections after test
DB_AFTER=$(docker exec them-postgres psql -U them -d them -t -c "SELECT count(*) FROM pg_stat_activity;" | xargs)

# Get sample latency from logs
LATENCY_SAMPLE=$(docker logs them-test-runner-backend 2>&1 | grep "\"user\":[0-9]\\+" | grep "latency_ms" | tail -20 | grep -oP '"latency_ms":\K[0-9]+' | awk '{sum+=$1; count++} END {print int(sum/count)}')

echo
echo "=== Test Results ==="
echo "Test ID: $TEST_ID"
echo "Run ID: $RUN_ID"
echo "Users: $N_USERS"
echo "Passed: $PASSED"
echo "Failed: $FAILED"
echo "Success Rate: $(awk "BEGIN {printf \"%.1f\", ($PASSED/($PASSED+$FAILED))*100}")%"
echo "Avg Latency (sample): ${LATENCY_SAMPLE:-N/A} ms"
echo "DB Connections Before: $DB_BEFORE"
echo "DB Connections After: $DB_AFTER"
echo

# Save results
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
RESULT_FILE="$RESULTS_DIR/test-${TEST_ID}-${TIMESTAMP}.json"

cat > "$RESULT_FILE" <<EOF
{
  "test_id": "$TEST_ID",
  "run_id": "$RUN_ID",
  "timestamp": "$(date -Iseconds)",
  "config": {
    "users": $N_USERS,
    "workers": 4,
    "worker_concurrency": 25,
    "total_slots": 100
  },
  "results": {
    "total_users": $N_USERS,
    "passed": $PASSED,
    "failed": $FAILED,
    "success_rate": $(awk "BEGIN {printf \"%.1f\", ($PASSED/($PASSED+$FAILED))*100}"),
    "avg_latency_ms": ${LATENCY_SAMPLE:-0},
    "db_connections_before": $DB_BEFORE,
    "db_connections_after": $DB_AFTER
  }
}
EOF

echo "Results saved to: $RESULT_FILE"
echo
echo "=== Next Steps ==="
if [ "$FAILED" -eq 0 ]; then
  echo "✅ All users passed! Try next test with more users."
else
  echo "⚠️  Some failures detected. Check logs:"
  echo "   docker logs them-test-runner-backend --since 5m | grep error"
  echo "   docker logs them-go-bridge --since 5m | grep -E '502|error'"
fi
