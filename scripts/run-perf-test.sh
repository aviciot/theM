#!/bin/bash
# Automated Gateway Performance Test — API-driven
# Usage: ./scripts/run-perf-test.sh <test_id> <n_users>
# Example: ./scripts/run-perf-test.sh 1a 10

set -e

TEST_ID="${1:-1a}"
N_USERS="${2:-10}"
TEST_RUNNER_BACKEND="localhost:8093"
RESULTS_DIR="/opt/docker/them/test-results"

mkdir -p "$RESULTS_DIR"

echo "=== Gateway Performance Test (Automated) ==="
echo "Test ID: $TEST_ID"
echo "Users: $N_USERS"
echo "Timestamp: $(date -Iseconds)"
echo

# Check DB connections before test
echo "[1/6] Checking DB connection baseline..."
DB_BEFORE=$(docker exec them-postgres psql -U them -d them -t -c "SELECT count(*) FROM pg_stat_activity;" | xargs)
echo "   Active DB connections: $DB_BEFORE"

# Check Redis baseline
echo "[2/6] Checking Redis baseline..."
REDIS_OPS=$(docker exec them-redis redis-cli INFO stats 2>/dev/null | grep "instantaneous_ops_per_sec" | cut -d: -f2 | tr -d '\r' || echo "0")
echo "   Redis ops/sec: $REDIS_OPS"

# Start test via API
echo "[3/6] Starting test via API..."
START_RESPONSE=$(curl -s -X POST "http://${TEST_RUNNER_BACKEND}/run" \
  -H "Content-Type: application/json" \
  -d "{
    \"scenario_id\": \"test-policy-mock\",
    \"n_users\": $N_USERS
  }")

RUN_ID=$(echo "$START_RESPONSE" | grep -oP '"run_id":"[^"]+' | cut -d'"' -f4)

if [ -z "$RUN_ID" ]; then
  echo "❌ Failed to start test. Response:"
  echo "$START_RESPONSE"
  exit 1
fi

echo "✅ Test started: run_id=$RUN_ID"
echo "   Stream: http://${TEST_RUNNER_BACKEND}/run/${RUN_ID}/stream"
echo "   UI: http://10.55.125.43:3210/run/${RUN_ID}"

# Wait for test to complete
echo "[4/6] Waiting for test to complete..."
EXPECTED_DURATION=$(( N_USERS * 3 / 10 ))
echo "   Expected duration: ~${EXPECTED_DURATION}s"

# Poll for completion (check every 5 seconds)
MAX_WAIT=$(( EXPECTED_DURATION + 120 ))  # Add 2 min buffer
ELAPSED=0

while [ $ELAPSED -lt $MAX_WAIT ]; do
  sleep 5
  ELAPSED=$(( ELAPSED + 5 ))

  # Check if run is complete in logs
  COMPLETE_LOG=$(docker logs them-test-runner-backend 2>&1 | grep "\"run_id\":\"$RUN_ID\"" | grep "run: complete" || echo "")

  if [ -n "$COMPLETE_LOG" ]; then
    echo "✅ Test completed (${ELAPSED}s)"
    break
  fi

  printf "."
done

echo

if [ $ELAPSED -ge $MAX_WAIT ]; then
  echo "⚠️  Test timeout after ${MAX_WAIT}s"
  echo "   Check: docker logs them-test-runner-backend | grep $RUN_ID"
fi

# Collect results
echo "[5/6] Collecting results..."
sleep 2  # Small delay to ensure logs are flushed

RESULT_JSON=$(docker logs them-test-runner-backend 2>&1 | grep "\"run_id\":\"$RUN_ID\"" | grep "run: complete" | tail -1)

if [ -z "$RESULT_JSON" ]; then
  echo "❌ Could not find completion log for run_id: $RUN_ID"
  echo "   Showing last 20 lines for this run:"
  docker logs them-test-runner-backend 2>&1 | grep "$RUN_ID" | tail -20
  exit 1
fi

