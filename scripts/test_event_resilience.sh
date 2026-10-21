#!/bin/bash
# Event Delivery Resilience Tests
# Tests delivery during: writer restart, Redis restart, ClickHouse outage

set -e

echo "=== Event Delivery Resilience Tests ==="
echo ""

# Get test runner URL and credentials
TEST_RUNNER_URL="http://10.55.125.43:3210"
SCENARIO_ID="50-event-delivery-test"

# Helper: trigger test via API
run_test() {
    local n_users=$1
    local description=$2

    echo "📋 $description"
    echo "  Triggering $n_users requests via test runner..."

    RESPONSE=$(curl -s -X POST "$TEST_RUNNER_URL/api/execute" \
        -H "Content-Type: application/json" \
        -d "{\"scenario_id\":\"$SCENARIO_ID\",\"n_users\":$n_users}")

    RUN_ID=$(echo "$RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('run_id', ''))" 2>/dev/null || echo "")

    if [ -z "$RUN_ID" ]; then
        echo "  ❌ Failed to trigger test"
        return 1
    fi

    echo "  Run ID: $RUN_ID"
    echo "  Waiting 30s for completion..."
    sleep 30

    return 0
}

# Helper: verify delivery
verify_delivery() {
    local test_name=$1
    local expected_count=$2
    local start_time=$3

    echo ""
    echo "🔍 Verifying delivery: $test_name"

    # Count in PG
    PG_COUNT=$(docker exec them-postgres psql -U them -d them -t -c "
        SELECT COUNT(*)
        FROM gateway_requests
        WHERE created_at >= '$start_time' AND model_served = 'mock-model-v1';
    " | xargs)

    # Count in ClickHouse
    CH_COUNT=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "
        SELECT COUNT(*)
        FROM events_gateway_request
        WHERE ts >= '$start_time' AND model_served = 'mock-model-v1';
    " | xargs)

    # Check Redis state
    REDIS_LAG=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1 || echo "0")
    REDIS_PENDING=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^pending$" | tail -1 || echo "0")

    echo "  PG: $PG_COUNT"
    echo "  CH: $CH_COUNT"
    echo "  Expected: $expected_count"
    echo "  Redis lag: $REDIS_LAG"
    echo "  Redis pending: $REDIS_PENDING"

    # Verify
    if [ "$PG_COUNT" -eq "$expected_count" ] && [ "$CH_COUNT" -eq "$expected_count" ] && [ "$REDIS_LAG" -eq 0 ]; then
        echo "  ✅ PASS"
        return 0
    else
        echo "  ❌ FAIL"
        return 1
    fi
}

# Test 1: Event-writer restart mid-processing
echo "=========================================="
echo "Test 1: Event-writer Restart Mid-Processing"
echo "=========================================="
echo ""

TEST1_START=$(date -u +"%Y-%m-%d %H:%M:%S")

# Send 500 requests
run_test 500 "Sending 500 requests (batch 1)"

# Wait 2 seconds then restart writer
echo "  ⏳ Waiting 2s..."
sleep 2
echo "  🔄 Restarting event-writer..."
docker restart them-event-writer
sleep 3

# Send another 500 requests
run_test 500 "Sending 500 requests (batch 2)"

# Wait for processing
echo "  ⏳ Waiting 30s for processing..."
sleep 30

# Verify
verify_delivery "Writer Restart" 1000 "$TEST1_START"
TEST1_RESULT=$?

echo ""

# Test 2: Redis restart (AOF recovery)
echo "=========================================="
echo "Test 2: Redis Restart (AOF Recovery)"
echo "=========================================="
echo ""

TEST2_START=$(date -u +"%Y-%m-%d %H:%M:%S")

# Send 500 requests
run_test 500 "Sending 500 requests before Redis restart"

# Restart Redis
echo "  🔄 Restarting Redis..."
docker restart them-redis-events
sleep 5

# Send another 500 requests
run_test 500 "Sending 500 requests after Redis restart"

# Wait for processing
echo "  ⏳ Waiting 30s for processing..."
sleep 30

# Verify
verify_delivery "Redis Restart" 1000 "$TEST2_START"
TEST2_RESULT=$?

echo ""

# Test 3: ClickHouse outage and recovery
echo "=========================================="
echo "Test 3: ClickHouse Outage & Recovery"
echo "=========================================="
echo ""

TEST3_START=$(date -u +"%Y-%m-%d %H:%M:%S")

# Stop ClickHouse
echo "  🛑 Stopping ClickHouse..."
docker stop them-clickhouse
sleep 3

# Send 500 requests (will queue in Redis)
run_test 500 "Sending 500 requests while ClickHouse is down"

# Check Redis backlog
BACKLOG=$(docker exec them-redis-events redis-cli XLEN them:events)
echo "  📊 Redis backlog: $BACKLOG events"

# Start ClickHouse
echo "  ▶️  Starting ClickHouse..."
docker start them-clickhouse
sleep 10

# Wait for delivery
echo "  ⏳ Waiting 60s for delivery..."
sleep 60

# Send another 500 requests (should process immediately)
run_test 500 "Sending 500 requests after ClickHouse recovery"

# Wait for processing
echo "  ⏳ Waiting 30s for processing..."
sleep 30

# Verify
verify_delivery "ClickHouse Outage" 1000 "$TEST3_START"
TEST3_RESULT=$?

echo ""

# Summary
echo "=========================================="
echo "RESILIENCE TEST SUMMARY"
echo "=========================================="
echo ""

if [ $TEST1_RESULT -eq 0 ]; then
    echo "✅ Test 1: Writer Restart - PASSED"
else
    echo "❌ Test 1: Writer Restart - FAILED"
fi

if [ $TEST2_RESULT -eq 0 ]; then
    echo "✅ Test 2: Redis Restart - PASSED"
else
    echo "❌ Test 2: Redis Restart - FAILED"
fi

if [ $TEST3_RESULT -eq 0 ]; then
    echo "✅ Test 3: ClickHouse Outage - PASSED"
else
    echo "❌ Test 3: ClickHouse Outage - FAILED"
fi

echo ""

# Overall result
if [ $TEST1_RESULT -eq 0 ] && [ $TEST2_RESULT -eq 0 ] && [ $TEST3_RESULT -eq 0 ]; then
    echo "=========================================="
    echo "✅ ALL RESILIENCE TESTS PASSED"
    echo "=========================================="
    exit 0
else
    echo "=========================================="
    echo "❌ SOME RESILIENCE TESTS FAILED"
    echo "=========================================="
    exit 1
fi
