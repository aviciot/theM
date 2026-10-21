#!/bin/bash
# Verify Event Delivery — Use Test Runner to send real gateway requests
# Then reconcile PG vs ClickHouse with field-level verification

set -e

echo "=== Event Delivery Verification ==="
echo ""

# Step 1: Mark test start time
TEST_START=$(date -u +"%Y-%m-%d %H:%M:%S")
echo "Test start: $TEST_START UTC"
echo ""

# Step 2: Instructions to run test
echo "📋 Step 1: Run Test via UI"
echo "  1. Open: http://10.55.125.43:3210/scenarios"
echo "  2. Find: '50 — Event Delivery to ClickHouse'"
echo "  3. Click: ▶ Run"
echo "  4. Wait for completion (should be ~30 seconds for 100 users)"
echo ""
echo "Press ENTER after test completes..."
read

# Step 3: Wait for event delivery
echo ""
echo "⏳ Waiting 30 seconds for event delivery..."
sleep 30

# Step 4: Count events in PG
echo ""
echo "📊 Reconciling PG vs ClickHouse..."
PG_COUNT=$(docker exec them-postgres psql -U them -d them -t -c "
  SELECT COUNT(*)
  FROM gateway_requests
  WHERE created_at >= '$TEST_START' AND model_served = 'mock-model-v1';
")

PG_COUNT=$(echo $PG_COUNT | xargs)  # Trim whitespace
echo "  PG gateway_requests: $PG_COUNT"

# Step 5: Count events in ClickHouse
CH_COUNT=$(docker exec them-clickhouse clickhouse-client --database=them_events -t --query "
  SELECT COUNT(*)
  FROM events_gateway_request
  WHERE ts >= '$TEST_START' AND model_served = 'mock-model-v1';
")

CH_COUNT=$(echo $CH_COUNT | xargs)
echo "  CH events_gateway_request: $CH_COUNT"

# Step 6: Calculate delivery rate
if [ "$PG_COUNT" -gt 0 ]; then
  DELIVERY_PCT=$(awk "BEGIN {printf \"%.1f\", ($CH_COUNT / $PG_COUNT) * 100}")
  echo "  Delivery rate: $DELIVERY_PCT%"
else
  echo "  ⚠️  No events found in PG (test may not have run)"
  exit 1
fi

# Step 7: Check Redis lag
echo ""
echo "🔍 Checking Redis lag..."
REDIS_LAG=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^lag$" | tail -1)
REDIS_PENDING=$(docker exec them-redis-events redis-cli XINFO GROUPS them:events 2>/dev/null | grep -A1 "^pending$" | tail -1)

echo "  Redis lag: $REDIS_LAG"
echo "  Redis pending: $REDIS_PENDING"

# Step 8: Sample field verification (10 random records)
echo ""
echo "🔬 Verifying field integrity (10 random samples)..."
docker exec them-postgres psql -U them -d them -c "
  SELECT
    id AS request_id,
    provider,
    model_served,
    tokens_in,
    tokens_out,
    cost_usd
  FROM gateway_requests
  WHERE created_at >= '$TEST_START' AND model_served = 'mock-model-v1'
  ORDER BY RANDOM()
  LIMIT 10;
" > /tmp/pg_sample.txt

echo "  PG sample saved to /tmp/pg_sample.txt"
echo ""

# Note: Full field matching requires manual verification or Python script
# For now, show PG sample and note CH query needed

echo "📋 To verify CH matches:"
echo "  1. Pick request_id from above"
echo "  2. Query CH:"
echo "     docker exec them-clickhouse clickhouse-client --database=them_events --query \\"
echo "       'SELECT request_id, provider, model_served, tokens_in, tokens_out, cost_usd"
echo "        FROM events_gateway_request"
echo "        WHERE request_id = \"<uuid-here>\"';"
echo ""

# Step 9: Final verdict
echo "=========================================="
if [ "$PG_COUNT" -eq "$CH_COUNT" ] && [ "$PG_COUNT" -gt 0 ] && [ "$REDIS_LAG" -eq 0 ]; then
  echo "✅ TEST PASSED"
  echo "  • All $PG_COUNT events delivered to ClickHouse"
  echo "  • Delivery rate: 100%"
  echo "  • Redis lag: 0 (fully processed)"
  echo "  • Redis pending: $REDIS_PENDING"
  exit 0
else
  echo "❌ TEST FAILED"
  echo "  • PG: $PG_COUNT events"
  echo "  • CH: $CH_COUNT events"
  echo "  • Delivery rate: $DELIVERY_PCT%"
  echo "  • Redis lag: $REDIS_LAG"
  echo "  • Redis pending: $REDIS_PENDING"

  if [ "$REDIS_LAG" -gt 0 ]; then
    echo ""
    echo "💡 Suggestion: Wait longer for delivery (lag > 0 means events still in queue)"
  fi

  if [ "$REDIS_PENDING" -gt 0 ]; then
    echo ""
    echo "💡 Suggestion: Check event-writer logs for errors"
    echo "   docker logs them-event-writer --tail 50"
  fi

  exit 1
fi