# Parse results
PASSED=$(echo "$RESULT_JSON" | grep -oP '"passed":\K[0-9]+' || echo "0")
FAILED=$(echo "$RESULT_JSON" | grep -oP '"failed":\K[0-9]+' || echo "0")

# Check DB connections after test
DB_AFTER=$(docker exec them-postgres psql -U them -d them -t -c "SELECT count(*) FROM pg_stat_activity;" | xargs)

# Get average latency from user logs
LATENCY_SAMPLE=$(docker logs them-test-runner-backend 2>&1 | \
  grep "\"user\":[0-9]\\+" | grep "latency_ms" | tail -50 | \
  grep -oP '"latency_ms":\K[0-9]+' | \
  awk '{sum+=$1; count++} END {if(count>0) print int(sum/count); else print 0}')

# Calculate success rate
if [ $(( PASSED + FAILED )) -gt 0 ]; then
  SUCCESS_RATE=$(awk "BEGIN {printf \"%.1f\", ($PASSED/($PASSED+$FAILED))*100}")
else
  SUCCESS_RATE="0.0"
fi

echo "[6/6] Results:"
echo
echo "=== Test Results ==="
echo "Test ID: $TEST_ID"
echo "Run ID: $RUN_ID"
echo "Users: $N_USERS"
echo "Passed: $PASSED"
echo "Failed: $FAILED"
echo "Success Rate: ${SUCCESS_RATE}%"
echo "Avg Latency (sample): ${LATENCY_SAMPLE} ms"
echo "DB Connections Before: $DB_BEFORE"
echo "DB Connections After: $DB_AFTER"
echo

# Determine status
if [ "$FAILED" -eq 0 ]; then
  STATUS="✅ PASS"
else
  FAIL_PCT=$(awk "BEGIN {printf \"%.0f\", ($FAILED/($PASSED+$FAILED))*100}")
  if [ "$FAIL_PCT" -gt 50 ]; then
    STATUS="❌ FAIL (${FAIL_PCT}% failed)"
  else
    STATUS="⚠️  PARTIAL (${FAIL_PCT}% failed)"
  fi
fi

echo "Status: $STATUS"
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
    "success_rate": $SUCCESS_RATE,
    "avg_latency_ms": ${LATENCY_SAMPLE:-0},
    "db_connections_before": $DB_BEFORE,
    "db_connections_after": $DB_AFTER,
    "duration_seconds": $ELAPSED
  },
  "status": "$STATUS"
}
EOF

echo "Results saved to: $RESULT_FILE"
echo

# Show next steps
echo "=== Next Steps ==="
if [ "$FAILED" -eq 0 ]; then
  echo "✅ All users passed! Try next test:"

  case "$TEST_ID" in
    "1a")
      echo "   ./scripts/run-perf-test.sh 1b 25"
      ;;
    "1b")
      echo "   ./scripts/run-perf-test.sh 1c 50"
      ;;
    "1c")
      echo "   ⚠️  If failures started, worker bottleneck confirmed."
      echo "   Next: Increase worker concurrency (see docs/_archive/GATEWAY_PERF_TEST_QUICKSTART.md Phase B)"
      ;;
    *)
      echo "   Continue with test series per docs/_archive/GATEWAY_PERF_TEST_QUICKSTART.md"
      ;;
  esac
else
  echo "⚠️  Some failures detected."
  echo
  echo "Analysis:"
  echo "  - Latency breakdown: ./scripts/latency-breakdown.sh"
  echo "  - Error logs: docker logs them-test-runner-backend --since 5m | grep error"
  echo "  - Bridge logs: docker logs them-go-bridge --since 5m | grep -E '502|error'"
  echo

  if [ "$FAIL_PCT" -gt 80 ]; then
    echo "⚠️  High failure rate (${FAIL_PCT}%) suggests bottleneck hit."
    echo "   Expected at ~30-50 users with 100 worker slots."
  fi
fi

echo
echo "View detailed logs:"
echo "  docker logs them-test-runner-backend | grep $RUN_ID | tail -50"
