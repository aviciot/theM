#!/bin/bash
# Test 1: Event-writer Restart Mid-Processing
set -e

echo "=== Test 1: Event-writer Restart Mid-Processing ==="
echo ""

TEST_START=$(date -u +"%Y-%m-%d %H:%M:%S")
echo "Test start: $TEST_START"
echo ""

echo "Instructions:"
echo "1. Open http://10.55.125.43:3210/scenarios in browser"
echo "2. Find '50 — Event Delivery to ClickHouse' (should show 1000 users)"
echo "3. Click Run button"
echo "4. Wait 5 seconds"
echo "5. Press ENTER here to restart event-writer"
echo ""
read -p "Press ENTER after clicking Run and waiting 5 seconds: "

echo ""
echo "🔄 Restarting event-writer..."
docker restart them-event-writer
sleep 3
echo "✅ Event-writer restarted"
echo ""

echo "⏳ Waiting 120 seconds for processing..."
sleep 120

echo ""
echo "📊 Verification:"
echo ""

# Count in PG
PG_COUNT=$(docker exec them-postgres psql -U them -d them -t -c "
  SELECT COUNT(*)
  FROM gateway_requests
  WHERE created_at >= '$TEST_START' AND model_served = 'mock-model-v1';
" | xargs)

# Count in ClickHouse
CH_COUNT=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "
  SELECT COUNT(*)
  FROM events_gateway_request
  WHERE ts >= '$TEST_START' AND model_served = 'mock-model-v1';
" | xargs)

# Check Redis state
REDIS_LAG=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1 || echo "0")
REDIS_PENDING=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^pending$" | tail -1 || echo "0")

echo "PG count: $PG_COUNT"
echo "CH count: $CH_COUNT"
echo "Expected: 1000"
echo "Redis lag: $REDIS_LAG"
echo "Redis pending: $REDIS_PENDING"
echo ""

# Get all request IDs from PG
echo "Fetching all request IDs from PG..."
PG_IDS=$(docker exec them-postgres psql -U them -d them -t -c "
  SELECT id
  FROM gateway_requests
  WHERE created_at >= '$TEST_START' AND model_served = 'mock-model-v1'
  ORDER BY id;
")

# Check each ID in ClickHouse
MISSING_IDS=()
MISMATCHED=()

echo "Reconciling request IDs..."
while IFS= read -r request_id; do
    request_id=$(echo "$request_id" | xargs)
    if [ -n "$request_id" ]; then
        # Check if ID exists in CH
        CH_EXISTS=$(docker exec them-clickhouse clickhouse-client --database=them_events --query "
          SELECT COUNT(*)
          FROM events_gateway_request
          WHERE request_id = '$request_id';
        " | xargs)

        if [ "$CH_EXISTS" -eq 0 ]; then
            MISSING_IDS+=("$request_id")
        else
            # Verify fields match
            PG_FIELDS=$(docker exec them-postgres psql -U them -d them -t -c "
              SELECT provider, model_served, status, tokens_in, tokens_out, latency_ms
              FROM gateway_requests
              WHERE id = '$request_id';
            " | xargs)

            CH_FIELDS=$(docker exec them-clickhouse clickhouse-client --database=them_events --query "
              SELECT provider, model_served, status, tokens_in, tokens_out, latency_ms
              FROM events_gateway_request
              WHERE request_id = '$request_id';
            " | head -1 | xargs)

            if [ "$PG_FIELDS" != "$CH_FIELDS" ]; then
                MISMATCHED+=("$request_id: PG=[$PG_FIELDS] CH=[$CH_FIELDS]")
            fi
        fi
    fi
done <<< "$PG_IDS"

echo ""
echo "Missing IDs in ClickHouse: ${#MISSING_IDS[@]}"
if [ ${#MISSING_IDS[@]} -gt 0 ]; then
    echo "First 10 missing:"
    printf '%s\n' "${MISSING_IDS[@]}" | head -10
fi

echo ""
echo "Mismatched fields: ${#MISMATCHED[@]}"
if [ ${#MISMATCHED[@]} -gt 0 ]; then
    echo "First 5 mismatches:"
    printf '%s\n' "${MISMATCHED[@]}" | head -5
fi

echo ""
echo "=========================================="
if [ "$PG_COUNT" -eq 1000 ] && [ "$CH_COUNT" -eq 1000 ] && [ "$REDIS_LAG" -eq 0 ] && [ ${#MISSING_IDS[@]} -eq 0 ] && [ ${#MISMATCHED[@]} -eq 0 ]; then
    echo "✅ TEST 1 PASSED"
    echo "  • All 1000 events delivered"
    echo "  • All IDs reconciled"
    echo "  • All fields match"
    echo "  • No lag or pending"
    exit 0
else
    echo "❌ TEST 1 FAILED"
    echo "  • PG: $PG_COUNT (expected: 1000)"
    echo "  • CH: $CH_COUNT (expected: 1000)"
    echo "  • Missing: ${#MISSING_IDS[@]}"
    echo "  • Mismatched: ${#MISMATCHED[@]}"
    echo "  • Lag: $REDIS_LAG"
    echo "  • Pending: $REDIS_PENDING"
    exit 1
fi
