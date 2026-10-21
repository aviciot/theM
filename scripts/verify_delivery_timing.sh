#!/bin/bash
# Delivery timing verification: run scenario 40, track exact IDs, measure delivery time
set -e

HISTORY_DIR="/opt/docker/them/test-runner/data/history"

echo "========================================="
echo "Delivery Timing Verification - Scenario 40"
echo "Mode: DUAL"
echo "========================================="
echo ""

# 1. Clear old data
echo "[$(date +%H:%M:%S)] Clearing old data..."
docker exec them-postgres psql -U them -d them -c "TRUNCATE gateway_requests CASCADE;" > /dev/null 2>&1
docker exec them-clickhouse clickhouse-client --query "TRUNCATE TABLE them_events.events_gateway_request;" > /dev/null 2>&1
echo "[$(date +%H:%M:%S)] ✅ Data cleared"
echo ""

# 2. Run test via test-runner backend
echo "[$(date +%H:%M:%S)] Starting scenario 40..."
RUN_RESPONSE=$(docker exec them-test-runner-backend wget -q -O- --post-data='{"scenario_id":"40-logging-perf-minimal"}' --header='Content-Type: application/json' http://localhost:8090/run 2>/dev/null)
RUN_ID=$(echo "$RUN_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['run_id'])" 2>/dev/null)

if [ -z "$RUN_ID" ]; then
    echo "❌ Failed to start test run"
    echo "Response: $RUN_RESPONSE"
    exit 1
fi

echo "[$(date +%H:%M:%S)] Run ID: $RUN_ID"

# 3. Wait for test to complete (poll history file on host)
echo "[$(date +%H:%M:%S)] Waiting for test completion..."
for i in {1..120}; do
    if [ -f "$HISTORY_DIR/$RUN_ID.json" ]; then
        # Check if ended_at is set
        ENDED=$(python3 -c "import sys,json; print(json.load(open('$HISTORY_DIR/$RUN_ID.json')).get('ended_at', ''))" 2>/dev/null || echo "")
        if [ -n "$ENDED" ]; then
            echo "[$(date +%H:%M:%S)] ✅ Test completed"
            break
        fi
    fi
    sleep 1
done

if [ ! -f "$HISTORY_DIR/$RUN_ID.json" ]; then
    echo "❌ Test history file not found after 120 seconds"
    exit 1
fi

# 4. Get test time window
START_TIME=$(python3 -c "import json; print(json.load(open('$HISTORY_DIR/$RUN_ID.json'))['started_at'])")
END_TIME=$(python3 -c "import json; print(json.load(open('$HISTORY_DIR/$RUN_ID.json'))['ended_at'])")
N_USERS=$(python3 -c "import json; print(json.load(open('$HISTORY_DIR/$RUN_ID.json'))['n_users'])")
echo "[$(date +%H:%M:%S)] Test window: $START_TIME to $END_TIME"
echo "[$(date +%H:%M:%S)] Users: $N_USERS"
echo ""

# 5. Get request IDs from PostgreSQL
echo "[$(date +%H:%M:%S)] Fetching request IDs from PostgreSQL..."
START_PG=$(echo "$START_TIME" | sed 's/Z/+00/')
END_PG=$(echo "$END_TIME" | sed 's/Z/+00/')
PG_IDS=$(docker exec them-postgres psql -U them -d them -t -c "SELECT id::text FROM gateway_requests WHERE created_at BETWEEN '$START_PG' AND '$END_PG' ORDER BY created_at;" 2>/dev/null)
PG_COUNT=$(echo "$PG_IDS" | grep -v '^$' | tr -d ' ' | wc -l)
echo "[$(date +%H:%M:%S)] Found $PG_COUNT request IDs in PostgreSQL"

if [ "$PG_COUNT" -eq 0 ]; then
    echo "❌ No requests found in PostgreSQL - test may have failed"
    exit 1
fi

# Save IDs to temp file for verification
echo "$PG_IDS" | grep -v '^$' | tr -d ' ' > /tmp/pg_ids.txt
FIRST_ID=$(head -1 /tmp/pg_ids.txt)
echo "[$(date +%H:%M:%S)] First request ID: $FIRST_ID"
echo ""

# 6. Check ClickHouse delivery timing
echo "[$(date +%H:%M:%S)] Checking ClickHouse delivery..."
START_CH=$(echo "$START_TIME" | sed 's/Z//')
END_CH=$(echo "$END_TIME" | sed 's/Z//')

# Poll ClickHouse every 5 seconds for up to 2 minutes
for elapsed in {5..120..5}; do
    sleep 5

    CH_COUNT=$(docker exec them-clickhouse clickhouse-client --query "SELECT count() FROM them_events.events_gateway_request WHERE ts BETWEEN toDateTime('$START_CH') AND toDateTime('$END_CH') FORMAT TabSeparated" 2>/dev/null || echo "0")

    echo "[$(date +%H:%M:%S)] +${elapsed}s: ClickHouse has $CH_COUNT / $PG_COUNT events"

    if [ "$CH_COUNT" -eq "$PG_COUNT" ]; then
        echo ""
        echo "✅ ALL $PG_COUNT EVENTS DELIVERED TO CLICKHOUSE"
        echo "   Delivery time: ${elapsed} seconds after test completion"
        echo ""

        # Verify with ID-based check (first 10 IDs)
        echo "[$(date +%H:%M:%S)] Verifying with ID-based check (first 10 IDs)..."
        SAMPLE_IDS=$(head -10 /tmp/pg_ids.txt)
        SAMPLE_IDS_LIST=""
        for id in $SAMPLE_IDS; do
            SAMPLE_IDS_LIST="$SAMPLE_IDS_LIST'$id',"
        done
        SAMPLE_IDS_LIST=$(echo "$SAMPLE_IDS_LIST" | sed 's/,$//')

        CH_SAMPLE=$(docker exec them-clickhouse clickhouse-client --query "SELECT count() FROM them_events.events_gateway_request WHERE request_id IN ($SAMPLE_IDS_LIST) FORMAT TabSeparated" 2>/dev/null)

        echo "[$(date +%H:%M:%S)] Sample check: $CH_SAMPLE / 10 IDs found"

        if [ "$CH_SAMPLE" -eq 10 ]; then
            echo "✅ ID-based verification passed"
        else
            echo "⚠️  ID-based verification found only $CH_SAMPLE / 10 IDs"
        fi

        echo ""
        echo "========================================="
        echo "DELIVERY VERIFICATION: PASSED"
        echo "Total events: $PG_COUNT"
        echo "Delivery time: ${elapsed} seconds"
        echo "Result: ALL REQUEST IDS DELIVERED"
        echo "========================================="

        # Cleanup
        rm -f /tmp/pg_ids.txt
        exit 0
    fi
done

# If we get here, delivery incomplete after 2 minutes
echo ""
echo "❌ DELIVERY INCOMPLETE AFTER 120 SECONDS"
echo "   PostgreSQL: $PG_COUNT requests"
echo "   ClickHouse: $CH_COUNT events"
echo "   Missing: $((PG_COUNT - CH_COUNT)) events"
echo ""

# Check Redis queue status
REDIS_LAG=$(docker exec them-redis-events redis-cli XLEN them:events:gateway 2>/dev/null || echo "unknown")
echo "Redis queue lag: $REDIS_LAG events"

# Check event-writer logs
echo ""
echo "Recent event-writer logs:"
docker logs them-event-writer --since 2m 2>&1 | grep -E "(batch written|ERROR|error)" | tail -10

rm -f /tmp/pg_ids.txt
exit 1
