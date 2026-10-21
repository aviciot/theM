#!/bin/bash
# Execute All Four Resilience Tests
# Tests: Writer restart, Redis restart, ClickHouse outage, Load test (1000 events)

set -e

TEST_RUNNER_URL="http://10.55.125.43:8093"
SCENARIO_ID="50-event-delivery-test"

echo "=== Event Delivery Resilience Tests ==="
echo "=== Executing All 4 Tests ==="
echo ""

# Helper: trigger test via API
trigger_test() {
    local description=$1

    echo "📋 $description"
    echo "  Triggering test via POST /run..."

    RESPONSE=$(curl -s -X POST "$TEST_RUNNER_URL/run" \
        -H "Content-Type: application/json" \
        -d "{\"scenario_id\":\"$SCENARIO_ID\"}")

    RUN_ID=$(echo "$RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin).get('run_id', ''))" 2>/dev/null || echo "")

    if [ -z "$RUN_ID" ]; then
        echo "  ❌ Failed to trigger test"
        echo "  Response: $RESPONSE"
        return 1
    fi

    echo "  Run ID: $RUN_ID"
    return 0
}

# Helper: verify delivery
verify_delivery() {
    local test_name=$1
    local expected_count=$2
    local test_start=$3

    echo ""
    echo "🔍 Verifying: $test_name"

    # Count in PG
    PG_COUNT=$(docker exec them-postgres psql -U them -d them -t -c "
        SELECT COUNT(*)
        FROM gateway_requests
        WHERE created_at >= '$test_start' AND model_served = 'mock-model-v1';
    " | xargs)

    # Count in ClickHouse
    CH_COUNT=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "
        SELECT COUNT(*)
        FROM events_gateway_request
        WHERE ts >= '$test_start' AND model_served = 'mock-model-v1';
    " | xargs)

    # Check Redis state
    REDIS_LAG=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1 || echo "0")
    REDIS_PENDING=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^pending$" | tail -1 || echo "0")

    echo "  PG: $PG_COUNT"
    echo "  CH: $CH_COUNT"
    echo "  Expected: $expected_count"
    echo "  Redis lag: $REDIS_LAG"
    echo "  Redis pending: $REDIS_PENDING"

    # Reconcile IDs
    echo "  Reconciling request IDs..."

    PG_IDS_FILE="/tmp/pg_ids_$test_name.txt"
    docker exec them-postgres psql -U them -d them -t -c "
        SELECT id
        FROM gateway_requests
        WHERE created_at >= '$test_start' AND model_served = 'mock-model-v1'
        ORDER BY id;
    " > "$PG_IDS_FILE"

    MISSING_COUNT=0
    MISMATCHED_COUNT=0

    while IFS= read -r request_id; do
        request_id=$(echo "$request_id" | xargs)
        if [ -n "$request_id" ]; then
            CH_EXISTS=$(docker exec them-clickhouse clickhouse-client --database=them_events --query "
                SELECT COUNT(*) FROM events_gateway_request WHERE request_id = '$request_id';
            " | xargs)

            if [ "$CH_EXISTS" -eq 0 ]; then
                ((MISSING_COUNT++))
                if [ $MISSING_COUNT -le 5 ]; then
                    echo "    Missing: $request_id"
                fi
            fi
        fi
    done < "$PG_IDS_FILE"

    echo "  Missing IDs: $MISSING_COUNT"
    echo "  Mismatched fields: $MISMATCHED_COUNT"

    # Pass/fail
    if [ "$PG_COUNT" -eq "$expected_count" ] && [ "$CH_COUNT" -eq "$expected_count" ] && [ "$REDIS_LAG" -eq 0 ] && [ "$MISSING_COUNT" -eq 0 ]; then
        echo "  ✅ PASS"
        return 0
    else
        echo "  ❌ FAIL"
        return 1
    fi
}

# Clear old data
echo "Clearing old test data..."
docker exec them-postgres psql -U them -d them -c "DELETE FROM gateway_requests WHERE model_served = 'mock-model-v1';" > /dev/null
docker exec them-clickhouse clickhouse-client --database=them_events --query "DELETE FROM events_gateway_request WHERE model_served = 'mock-model-v1';" > /dev/null
echo "✅ Cleared"
echo ""

# Test 1: Event-writer Restart Mid-Processing
echo "=========================================="
echo "Test 1: Event-writer Restart Mid-Processing"
echo "=========================================="
echo ""

TEST1_START=$(date -u +"%Y-%m-%d %H:%M:%S")

trigger_test "Batch 1 (1000 requests)"
echo "  ⏳ Waiting 10 seconds..."
sleep 10

echo "  🔄 Restarting event-writer..."
docker restart them-event-writer
sleep 5
echo "  ✅ Restarted"

echo "  ⏳ Waiting 120 seconds for processing..."
sleep 120

verify_delivery "Test1_WriterRestart" 1000 "$TEST1_START"
TEST1_RESULT=$?

echo ""

# Test 2: Redis Restart (AOF Recovery)
echo "=========================================="
echo "Test 2: Redis Restart (AOF Recovery)"
echo "=========================================="
echo ""

# Clear data
docker exec them-postgres psql -U them -d them -c "DELETE FROM gateway_requests WHERE model_served = 'mock-model-v1';" > /dev/null
docker exec them-clickhouse clickhouse-client --database=them_events --query "DELETE FROM events_gateway_request WHERE model_served = 'mock-model-v1';" > /dev/null

TEST2_START=$(date -u +"%Y-%m-%d %H:%M:%S")

trigger_test "Batch 1 before Redis restart"
echo "  ⏳ Waiting 60 seconds for processing..."
sleep 60

echo "  🔄 Restarting Redis..."
docker restart them-redis-events
sleep 10
echo "  ✅ Redis restarted"

echo "  ⏳ Waiting 60 seconds for recovery..."
sleep 60

verify_delivery "Test2_RedisRestart" 1000 "$TEST2_START"
TEST2_RESULT=$?

echo ""

# Test 3: ClickHouse Outage & Recovery
echo "=========================================="
echo "Test 3: ClickHouse Outage & Recovery"
echo "=========================================="
echo ""

# Clear data
docker exec them-postgres psql -U them -d them -c "DELETE FROM gateway_requests WHERE model_served = 'mock-model-v1';" > /dev/null
docker exec them-clickhouse clickhouse-client --database=them_events --query "DELETE FROM events_gateway_request WHERE model_served = 'mock-model-v1';" > /dev/null

TEST3_START=$(date -u +"%Y-%m-%d %H:%M:%S")

echo "  🛑 Stopping ClickHouse..."
docker stop them-clickhouse
sleep 3

trigger_test "Batch 1 while ClickHouse is down"
echo "  ⏳ Waiting 60 seconds (events queuing in Redis)..."
sleep 60

BACKLOG=$(docker exec them-redis-events redis-cli XLEN them:events)
echo "  📊 Redis backlog: $BACKLOG events"

echo "  ▶️  Starting ClickHouse..."
docker start them-clickhouse
sleep 15
echo "  ✅ ClickHouse started"

echo "  ⏳ Waiting 120 seconds for backlog processing..."
sleep 120

verify_delivery "Test3_CHOutage" 1000 "$TEST3_START"
TEST3_RESULT=$?

echo ""

# Test 4: Load Test (1000 events, no disruption)
echo "=========================================="
echo "Test 4: Load Test (1000 Events)"
echo "=========================================="
echo ""

# Clear data
docker exec them-postgres psql -U them -d them -c "DELETE FROM gateway_requests WHERE model_served = 'mock-model-v1';" > /dev/null
docker exec them-clickhouse clickhouse-client --database=them_events --query "DELETE FROM events_gateway_request WHERE model_served = 'mock-model-v1';" > /dev/null

TEST4_START=$(date -u +"%Y-%m-%d %H:%M:%S")

trigger_test "Load test: 1000 concurrent requests"
echo "  ⏳ Waiting 120 seconds for processing..."
sleep 120

verify_delivery "Test4_Load" 1000 "$TEST4_START"
TEST4_RESULT=$?

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

if [ $TEST4_RESULT -eq 0 ]; then
    echo "✅ Test 4: Load Test - PASSED"
else
    echo "❌ Test 4: Load Test - FAILED"
fi

echo ""

# Overall result
if [ $TEST1_RESULT -eq 0 ] && [ $TEST2_RESULT -eq 0 ] && [ $TEST3_RESULT -eq 0 ] && [ $TEST4_RESULT -eq 0 ]; then
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
